package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"sync"

	"uk.ac.bris.cs/gameoflife/stubs"
)

const (
	alive byte = 255
	dead  byte = 0
)

type Worker struct {
	shutdown chan bool
	mu       sync.Mutex
	pool     *workerPool
}

// one goroutine inside this worker node, responsible for a fixed piece of the strip
type worker struct {
	startY int
	endY   int
	work   chan stubs.StripJob // receives the current strip (with halo rows)
	result chan [][]byte
}

// workerPool is a fixed set of goroutines reused for every turn.
type workerPool struct {
	threads     int
	stripHeight int
	workers     []worker
	done        chan bool
}

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

// calculateStrip returns the next state of rows[startY:endY].
// rows includes a halo row above and below, so startY >= 1 and endY <= len(rows)-1.
func calculateStrip(rows [][]byte, startY, endY int) [][]byte {
	height := len(rows)
	width := len(rows[0])

	newStrip := make([][]byte, endY-startY)
	for y := range newStrip {
		newStrip[y] = make([]byte, width)
		copy(newStrip[y], rows[startY+y])
	}

	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			liveNeighbours := countLiveNeighbours(rows, width, height, y, x)
			if rows[y][x] == alive && (liveNeighbours < 2 || liveNeighbours > 3) {
				// y-startY sets index back to start at 0
				newStrip[y-startY][x] = dead
			} else if rows[y][x] == dead && liveNeighbours == 3 {
				newStrip[y-startY][x] = alive
			}
		}
	}
	return newStrip
}

func runWorker(w worker, done chan<- bool) {
	// range blocks until work arrives, and the loop ends when the channel is closed
	for job := range w.work {
		w.result <- calculateStrip(job.Rows, w.startY, w.endY)
	}
	done <- true
}

func newWorkerPool(threads, stripHeight int) *workerPool {
	pool := &workerPool{
		threads:     threads,
		stripHeight: stripHeight,
		workers:     make([]worker, threads),
		done:        make(chan bool),
	}

	for i := 0; i < threads; i++ {
		w := worker{
			// +1 skips the halo row at the top of job.Rows
			startY: 1 + i*stripHeight/threads,
			endY:   1 + (i+1)*stripHeight/threads,
			work:   make(chan stubs.StripJob),
			result: make(chan [][]byte),
		}
		pool.workers[i] = w
		go runWorker(w, pool.done)
	}

	return pool
}

func nextTurn(pool *workerPool, job stubs.StripJob) [][]byte {
	for _, w := range pool.workers {
		w.work <- job
	}

	// append back in order via waiting on result channel in each worker
	newStrip := make([][]byte, 0, pool.stripHeight)
	for _, w := range pool.workers {
		newStrip = append(newStrip, <-w.result...)
	}
	return newStrip
}

func stopPool(pool *workerPool) {
	for _, w := range pool.workers {
		close(w.work)
	}
	for range pool.workers {
		<-pool.done
	}
}

// RPC methods called by the broker

// calculateStrip is the callback the broker invokes for each strip.
func (w *Worker) CalculateStrip(job stubs.StripJob, res *stubs.StripResult) (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stripHeight := len(job.Rows) - 2
	threads := job.Threads
	if threads > stripHeight {
		threads = stripHeight
	}
	if threads < 1 {
		threads = 1
	}

	// the pool is reused every turn, and only rebuilt when a new run changes its shape i.e number of threads
	// also if the image size changes then the worker pool also needs to be changed
	if w.pool == nil || w.pool.threads != threads || w.pool.stripHeight != stripHeight {
		if w.pool != nil {
			// if a pool exists we need to change it so stop it
			stopPool(w.pool)
		}
		w.pool = newWorkerPool(threads, stripHeight)
	}

	res.Rows = nextTurn(w.pool, job)
	return
}

// Shutdown is called by the broker when the controller presses k.
// stops the pool's goroutines, then wakes main, which returns and ends the program.
func (w *Worker) Shutdown(req stubs.ShutdownRequest, res *stubs.ShutdownResponse) (err error) {
	w.mu.Lock()
	if w.pool != nil {
		stopPool(w.pool)
		w.pool = nil
	}
	w.mu.Unlock()
	w.shutdown <- true
	return
}

func main() {
	pAddr := flag.String("ip", "127.0.0.1:8040", "IP and port to listen on")
	brokerAddr := flag.String("broker", "127.0.0.1:8030", "Address of broker instance")
	flag.Parse()

	client, err := rpc.Dial("tcp", *brokerAddr)
	if err != nil {
		fmt.Println("Could not reach broker:", err)
		return
	}
	defer client.Close()

	worker := &Worker{shutdown: make(chan bool, 1)}
	rpc.Register(worker)

	listener, err := net.Listen("tcp", *pAddr)
	if err != nil {
		fmt.Println("Could not listen on own port:", err)
		return
	}
	defer listener.Close()
	myAddr := listener.Addr().String()

	subscription := stubs.Subscription{
		WorkerAddress: myAddr,
	}

	status := new(stubs.StatusReport)
	err = client.Call(stubs.SubscribeHandler, subscription, status)
	if err != nil {
		fmt.Println("Error subscribing to broker:", err)
		return
	}

	fmt.Println("Listening for jobs from broker")
	go rpc.Accept(listener)

	<-worker.shutdown
}
