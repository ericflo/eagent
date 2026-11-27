package relay

// The sixteenth review of the relay area, fifth pass (sr5).

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// SR5-1, the seat's half. The harness holds a model change back while the
// seat is not quiet (heldBack, from Status.Quiet), and EVENTS.md promises
// the change waits while a send is "still being typed". An ordinary send is
// tracked before it is typed (pending), so it holds; one of the agent's own
// commands (pi's /new, /compact, /clear) is not tracked at all while it is
// pasted, settled and entered: held and moveLook are set only after
// typeText returns. For those ~350 ms (typeSettle) and the paste itself
// the seat says it is quiet, with the command half typed.
func TestSR5SeatIsQuietWhileItTypesOneOfTheAgentsOwnCommands(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	dir := t.TempDir()
	pasting, release := make(chan struct{}, 1), make(chan struct{})
	h := &k1Host{fakeHost: &fakeHost{alive: true}}
	h.paste = func() error {
		pasting <- struct{}{}
		<-release
		return nil
	}
	s := newSeat(Spec{StatusFile: filepath.Join(dir, "pi-status.json"), Dir: dir}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "sr5-cmd-"+filepath.Base(dir), "first")
	s.state = "idle"
	if !s.Snapshot().Quiet {
		t.Fatal("an idle seat with nothing sent is not quiet")
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Send("c1", "/new", "queue")
		done <- err
	}()
	select {
	case <-pasting:
	case <-time.After(5 * time.Second):
		t.Fatal("the command was never pasted")
	}
	quiet := s.Snapshot().Quiet
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}
	if quiet {
		t.Error("the seat says it is quiet while /new is being typed into pi's prompt: a model change asked now (thread settings, /model) restarts pi under the command")
	}
	// The same send as ordinary words holds (tracked before it is typed).
	h.paste = func() error {
		pasting <- struct{}{}
		<-release
		return nil
	}
	release = make(chan struct{})
	go func() {
		_, err := s.Send("r2", "run the tests", "queue")
		done <- err
	}()
	<-pasting
	words := s.Snapshot().Quiet
	close(release)
	<-done
	if words {
		t.Error("the seat is quiet while an ordinary message is typed too")
	}
}

// A send that fails (its paste does) leaves nothing tracked and sends no
// event of the seat's own: quiet is said again once it holds, so a change
// waiting on it goes ahead.
func TestK6QuietIsSaidAfterASendThatFailed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &k1Host{fakeHost: &fakeHost{alive: true}, paste: func() error { return errors.New("no such pane") }}
	s := newSeat(Spec{}, claudeAgent{}, h, "k6-fail", "id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("r1", "run the tests", "queue"); err == nil {
		t.Fatal("the paste failed; Send said it went")
	}
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("after the failed send: %q; want quiet said again", got)
	}
}
