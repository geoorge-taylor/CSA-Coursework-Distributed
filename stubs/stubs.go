package stubs

import "uk.ac.bris.cs/gameoflife/util"

var ProcessTurnsHandler = "GolWorker.ProcessTurns"
var CountAliveCellsHandler = "GolWorker.CountAliveCells"
var GetWorldHandler = "GolWorker.GetWorld"
var StopProcessingHandler = "GolWorker.StopProcessing"
var ShutDownRequestHandler = "GolWorker.Shutdown"
var TogglePauseHandler = "GolWorker.TogglePause"

type StopProcessingResponse struct{}
type StopProcessingRequest struct{}
type TogglePauseRequest struct{}

type ShutdownRequest struct{}
type ShutdownResponse struct{}

type GetWorldRequest struct{}

type TogglePauseResponse struct {
	Paused         bool
	CompletedTurns int
}

type GetWorldResponse struct {
	World          [][]byte
	CompletedTurns int
}

type CountAliveCellsRequest struct{}

type CountAliveCellsResponse struct {
	CellsCount     int
	CompletedTurns int
}

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
