package relay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Finding 7: a seat closed during the send's last look at the screen (a
// switch, stop or restart) holds nothing for after the turn: no spool file
// for Claude or pi, no paste into Antigravity. The send says ErrClosed, so
// the harness hands it to the agent that took over.
func TestR11AnAfterTurnSendToASeatClosedDuringItsLastLookHoldsNothing(t *testing.T) {
	t.Parallel()
	for _, agent := range []Agent{claudeAgent{}, piAgent{name: "pi", label: "Pi", binary: "pi"}, antigravityAgent{}} {
		spool := filepath.Join(t.TempDir(), "after_turn")
		spec := Spec{Name: "x"}
		if _, ok := agent.(antigravityAgent); !ok {
			spec.Spool, spec.Hook = spool, "eagent relay-hook"
		}
		fh := &fakeHost{alive: true}
		h := &closingHost{fakeHost: fh}
		s := newSeat(spec, agent, h, "x", "")
		h.seat = s
		s.state = "working"
		close(s.done)
		msg, err := s.Send("r1", "then run the tests", "after_turn")
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%T: Send returned %q, %v; want ErrClosed", agent, msg, err)
		}
		fh.mu.Lock()
		typed := fh.typed
		fh.mu.Unlock()
		if names := spoolNames(spool); len(names) != 0 || len(typed) != 0 || len(s.Snapshot().Pending) != 0 {
			t.Errorf("%T: spool %v, typed %q, pending %v", agent, names, typed, s.Snapshot().Pending)
		}
	}
}

// Finding 9: Claude's Stop hook claimed a held message (.txt.taken) and
// died before it removed the file or handed the text over. Once the turn
// it waited for is over and the hook had its chance, the message is lost:
// reported unconfirmed after the usual wait, once, never typed or pressed,
// and the stale file is gone.
func TestR11AHeldSendWhoseHookDiedAfterClaimingItIsReported(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, h, spool := heldClaudeSeat(t, "then run the tests")
	if taken, _ := takeSpool(spool); len(taken) != 1 { // the hook's rename, then it dies
		t.Fatalf("claimed %v", taken)
	}
	s.fold(obs{kind: "turn", text: "Done."})
	drain(s.events)
	seen := time.Now().Add(20 * time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once (pending %v)", got, s.Snapshot().Pending)
	}
	if names := spoolNames(spool); len(h.typed) != 0 || len(h.keys) != 0 || len(names) != 0 {
		t.Fatalf("typed %q, pressed %q, spool %v", h.typed, h.keys, names)
	}
}

// Finding 8: pi's extension hands a held message over into a run that is
// then interrupted; pi puts it back in its editor and the extension removes
// its spool file. While the extension still holds it (.taken, pi has not
// taken it yet) it stays held, however long. Once its file is gone and the
// turn it waited for is over, it is lost: reported unconfirmed after the
// usual wait, once; never typed, and no Enter on the words in the editor.
func TestR11APiHeldSendPutBackInTheEditorIsReported(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	dir := t.TempDir()
	spool, status := filepath.Join(dir, "after_turn"), filepath.Join(dir, "pi-status.json")
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: filepath.Join(dir, "s.jsonl"), SessionID: "id"})
	h := &fakeHost{alive: true, screen: "  then run the tests\n" + piFooterScreen}
	pi := piAgent{name: "pi", label: "Pi", binary: "pi"}
	s := newSeat(Spec{Name: "pi", Spool: spool, Hook: filepath.Join(dir, "eagent-relay.ts"), StatusFile: status}, pi, h, "x", "id")
	s.state = "working"
	if msg, err := s.Send("r1", "then run the tests", "after_turn"); err != nil || !strings.Contains(msg, "held for") {
		t.Fatalf("send: %q %v", msg, err)
	}
	taken, _ := takeSpool(spool) // the extension's claim of it
	s.fold(obs{kind: "aborted"})
	drain(s.events)
	seen := time.Now().Add(20 * time.Second)
	look := func(at time.Time) {
		pi.poll(s, at)
		s.checkScreen(at)
	}
	for _, after := range []time.Duration{0, 46, 200} {
		look(seen.Add(after * time.Second))
	}
	if got := deliveries(drain(s.events)); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
		t.Fatalf("while the extension holds it: deliveries %+v, pending %v", got, s.Snapshot().Pending)
	}
	for _, path := range taken { // put back in the editor: handed() removes it
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, after := range []time.Duration{300, 346, 420} {
		look(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once (pending %v)", got, s.Snapshot().Pending)
	}
	if len(h.typed) != 0 || len(h.keys) != 0 {
		t.Fatalf("typed %q, pressed %q", h.typed, h.keys)
	}
}

// Finding 10: eagent stopped while the record said the agent was blocked
// (here quiet in the middle of a turn, on a permission dialog). The
// history the reattached seat re-reads ends inside that turn: the record's
// blocked, and its detail, stand. The seat says nothing, holds the relay
// blocked, and types nothing into the dialog. A record that said working
// is told working as before, and so is a blocked one when the transcript
// shows the agent at work after the record ends.
func TestR11AReattachToABlockedAgentMidTurnKeepsItBlocked(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	history := claudeTurnLines("sort the shelves", "Sorted.", since.Add(-10*time.Second))
	history = append(history, claudeUser(since.Add(-5*time.Second), "now count the chairs"))
	news := fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls chairs"}}]}}`, stamp(since.Add(time.Second)))
	for _, c := range []struct {
		recorded string
		news     bool
		said     []string
		state    string
	}{
		{"blocked", false, nil, "blocked"},
		{"working", false, []string{"working"}, "working"},
		{"blocked", true, []string{"working"}, "working"},
	} {
		h := &fakeHost{alive: true, screen: "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"}
		s := attachSeat(Spec{State: c.recorded}, claudeAgent{}, h, "x", "", "", since)
		claudeLines(s, history...)
		if c.news {
			claudeLines(s, news)
		}
		s.caughtUp(time.Now())
		var said []string
		for _, ev := range drain(s.events) {
			if ev.Kind == KindState {
				said = append(said, ev.State)
			}
		}
		if !slices.Equal(said, c.said) || s.Snapshot().State != c.state {
			t.Errorf("record %s (news %v): said %v and the seat is %s; want %v and %s", c.recorded, c.news, said, s.Snapshot().State, c.said, c.state)
		}
		if c.state != "blocked" {
			continue
		}
		if msg, err := s.Send("q1", "and the tables", "queue"); err == nil {
			t.Errorf("record blocked: a send went (%q)", msg)
		}
		if len(h.typed) != 0 || len(h.keys) != 0 {
			t.Errorf("record blocked: typed %q, pressed %q", h.typed, h.keys)
		}
	}
}
