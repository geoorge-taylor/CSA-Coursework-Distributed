package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"

	"uk.ac.bris.cs/gameoflife/stubs"
)

// ---- Types and constants ----

const (
	alive byte = 255
	dead  byte = 0
)

type Worker struct {
	shutdown chan bool
}

// ---- Helpers ----

func countLiveNeighbours(world [][]byte, width, height, row, column int) int {
	count := 0
	for i := -1; i < 2; i++ {
		for j := -1; j < 2; j++ {
			if i == 0 && j == 0 {
				continue
			}
			neighbourRow := i + row
			neighbourCol := j + column
			if neighbourRow < 0 {
				neighbourRow = height - 1
			}
			if neighbourCol < 0 {
				neighbourCol = width - 1
			}
			if neighbourRow >= height {
				neighbourRow = 0
			}
			if neighbourCol >= width {
				neighbourCol = 0
			}
			if world[neighbourRow][neighbourCol] == alive {
				count++
			}
		}
	}
	return count
}

// calculateStrip returns the next state of the strip inside rows.
// The first and last rows are halo rows: they are read as neighbours but not
// calculated, which is why the loops start at 1 and end at height-1.
func calculateStrip(rows [][]byte) [][]byte {
	height := len(rows)
	width := len(rows[0])

	newStrip := make([][]byte, height-2)
	for y := 1; y < height-1; y++ {
		newStrip[y-1] = make([]byte, width)
		copy(newStrip[y-1], rows[y])
	}

	for y := 1; y < height-1; y++ {
		for x := 0; x < width; x++ {
			liveNeighbours := countLiveNeighbours(rows, width, height, y, x)
			if rows[y][x] == alive && (liveNeighbours < 2 || liveNeighbours > 3) {
				// y-1 sets index back to start at 0, since the first row was ignored
				newStrip[y-1][x] = dead
			} else if rows[y][x] == dead && liveNeighbours == 3 {
				newStrip[y-1][x] = alive
			}
		}
	}
	return newStrip
}

// ---- RPC methods called by the broker ----

// CalculateStrip is the callback the broker invokes for each strip.
// job.Rows is the strip plus the extra halo rows so the whole strip can be calculated
func (w *Worker) CalculateStrip(job stubs.StripJob, res *stubs.StripResult) (err error) {
	res.Rows = calculateStrip(job.Rows)
	return
}

// Shutdown is called by the broker when the controller presses k.
// It wakes main, which then returns and ends the program.
func (w *Worker) Shutdown(req stubs.ShutdownRequest, res *stubs.ShutdownResponse) (err error) {
	w.shutdown <- true
	return
}

// ---- Entry point ----

func main() {
	pAddr := flag.String("ip", "127.0.0.1:8040", "IP and port to listen on")
	brokerAddr := flag.String("broker", "127.0.0.1:8030", "Address of broker instance")
	flag.Parse()

	// 1. Connect to the broker first
	client, err := rpc.Dial("tcp", *brokerAddr)
	if err != nil {
		fmt.Println("Could not reach broker:", err)
		return
	}
	defer client.Close()

	// 2. Create the worker and register it
	worker := &Worker{shutdown: make(chan bool, 1)}
	rpc.Register(worker)

	// 3. Start listening, so the broker can connect back
	listener, err := net.Listen("tcp", *pAddr)
	if err != nil {
		fmt.Println("Could not listen on own port:", err)
		return
	}
	defer listener.Close()
	myAddr := listener.Addr().String()

	// 4. Subscribe to the broker
	subscription := stubs.Subscription{
		WorkerAddress: myAddr,
	}

	status := new(stubs.StatusReport)
	err = client.Call(stubs.SubscribeHandler, subscription, status)
	if err != nil {
		fmt.Println("Error subscribing to broker:", err)
		return
	}

	// 5. Handle incoming jobs in the background
	fmt.Println("Listening for jobs from broker")
	go rpc.Accept(listener)

	// 6. Wait until the broker tells this worker to shut down
	<-worker.shutdown
}
