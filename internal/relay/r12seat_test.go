package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// stateEvents is the states a seat said, in order.
func stateEvents(s *Seat) []string {
	var said []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindState {
			said = append(said, ev.State)
		}
	}
	return said
}

// Finding 9: pi went quiet for ten minutes in a turn (blocked, hung), then
// its extension reports a dialog, which is answered with no transcript
// line in between. pi is still in its turn: the seat goes back to working,
// not idle, whether pi's status says it works (closed) or says nothing
// any more (gone).
func TestR12APiDialogOverAQuietTurnGoesBackToWorking(t *testing.T) {
	t.Parallel()
	pi := piAgent{label: "Pi"}
	for _, end := range []string{"closed", "gone"} {
		s, status := piReadySeat(t, piFooterScreen, &piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
		now := time.Now()
		s.state, s.stateAt, s.lastLine = "working", now.Add(-11*time.Minute), now.Add(-11*time.Minute)
		s.checkScreen(now)
		if st := s.Snapshot().State; st != "blocked" || !s.hung {
			t.Fatalf("%s: not marked quiet: %s", end, st)
		}
		drain(s.events)
		writePiStatus(t, status, piStatus{State: "working", Prompt: "Allow rm -rf build?", SessionFile: "/x.jsonl", SessionID: "id"})
		s.reportDialog(pi.reportedDialog(s))
		if end == "closed" {
			writePiStatus(t, status, piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
		} else if err := os.Remove(status); err != nil {
			t.Fatal(err)
		}
		s.reportDialog(pi.reportedDialog(s))
		if said := stateEvents(s); !slices.Equal(said, []string{"blocked", "working"}) || s.Snapshot().State != "working" {
			t.Errorf("%s: said %v and the seat is %s; want blocked, working", end, said, s.Snapshot().State)
		}
	}
}

// sendNow is a send made as soon as the seat is watching (a resume with a
// prompt), bounded so a send that waits for nothing fails the test.
func sendNow(t *testing.T, s *Seat, id, text, mode string) (string, error) {
	t.Helper()
	type result struct {
		msg string
		err error
	}
	got := make(chan result, 1)
	go func() {
		msg, err := s.Send(id, text, mode)
		got <- result{msg, err}
	}()
	select {
	case r := <-got:
		return r.msg, r.err
	case <-time.After(5 * time.Second):
		t.Fatalf("send %s still waiting after the catch-up", id)
		return "", nil
	}
}

// Finding 7: a send made the moment a reattached seat starts watching (a
// resume with a prompt) waits for the seat's first look, which reads the
// history to its end at once. So it goes by the agent's state: not into
// the dialog of an agent the record says is blocked; with an interrupt
// first, or held for the end of the turn, to one still in its turn; and
// it is not confirmed by a line the agent wrote while eagent was down.
func TestR12ASendAtOnceOnAReattachWaitsForTheCatchUp(t *testing.T) {
	savedSettle, savedPause := typeSettle, interruptPause
	typeSettle, interruptPause = 0, 0
	t.Cleanup(func() { typeSettle, interruptPause = savedSettle, savedPause })
	since := time.Now().Add(-time.Minute)
	midTurn := append(claudeTurnLines("sort the shelves", "Sorted.", since.Add(-10*time.Second)), claudeUser(since.Add(-5*time.Second), "now count the chairs"))
	const busy = "✻ Thinking… (12s · esc to interrupt)\n\n> \n  ? for shortcuts\n"
	seat := func(t *testing.T, spec Spec, screen string, poll time.Duration, lines ...string) (*Seat, *fakeHost) {
		path := filepath.Join(t.TempDir(), "t.jsonl")
		writeLines(t, path, lines...)
		h := &fakeHost{alive: true, screen: screen}
		s := attachSeat(spec, claudeAgent{}, h, "r12s-"+filepath.Base(filepath.Dir(path)), "", path, since)
		s.poll = poll
		go s.watch()
		t.Cleanup(s.Close)
		return s, h
	}
	t.Run("blocked", func(t *testing.T) {
		t.Parallel()
		s, h := seat(t, Spec{State: "blocked"}, "Do you want to proceed?\n❯ 1. Yes\n  2. No\n", time.Hour, midTurn...)
		if msg, err := sendNow(t, s, "q1", "and the tables", "queue"); err == nil {
			t.Errorf("a send went into the dialog: %q", msg)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.typed) != 0 || len(h.keys) != 0 {
			t.Errorf("typed %q, pressed %q", h.typed, h.keys)
		}
	})
	t.Run("interrupt", func(t *testing.T) {
		t.Parallel()
		s, h := seat(t, Spec{State: "working"}, busy, time.Hour, midTurn...)
		if _, err := sendNow(t, s, "i1", "STOP now", "interrupt"); err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if !slices.Equal(h.typed, []string{"STOP now"}) || !slices.Equal(h.keys, []string{"Escape", "Enter"}) {
			t.Errorf("typed %q, pressed %q; want Escape before the message", h.typed, h.keys)
		}
	})
	t.Run("after_turn", func(t *testing.T) {
		t.Parallel()
		spool := filepath.Join(t.TempDir(), "after_turn")
		s, h := seat(t, Spec{State: "working", Spool: spool, Hook: "eagent relay-hook"}, busy, time.Hour, midTurn...)
		if msg, err := sendNow(t, s, "a1", "then run the tests", "after_turn"); err != nil || !strings.Contains(msg, "held for") {
			t.Errorf("send: %q %v; want held", msg, err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.typed) != 0 || len(spoolNames(spool)) != 1 {
			t.Errorf("typed %q, spool %v", h.typed, spoolNames(spool))
		}
	})
	t.Run("downtime", func(t *testing.T) {
		t.Parallel()
		lines := append(claudeTurnLines("sort the shelves", "Sorted.", since.Add(-10*time.Second)), claudeTurnLines("run the tests", "Ran them.", since.Add(time.Second))...)
		s, _ := seat(t, Spec{State: "idle"}, "> \n  ? for shortcuts\n", piLook, lines...)
		if _, err := sendNow(t, s, "r9", "run the tests", "queue"); err != nil {
			t.Fatal(err)
		}
		within(4*piLook, func() bool { return false })
		for _, ev := range deliveries(drain(s.events)) {
			if ev.SendID == "r9" && ev.OK {
				t.Errorf("r9 confirmed by the line the agent wrote while eagent was down")
			}
		}
	})
}
