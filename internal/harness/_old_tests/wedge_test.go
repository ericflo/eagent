package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// burstSeat is a relay seat whose agent produces a burst of transcript
// observations, as a reattach backfill or a chatty agent does.
type burstSeat struct {
	events chan relay.Event
	quit   chan struct{} // closed by Close; the burst stops and closes events
	once   sync.Once
	mu     sync.Mutex
	sent   []string
}

func (f *burstSeat) Events() <-chan relay.Event { return f.events }
func (f *burstSeat) Send(id, text, mode string) (string, error) {
	f.mu.Lock()
	f.sent = append(f.sent, text)
	f.mu.Unlock()
	return "typed", nil
}
func (f *burstSeat) Interrupt() error           { return nil }
func (f *burstSeat) Keys(...string) error       { return nil }
func (f *burstSeat) Screen(int) (string, error) { return "", nil }
func (f *burstSeat) Kill() error                { f.Close(); return nil }
func (f *burstSeat) Close()                     { f.once.Do(func() { close(f.quit) }) }
func (f *burstSeat) Snapshot() relay.Status     { return relay.Status{State: "working", Since: time.Now()} }

// A user message recorded while the loop's queue is full (a burst of relay
// events) must not wedge the loop: it used to queue the forward to the agent
// on its own channel and wait on itself forever.
func TestFullLoopQueueDoesNotWedgeTheSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := &burstSeat{events: make(chan relay.Event, 256), quit: make(chan struct{})}
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name)}, nil
	}
	defer func() { launchRelay = old }()
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for Codex"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	})
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Interactive: true, Prompt: "start", Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan int, 1)
	go func() { runDone <- rt.Run(ctx) }()
	time.Sleep(1500 * time.Millisecond)
	go func() {
		defer close(seat.events)
		for i := 0; ; i++ {
			ev := relay.Event{Kind: relay.KindProgress, Text: "step"}
			if i >= 4000 {
				<-seat.quit
				return
			}
			select {
			case seat.events <- ev:
			case <-seat.quit:
				return
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(rt.loop) < cap(rt.loop) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "please also run the tests", From: "web"}); err != nil {
		t.Fatal(err)
	}
	forwarded := func() bool {
		seat.mu.Lock()
		defer seat.mu.Unlock()
		for _, s := range seat.sent {
			if s != "" && contains(s, "please also run the tests") {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(30 * time.Second); !forwarded(); {
		if time.Now().After(deadline) {
			t.Fatalf("the message never reached the agent (loop queue %d/%d)", len(rt.loop), cap(rt.loop))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the session did not stop")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
