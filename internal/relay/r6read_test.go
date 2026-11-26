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

// Two messages Claude queued while it worked, taken off the queue together
// and run as two prompts, each a user line of its own. Each arrived at its
// enqueue line; neither user line is a second arrival, so the same words
// sent a third time, still unshown, stay unconfirmed.
func TestTwoDequeuedPromptsOnLinesOfTheirOwnAreNotSecondArrivals(t *testing.T) {
	t.Parallel()
	at := time.Now()
	a, b := "please water the ferns too", "and close the shed"
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", a, "queue")
	s.track("r2", b, "queue")
	s.track("r3", b, "queue")
	got := claudeLines(s,
		claudeQueue(at, "enqueue", a),
		claudeQueue(at.Add(time.Second), "enqueue", b),
		claudeQueue(at.Add(2*time.Second), "dequeue", ""),
		claudeQueue(at.Add(2*time.Second), "dequeue", ""),
		claudeUser(at.Add(3*time.Second), a),
		claudeUser(at.Add(3*time.Second), b),
	)
	if !slices.Equal(got, []string{"r1", "r2"}) {
		t.Fatalf("confirmed %v, want r1 and r2", got)
	}
}

// A message from another Claude session is queued and dequeued too, and
// Claude records its prompt as a meta line, quoting it. That dequeue must
// not stay behind: the next queued message's prompt line is still not a
// second arrival.
func TestADequeuedMessageClaudeRunsAsAMetaLineDoesNotStayBehind(t *testing.T) {
	t.Parallel()
	at := time.Now()
	cross := "<cross-session-message from=\"uds:/tmp/a.sock\" from-name=\"other\">\nthanks, got it\n</cross-session-message>"
	a := "Turn the lamp off now"
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", a, "queue")
	s.track("r2", a, "queue")
	got := claudeLines(s,
		claudeQueue(at, "enqueue", cross),
		claudeQueue(at, "dequeue", ""),
		claudeMeta(at, "Another Claude session sent a message:\n"+cross),
		claudeQueue(at.Add(time.Minute), "enqueue", a),
		claudeQueue(at.Add(time.Minute+time.Second), "dequeue", ""),
		claudeUser(at.Add(time.Minute+time.Second), a),
	)
	if !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("confirmed %v, want r1 only", got)
	}
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
