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

// rv15: a transcript the agent replaces at the same path (the tailer starts
// it again from the top: "Truncated or replaced") holds the send's arrival
// at an offset below the size the old file had when the send was tracked.
// Also when the new content is longer than that size but shorter than what
// the watcher had read (the tailer's start again, not the size, decides).
// The watcher's read (readTail) folds what the tailer returns.
func TestRV15ReplacedTranscriptArrivalIsConfirmed(t *testing.T) {
	older := func(n int) []string {
		var lines []string
		for i := 0; i < n; i++ {
			lines = append(lines, claudeUser(time.Now(), "an older prompt with some words in it, number "+strings.Repeat("x", 40)))
		}
		return lines
	}
	for _, c := range []struct {
		name       string
		afterTrack int // older lines the agent writes after the send was tracked, read before the rewrite
		rewritten  int // older lines the rewritten transcript starts with
	}{
		{"shorter_than_the_mark", 0, 0},
		{"longer_than_the_mark", 10, 22},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			writeLines(t, path, older(20)...)
			s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
			s.transcript, s.state = path, "working"
			tl := &tailer{path: path}
			p := &claudeParser{}
			feed := func() {
				if err := s.readTail(tl, p, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			feed()
			s.track("r1", "run the tests", "queue")
			mark, _ := os.Stat(path)
			if c.afterTrack > 0 {
				appendLines(t, path, older(c.afterTrack)...)
				feed()
			}
			// The agent rewrites its transcript, recording the send first.
			writeLines(t, path, append([]string{claudeUser(time.Now(), "run the tests")}, older(c.rewritten)...)...)
			if st, _ := os.Stat(path); c.rewritten > 0 && (st.Size() <= mark.Size() || st.Size() >= tl.offset) {
				t.Fatalf("rewritten to %d bytes; want between the mark %d and the read %d", st.Size(), mark.Size(), tl.offset)
			}
			feed()
			if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r1"}) {
				t.Fatalf("confirmed %v; want r1 (its arrival is in the replaced transcript)", ok)
			}
		})
	}
}

// rv15: words left on the parser's queue list by a message Claude took
// into its running turn (enqueue then remove: the list never drops them)
// mark a later prompt run off the queue with those words, though that
// prompt is the first record of the seat's send (its enqueue line showed
// Claude's paste placeholder).
func TestRV15StaleQueuedWordsHideAPastedSendsArrival(t *testing.T) {
	long := "check the migration plan\nagainst the schema"
	k1Case{"stale_entry_then_pasted_send",
		nil,
		[]string{
			// The person typed the same words while Claude worked; Claude
			// took them into that turn.
			k1Queue("enqueue", long), k1Queue("remove", long), k1TurnEnd(),
		},
		[][2]string{{"r1", long}},
		"",
		[]string{"r1"},
	}.runRV15(t, []string{k1Queue("enqueue", "[Pasted text #1 +1 lines]"), k1TurnEnd(), k1Queue("dequeue", "")}, claudeUser(time.Now(), long))
}

func (c k1Case) runRV15(t *testing.T, after []string, last string) {
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	p := &claudeParser{}
	feed := func(l string) {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	for _, l := range c.lines {
		feed(l)
	}
	for _, p := range c.later {
		s.track(p[0], p[1], "queue")
	}
	for _, l := range after {
		feed(l)
	}
	feed(last)
	if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, c.want) {
		t.Fatalf("confirmed %v; want %v", ok, c.want)
	}
}

// The same with a message Claude took off its queue and ran as a meta
// line: the dequeue took its words off the list, so they mark nothing
// later.
func TestK1DequeuedWordsLeaveTheQueue(t *testing.T) {
	long := "check the migration plan\nagainst the schema"
	meta := `{"type":"user","isMeta":true,"timestamp":"` + stamp(time.Now()) + `","message":{"content":"check the migration plan against the schema"}}`
	k1Case{"dequeued_ran_as_meta_then_pasted_send",
		nil,
		[]string{k1Queue("enqueue", long), k1TurnEnd(), k1Queue("dequeue", ""), meta, k1TurnEnd()},
		[][2]string{{"r1", long}},
		"",
		[]string{"r1"},
	}.runRV15(t, []string{k1Queue("enqueue", "[Pasted text #1 +1 lines]"), k1TurnEnd(), k1Queue("dequeue", "")}, claudeUser(time.Now(), long))
}

// Suspicions checked (round 15 reader), none failing.

// aa4b82b: a queued send's next turn starts late, after the grace: the
// send is lost by then, and its arrival in that turn still confirms it;
// no report goes while the 45 s wait from the loss runs.
func TestK1AQueuedSendWhoseTurnStartsLateIsConfirmed(t *testing.T) {
	s := newSeat(Spec{}, handOverWorks{}, &fakeHost{alive: true}, "x", "thread-1")
	s.state = "working"
	if _, err := s.Send("r1", "then commit the fix", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "turn", text: "Done."})
	now := time.Now()
	s.agent.(poller).poll(s, now.Add(heldGrace))
	s.checkScreen(now.Add(40 * time.Second))
	s.fold(obs{kind: "working"})
	s.fold(obs{kind: "user", text: "then commit the fix"})
	var got []string
	for _, ev := range deliveries(drain(s.events)) {
		got = append(got, fmt.Sprintf("%s:%v", ev.SendID, ev.OK))
	}
	if !slices.Equal(got, []string{"r1:true"}) {
		t.Fatalf("reported %v; want r1 confirmed", got)
	}
}

// aa4b82b with Antigravity's own turn end from its status file: a quiet
// stretch while agy's status says it runs is no turn end, so a send held
// in agy's queue stays held; one agy says it is idle at (an interrupt) is,
// and the queued message's turn then confirms it.
func TestK1AntigravityQueuedSendAndItsStatusTurnEnd(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const id = "11111111-2222-3333-4444-555555555555"
	for _, c := range []struct {
		state string
		lost  bool
	}{{"running", false}, {"idle", true}} {
		status := filepath.Join(t.TempDir(), "agy-status.json")
		if err := os.WriteFile(status, []byte(`{"conversation_id":"`+id+`","agent_state":"`+c.state+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		s := newSeat(Spec{StatusFile: status}, antigravityAgent{}, &fakeHost{alive: true}, "x", id)
		s.state = "working"
		if _, err := s.Send("r1", "then commit the fix", "after_turn"); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.lastLine = time.Now().Add(-time.Minute)
		s.mu.Unlock()
		now := time.Now()
		antigravityAgent{}.poll(s, now)
		antigravityAgent{}.poll(s, now.Add(heldGrace+time.Second))
		s.mu.Lock()
		lost := len(s.pending) == 1 && s.pending[0].lost
		s.mu.Unlock()
		if lost != c.lost {
			t.Fatalf("status %s: lost %v, want %v", c.state, lost, c.lost)
		}
		s.fold(obs{kind: "user", text: "then commit the fix"})
		if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r1"}) {
			t.Fatalf("status %s: confirmed %v; want r1", c.state, ok)
		}
	}
}

// 235bbde with pi: a hand-over pi's extension claimed during the seat's
// close is left to it (Send says held); one it gave back as refused (a
// .txt again) is no message pi has, and is taken back. The give-back
// comes 5 s or more after the claim (GIVE_BACK_MS), never within one
// Send, so a file the take-back finds is one pi never took.
func TestK1PiHandOverClaimedDuringTheCloseIsLeftToIt(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	s := newSeat(Spec{Name: "x", Spool: spool}, k1ClosedDuringHandOver{Agent: piAgent{name: "pi", label: "Pi", binary: "pi"}, take: true}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	close(s.done)
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatalf("Send: %v; want held (pi's extension has it)", err)
	}
	if names := spoolNames(spool); len(names) != 1 || !strings.HasSuffix(names[0], ".txt.taken") {
		t.Fatalf("spool %v; want the extension's claimed file", names)
	}
}
