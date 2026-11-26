package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A session's end clears its open turns (an ended session is not busy),
// and keeps them apart: a resume has to know a turn was left open. A turn
// that is closed after the end, or a resume of the session, settles it.
func TestTurnsLeftOpenAtTheEndAreKeptForTheResume(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: "the user wrote"}))
	b.add(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "interrupted"}))
	st := Replay(b.events)
	if st.Turns[event.ActorEagent] != 0 {
		t.Fatalf("an ended session still counts %d open turn(s)", st.Turns[event.ActorEagent])
	}
	if st.OpenAtEnd[event.ActorEagent] != 1 {
		t.Fatalf("the turn open at the end is not kept: %v", st.OpenAtEnd)
	}
	closed := &builder{events: append([]event.Event(nil), b.events...), seq: b.seq, t: b.t}
	closed.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "interrupted"}))
	if n := Replay(closed.events).OpenAtEnd[event.ActorEagent]; n != 0 {
		t.Fatalf("a turn closed after the end still counts: %d", n)
	}
	b.add(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
	if n := Replay(b.events).OpenAtEnd[event.ActorEagent]; n != 0 {
		t.Fatalf("a resumed session still counts a turn left open at the last end: %d", n)
	}
}
