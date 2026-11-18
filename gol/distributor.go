package gol

import (
	"fmt"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/stubs"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

// distributor divides the work between workers and interacts with other goroutines.

func loadWorld(p Params, c distributorChannels) [][]byte {
	// Request initial image
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)

	world := make([][]byte, p.ImageHeight)
	for y := 0; y < p.ImageHeight; y++ {
		world[y] = make([]byte, p.ImageWidth)
		for x := 0; x < p.ImageWidth; x++ {
			world[y][x] = <-c.ioInput
		}
	}

	return world
}

func writeImage(p Params, c distributorChannels, world [][]byte, turn int) {
	filename := fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, turn)

	c.ioCommand <- ioOutput
	c.ioFilename <- filename
	for _, row := range world {
		for _, cell := range row {
			c.ioOutput <- cell
		}
	}

	// Make sure that the Io has finished any output before carrying on.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

func distributor(p Params, c distributorChannels) {
	world := loadWorld(p, c)
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	// connect, need to change to the AWS port when set up
	client, err := rpc.Dial("tcp", "127.0.0.1:8030")
	if err != nil {
		panic(err)
	}
	defer client.Close()

	done := make(chan bool) // unbuffered

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				res := new(stubs.CountAliveCellsResponse)
				if err := client.Call(stubs.CountAliveCellsHandler, stubs.CountAliveCellsRequest{}, res); err != nil {
					return
				}
				c.events <- AliveCellsCount{CompletedTurns: res.CompletedTurns, CellsCount: res.CellsCount}
			}
		}
	}()

	// build request
	request := stubs.ProcessTurnsRequest{
		World:       world,
		ImageWidth:  p.ImageWidth,
		ImageHeight: p.ImageHeight,
		Turns:       p.Turns,
	}

	// pointer to the response
	response := new(stubs.ProcessTurnsResponse)

	// call (blocks until the server returns)
	err = client.Call(stubs.ProcessTurnsHandler, request, response)
	if err != nil {
		panic(err)
	}

	done <- true // blocks until the ticker goroutine receives it

	// extract
	finalWorld := response.World
	completedTurns := response.CompletedTurns

	c.events <- FinalTurnComplete{CompletedTurns: completedTurns, Alive: response.AliveCells}
	writeImage(p, c, finalWorld, completedTurns)

	c.events <- StateChange{completedTurns, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
