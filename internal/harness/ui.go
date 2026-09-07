package harness

import "github.com/ericflo/eagent/internal/event"

// UI is what the runtime needs from a front end. All methods are called from
// the runtime's loop goroutine or actor goroutines; implementations must be
// safe for concurrent use.
type UI interface {
	// Narrate shows a user-visible message from the narrator.
	Narrate(text string)
	// Ask shows a question; the answer arrives through Input.
	Ask(id, text string, options []string)
	// Status updates the live status line.
	Status(s Status)
	// Trace reports an event for verbose display.
	Trace(ev event.Event)
	// Stream reports streaming activity for an actor ("" clears).
	Stream(actor, task, kind, delta string)
	// Log prints a diagnostic line.
	Log(format string, args ...any)
	// Input yields lines the user typed. Nil when non-interactive.
	Input() <-chan string
	// Idle tells the UI the orchestrator is waiting for the user.
	Idle(waiting bool)
}

// Status is a snapshot for the status line.
type Status struct {
	OrchestratorBusy bool
	OrchestratorFor  string // human duration of the current call
	NarratorBusy     bool
	TasksRunning     int
	TasksQueued      int
	Procs            int
	ContextTokens    int
	Rollover         bool
	Waiting          bool // orchestrator waiting on user
	Phone            bool // the session is mirrored to the user's phone
}
