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

func getWorldFromServer(client *rpc.Client) (*stubs.GetWorldResponse, error) {
	response := new(stubs.GetWorldResponse)
	err := client.Call(stubs.GetWorldHandler, stubs.GetWorldRequest{}, response)
	return response, err
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

	request := stubs.ProcessTurnsRequest{
		World:       world,
		ImageWidth:  p.ImageWidth,
		ImageHeight: p.ImageHeight,
		Turns:       p.Turns,
	}
	response := new(stubs.ProcessTurnsResponse)

	// use go so we can listen to keys meanwhile
	call := client.Go(stubs.ProcessTurnsHandler, request, response, nil)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	quitting := false
	shutdown := false
	lastTurn := 0

	handleKey := func(key rune) {
		switch key {
		case 's':
			// TODO: GetWorld RPC -> writeImage(p, c, res.World, res.CompletedTurns)
			response, err := getWorldFromServer(client)
			if err != nil || response.World == nil {
				fmt.Println("could not get world from server:", err)
				return // skip writing the image
			}
			writeImage(p, c, response.World, response.CompletedTurns)
			lastTurn = response.CompletedTurns
		case 'k':
			quitting = true // also need to skip final turn processed event
			shutdown = true
			response := new(stubs.StopProcessingResponse)
			client.Call(stubs.StopProcessingHandler, stubs.StopProcessingRequest{}, response)
		case 'q':
			// can re-use the logic from the <-call.Done part to ensure we fully wait for the server to stop processing before writing an image
			quitting = true
			response := new(stubs.StopProcessingResponse)
			client.Call(stubs.StopProcessingHandler, stubs.StopProcessingRequest{}, response)
		case 'p':
			res := new(stubs.TogglePauseResponse)
			client.Call(stubs.TogglePauseHandler, stubs.TogglePauseRequest{}, res)
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
			// finished normally: the old end-of-function code moves in here
			writeImage(p, c, response.World, response.CompletedTurns)

			// we are ignoring potential errors sent from the server due to that processing terminating before the server can serve a reply
			// but this is tedious as there could be actual errors that we are missing
			if shutdown {
				response := new(stubs.ShutdownResponse)
				_ = client.Call(stubs.ShutDownRequestHandler, stubs.ShutdownRequest{}, response)
			}

			c.events <- StateChange{response.CompletedTurns, Quitting}
			close(c.events)
			return

		case <-ticker.C:
			// your existing CountAliveCells call + AliveCellsCount event moves here
			res := new(stubs.CountAliveCellsResponse)
			err := client.Call(stubs.CountAliveCellsHandler, stubs.CountAliveCellsRequest{}, res)
			lastTurn = res.CompletedTurns

			if err != nil {
				fmt.Println("alive cells count failed:", err)
				continue // skip this tick; call.Done will handle a dead server
			}
			c.events <- AliveCellsCount{CompletedTurns: res.CompletedTurns, CellsCount: res.CellsCount}

		case key := <-c.keyPresses:
			handleKey(key)
		}
	}
}
