package gol

import (
	"flag"
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

var brokerAddr = flag.String("broker", "127.0.0.1:8030", "Address of broker instance")

// distributor divides the work between workers and interacts with other goroutines.
func loadWorld(p Params, c distributorChannels) [][]byte {
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

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

func getWorldFromServer(broker *rpc.Client) (*stubs.GetWorldResponse, error) {
	response := new(stubs.GetWorldResponse)
	err := broker.Call(stubs.GetWorldHandler, stubs.GetWorldRequest{}, response)
	return response, err
}

func distributor(p Params, c distributorChannels) {
	world := loadWorld(p, c)
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	broker, err := rpc.Dial("tcp", *brokerAddr)
	if err != nil {
		panic(err)
	}
	defer broker.Close()

	request := stubs.ProcessTurnsRequest{
		World:       world,
		ImageWidth:  p.ImageWidth,
		ImageHeight: p.ImageHeight,
		Turns:       p.Turns,
		Threads:     p.Threads,
	}
	response := new(stubs.ProcessTurnsResponse)

	// use go so we can listen to keys meanwhile
	call := broker.Go(stubs.ProcessTurnsHandler, request, response, nil)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	quitting := false
	shutdown := false
	lastTurn := 0

	handleKey := func(key rune) {
		switch key {
		case 's':
			response, err := getWorldFromServer(broker)
			if err != nil || response.World == nil {
				fmt.Println("could not get world from server:", err)
				return
			}
			writeImage(p, c, response.World, response.CompletedTurns)
			lastTurn = response.CompletedTurns
		case 'k':
			quitting = true
			shutdown = true
			response := new(stubs.StopProcessingResponse)
			broker.Call(stubs.StopProcessingHandler, stubs.StopProcessingRequest{}, response)
		case 'q':
			quitting = true
			response := new(stubs.StopProcessingResponse)
			broker.Call(stubs.StopProcessingHandler, stubs.StopProcessingRequest{}, response)
		case 'p':
			res := new(stubs.TogglePauseResponse)
			broker.Call(stubs.TogglePauseHandler, stubs.TogglePauseRequest{}, res)
			lastTurn = res.CompletedTurns
			if res.Paused {
				fmt.Println(res.CompletedTurns)
				c.events <- StateChange{res.CompletedTurns, Paused}
			} else {
				fmt.Println("Continuing")
				c.events <- StateChange{res.CompletedTurns, Executing}
			}
		}
	}

	for {
		select {
		case <-call.Done:
			if call.Error != nil {
				fmt.Println("lost connection to server:", call.Error)
				c.events <- StateChange{lastTurn, Quitting}
				close(c.events)
				return
			}

			// if controller never quit then the final turn was completed
			if !quitting {
				c.events <- FinalTurnComplete{CompletedTurns: response.CompletedTurns, Alive: response.AliveCells}
			}

			writeImage(p, c, response.World, response.CompletedTurns)

			// we are ignoring potential errors sent from the server due to that processing terminating before the server can serve a reply
			// but this is tedious as there could be actual errors that we are missing
			if shutdown {
				response := new(stubs.ShutdownResponse)
				_ = broker.Call(stubs.ShutdownHandler, stubs.ShutdownRequest{}, response)
			}

			c.events <- StateChange{response.CompletedTurns, Quitting}
			close(c.events)
			return

		case <-ticker.C:
			res := new(stubs.CountAliveCellsResponse)
			err := broker.Call(stubs.CountAliveCellsHandler, stubs.CountAliveCellsRequest{}, res)
			lastTurn = res.CompletedTurns

			if err != nil {
				fmt.Println("alive cells count failed:", err)
				continue
			}
			c.events <- AliveCellsCount{CompletedTurns: res.CompletedTurns, CellsCount: res.CellsCount}

		case key := <-c.keyPresses:
			handleKey(key)
		}
	}
}
