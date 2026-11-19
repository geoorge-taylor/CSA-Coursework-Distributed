package gol

import (
	"flag"
	"fmt"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

const alive byte = 255

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

// flag needed to be put here at package level to fix error of flag being redefined when distributor was run many times
var brokerAddr = flag.String("broker", "127.0.0.1:8030", "Address of broker instance")

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

func getInitialFlippedCells(world [][]byte) []util.Cell {
	initial := []util.Cell{}
	for y, row := range world {
		for x, cell := range row {
			if cell == alive {
				initial = append(initial, util.Cell{X: x, Y: y})
			}
		}
	}
	return initial
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

	// make sure that the Io has finished any output before carrying on.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

func getWorldFromServer(broker *rpc.Client, gameID int64) (*stubs.GetWorldResponse, error) {
	response := new(stubs.GetWorldResponse)
	err := broker.Call(stubs.GetWorldHandler, stubs.GameRequest{GameID: gameID}, response)
	return response, err
}

func distributor(p Params, c distributorChannels) {
	world := loadWorld(p, c)
	initial := getInitialFlippedCells(world)

	c.events <- CellsFlipped{CompletedTurns: 0, Cells: initial}
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	broker, err := rpc.Dial("tcp", *brokerAddr)
	if err != nil {
		panic(err)
	}
	defer broker.Close()

	// start a fresh game on the broker and get its id, this also stops any game an old controller left running
	startResponse := new(stubs.StartGameResponse)
	err = broker.Call(stubs.StartGameHandler, stubs.StartGameRequest{
		World:       world,
		ImageWidth:  p.ImageWidth,
		ImageHeight: p.ImageHeight,
		Threads:     p.Threads,
	}, startResponse)

	if err != nil {
		fmt.Println("could not start game:", err)
		c.events <- StateChange{0, Quitting}
		close(c.events)
		return
	}

	gameID := startResponse.GameID

	request := stubs.ProcessTurnsRequest{GameID: gameID, Turns: p.Turns}
	response := new(stubs.ProcessTurnsResponse)

	// use go so we can listen to keys meanwhile and also get turn updates
	call := broker.Go(stubs.ProcessTurnsHandler, request, response, nil)

	// start listening to get turn update requests and push the events onto the channels
	// this is for the live sdl view extension
	done := make(chan bool, 1)
	go func() {
		for {
			res := new(stubs.GetTurnUpdatesResponse)
			err := broker.Call(stubs.GetTurnUpdatesHandler, stubs.GameRequest{GameID: gameID}, res)
			if err != nil {
				fmt.Println("live view stopped:", err)
				done <- true
				return
			}
			for _, update := range res.Updates {
				if len(update.Flipped) > 0 {
					c.events <- CellsFlipped{CompletedTurns: update.Turn, Cells: update.Flipped}
				}
				c.events <- TurnComplete{CompletedTurns: update.Turn}
			}
			if res.Finished {
				done <- true
				return
			}
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	quitting := false
	shutdown := false
	lastTurn := 0

	// can re-use the logic from the <-call.Done part to ensure we fully wait for the server to stop processing before writing an image
	handleKey := func(key rune) {
		switch key {
		case 's':
			response, err := getWorldFromServer(broker, gameID)
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
			broker.Call(stubs.StopProcessingHandler, stubs.GameRequest{GameID: gameID}, response)
		case 'q':
			quitting = true
			response := new(stubs.StopProcessingResponse)
			broker.Call(stubs.StopProcessingHandler, stubs.GameRequest{GameID: gameID}, response)
		case 'p':
			res := new(stubs.TogglePauseResponse)
			broker.Call(stubs.TogglePauseHandler, stubs.GameRequest{GameID: gameID}, res)
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
		// runs when the go call to process turns finishes (i.e server has stopped processing turns)
		case <-call.Done:
			<-done // wait until every TurnComplete for the live sdl has been sent before sending final turn complete

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
			// this is an issue with fault tollerance but still passes all the tests as its a rare issue
			if shutdown {
				response := new(stubs.ShutdownResponse)
				_ = broker.Call(stubs.ShutdownHandler, stubs.ShutdownRequest{}, response)
			}

			c.events <- StateChange{response.CompletedTurns, Quitting}
			close(c.events)
			return

		case <-ticker.C:
			res := new(stubs.CountAliveCellsResponse)
			err := broker.Call(stubs.CountAliveCellsHandler, stubs.GameRequest{GameID: gameID}, res)
			lastTurn = res.CompletedTurns

			if err != nil {
				fmt.Println("alive cells count failed:", err)
			} else {
				c.events <- AliveCellsCount{CompletedTurns: res.CompletedTurns, CellsCount: res.CellsCount}
			}

		case key := <-c.keyPresses:
			handleKey(key)
		}
	}
}
