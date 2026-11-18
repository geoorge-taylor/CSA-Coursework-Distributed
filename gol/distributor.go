package gol

import (
	"fmt"
	"net/rpc"

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

func distributor(p Params, c distributorChannels) {
	world := loadWorld(p, c)
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	// connect, need to change to the AWS port when set up
	client, err := rpc.Dial("tcp", "127.0.0.1:8030")
	if err != nil {
		panic(err)
	}
	defer client.Close()

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

	// extract
	completedTurns := response.CompletedTurns

	c.events <- FinalTurnComplete{CompletedTurns: completedTurns, Alive: response.AliveCells}

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{completedTurns, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
