package stubs

import "uk.ac.bris.cs/gameoflife/util"

// Controller to broker

const (
	ProcessTurnsHandler    = "Broker.ProcessTurns"
	CountAliveCellsHandler = "Broker.CountAliveCells"
	GetWorldHandler        = "Broker.GetWorld"
	TogglePauseHandler     = "Broker.TogglePause"
	StopProcessingHandler  = "Broker.StopProcessing"
	ShutdownHandler        = "Broker.Shutdown"
)

// ProcessTurns
type ProcessTurnsRequest struct {
	World       [][]byte
	ImageWidth  int
	ImageHeight int
	Turns       int
	Threads     int
}

type ProcessTurnsResponse struct {
	World          [][]byte
	CompletedTurns int
	AliveCells     []util.Cell
}

// CountAliveCells
type CountAliveCellsRequest struct{}

type CountAliveCellsResponse struct {
	CellsCount     int
	CompletedTurns int
}

// GetWorld
type GetWorldRequest struct{}

type GetWorldResponse struct {
	World          [][]byte
	CompletedTurns int
}

// TogglePause
type TogglePauseRequest struct{}

type TogglePauseResponse struct {
	Paused         bool
	CompletedTurns int
}

// StopProcessing
type StopProcessingRequest struct{}
type StopProcessingResponse struct{}

// Shutdown (also used by the broker to shut down each worker node)
type ShutdownRequest struct{}
type ShutdownResponse struct{}

// Broker to worker nodes

const (
	SubscribeHandler      = "Broker.Subscribe"
	CalculateStripHandler = "Worker.CalculateStrip"
	WorkerShutdownHandler = "Worker.Shutdown"
)

// Subscribe (worker to broker)
type Subscription struct {
	WorkerAddress string
}

type StatusReport struct {
	Message string
}

// CalculateStrip (broker to worker)
type StripJob struct {
	Rows    [][]byte
	Threads int
}

type StripResult struct {
	Rows [][]byte
}
