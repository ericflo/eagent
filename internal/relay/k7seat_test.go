package relay

// The sixteenth review's fifth pass over the seat (sr5seat).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sr5ClaudeSeat is a Claude seat on session a-id (transcript a, holding
// lines), with the SessionStart hook's file naming a-id, watched at piLook.
func sr5ClaudeSeat(t *testing.T, h Host, name string, lines ...string) (s *Seat, spool, a, b string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b = filepath.Join(project, "a-id.jsonl"), filepath.Join(project, "b-id.jsonl")
	writeLines(t, a, lines...)
	state := t.TempDir()
	spool = filepath.Join(state, "after_turn")
	sr4ClaudeHook(t, spool, "a-id", a, "startup")
	s = newSeat(Spec{Spool: spool, StatusFile: filepath.Join(state, "claude-session.json"), Hook: "eagent relay-hook"}, claudeAgent{}, h, name+"-"+filepath.Base(state), "a-id")
	s.state, s.poll = "idle", piLook
	return s, spool, a, b
}

// SR5-1. Claude Code's /clear answers to /new and /reset too (its command
// table: name "clear", aliases ["reset","new"], Claude Code 2.1.332).
// claudeAgent.movesSession knows only "/clear" and "/resume", so a /new
// typed into Claude sets no pending move: the seat says quiet as soon as
// the command's delivered is out, before Claude's SessionStart hook names
// the new session. A model change waiting for quiet then starts Claude
// again on the conversation the user just left (the SR4-2 case, for
// Claude's aliases). /clear itself passes (TestK5QuietAfterClaudesClearWaitsForItsHook).
func TestSr5SeatClaudeNewAliasIsQuietBeforeItsMove(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	for _, cmd := range []string{"/new", "/reset"} {
		t.Run(cmd[1:], func(t *testing.T) {
			s, spool, a, b := sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, "sr5-alias", claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
			_ = a
			go s.watch()
			t.Cleanup(s.Close)
			if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
				t.Fatalf("never quiet at the start: %q", got)
			}
			if _, err := s.Send("c1", cmd, "queue"); err != nil {
				t.Fatal(err)
			}
			within(looks(3), func() bool { return false }) // the hook is a process of its own
			sr4ClaudeHook(t, spool, "b-id", b, "clear")
			got := quietOrder(t, s, 5*time.Second)
			if n := len(got); n < 2 || got[n-1] != "quiet" || got[n-2] != "inner b-id" {
				t.Fatalf("%s: events %q; want Claude's move to b-id said before quiet, as for /clear", cmd, got)
			}
		})
	}
}

// SR5-1, pi: pi 1.6.1's /clone ("Duplicate the current session at the
// current position": handleCloneCommand calls runtimeHost.fork(leafId,
// {position: "at"}) and says "Cloned to new session") moves pi to a new
// session as /fork does, but piAgent.movesSession knows only /new,
// /resume and /fork. The same test as TestK5QuietAfterPisNewOnceTheMoveIsSaid,
// with /clone.
func TestSr5SeatPiCloneIsQuietBeforeItsMove(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	dir := t.TempDir()
	first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
	writeLines(t, first, piHeaderLine("first", time.Now()))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: first, SessionID: "first", SessionEntries: entries(0)})
	h := &k1Host{fakeHost: &fakeHost{alive: true}}
	moved := make(chan struct{})
	h.enter = func() error {
		go func() { // pi moves a few looks later
			<-moved
			writeLines(t, second, piHeaderLine("second", time.Now()))
			writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		}()
		return nil
	}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "sr5-pi-clone-"+filepath.Base(dir), "first")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("c1", "/clone", "queue"); err != nil {
		t.Fatal(err)
	}
	within(looks(3), func() bool { return false })
	close(moved)
	got := quietOrder(t, s, 5*time.Second)
	if len(got) != 3 || got[0] != "delivered c1" || got[1] != "inner second" || got[2] != "quiet" {
		t.Fatalf("events %q; want the delivered, the move, then quiet (as for /new)", got)
	}
}

// SR5-2. Status.Quiet is true while one of the agent's own commands is
// being typed: Send tracks no command, and puts its delivered (and, for
// /clear, the pending move) in place only once typeText has returned.
// EVENTS.md: quiet is "no send it waits to confirm or still being typed".
// A model change asked in that window (heldBack reads Snapshot) starts the
// agent again at once; the previous head held it (heldBack's "typing",
// from the record's undelivered /clear).
func TestSr5SeatQuietWhileACommandIsBeingTyped(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	typing, release := make(chan struct{}), make(chan struct{})
	h := &k1Host{fakeHost: &fakeHost{alive: true, screen: claudePromptScreen}}
	h.enter = func() error {
		close(typing)
		<-release
		return nil
	}
	s, _, _, _ := sr5ClaudeSeat(t, h, "sr5-typing", claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	sent := make(chan error, 1)
	go func() {
		_, err := s.Send("c1", "/clear", "queue")
		sent <- err
	}()
	<-typing
	quiet := s.Snapshot().Quiet
	close(release)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if quiet {
		t.Fatal("the seat said quiet while /clear was being typed into Claude; want not quiet until its delivered and its move are in place")
	}
}
