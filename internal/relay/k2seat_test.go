package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A12: a held message pi's extension took out of the spool and never ran
// (an interrupt put it back in pi's editor) is lost once the turn is over,
// also when a pi dialog opened within the grace after that turn end: the
// dialog holds the seat while it is up, and the turn end stands after it.
// Lost, it is reported unconfirmed after the usual wait; never typed again.
func TestK2APiDialogAfterATurnEndKeepsThatTurnEnd(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "pi", Spool: spool}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k2-a12", "")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.reportDialog("Allow rm -rf build?") // opens within the grace
	names := spoolNames(spool)
	for _, n := range names {
		if err := os.Remove(filepath.Join(spool, n)); err != nil { // the extension removed it
			t.Fatal(err)
		}
	}
	s.reportDialog("")
	now := time.Now()
	s.leftOvers(now.Add(heldGrace), false)
	s.mu.Lock()
	lost := len(s.pending) == 1 && s.pending[0].lost
	s.stateAt = now.Add(-time.Hour)
	s.mu.Unlock()
	if !lost {
		t.Fatalf("the held message whose file is gone is not lost (pending %v, spool had %v)", s.Snapshot().Pending, names)
	}
	s.checkScreen(now.Add(time.Hour))
	var got []string
	for _, ev := range deliveries(drain(s.events)) {
		got = append(got, ev.SendID+":"+ev.Outcome)
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeUnconfirmed}) || len(k1Typed(h)) != 0 {
		t.Fatalf("reported %v, typed %q; want r1 unconfirmed, nothing typed", got, k1Typed(h))
	}
}

// A13: eagent was down while the agent's turn came to a dialog the seat
// does not know (a permission prompt) and stayed there. The reattached seat
// reads a history that ends inside that turn, its transcript quiet for
// longer than a live seat waits before calling a working agent blocked: it
// is blocked as the live seat would be by then (hung), so a send goes only
// while the screen shows the agent's own prompt or busy line; into the
// dialog it types nothing. A turn whose transcript moved recently is still
// working.
func TestK2AReattachedSeatQuietInATurnTypesNothingIntoADialog(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	since := time.Now().Add(-15 * time.Minute)
	at := since.Add(-time.Minute)
	claude := []string{
		claudeUser(at, "run the build"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make build"}}]}}`, stamp(at.Add(time.Second))),
	}
	codex := []string{
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"thread-1","cwd":"/w","timestamp":"%s"}}`, stamp(at), stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["bash","-lc","make build"],"parsed_cmd":[{"cmd":"make build"}]}}}`, stamp(at.Add(time.Second))),
	}
	dialog := "Do you want to proceed?\n❯ 1. Yes\n  2. No, and tell the agent what to do differently"
	for _, c := range []struct {
		name   string
		agent  Agent
		lines  []string
		quiet  time.Duration // since the transcript was last written
		record string
		want   string // the state the catch-up says
	}{
		{"claude", claudeAgent{}, claude, 14 * time.Minute, "working", "blocked"},
		{"claude_record_idle", claudeAgent{}, claude, 14 * time.Minute, "idle", "blocked"},
		{"codex", codexAgent{}, codex, 14 * time.Minute, "working", "blocked"},
		{"claude_moved_lately", claudeAgent{}, claude, time.Minute, "working", "working"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			writeLines(t, path, c.lines...)
			old := time.Now().Add(-c.quiet)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			h := &fakeHost{alive: true, screen: dialog}
			s := attachSeat(Spec{State: c.record}, c.agent, h, "k2-a13-"+c.name, "thread-1", path, since)
			s.poll = time.Millisecond
			go s.watch()
			t.Cleanup(s.Close)
			ev := nextState(t, s.events)
			if c.want == "blocked" {
				msg, err := s.Send("r1", "also run the tests", "queue")
				if typed := k1Typed(h); len(typed) != 0 || err == nil {
					t.Fatalf("typed %q into the dialog (Send: %q, %v)", typed, msg, err)
				}
			}
			if ev.State != c.want {
				t.Fatalf("the catch-up said %q; want %q", ev.State, c.want)
			}
		})
	}
}
