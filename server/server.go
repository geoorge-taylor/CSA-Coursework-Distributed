package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"sync"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

const alive byte = 255

var errNotCurrentGame = errors.New("this game has been replaced by a newer one")

type Broker struct {
	mu           sync.Mutex
	cond         *sync.Cond
	shutdown     chan bool
	shuttingDown bool
	workers      []worker
	nextID       int64 // next id of the next game
	current      *game
}

type game struct {
	id      int64
	world   [][]byte
	turn    int
	paused  bool
	quit    bool
	running bool
	done    bool
	pending []stubs.TurnUpdate
	threads int
}

type worker struct {
	startY     int
	endY       int
	haloAbove  int
	haloBelow  int
	workerNode *rpc.Client
}

func aliveCells(world [][]byte) []util.Cell {
	var cells []util.Cell
	for y, row := range world {
		for x, cell := range row {
			if cell == alive {
				cells = append(cells, util.Cell{X: x, Y: y})
			}
		}
	}
	return cells
}

// Internal methods not used via RPC

func (b *Broker) gameFor(id int64) *game {
	if b.current != nil && b.current.id == id {
		return b.current
	}
	return nil
}

// conect back to a given worker via rpc so the broker can eventually communicate with ti
func (b *Broker) addWorker(workerAddress string) (err error) {
	workerNode, err := rpc.Dial("tcp", workerAddress)
	if err != nil {
		return err
	}
	newWorker := worker{workerNode: workerNode}
	b.mu.Lock()
	b.workers = append(b.workers, newWorker)
	b.mu.Unlock()
	return
}

// works out what part of the image each worker should be working on (incl halo rows)
func assignStrips(workers []worker, height int) {
	n := len(workers)
	for i := range workers {
		startY := i * height / n
		endY := (i + 1) * height / n

		workers[i].startY = startY
		workers[i].endY = endY
		workers[i].haloAbove = (startY - 1 + height) % height // wraps to the last row
		workers[i].haloBelow = endY % height                  // wraps to row 0
	}
}

// computes the next turn of the world given every worker and how many threads should be used for each worker
// uses the same logic as the paralell implementation, but just over RPC instead, which is why we use rpc call slice and block on <-call.Done
func nextTurn(workers []worker, world [][]byte, threads int) ([][]byte, []util.Cell) {
	height := len(world)

	// send every worker node its strip, with a halo row above and below
	calls := make([]*rpc.Call, len(workers))

	for i, w := range workers {
		rows := make([][]byte, 0, w.endY-w.startY+2) // +2 for the halo rows
		rows = append(rows, world[w.haloAbove])
		rows = append(rows, world[w.startY:w.endY]...)
		rows = append(rows, world[w.haloBelow])

		job := stubs.StripJob{Rows: rows, StartY: w.startY, Threads: threads}
		calls[i] = w.workerNode.Go(
			stubs.CalculateStripHandler,
			job,
			new(stubs.StripResult),
			nil,
		)
	}

	// collect the strips in worker order
	newWorld := make([][]byte, 0, height)
	flippedCells := []util.Cell{}

	for _, call := range calls {
		<-call.Done
		if call.Error != nil {
			fmt.Println("worker node failed:", call.Error)
		}
		stripResult := call.Reply.(*stubs.StripResult)
		newWorld = append(newWorld, stripResult.Rows...)
		flippedCells = append(flippedCells, stripResult.Flipped...)
	}
	return newWorld, flippedCells
}

// RPC methods called by the worker nodes

// allows a worker to connect to the server (broker), and add the worker to known workers too
func (b *Broker) Subscribe(req stubs.Subscription, res *stubs.StatusReport) (err error) {
	err = b.addWorker(req.WorkerAddress)
	if err != nil {
		res.Message = "Failed to subscribe to worker"
	}
	return
}

// RPC methods called by the local controller

func (b *Broker) StartGame(req stubs.StartGameRequest, res *stubs.StartGameResponse) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// if an old game still exists, make it quit and wake up the loops in it so it realises it has quit
	// also wait untill the old game has stopped running (finished)
	old := b.current
	if old != nil {
		old.quit = true
		b.cond.Broadcast()
		for old.running {
			b.cond.Wait()
		}
	}

	if len(b.workers) == 0 {
		return errors.New("no worker nodes have subscribed")
	}

	// figure out how to assign the strips to all the workers, different start game requests may have different image sizes
	assignStrips(b.workers, req.ImageHeight)
	b.nextID++
	b.current = &game{id: b.nextID, world: req.World, threads: req.Threads}
	b.cond.Broadcast() // wakes an old poller still waiting on the replaced game so it sees its game is gone
	res.GameID = b.nextID
	return
}

// called from controller to start running all the turns of a given game id
func (b *Broker) ProcessTurns(req stubs.ProcessTurnsRequest, res *stubs.ProcessTurnsResponse) (err error) {
	b.mu.Lock()
	// if this is being called for the wrong game, or the current game is either running or done, return an error
	// stops process turns from being called multiple times and for the wrong game
	g := b.gameFor(req.GameID)
	if g == nil || g.running || g.done {
		b.mu.Unlock()
		return errNotCurrentGame
	}

	// set running to true as soon as we know we can start processing turns
	g.running = true
	b.mu.Unlock()

	for t := 0; t < req.Turns; t++ {
		b.mu.Lock()
		for g.paused && !g.quit {
			// sleep this go routine but keep mutex unlocked
			b.cond.Wait()
		}
		if g.quit {
			b.mu.Unlock()
			break
		}
		world := g.world
		b.mu.Unlock()

		// calculate next turn using the brokers registered workers, and the number of threads for the specific game
		newWorld, flippedCells := nextTurn(b.workers, world, g.threads)
		b.mu.Lock()
		g.world = newWorld
		g.turn++

		// add the flipped cells and the corresponding turn to the pending slice
		g.pending = append(g.pending, stubs.TurnUpdate{
			Turn:    g.turn,
			Flipped: flippedCells,
		})

		// alert the get turn update that pending cells are ready to be sent to controller
		b.cond.Broadcast()
		b.mu.Unlock()
	}

	// read results from the game and set the games flags to signal that it has finished processing all turns
	b.mu.Lock()
	finalWorld := g.world
	finalTurn := g.turn
	g.running = false
	g.done = true
	b.cond.Broadcast()
	b.mu.Unlock()

	res.World = finalWorld
	res.CompletedTurns = finalTurn
	res.AliveCells = aliveCells(finalWorld)
	fmt.Println("Processed all turns on the server")
	return
}

func (b *Broker) CountAliveCells(req stubs.GameRequest, res *stubs.CountAliveCellsResponse) (err error) {
	b.mu.Lock()

	// if this request was for an old game, just simply ignore it and return an error
	g := b.gameFor(req.GameID)
	if g == nil {
		b.mu.Unlock()
		return errNotCurrentGame
	}
	currentWorld := g.world
	completedTurns := g.turn
	b.mu.Unlock()

	res.CellsCount = len(aliveCells(currentWorld))
	res.CompletedTurns = completedTurns
	return
}

func (b *Broker) GetWorld(req stubs.GameRequest, res *stubs.GetWorldResponse) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	g := b.gameFor(req.GameID)
	if g == nil {
		return errNotCurrentGame
	}
	res.World = g.world
	res.CompletedTurns = g.turn
	return
}

func (b *Broker) TogglePause(req stubs.GameRequest, res *stubs.TogglePauseResponse) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	g := b.gameFor(req.GameID)
	if g == nil {
		return errNotCurrentGame
	}
	g.paused = !g.paused
	res.Paused = g.paused
	res.CompletedTurns = g.turn
	b.cond.Broadcast()
	return
}

func (b *Broker) StopProcessing(req stubs.GameRequest, res *stubs.StopProcessingResponse) (err error) {
	b.mu.Lock()
	g := b.gameFor(req.GameID)
	if g != nil {
		g.quit = true
		b.cond.Broadcast()
	}
	b.mu.Unlock()
	return
}

func (b *Broker) Shutdown(req stubs.ShutdownRequest, res *stubs.ShutdownResponse) (err error) {
	// check that we are already shutting down so two key presses of k dont cause the client to hang waiting for a response
	b.mu.Lock()
	if b.shuttingDown {
		b.mu.Unlock()
		return
	}

	b.shuttingDown = true
	workers := b.workers
	b.mu.Unlock()

	// cleanly stop all workers and close the rpc connection to them
	for _, w := range workers {
		// the worker may exit before its reply arrives, so the error is ignored
		w.workerNode.Call(stubs.WorkerShutdownHandler, stubs.ShutdownRequest{}, new(stubs.ShutdownResponse))
		w.workerNode.Close()
	}

	// this allows the main function to get past the blocking call on this channel
	b.shutdown <- true
	return
}

func (b *Broker) GetTurnUpdates(req stubs.GameRequest, res *stubs.GetTurnUpdatesResponse) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for {
		// alert controller that a new game has started so the current one they are requesting has finished
		g := b.gameFor(req.GameID)
		if g == nil {
			res.Finished = true
			return
		}

		// alert controller that updates are ready to be displayed and clear out the pending updates
		if len(g.pending) > 0 || g.done {
			res.Updates = g.pending
			res.Finished = g.done
			g.pending = nil
			return
		}

		// if nothing can happen just wait untill processTurns alerts this loop to wake up as more updates are pending
		b.cond.Wait()
	}
}

func main() {
	pAddr := flag.String("port", ":8030", "Port to listen on")
	flag.Parse()

	broker := &Broker{shutdown: make(chan bool, 1)}
	broker.cond = sync.NewCond(&broker.mu)
	rpc.Register(broker)

	listener, err := net.Listen("tcp", *pAddr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	go rpc.Accept(listener)
	<-broker.shutdown
}
