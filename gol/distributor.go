package gol

import (
	"flag"
	"fmt"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
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

func getInitialFlippedCells(world [][]byte) []util.Cell {
	initial := []util.Cell{}
	for y, row := range world {
		for x, cell := range row {
			if cell == 255 {
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

	// Make sure that the Io has finished any output before carrying on.
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
	initial := getInitialFlippedCells(world)

	c.events <- CellsFlipped{CompletedTurns: 0, Cells: initial}
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	// connect, need to change to the AWS port when set up
	broker, err := rpc.Dial("tcp", *brokerAddr)
	if err != nil {
		panic(err)
	}
	defer broker.Close()

	// unique id for this game, so the broker can tell this controller's requests apart from an older one's
	runID := time.Now().UnixNano()
	request := stubs.ProcessTurnsRequest{
		RunID:       runID,
		World:       world,
		ImageWidth:  p.ImageWidth,
		ImageHeight: p.ImageHeight,
		Turns:       p.Turns,
		Threads:     p.Threads,
	}
	response := new(stubs.ProcessTurnsResponse)

	// use go so we can listen to keys meanwhile
	call := broker.Go(stubs.ProcessTurnsHandler, request, response, nil)

	// start listening to get turn update requests and push the events onto the channels
	done := make(chan bool, 1)
	go func() {
		for {
			res := new(stubs.GetTurnUpdatesResponse)
			err := broker.Call(stubs.GetTurnUpdatesHandler, stubs.GetTurnUpdatesRequest{RunID: runID}, res)
			if err != nil {
				fmt.Println("live view stopped:", err)
				done <- true
				return
			}
			for _, u := range res.Updates {
				if len(u.Flipped) > 0 {
					c.events <- CellsFlipped{CompletedTurns: u.Turn, Cells: u.Flipped}
				}
				c.events <- TurnComplete{CompletedTurns: u.Turn}
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

	handleKey := func(key rune) {
		switch key {
		case 's':
			// TODO: GetWorld RPC -> writeImage(p, c, res.World, res.CompletedTurns)
			response, err := getWorldFromServer(broker)
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
			broker.Call(stubs.StopProcessingHandler, stubs.StopProcessingRequest{}, response)
		case 'q':
			// can re-use the logic from the <-call.Done part to ensure we fully wait for the server to stop processing before writing an image
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
			<-done // wait until every TurnComplete has been sent before sending final turn complete

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
				_ = broker.Call(stubs.ShutdownHandler, stubs.ShutdownRequest{}, response)
			}

			c.events <- StateChange{response.CompletedTurns, Quitting}
			close(c.events)
			return

		case <-ticker.C:
			// your existing CountAliveCells call + AliveCellsCount event moves here
			res := new(stubs.CountAliveCellsResponse)
			err := broker.Call(stubs.CountAliveCellsHandler, stubs.CountAliveCellsRequest{}, res)
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
