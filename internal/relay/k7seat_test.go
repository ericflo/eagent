package relay

// The sixteenth review's fifth pass over the seat (sr5seat).

import (
	"os"
	"path/filepath"
	"slices"
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

// SR5-3. The sibling of 691e5fa: a command whose Enter goes while the seat
// is closing (a restart's Kill closes the seat, waits for the watcher, and
// only then kills the pane) is typed, but Send puts its delivered in held
// after the watcher has already released what was held and ended. Nothing
// sends it: the record settles the command as a send that cannot be
// confirmed, though Send typed it and typed is delivered.
func TestSr5SeatCommandTypedWhileTheSeatClosesHasNoDelivered(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &k1Host{fakeHost: &fakeHost{alive: true, screen: claudePromptScreen}}
	var s *Seat
	h.enter = func() error {
		s.Close() // the close lands as the Enter goes; the pane is still up
		return nil
	}
	s = newSeat(Spec{}, claudeAgent{}, h, "sr5-cmd-closing", "id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.looks > 0 }) {
		t.Fatal("never looked")
	}
	res, err := s.Send("c1", "/compact", "queue")
	if err != nil {
		t.Fatal(err)
	}
	delivered := false
	for ev := range s.Events() {
		delivered = delivered || ev.Kind == KindDelivered && ev.SendID == "c1" && ev.OK
	}
	if !delivered {
		t.Fatalf("Send said %q (typed %q), and the seat closed with no delivered for it; want typed to be delivered", res, k1Typed(h.fakeHost))
	}
}

// SR5-4. A message typed while Claude works is confirmed by its enqueue
// line, and runs only once Claude takes it off its queue after the turn
// ends (dequeue, then the prompt). The seat says quiet at a look that read
// the turn end and not yet the dequeue: idle, nothing pending. A model
// change waiting for quiet then kills Claude with the message still in its
// queue (memory only): it never runs, and the record says delivered. The
// parser knows the queue still holds it (claudeParser.queued). On main the
// waiting change applied at the turn end itself (relay.turn), which loses
// it the same way.
func TestSr5SeatClaudeQuietWhileItsQueueHoldsAMessage(t *testing.T) {
	s, _, a, _ := sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, "sr5-queue", claudeUser(time.Now().Add(-time.Second), "sort the shelves"))
	go s.watch()
	t.Cleanup(s.Close)
	if !eventually(func() bool { return s.Snapshot().State == "working" }) {
		t.Fatal("never working")
	}
	s.track("r1", "also update the docs", "queue") // typed while Claude works
	appendLines(t, a, k1Queue("enqueue", "also update the docs"))
	if got := quietOrder(t, s, looks(8)); !slices.Contains(got, "delivered r1") {
		t.Fatalf("events %q; want r1 confirmed by its enqueue line", got)
	}
	appendLines(t, a, `{"type":"assistant","timestamp":"`+stamp(time.Now())+`","message":{"content":[{"type":"text","text":"Sorted."}]}}`, k1TurnEnd())
	got := quietOrder(t, s, looks(12))
	if got[len(got)-1] == "quiet" {
		t.Fatalf("events %q: quiet with r1 still in Claude's queue (no dequeue yet); want not quiet until Claude has taken it", got)
	}
}

// SR5-5. A move the seat has seen but not yet followed (refollow: the next
// look says it, KindInner) does not keep the seat from quiet. EVENTS.md:
// quiet is "no move to another session not yet followed". Here Claude
// ends a turn and its SessionStart hook names the session /clear started
// (typed at Claude's own terminal) before the same look: the seat says
// quiet in that look, before the move, so a waiting model change starts
// Claude again on the session it left.
func TestSr5SeatQuietBeforeAMoveItHasSeenIsSaid(t *testing.T) {
	s, spool, _, b := sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, "sr5-seen", claudeTurnLines("sort the shelves", "Sorted.", time.Now())...)
	s.state = "working"
	sr4ClaudeHook(t, spool, "b-id", b, "clear") // before the first look
	go s.watch()
	t.Cleanup(s.Close)
	var order []string
	deadline := time.After(5 * time.Second)
	for len(order) == 0 || order[len(order)-1] != "inner b-id" {
		select {
		case ev := <-s.events:
			switch ev.Kind {
			case KindInner:
				order = append(order, "inner "+ev.InnerID)
			case KindTurn:
				order = append(order, "turn")
			case KindQuiet:
				order = append(order, "quiet")
			}
		case <-deadline:
			t.Fatalf("events %q; never followed b-id", order)
		}
	}
	for _, o := range order {
		if o == "quiet" {
			t.Fatalf("events %q: quiet before the move to b-id was said", order)
		}
	}
}
