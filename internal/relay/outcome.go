package relay

// How sure a KindDelivered event with OK false is (Event.Outcome). The
// values are the record's (event.DeliveryNotSent, event.DeliveryUnconfirmed).
const (
	// OutcomeNotSent: nothing of it reached the agent.
	OutcomeNotSent = "not_sent"
	// OutcomeUnconfirmed: it was typed, and its arrival never showed.
	OutcomeUnconfirmed = "unconfirmed"
)

// TypingError is a Send that failed while its text was being typed: some
// of it, or all of it without the Enter, may be on the agent's screen.
// Every other Send error comes before anything is typed.
type TypingError struct{ Err error }

func (e *TypingError) Error() string { return e.Err.Error() }
func (e *TypingError) Unwrap() error { return e.Err }
