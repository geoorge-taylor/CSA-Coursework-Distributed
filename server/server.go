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

// ---- Types and constants ----

const alive byte = 255

type Broker struct {
	mu           sync.Mutex
	cond         *sync.Cond
	world        [][]byte
	turn         int
	height       int
	quit         bool
	paused       bool
	shutdown     chan bool
	shuttingDown bool
	workers      []worker
}

type worker struct {
	startY     int
	endY       int
	haloAbove  int
	haloBelow  int
	workerNode *rpc.Client
}

// ---- Helpers ----

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

// ---- Internal methods ----

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

func (b *Broker) assignStrips() {
	n := len(b.workers)
	for i := range b.workers {
		startY := i * b.height / n
		endY := (i + 1) * b.height / n

		b.workers[i].startY = startY
		b.workers[i].endY = endY
		b.workers[i].haloAbove = (startY - 1 + b.height) % b.height // wraps to the last row
		b.workers[i].haloBelow = endY % b.height                    // wraps to row 0
	}
}

func (b *Broker) nextTurn(world [][]byte) [][]byte {
	height := len(world)

	// send every worker node its strip, with a halo row above and below
	calls := make([]*rpc.Call, len(b.workers))
	for i, w := range b.workers {
		rows := make([][]byte, 0, w.endY-w.startY+2) // +2 for the halo rows
		rows = append(rows, world[w.haloAbove])
		rows = append(rows, world[w.startY:w.endY]...)
		rows = append(rows, world[w.haloBelow])

		job := stubs.StripJob{Rows: rows}
		calls[i] = w.workerNode.Go(
			stubs.CalculateStripHandler,
			job,
			new(stubs.StripResult),
			nil,
		)
	}

	// collect the strips in worker order, same as the parallel version
	newWorld := make([][]byte, 0, height)
	for _, call := range calls {
		<-call.Done
		if call.Error != nil {
			fmt.Println("worker node failed:", call.Error)
		}
		strip := call.Reply.(*stubs.StripResult)
		newWorld = append(newWorld, strip.Rows...)
	}
	return newWorld
}

// ---- RPC methods called by worker nodes ----

func (b *Broker) Subscribe(req stubs.Subscription, res *stubs.StatusReport) (err error) {
	err = b.addWorker(req.WorkerAddress)
	if err != nil {
		res.Message = "Failed to subscribe to worker"
	}
	return
}

// ---- RPC methods called by the local controller ----

// Receive the world from the local controller, evolve it for the requested number of turns and send back the result
func (b *Broker) ProcessTurns(req stubs.ProcessTurnsRequest, res *stubs.ProcessTurnsResponse) (err error) {
	b.mu.Lock()
	b.world = req.World
	b.height = req.ImageHeight
	if len(b.workers) == 0 {
		b.mu.Unlock()
		return errors.New("no worker nodes have subscribed")
	}
	b.assignStrips()
	b.mu.Unlock()

	for t := 0; t < req.Turns; t++ {
		b.mu.Lock()
		for b.paused && !b.quit {
			// sleep this go routine but keep mutex unlocked
			b.cond.Wait()
		}
		if b.quit {
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()

		newWorld := b.nextTurn(b.world)
		b.mu.Lock()
		b.world = newWorld
		b.turn++
		b.mu.Unlock()
	}

	b.mu.Lock()
	finalWorld := b.world
	finalTurn := b.turn
	b.quit = false
	b.paused = false
	b.turn = 0
	b.mu.Unlock()

	res.World = finalWorld
	res.CompletedTurns = finalTurn
	res.AliveCells = aliveCells(finalWorld)
	fmt.Println("Processed all turns on the server")
	return
}

func (b *Broker) CountAliveCells(req stubs.CountAliveCellsRequest, res *stubs.CountAliveCellsResponse) (err error) {
	b.mu.Lock()
	currentWorld := b.world
	completedTurns := b.turn
	b.mu.Unlock()

	res.CellsCount = len(aliveCells(currentWorld))
	res.CompletedTurns = completedTurns
	return
}

func (b *Broker) GetWorld(req stubs.GetWorldRequest, res *stubs.GetWorldResponse) (err error) {
	b.mu.Lock()
	res.World = b.world
	res.CompletedTurns = b.turn
	b.mu.Unlock()
	return
}

func (b *Broker) TogglePause(req stubs.TogglePauseRequest, res *stubs.TogglePauseResponse) (err error) {
	b.mu.Lock()
	b.paused = !b.paused
	res.Paused = b.paused
	res.CompletedTurns = b.turn
	b.cond.Broadcast()
	b.mu.Unlock()
	return
}

func (b *Broker) StopProcessing(req stubs.StopProcessingRequest, res *stubs.StopProcessingResponse) (err error) {
	b.mu.Lock()
	b.quit = true
	b.cond.Broadcast() // wake up paused loop to check if its paused again
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

	for _, w := range workers {
		// the worker may exit before its reply arrives, so the error is ignored
		w.workerNode.Call(stubs.WorkerShutdownHandler, stubs.ShutdownRequest{}, new(stubs.ShutdownResponse))
		w.workerNode.Close()
	}

	b.shutdown <- true
	return
}

// ---- Entry point ----

func main() {
	// Listen on some address for local controller requests
	pAddr := flag.String("port", ":8030", "Port to listen on")
	flag.Parse()

	broker := &Broker{
		shutdown: make(chan bool, 1),
	}
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
