package relay

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// longHistory is a Claude transcript of many finished turns, well over the
// (lowered) tail a reattach re-reads, whose first prompt is first: a send
// that arrived long before the record's last event.
func longHistory(t *testing.T, since time.Time, first string) string {
	t.Helper()
	reply := strings.Repeat("The shelves are sorted now. ", 40)
	lines := claudeTurnLines(first, reply, since.Add(-time.Hour))
	for i := 1; i < 300; i++ {
		lines = append(lines, claudeTurnLines(fmt.Sprintf("next shelf %d", i), reply, since.Add(-time.Hour+time.Duration(i)*time.Second))...)
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeLines(t, path, lines...)
	return path
}

// lowTail lowers how much of a transcript a reattach re-reads, so a test's
// transcript is long without being megabytes.
func lowTail(t *testing.T) {
	saved := tailBackfill
	tailBackfill = 32 << 10
	t.Cleanup(func() { tailBackfill = saved })
}

// What a reattached seat reports from a transcript, less the confirmations.
func (w *reattachedSeat) news() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, ev := range w.events {
		if ev.Kind != KindDelivered {
			out = append(out, ev.Kind+"/"+ev.State+"/"+ev.Text)
		}
	}
	return out
}

// settle waits until the seat has reported nothing new for a while.
func (w *reattachedSeat) settle() {
	last, still := -1, 0
	within(2*time.Second, func() bool {
		w.mu.Lock()
		n := len(w.events)
		w.mu.Unlock()
		if n == last {
			still++
		} else {
			still = 0
		}
		last = n
		return still >= 5
	})
}

// A restored send whose prompt is far back in a long transcript, before the
// part a reattach re-reads, is still found and confirmed. Looking for it
// reports nothing else: the reattach writes the same events as one with
// nothing restored, apart from the confirmation.
func TestARestoredSendFarBackIsFoundWithoutReportingTheHistoryAgain(t *testing.T) {
	isolatePi(t)
	lowTail(t)
	since := time.Now().Add(-time.Minute)
	first := "label the jars on the top shelf"
	path := longHistory(t, since, first)
	plain := reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(path)}, path, since)
	plain.settle()
	for _, c := range []struct {
		text string
		want []string
	}{{first, []string{"r1"}}, {"words it was never sent", nil}} {
		w := reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(path)}, path, since, Restored{ID: "r1", Text: c.text, Mode: "queue", At: since.Add(-time.Hour - time.Second)})
		w.caughtUp(t)
		w.settle()
		if ok, failed := w.delivered(); !slices.Equal(ok, c.want) || len(failed) > 0 {
			t.Fatalf("%q: confirmed %v, not confirmed %v", c.text, ok, failed)
		}
		if a, b := plain.news(), w.news(); !slices.Equal(a, b) {
			t.Fatalf("%q: with a restored send the reattach reported %d things, without it %d", c.text, len(b), len(a))
		}
	}
}

// The search for restored sends starts near the oldest one's time, never
// after a line stamped at or after it, and skips most of a long history.
func TestTheSearchForRestoredSendsStartsNearTheirTime(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	path := longHistory(t, since, "first")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := since.Add(-time.Hour + 150*time.Second)
	mark := []byte(`"timestamp":"` + stamp(want) + `"`)
	at := int64(bytes.Index(raw, mark))
	at = int64(bytes.LastIndexByte(raw[:at], '\n') + 1) // the line's start
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := seekBefore(f, int64(len(raw)), want)
	if got > at || got < at/2 {
		t.Fatalf("starts at %d; the first line at that time starts at %d", got, at)
	}
}

// After a restart, a send made since whose Enter was lost (its words sit
// in the box) is pressed after six seconds, as on a seat with nothing
// restored: a restored send ahead of it, which is never pressed, does not
// hold that up. The control is the same seat with nothing restored.
func TestARestoredSendDoesNotHoldUpALiveSendsEnter(t *testing.T) {
	t.Parallel()
	for _, restore := range [][]Restored{nil, {{ID: "r1", Text: "sweep the porch", Mode: "queue"}}} {
		since := time.Now().Add(-time.Minute)
		if len(restore) > 0 {
			restore[0].At = since.Add(-time.Second)
		}
		h := &fakeHost{alive: true}
		s := attachSeat(Spec{Restore: restore}, claudeAgent{}, h, "x", "", "", since)
		now := time.Now()
		s.caughtUp(now)
		s.track("r2", "fold the towels", "queue")
		h.mu.Lock()
		h.screen = "> fold the towels\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
		h.mu.Unlock()
		s.mu.Lock()
		s.state, s.stateAt = "idle", now.Add(-time.Minute)
		s.mu.Unlock()
		s.checkScreen(now.Add(7 * time.Second))
		h.mu.Lock()
		keys := slices.Clone(h.keys)
		h.mu.Unlock()
		if !slices.Contains(keys, "Enter") {
			t.Fatalf("with %d restored: pressed %q", len(restore), keys)
		}
	}
}

// Antigravity stamps its steps to the whole second, so the step of a send
// typed just before eagent stopped can carry a time before the send's own.
// It is still the send's arrival.
func TestARestoredSendIsConfirmedByAnAntigravityStepStampedToTheSecond(t *testing.T) {
	t.Parallel()
	sec := time.Now().Add(-time.Minute).Truncate(time.Second)
	text := "count the chairs"
	r := Restored{ID: "r1", Text: text, Mode: "queue", At: sec.Add(400 * time.Millisecond)}
	s := attachSeat(Spec{Restore: []Restored{r}}, antigravityAgent{}, &fakeHost{alive: true}, "x", "", "", sec.Add(800*time.Millisecond))
	line := fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"%s","content":%q}`,
		sec.UTC().Format(time.RFC3339), "<USER_REQUEST>\n"+text+"\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nmade-up metadata\n</ADDITIONAL_METADATA>")
	for _, o := range (antigravityParser{}).line([]byte(line)) {
		s.fold(o)
	}
	var ok []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && ev.OK {
			ok = append(ok, ev.SendID)
		}
	}
	if !slices.Equal(ok, []string{"r1"}) {
		t.Fatalf("confirmed %v", ok)
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
