package stubs

import "uk.ac.bris.cs/gameoflife/util"

// Controller to broker

const (
	StartGameHandler       = "Broker.StartGame"
	ProcessTurnsHandler    = "Broker.ProcessTurns"
	GetTurnUpdatesHandler  = "Broker.GetTurnUpdates"
	CountAliveCellsHandler = "Broker.CountAliveCells"
	GetWorldHandler        = "Broker.GetWorld"
	TogglePauseHandler     = "Broker.TogglePause"
	StopProcessingHandler  = "Broker.StopProcessing"
	ShutdownHandler        = "Broker.Shutdown"
)

// the request for every call that only needs to say which game it is for:
// GetTurnUpdates, CountAliveCells, GetWorld, TogglePause and StopProcessing
type GameRequest struct {
	GameID int64
}

// StartGame
type StartGameRequest struct {
	World       [][]byte
	ImageWidth  int
	ImageHeight int
	Threads     int
}

type StartGameResponse struct {
	GameID int64
}

// ProcessTurns
type ProcessTurnsRequest struct {
	GameID int64
	Turns  int
}

type ProcessTurnsResponse struct {
	World          [][]byte
	CompletedTurns int
	AliveCells     []util.Cell
}

// GetTurnUpdates (live view)
type TurnUpdate struct {
	Turn    int
	Flipped []util.Cell
}

type GetTurnUpdatesResponse struct {
	Updates  []TurnUpdate
	Finished bool // no more turns coming
}

// CountAliveCells
type CountAliveCellsResponse struct {
	CellsCount     int
	CompletedTurns int
}

// GetWorld
type GetWorldResponse struct {
	World          [][]byte
	CompletedTurns int
}

// TogglePause
type TogglePauseResponse struct {
	Paused         bool
	CompletedTurns int
}

// StopProcessing
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
	StartY  int
	Threads int
}

type StripResult struct {
	Rows    [][]byte
	Flipped []util.Cell
}
