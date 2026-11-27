package relay

import (
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

// longHistory is a Claude transcript of many finished turns, well over the
// (lowered) tail a reattach re-reads, whose first prompt is first: a send
// that arrived long before the record's last event.
func longHistory(t *testing.T, since time.Time, first string) string {
	t.Helper()
	reply := strings.Repeat("The shelves are sorted now. ", 40)
	lines := claudeTurnLines(first, reply, since.Add(-time.Hour))
	for i := 1; i < 100; i++ {
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
	tailBackfill = 16 << 10
	t.Cleanup(func() { tailBackfill = saved })
}

// What a reattached seat reports from a transcript, less the confirmations
// (and whether nothing waits on the agent, which a restored send changes).
func (w *reattachedSeat) news() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, ev := range w.events {
		if ev.Kind != KindDelivered && ev.Kind != KindQuiet {
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
		return still >= 3
	})
}

// A reattach with restored sends records the same events as one with
// nothing restored, apart from the confirmations. One whose prompt is in
// the part a reattach re-reads is confirmed, and so is one whose prompt is
// older than that part (a reattach with restored sends reads the whole
// transcript); one never sent is not.
func TestAReattachWithRestoredSendsRecordsTheSameEventsAsOneWithout(t *testing.T) {
	lowTail(t)
	since := time.Now().Add(-time.Minute)
	first := "label the jars on the top shelf"
	path := longHistory(t, since, first)
	cases := []struct {
		text string
		want []string
		w    *reattachedSeat
	}{{text: "next shelf 99", want: []string{"r1"}}, {text: first, want: []string{"r1"}}, {text: "words it was never sent"}}
	// The seats watch copies, all at once: one transcript is followed by
	// one seat at a time.
	copyOf := func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	plain := reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(path)}, path, since)
	for i := range cases {
		p := copyOf()
		cases[i].w = reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(p)}, p, since, Restored{ID: "r1", Text: cases[i].text, Mode: "queue", At: since.Add(-time.Hour - time.Second)})
	}
	plain.settle()
	for _, c := range cases {
		w := c.w
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

// A restored send the transcript never shows arriving (here its words sit
// in the box) is reported unconfirmed after the wait a live send gets,
// once, and it is never typed or pressed.
func TestARestoredSendNeverShownEndsUnconfirmedOnceAndIsNotTyped(t *testing.T) {
	lowTail(t)
	since := time.Now().Add(-time.Minute)
	path := longHistory(t, since, "label the jars on the top shelf")
	w := reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(path)}, path, since, Restored{ID: "r1", Text: "dust the jars on the low shelf", Mode: "queue", At: since.Add(-time.Hour - time.Second)})
	w.caughtUp(t)
	w.h.mu.Lock()
	w.h.screen = "> dust the jars on the low shelf\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	w.h.mu.Unlock()
	start := time.Now()
	w.s.mu.Lock()
	w.s.stateAt = start.Add(-time.Minute)
	w.s.mu.Unlock()
	for _, after := range []time.Duration{10, 50, 100} {
		w.s.checkScreen(start.Add(after * time.Second))
	}
	if !eventually(func() bool { _, failed := w.delivered(); return len(failed) > 0 }) {
		t.Fatal("never reported")
	}
	w.settle()
	ok, failed := w.delivered()
	if len(ok) > 0 || len(failed) != 1 || !strings.HasPrefix(failed[0], "r1: ") {
		t.Fatalf("confirmed %v, not confirmed %q", ok, failed)
	}
	w.mu.Lock()
	var outcome string
	for _, ev := range w.events {
		if ev.Kind == KindDelivered {
			outcome = ev.Outcome
		}
	}
	w.mu.Unlock()
	if outcome != OutcomeUnconfirmed {
		t.Fatalf("outcome %q", outcome)
	}
	w.h.mu.Lock()
	defer w.h.mu.Unlock()
	if len(w.h.typed) > 0 || len(w.h.keys) > 0 {
		t.Fatalf("typed %q, pressed %q for a restored send", w.h.typed, w.h.keys)
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

// A send that arrived word for word keeps its entry when a longer waiting
// send contains its words (a follow-up that repeats and extends it): the
// longer one, which has not arrived, is not given the entry through the
// rule for a transcript that shows only a piece of a message.
func TestAnExactArrivalIsNotGivenToALongerSendThatContainsIt(t *testing.T) {
	t.Parallel()
	short := "sort the photos by the date taken"
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", short, "queue")
	s.track("r2", short+" and then rename them by place", "queue")
	if got := s.matchPending(short); !slices.Equal(got, []string{"r1"}) {
		t.Errorf("the entry %q confirmed %v, want r1", short, got)
	}
	mid := "the photos by the date taken"
	s2 := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s2.track("r1", mid, "queue")
	s2.track("r2", "sort "+mid+" first", "queue")
	if got := s2.matchPending(mid); !slices.Equal(got, []string{"r1"}) {
		t.Errorf("the entry %q confirmed %v, want r1", mid, got)
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
