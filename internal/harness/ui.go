package harness

import "github.com/ericflo/eagent/internal/event"

// UI is what the runtime needs from a front end. All methods are called from
// the runtime's loop goroutine or from a goroutine making a model call;
// implementations must be safe for concurrent use.
type UI interface {
	// Say shows something eagent told the user.
	Say(text string)
	// Ask shows a question; the answer arrives through Input.
	Ask(id, text string, options []string)
	// Status updates the live status line.
	Status(s Status)
	// Trace reports an event for verbose display.
	Trace(ev event.Event)
	// Stream reports the model's output as it arrives: kind is "text",
	// "reasoning" or "tool", and "" clears the display.
	Stream(kind, delta string)
	// Log prints a diagnostic line.
	Log(format string, args ...any)
	// Input yields lines the user typed. Nil when non-interactive.
	Input() <-chan string
	// Idle tells the UI whether the session is waiting for the user.
	Idle(waiting bool)
}

// Status is a snapshot for the status line.
type Status struct {
	// Busy says eagent's model is being called; BusyFor is how long the
	// call has run, for people ("12s").
	Busy    bool
	BusyFor string
	// Agents are the session's running agents with what each is doing.
	Agents []AgentStatus
	// ContextTokens is the size of eagent's last prompt.
	ContextTokens int
	// Compacting says the conversation is being restarted from a briefing.
	Compacting bool
	// Waiting says nothing is happening until the user says something.
	Waiting bool
	// Phone says the session is mirrored to the user's phone.
	Phone bool
}

// AgentStatus is one running agent in the status line.
type AgentStatus struct {
	Name  string // how the session refers to it ("claude")
	Who   string // how people call it ("Claude")
	State string // starting, working, idle, blocked, exited
}
