package relay

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// The words in these tests are made up; only the shapes are the agents'.

// claudeLines feeds Claude Code transcript lines through its parser into
// the seat, as the watcher does, and returns the ids the seat confirmed.
func claudeLines(s *Seat, lines ...string) []string {
	p := &claudeParser{}
	for _, l := range lines {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	var ok []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && ev.OK {
			ok = append(ok, ev.SendID)
		}
	}
	return ok
}

func claudeUser(at time.Time, content string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":"%s","message":{"role":"user","content":%q}}`, stamp(at), content)
}

func claudeMeta(at time.Time, content string) string {
	return fmt.Sprintf(`{"type":"user","isMeta":true,"timestamp":"%s","message":{"role":"user","content":%q}}`, stamp(at), content)
}

func claudeQueue(at time.Time, op, content string) string {
	if content == "" {
		return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s"}`, op, stamp(at))
	}
	return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s","content":%q}`, op, stamp(at), content)
}

// Claude Code records a pasted message of several lines wrapped in a
// <pasted_content> tag. The message arrived and is confirmed.
func TestAMultiLinePasteClaudeWrapsInATagIsConfirmed(t *testing.T) {
	t.Parallel()
	text := "Three things for the garden:\n1. water the ferns\n2. move the pots\n3. sweep the path\nThen tell me how it looks."
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", text, "queue")
	got := claudeLines(s, claudeUser(time.Now(), "\n\n<pasted_content id=\"9f3c\">\n"+text+"\n</pasted_content id=\"9f3c\">\n"))
	if !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("confirmed %v, want r1", got)
	}
}

// A send whose Enter was lost stays in Claude's input box, and the next is
// pasted after it: Claude records both as one prompt, glued together. Both
// arrived.
func TestASendTypedAfterTextLeftInTheBoxIsConfirmedWithIt(t *testing.T) {
	t.Parallel()
	a := "Please hold the kettle order until the blue ones are back in stock."
	b := "Wait a moment, I want to add something first"
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r5", a, "queue")
	s.track("r6", b, "queue")
	if got := claudeLines(s, claudeUser(time.Now(), a+b)); !slices.Equal(got, []string{"r5", "r6"}) {
		t.Fatalf("confirmed %v, want r5 and r6", got)
	}
}
