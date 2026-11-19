package tests

import (
	"os"
	"runtime/trace"
	"testing"

	"uk.ac.bris.cs/gameoflife/gol"
)

// TestGolTrace records an execution trace of the local controller while a
// 512x512 game runs on a private broker with 2 worker nodes.
// View it with: go tool trace trace-distributed.out
func TestGolTrace(t *testing.T) {
	stop := startCluster(t, 2)
	defer stop()

	f, err := os.Create("trace-distributed.out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := trace.Start(f); err != nil {
		t.Fatal(err)
	}
	defer trace.Stop()

	p := gol.Params{
		ImageWidth:  512,
		ImageHeight: 512,
		Turns:       100,
		Threads:     4,
	}

	events := make(chan gol.Event)
	go gol.Run(p, events, nil)
	for range events {
	}
}
