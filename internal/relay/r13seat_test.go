package relay

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// deliveredEvents is the delivery events a seat said, in order.
func deliveredEvents(s *Seat) []Event {
	var out []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			out = append(out, ev)
		}
	}
	return out
}

// hungSeat is a Claude seat checked by hand (no watcher), blocked for
// going quiet in a turn (hung), with one send typed into it at.
func hungSeat(screen string, at time.Time) (*Seat, *fakeHost) {
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{}, claudeAgent{}, h, "r13s-hung", "")
	s.state, s.stateAt, s.hung = "blocked", at.Add(-11*time.Minute), true
	s.pending = []*pending{{id: "r1", text: "and the tables", mode: "queue", at: at}}
	return s, h
}

// Finding 15: a seat quiet for ten minutes in a turn (blocked, hung) takes
// a queued send while its screen shows it busy. No Enter goes to it while
// it is blocked: the agent is busy, or the screen is a dialog the seat does
// not know, and the Enter would answer it.
func TestR13SHungSeatPressesNoEnter(t *testing.T) {
	t.Parallel()
	for name, screen := range map[string]string{
		"busy":   "✻ Thinking… (12m · esc to interrupt)\n\n> and the tables\n  ? for shortcuts\n",
		"dialog": "Do you want to proceed?\n❯ 1. Yes\n  2. No\n\n> and the tables\n",
	} {
		now := time.Now()
		s, h := hungSeat(screen, now)
		s.checkScreen(now.Add(7 * time.Second))
		h.mu.Lock()
		keys := h.keys
		h.mu.Unlock()
		if len(keys) != 0 {
			t.Errorf("%s: pressed %q into a hung seat", name, keys)
		}
	}
}

// Finding 15: a hung seat whose screen (Claude) or status file (pi) says it
// still works reads a queued send at its next step, as a working one does:
// it is not reported unconfirmed, and stays watched.
func TestR13SHungWorkingSeatKeepsItsSend(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claude, _ := hungSeat("✻ Thinking… (12m · esc to interrupt)\n\n> \n  ? for shortcuts\n", now.Add(-46*time.Second))
	pi, _ := piReadySeat(t, piFooterScreen, &piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
	pi.state, pi.stateAt, pi.hung = "blocked", now.Add(-11*time.Minute), true
	pi.pending = []*pending{{id: "r1", text: "and the tables", mode: "queue", at: now.Add(-46 * time.Second)}}
	for name, s := range map[string]*Seat{"claude": claude, "pi": pi} {
		s.checkScreen(now)
		if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
			t.Errorf("%s: reported %+v, pending %v; want the send still watched", name, got, s.Snapshot().Pending)
		}
	}
}

// Finding 12: pi's turn ends (or is interrupted) while its extension
// reports a dialog, and the dialog then ends (in the same look, for an
// interrupt). A message held for the end of the turn whose spool file is
// gone (an interrupt put it back in pi's editor) is lost once that turn is
// over, and reported unconfirmed after the usual wait.
func TestR13SPiTurnEndUnderADialogCountsForAHeldSend(t *testing.T) {
	t.Parallel()
	pi := piAgent{label: "Pi"}
	for _, kind := range []string{"turn", "aborted"} {
		dir := t.TempDir()
		status := filepath.Join(dir, "pi-status.json")
		writePiStatus(t, status, piStatus{State: "working", Prompt: "Allow rm -rf build?", SessionFile: "/x.jsonl", SessionID: "id"})
		spec := Spec{StatusFile: status, Dir: dir, Spool: filepath.Join(dir, "after_turn"), Hook: filepath.Join(dir, "eagent-relay.ts")}
		s := newSeat(spec, pi, &fakeHost{alive: true, screen: piFooterScreen}, "r13s-pi", "id")
		s.state = "working"
		s.pending = []*pending{{id: "r1", text: "then run the tests", mode: "after_turn", at: time.Now().Add(-time.Second), held: true}}
		s.reportDialog(pi.reportedDialog(s))
		s.fold(obs{kind: kind, at: time.Now()})
		writePiStatus(t, status, piStatus{State: "idle", SessionFile: "/x.jsonl", SessionID: "id", Settled: 1})
		s.reportDialog(pi.reportedDialog(s))
		if st := s.Snapshot().State; st != "idle" {
			t.Fatalf("%s: the seat is %s after the dialog; want idle", kind, st)
		}
		later := time.Now().Add(heldGrace)
		s.leftOvers(later, false)
		s.checkScreen(later.Add(46 * time.Second))
		if got := deliveredEvents(s); len(got) != 1 || got[0].SendID != "r1" || got[0].Outcome != OutcomeUnconfirmed {
			t.Errorf("%s: reported %+v, pending %v; want r1 unconfirmed", kind, got, s.Snapshot().Pending)
		}
	}
}

// piUserLine is a message pi records from the user, stamped at.
func piUserLine(id, text string, at time.Time) string {
	return fmt.Sprintf(`{"type":"message","id":"%s-u","parentId":null,"timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, id, at.UTC().Format("2006-01-02T15:04:05.000Z"), text)
}

// Finding 11: while eagent was down, pi moved to another session of its
// own (/new, /resume) and is in a turn there. The reattached seat's first
// look reads the session on the record (its turn over) and only then pi's
// status file, which names the other one. A send made then waits until
// the seat has read the session pi is on: held for the end of pi's turn,
// with an Escape first for an interrupt, and not confirmed by a line pi
// wrote there while eagent was down.
func TestR13SPiReattachReadsTheSessionPiMovedToFirst(t *testing.T) {
	isolatePi(t)
	savedSettle, savedPause := typeSettle, interruptPause
	typeSettle, interruptPause = 0, 0
	t.Cleanup(func() { typeSettle, interruptPause = savedSettle, savedPause })
	since := time.Now().Add(-time.Minute)
	seat := func(t *testing.T) (*Seat, *fakeHost, string) {
		dir := t.TempDir()
		first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
		writeLines(t, first, append([]string{piHeaderLine("first", since.Add(-time.Hour))}, piTurnLines("f1", "sort the shelves", "Sorted.", since.Add(-time.Minute))...)...)
		writeLines(t, second, piHeaderLine("second", since.Add(5*time.Second)), piUserLine("s1", "run the tests", since.Add(10*time.Second)))
		writePiStatus(t, status, piStatus{State: "working", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		spool := filepath.Join(dir, "after_turn")
		h := &fakeHost{alive: true, screen: piFooterScreen}
		s := attachSeat(Spec{StatusFile: status, Dir: dir, Spool: spool, Hook: filepath.Join(dir, "eagent-relay.ts")}, piAgent{label: "Pi"}, h, "r13s-"+filepath.Base(dir), "first", first, since)
		s.poll = 200 * time.Millisecond
		go s.watch()
		t.Cleanup(s.Close)
		// The first look has read pi's status file.
		if !within(time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.refollow }) {
			t.Fatal("the first look never read pi's status file")
		}
		return s, h, spool
	}
	t.Run("after_turn", func(t *testing.T) {
		t.Parallel()
		s, h, spool := seat(t)
		msg, err := sendNow(t, s, "a1", "then run the linter", "after_turn")
		if err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.typed) != 0 || len(spoolNames(spool)) != 1 {
			t.Errorf("send: %q; typed %q, spool %v; want it held", msg, h.typed, spoolNames(spool))
		}
	})
	t.Run("interrupt", func(t *testing.T) {
		t.Parallel()
		s, h, _ := seat(t)
		if _, err := sendNow(t, s, "i1", "STOP now", "interrupt"); err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.keys) == 0 || h.keys[0] != "Escape" {
			t.Errorf("typed %q, pressed %q; want Escape before the message", h.typed, h.keys)
		}
	})
	t.Run("downtime", func(t *testing.T) {
		t.Parallel()
		s, _, _ := seat(t)
		if _, err := sendNow(t, s, "r9", "run the tests", "queue"); err != nil {
			t.Fatal(err)
		}
		// The seat says working once it has read the line in the session pi
		// is on: any confirmation that line gives comes before.
		var seen []Event
		for deadline := time.After(2 * time.Second); len(seen) == 0 || seen[len(seen)-1].State != "working"; {
			select {
			case ev := <-s.events:
				seen = append(seen, ev)
			case <-deadline:
				t.Fatalf("never read the session pi is on; saw %+v", seen)
			}
		}
		for _, ev := range seen {
			if ev.Kind == KindDelivered && ev.SendID == "r9" && ev.OK {
				t.Errorf("r9 confirmed by the line pi wrote while eagent was down")
			}
		}
	})
}

// Finding 11, the dialog: pi's status file says a dialog is up when the
// seat reattaches. The catch-up ends with the seat blocked on it; it never
// says idle first, which let a send made then go into the dialog.
func TestR13SPiReattachUnderADialogNeverSaysIdle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "pi-status.json")
	since := time.Now().Add(-time.Minute)
	writeLines(t, first, append([]string{piHeaderLine("first", since.Add(-time.Hour))}, piTurnLines("f1", "sort the shelves", "Sorted.", since.Add(-time.Minute))...)...)
	writePiStatus(t, status, piStatus{State: "idle", Prompt: "Allow rm -rf build?", SessionFile: first, SessionID: "first"})
	s := attachSeat(Spec{StatusFile: status, Dir: dir}, piAgent{label: "Pi"}, &fakeHost{alive: true, screen: piFooterScreen}, "r13s-dialog", "first", first, since)
	s.poll = time.Hour
	go s.watch()
	t.Cleanup(s.Close)
	select {
	case ev := <-s.events:
		if ev.Kind != KindState || ev.State != "blocked" {
			t.Errorf("the seat said %+v first; want blocked", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the seat said nothing")
	}
}
