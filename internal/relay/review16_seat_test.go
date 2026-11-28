package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The record says how far a transcript was read, and the file is then
// replaced by a shorter one (the agent rewrote it), while eagent was down
// or after it reattached. The mark was taken on what the file held
// before: nothing in the new file is history by it. Every line of the new
// file started before the old mark, so the message's arrival and the
// turn's report were both dropped as already on the record.
func TestAReadMarkDoesNotOutliveTheTranscriptItWasTakenOn(t *testing.T) {
	for _, when := range []string{"while eagent was down", "after the reattach"} {
		t.Run(when, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			at := time.Now().Add(-time.Minute)
			var old []string
			for i := 0; i < 4; i++ {
				old = append(old, claudeTurnLines("build it", "Built it.", at.Add(time.Duration(i)*time.Second))...)
			}
			before := strings.Join(old, "\n") + "\n"
			since := at.Add(5 * time.Second) // the record's last news of the agent
			sent := since.Add(time.Second)
			after := strings.Join(claudeTurnLines("run the tests", "All 42 tests passed.", sent.Add(time.Second)), "\n") + "\n"
			if len(after) >= len(before) {
				t.Fatal("setup: the new file is not shorter than the mark")
			}
			first := before
			if when == "while eagent was down" {
				first = after
			}
			if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
				t.Fatal(err)
			}
			w := reattach(t, claudeAgent{}, Spec{Dir: dir, Read: int64(len(before))}, path, since, Restored{ID: "r1", Text: "run the tests", Mode: "queue", At: sent})
			w.caughtUp(t)
			if when == "after the reattach" {
				if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !eventually(func() bool {
				ok, _ := w.delivered()
				return slices.Contains(ok, "r1") && slices.Equal(w.turns(), []string{"All 42 tests passed."})
			}) {
				ok, failed := w.delivered()
				t.Fatalf("confirmed %q, failed %q, turns %q; want r1 confirmed and the turn reported", ok, failed, w.turns())
			}
		})
	}
}

// r16TurnEndsAtLook is a terminal whose agent's turn ends while a send
// takes its last look at the screen.
type r16TurnEndsAtLook struct {
	*fakeHost
	s *Seat
}

func (h *r16TurnEndsAtLook) Capture(target string, lines int) (string, error) {
	if h.s != nil {
		s := h.s
		h.s = nil
		s.fold(obs{kind: "turn", text: "Done.", at: time.Now()})
	}
	return h.fakeHost.Capture(target, lines)
}

// A message for the end of Antigravity's turn is sent while it works, and
// the turn ends during the send's last look at the screen, before the
// message is handed to the agent's own queue. No turn is left for it to
// wait on: it is watched on a timer from then, like one whose turn ended
// after the hand-over. It was held for a turn already over, never timed,
// and its not arriving was never reported.
func TestAMessageHeldForATurnThatEndedBeforeTheHandOverIsTimed(t *testing.T) {
	savedSettle, savedGrace := typeSettle, heldGrace
	typeSettle, heldGrace = 0, 0
	t.Cleanup(func() { typeSettle, heldGrace = savedSettle, savedGrace })
	const id = "11111111-2222-3333-4444-555555555555"
	status := filepath.Join(t.TempDir(), "agy-status.json")
	if err := os.WriteFile(status, []byte(`{"conversation_id":"`+id+`","agent_state":"idle"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &r16TurnEndsAtLook{fakeHost: &fakeHost{alive: true}}
	s := newSeat(Spec{StatusFile: status}, antigravityAgent{}, h, "x", id)
	s.state = "working"
	h.s = s
	if _, err := s.Send("r1", "then commit the fix", "after_turn"); err != nil {
		t.Fatal(err)
	}
	antigravityAgent{}.poll(s, time.Now().Add(time.Second))
	s.mu.Lock()
	timed := len(s.pending) == 1 && s.pending[0].timed()
	n := len(s.pending)
	s.mu.Unlock()
	if !timed {
		t.Fatalf("%d pending; the message handed over after its turn ended is not timed: nothing would ever say it did not arrive", n)
	}
}
