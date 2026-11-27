package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// The run the shutdown is ending was restarting its agent (a /restart, a
// model change) when the user pressed Stop. The Stop kills the agents the
// record shows before it waits for the run (8ca7c21): that is the old
// agent's terminal, which the restart has already stopped. The run's end
// then records the overtaken start (finish -> finishLaunch): the new agent
// is up, in the terminal of the same name (relay.Target is the session
// and the relay's name). Once the run lets go, stopAgents skips the name
// as already killed, so the new agent is never killed, its relay.end is
// recorded all the same, and the answer says it was stopped.
func TestSR4WebAStopDuringARestartAtTheShutdownLeavesTheNewAgentRunning(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	var mu sync.Mutex
	up := map[string]bool{"tmux eagent-sr-claude": false} // the restart stopped the old agent
	var kills []string
	old := killAgent
	killAgent = func(host, target string) error {
		mu.Lock()
		defer mu.Unlock()
		kills = append(kills, host+" "+target)
		up[host+" "+target] = false
		return nil
	}
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	rt, ui := r11Ended(t, cfg, project, r11Session(t, project))
	lock, _, err := store.Open(info.Path, time.Now()) // the run, finishing
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	slow := &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = slow
	s.mu.Unlock()
	s.rememberHosted()
	code, body, stopDone := make(chan int, 1), make(chan string, 1), make(chan struct{})
	go func() {
		w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
		code <- w.Code
		body <- w.Body.String()
		close(stopDone)
	}()
	k7Waiting(t, "web.(*Server).stopAgentsOf", stopDone) // it waits for the run
	// The run's end records the start the shutdown overtook: the new agent
	// is up in the same terminal.
	mu.Lock()
	up["tmux eagent-sr-claude"] = true
	mu.Unlock()
	if _, err := lock.Append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-sr-claude", Resumed: true})); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(slow.done)
	if c, b := <-code, <-body; c != http.StatusOK || !strings.Contains(b, "Stopped, with") {
		t.Fatalf("stop: %d %s", c, b)
	}
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the record still shows %q running", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if up["tmux eagent-sr-claude"] {
		t.Fatalf("answered Stopped and recorded relay.end, but the restarted agent still runs in eagent-sr-claude (kills: %q, all before its start was recorded)", kills)
	}
}

// A message held for the end of Claude's turn waits in its spool when the
// shutdown ends the run, and the run is slow to finish (the premise of
// 8ca7c21). The Stop kills the agent before it waits for the run. The run
// still watches its agent until finish closes the seats, and the seat's
// liveness check (seat.go, two misses aliveEvery apart) finds the pane
// gone and sweeps the spool (sweepSpool: the rename a hook claims with,
// then the remove), reporting the message not sent; the run is ending, so
// that report is dropped (harness relay.go, "if r.ending { return }").
// The test does the sweep's two file steps itself, in the wait. Once the
// run lets go, stopAgents finds nothing to take back and records the
// message unconfirmed ("whether it arrived cannot be confirmed"), though
// it never left the spool unread. With the kill after the wait (before
// 8ca7c21) the spool was intact for stopAgents, which recorded not_sent
// (a630b0b).
//
// Changed with the fix: the Stop kills nothing before the run has let go,
// so the test checks that (it used to wait for that early kill) and leaves
// the spool alone (the seat sweeps it only once it finds the pane gone,
// which nothing does now while the run finishes).
func TestSR4WebAHeldMessageTheSeatSweepsAfterTheEarlyKillIsCalledUnconfirmed(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	killedOnce := make(chan struct{})
	var once sync.Once
	old := killAgent
	killAgent = func(host, target string) error { once.Do(func() { close(killedOnce) }); return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "h1", From: "user", Mode: "after_turn", Text: "then run the linter"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	spool := filepath.Join(info.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(spool, "1700000000000000000-h1.txt")
	if err := os.WriteFile(held, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, ui := r11Ended(t, cfg, project, r11Session(t, project))
	lock, _, err := store.Open(info.Path, time.Now()) // the run, finishing slowly
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	slow := &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = slow
	s.mu.Unlock()
	s.rememberHosted()
	code, stopDone := make(chan int, 1), make(chan struct{})
	go func() {
		code <- do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil).Code
		close(stopDone)
	}()
	k7Waiting(t, "web.(*Server).stopAgentsOf", stopDone)
	select {
	case <-killedOnce:
		t.Fatal("the agent was killed before the run let go: its seat, still watching, would sweep the spool")
	default:
	}
	_ = lock.Close()
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(slow.done)
	if c := <-code; c != http.StatusOK {
		t.Fatalf("stop: %d", c)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "h1" {
			outcomes = append(outcomes, d.Outcome+": "+d.Detail)
		}
	}
	if len(outcomes) != 1 || !strings.HasPrefix(outcomes[0], event.DeliveryNotSent) {
		t.Fatalf("the message was swept from the spool unread after the kill; its outcomes on record: %q, want one not_sent", outcomes)
	}
}
