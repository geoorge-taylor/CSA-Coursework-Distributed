package main

import (
	"testing"

	"uk.ac.bris.cs/gameoflife/stubs"
)

func makePattern(size int, pattern string) [][]byte {
	world := make([][]byte, size)

	for y := range world {
		world[y] = make([]byte, size)
	}

	// settled should have less flipped cells than active
	for y := 3; y < size-2; y += 8 {
		for x := 3; x < size-2; x += 8 {
			switch pattern {
			case "settled":
				// 2x2 block, each cell has 3 living neighbours so it survives,
				// every other dead cell has <3 living neighbours so they all stay dead
				// hence, no flips
				world[y][x] = alive
				world[y][x+1] = alive
				world[y+1][x] = alive
				world[y+1][x+1] = alive

			case "active":
				// 3 cells in a line, the outer two cells have 1 living neighbour so dies,
				// the centre has 2 living neighbours so it survives
				// the cells above and below the centre have 3 alive neighbours so become alive
				// 4 cells flip each generation
				world[y][x-1] = alive
				world[y][x] = alive
				world[y][x+1] = alive
			}
		}
	}

	return world
}

// withHalo gives the whole world as one strip, with the wrapped halo rows the
// broker would send (last row above, first row below).
func withHalo(world [][]byte) [][]byte {
	rows := make([][]byte, 0, len(world)+2)
	rows = append(rows, world[len(world)-1])
	rows = append(rows, world...)
	return append(rows, world[0])
}

func countFlips(oldWorld, newWorld [][]byte) int {
	flips := 0
	for y := range oldWorld {
		for x := range oldWorld[y] {
			if oldWorld[y][x] != newWorld[y][x] {
				flips++
			}
		}
	}
	return flips
}

// BenchmarkPatternWorker times one worker node's goroutine pool on a board
// where nothing changes (settled) and one where cells flip every turn (active).
// No RPC is involved, so this is the pure computation cost on a node.
func BenchmarkPatternWorker(b *testing.B) {
	const (
		size    = 512
		turns   = 100
		threads = 8
	)

	patterns := []string{"settled", "active"}

	for _, pattern := range patterns {
		b.Run(pattern, func(b *testing.B) {
			pool := newWorkerPool(threads, size)
			defer stopPool(pool)

			totalFlips := 0
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// exclude board generation from timing
				b.StopTimer()
				world := makePattern(size, pattern)
				b.StartTimer()

				for turn := 0; turn < turns; turn++ {
					next := nextTurn(pool, stubs.StripJob{Rows: withHalo(world), Threads: threads})

					// counting flips is not part of the work being measured
					b.StopTimer()
					totalFlips += countFlips(world, next)
					b.StartTimer()

					world = next
				}
			}
			b.StopTimer()

			b.ReportMetric(float64(totalFlips)/float64(b.N*turns), "flips/turn")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*turns), "ns/turn")
		})
	}
}
