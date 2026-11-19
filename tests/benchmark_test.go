package tests

import (
	"fmt"
	"testing"

	"uk.ac.bris.cs/gameoflife/gol"
)

// runGol runs one full game through the controller, broker and worker nodes.
func runGol(p gol.Params) {
	events := make(chan gol.Event)
	go gol.Run(p, events, nil)
	// drain events until the controller closes the channel
	for range events {
	}
}

// runGolTimed runs one full game but only times the processing: from the
// controller's first Executing event (image already loaded) to FinalTurnComplete
// (before the final image is written). The provided io.go reads and writes the
// image one byte at a time, which would otherwise dominate the time on big boards.
func runGolTimed(b *testing.B, p gol.Params) {
	events := make(chan gol.Event)
	go gol.Run(p, events, nil)

	started := false
	for event := range events {
		switch e := event.(type) {
		case gol.StateChange:
			if e.NewState == gol.Executing && !started {
				started = true
				b.StartTimer()
			}
		case gol.FinalTurnComplete:
			b.StopTimer()
		}
	}
}

// BenchmarkDistributed times 100 turns on three board sizes for every
// combination of worker nodes and goroutines per worker node (gol.Params.Threads).
// 512x512 is provided; generate 768x768 and 1024x1024 first with: python3 benchmarks/make_images.py
// Turn the output into tables with: python3 benchmarks/table.py benchmarks/results.txt
func BenchmarkDistributed(b *testing.B) {
	sizes := []int{512, 768, 1024}
	workerCounts := []int{1, 2, 4, 8}
	threadCounts := []int{1, 2, 4, 8}

	if awsMode() {
		// t3.medium workers have 2 vCPUs, so 4 threads already oversubscribes them
		threadCounts = []int{1, 2, 4}
		workerCounts = nil
		for n := 1; n <= len(awsWorkerHosts(b)); n *= 2 {
			workerCounts = append(workerCounts, n)
		}
	}

	for i, workers := range workerCounts {
		// a fresh broker per worker count, since the broker never forgets a worker
		stop := startCluster(b, workers)

		// warm-up run: the first game on a fresh cluster is much slower (connections, pool creation)
		runGol(gol.Params{ImageWidth: 512, ImageHeight: 512, Turns: 10, Threads: 1})

		// 0 turns: only the cost of sending the board to the broker and back,
		// which matters when the broker is across the internet on AWS
		if i == 0 {
			for _, size := range sizes {
				p := gol.Params{ImageWidth: size, ImageHeight: size, Turns: 0, Threads: 1}
				b.Run(fmt.Sprintf("%dx%d/transfer-only", size, size), func(b *testing.B) {
					b.StopTimer()
					for i := 0; i < b.N; i++ {
						runGolTimed(b, p)
					}
				})
			}
		}

		for _, size := range sizes {
			for _, threads := range threadCounts {
				p := gol.Params{
					ImageWidth:  size,
					ImageHeight: size,
					Turns:       100,
					Threads:     threads,
				}
				name := fmt.Sprintf("%dx%d/workers=%d/threads=%d", size, size, workers, threads)
				b.Run(name, func(b *testing.B) {
					b.StopTimer()
					for i := 0; i < b.N; i++ {
						runGolTimed(b, p)
					}
				})
			}
		}

		stop()
	}
}
