package stubs

import "uk.ac.bris.cs/gameoflife/util"

// ---- Controller <-> broker ----

const (
	ProcessTurnsHandler    = "Broker.ProcessTurns"
	CountAliveCellsHandler = "Broker.CountAliveCells"
	GetWorldHandler        = "Broker.GetWorld"
	TogglePauseHandler     = "Broker.TogglePause"
	StopProcessingHandler  = "Broker.StopProcessing"
	ShutdownHandler        = "Broker.Shutdown"
)

type ProcessTurnsRequest struct {
	World       [][]byte
	ImageWidth  int
	ImageHeight int
	Turns       int
	Threads     int
	RunID       int64 // identifies this controller's game, so stale pollers can be told apart
}

type ProcessTurnsResponse struct {
	World          [][]byte
	CompletedTurns int
	AliveCells     []util.Cell
}

type CountAliveCellsRequest struct{}

type CountAliveCellsResponse struct {
	CellsCount     int
	CompletedTurns int
}

type GetWorldRequest struct{}

type GetWorldResponse struct {
	World          [][]byte
	CompletedTurns int
}

type TogglePauseRequest struct{}

type TogglePauseResponse struct {
	Paused         bool
	CompletedTurns int
}

type StopProcessingRequest struct{}
type StopProcessingResponse struct{}

// Also used by the broker when it shuts down a worker node.
type ShutdownRequest struct{}
type ShutdownResponse struct{}

// ---- Broker <-> worker node ----

const (
	// Called by a worker node on the broker to register itself.
	SubscribeHandler = "Broker.Subscribe"

	// The callback the broker invokes on a worker node for each strip.
	CalculateStripHandler = "Worker.CalculateStrip"

	// Called by the broker on a worker node to make it exit.
	WorkerShutdownHandler = "Worker.Shutdown"
)

// Sent by a worker node to the broker when it starts up.
type Subscription struct {
	WorkerAddress string // address the broker should dial back, e.g. "172.31.0.5:8040"
}

type StatusReport struct {
	Message string
}

// One strip of work. Rows holds the strip plus one extra row above and one below
// (already wrapped by the broker), so len(Rows) is the strip height + 2.
type StripJob struct {
	Rows    [][]byte
	StartY  int
	Threads int
}

// The next state of a strip, without the two extra rows.
type StripResult struct {
	Rows    [][]byte
	Flipped []util.Cell
}

// stubs.go
const GetTurnUpdatesHandler = "Broker.GetTurnUpdates"

type TurnUpdate struct {
	Turn    int
	Flipped []util.Cell
}
type GetTurnUpdatesRequest struct {
	RunID int64 // must match the RunID sent in ProcessTurnsRequest
}
type GetTurnUpdatesResponse struct {
	Updates  []TurnUpdate
	Finished bool // no more turns coming
}
