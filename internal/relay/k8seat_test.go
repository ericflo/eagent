package relay

// The sixteenth review's sixth pass over the seat (sr6seat).

import ()

// sr6Codex is Codex with its queue command stubbed: the hand-over goes.
type sr6Codex struct{ codexAgent }

func (sr6Codex) afterTurn(*Seat, string, string) error { return nil }
