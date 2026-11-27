package relay

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SR9-4, Antigravity: agy on another conversation than the one the seat
// follows (one of its own commands, or its picker) says so in its status
// file; the seat follows it there, and says the move.
func TestK10AntigravityOnAnotherConversationIsFollowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, second := "23124aa2-ad6d-4418-b186-4c4c21fa3001", "23124aa2-ad6d-4418-b186-4c4c21fa3002"
	transcript := func(id string) string {
		path := filepath.Join(antigravityHome(), "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a, b := transcript(first), transcript(second)
	status := filepath.Join(t.TempDir(), "status.json")
	on := func(id string) {
		if WriteAntigravityStatus(status, strings.NewReader(`{"conversation_id":"`+id+`","agent_state":"idle"}`), io.Discard) != 0 {
			t.Fatal("status not written")
		}
	}
	on(first)
	s := newSeat(Spec{StatusFile: status}, antigravityAgent{}, &fakeHost{alive: true, screen: "> \nAntigravity relay · idle · m"}, "k10-agy-move", first)
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if p, ok := transcriptFound(s, 5*time.Second); !ok || p != a {
		t.Fatalf("found %q; want %s", p, a)
	}
	on(second)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == KindInner && ev.InnerID == second && ev.Transcript == b {
				return
			}
		case <-deadline:
			t.Fatalf("agy is on %s and the seat still follows %s", second, s.Snapshot().Transcript)
		}
	}
}

// SR9-3, compacting: a message typed into pi while it compacts waits until
// the compaction is over; pi's status says idle and compacting all that
// time. The 45-second check counts that as at work, as it does a busy
// screen, and keeps the send for its arrival.
func TestK10SendTypedWhilePiCompactsIsKeptPast45s(t *testing.T) {
	isolatePi(t)
	savedSettle := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = savedSettle })
	status := filepath.Join(t.TempDir(), "pi-status.json")
	writePiStatus(t, status, piStatus{State: "idle", Compacting: true})
	h := &fakeHost{alive: true, screen: piFooterScreen}
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k10-pi-compact", "sess-1")
	s.state = "idle"
	if _, err := s.Send("q1", "also run the linter", "queue"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.checkScreen(now.Add(43500 * time.Millisecond)) // the screen checks before
	s.checkScreen(now.Add(46 * time.Second))
	if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
		t.Fatalf("reported %+v, pending %v; want q1 kept while pi compacts", got, s.Snapshot().Pending)
	}
	writePiStatus(t, status, piStatus{State: "idle"})
	s.fold(obs{kind: "user", text: "also run the linter", at: time.Now()})
	if got := deliveredEvents(s); len(got) != 1 || !got[0].OK {
		t.Fatalf("outcomes %+v; want q1 delivered by its arrival after the compaction", got)
	}
}
