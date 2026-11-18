package stubs

import "uk.ac.bris.cs/gameoflife/util"

var ProcessTurnsHandler = "GolWorker.ProcessTurns"

type ProcessTurnsRequest struct {
	World       [][]byte
	ImageWidth  int
	ImageHeight int
	Turns       int
}

type ProcessTurnsResponse struct {
	World          [][]byte
	CompletedTurns int
	AliveCells     []util.Cell
}
