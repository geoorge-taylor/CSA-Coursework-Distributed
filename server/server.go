package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"sync"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

type GolWorker struct {
	mu           sync.Mutex
	cond         *sync.Cond
	world        [][]byte
	turn         int
	width        int
	height       int
	quit         bool
	paused       bool
	shutdown     chan bool
	shuttingDown bool
}

func countliveneighbours(world [][]byte, width, height, row, column int) int {
	count := 0
	for i := -1; i < 2; i++ {
		for j := -1; j < 2; j++ {
			if i == 0 && j == 0 {
				continue
			}
			newHeight := i + row
			newWidth := j + column
			if newHeight < 0 {
				newHeight = height - 1
			}
			if newWidth < 0 {
				newWidth = width - 1
			}
			if newHeight >= height {
				newHeight = 0
			}
			if newWidth >= width {
				newWidth = 0
			}
			if world[newHeight][newWidth] == 255 {
				count++
			}

		}
	}
	return count
}

func executeTurn(world [][]byte, width, height int) [][]byte {
	// make copy of world
	// copilot suggestion: swap from nested for loop to inbuilt copy function to improve speed.

	newWorld := make([][]byte, height)
	for i := range newWorld {
		newWorld[i] = make([]byte, width)
		copy(newWorld[i], world[i])
	}

	// Loop through all cells and check whether it should be alive or dead
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			liveNeighbours := countliveneighbours(world, width, height, i, j)
			if world[i][j] == 255 && (liveNeighbours < 2 || liveNeighbours > 3) {
				newWorld[i][j] = 0
			} else if world[i][j] == 0 && liveNeighbours == 3 {
				newWorld[i][j] = 255
			}
		}
	}
	return newWorld
}

func aliveCells(world [][]byte) []util.Cell {
	var cells []util.Cell
	for y, row := range world {
		for x, cell := range row {
			if cell == 255 {
				cells = append(cells, util.Cell{X: x, Y: y})
			}
		}
	}
	return cells
}

func (g *GolWorker) CountAliveCells(req stubs.CountAliveCellsRequest, res *stubs.CountAliveCellsResponse) (err error) {
	g.mu.Lock()
	currentWorld := g.world
	completedTurns := g.turn
	g.mu.Unlock()

	res.CellsCount = len(aliveCells(currentWorld))
	res.CompletedTurns = completedTurns
	return
}

// Receive the world from the local controller, evolve it for the requested number of turns and send back the result
func (g *GolWorker) ProcessTurns(req stubs.ProcessTurnsRequest, res *stubs.ProcessTurnsResponse) (err error) {
	g.mu.Lock()
	g.world = req.World
	g.width, g.height = req.ImageWidth, req.ImageHeight
	g.mu.Unlock()

	for t := 0; t < req.Turns; t++ {
		g.mu.Lock()
		for g.paused && !g.quit {
			// sleep this go routine but keep mutex unlocked
			g.cond.Wait()
		}
		if g.quit {
			g.mu.Unlock()
			break
		}
		g.mu.Unlock()

		newWorld := executeTurn(g.world, req.ImageWidth, req.ImageHeight)
		g.mu.Lock()
		g.world = newWorld
		g.turn++
		g.mu.Unlock()
	}

	g.mu.Lock()
	finalWorld := g.world
	finalTurn := g.turn
	g.quit = false
	g.paused = false
	g.turn = 0
	g.mu.Unlock()

	res.World = finalWorld
	res.CompletedTurns = finalTurn
	res.AliveCells = aliveCells(g.world)
	fmt.Println("Processed all turns on the server")
	return
}

func (g *GolWorker) GetWorld(req stubs.GetWorldRequest, res *stubs.GetWorldResponse) (err error) {
	g.mu.Lock()
	res.World = g.world
	res.CompletedTurns = g.turn
	g.mu.Unlock()
	return
}

func (g *GolWorker) StopProcessing(req stubs.StopProcessingRequest, res *stubs.StopProcessingResponse) (err error) {
	g.mu.Lock()
	g.quit = true
	g.cond.Broadcast() // wake up paused loop to check if its paused again
	g.mu.Unlock()
	return
}

func (g *GolWorker) Shutdown(req stubs.ShutdownRequest, res *stubs.ShutdownResponse) (err error) {
	// check that we are already shutting down so two key presses of k dont cause the client to hang waiting for a response
	g.mu.Lock()
	if g.shuttingDown {
		g.mu.Unlock()
		return
	}
	g.shuttingDown = true
	g.mu.Unlock()

	g.shutdown <- true
	return
}

func (g *GolWorker) TogglePause(req stubs.TogglePauseRequest, res *stubs.TogglePauseResponse) (err error) {
	g.mu.Lock()
	g.paused = !g.paused
	res.Paused = g.paused
	res.CompletedTurns = g.turn
	g.cond.Broadcast()
	g.mu.Unlock()
	return
}

func main() {
	// Listen on some address for local controller requests
	pAddr := flag.String("port", ":8030", "Port to listen on")
	flag.Parse()

	worker := &GolWorker{shutdown: make(chan bool, 1)}
	worker.cond = sync.NewCond(&worker.mu)
	rpc.Register(worker)

	listener, err := net.Listen("tcp", *pAddr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	go rpc.Accept(listener)

	<-worker.shutdown
	listener.Close()
}
