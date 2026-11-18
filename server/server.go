package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

type GolWorker struct{}

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

// Receive the world from the local controller, evolve it for the requested number of turns and send back the result
func (g *GolWorker) ProcessTurns(req stubs.ProcessTurnsRequest, res *stubs.ProcessTurnsResponse) (err error) {
	// nothing else reads the world while this runs, so local variables are enough
	world := req.World
	turn := 0

	for t := 0; t < req.Turns; t++ {
		world = executeTurn(world, req.ImageWidth, req.ImageHeight)
		turn++
	}

	res.World = world
	res.CompletedTurns = turn
	res.AliveCells = aliveCells(world)
	fmt.Println("Processed all turns on the server")
	return
}

func main() {
	// Listen on some address for local controller requests
	pAddr := flag.String("port", ":8030", "Port to listen on")
	flag.Parse()

	rpc.Register(&GolWorker{})

	listener, err := net.Listen("tcp", *pAddr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	rpc.Accept(listener)
}
