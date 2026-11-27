package harness

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The closing turn's reply sets a timer and, in the same reply, tells the
// user about it with send_message. Nobody wrote: the timer the model itself
// set made the session stop ending, and the message goes out. (A check on
// closing words dropped it, and told the model the user had written.)
func TestK24AClosingReplyThatSetATimerStillTellsTheUser(t *testing.T) {
	const told = "I set a timer to check that main is still green in a moment."
	var phase atomic.Int32
	var refusedTold atomic.Bool
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if strings.Contains(allText(msgs), "not sent: ") {
			refusedTold.Store(true)
		}
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			args := `{"text":"` + told + `"}`
			return reply{calls: []event.ToolCall{
				tc("schedule", `{"spec":"in 300ms","note":"Check main is still green."}`),
				tc("send_message", args),
			}}
		case strings.Contains(tail, "The session is ending now"):
			return say("Main is still green; done.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	if phase.Load() < 2 {
		t.Fatalf("setup: no closing call (phase %d)", phase.Load())
	}
	said := s.ui.said()
	got := false
	for _, m := range said {
		got = got || strings.Contains(m, told)
	}
	if !got {
		t.Fatalf("exit %d: the message about the timer the closing reply set was never sent (model told the user wrote or an agent came up: %v); said %q", code, refusedTold.Load(), said)
	}
}

// closed is closed once the closing call has been made (phase 2), the
// agent it started is on the record and no turn is running.
func closed(s *session, phase *atomic.Int32) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			done := false
			if phase.Load() >= 2 {
				s.rt.sync(func() {
					started := false
					for _, ev := range s.rt.st.Events {
						var d event.RelayStartData
						if ev.Type == event.RelayStart && ev.Decode(&d) == nil && d.Profile == "codex" {
							started = true
						}
					}
					done = started && !s.rt.turns.busy
				})
			}
			if done {
				break
			}
			runtime.Gosched()
		}
		close(ch)
	}()
	return ch
}

// The sibling with relay_start: the closing reply starts an agent and tells
// the user so in the same reply. The message goes out.
func TestK24AClosingReplyThatStartedAnAgentStillTellsTheUser(t *testing.T) {
	const told = "I am starting Codex to paint the second coat."
	first, second := newFakeSeat("First coat on."), newFakeSeat("Second coat on.")
	useSeatList(t, first, second)
	var phase atomic.Int32
	var refusedTold atomic.Bool
	after := make(chan struct{})
	once := sync.OnceFunc(func() { close(after) })
	s, _ := r12Start(t, nil, func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if strings.Contains(allText(msgs), "not sent: ") {
			refusedTold.Store(true)
		}
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			if phase.Load() >= 2 {
				once()
			}
			return hold()
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			args := `{"text":"` + told + `"}`
			return reply{calls: []event.ToolCall{
				tc("relay_start", `{"profile":"codex"}`),
				tc("send_message", args),
			}}
		case strings.Contains(tail, "The session is ending now"):
			return say("Both coats are on; done.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		case strings.Contains(tail, "finished a turn"):
			return stopsWhenDone()
		}
		return hold()
	})
	code := -1
	select {
	case <-after:
	case code = <-s.done:
		s.done <- code
	case <-closed(s, &phase):
	}
	if phase.Load() < 2 {
		t.Fatalf("setup: no closing call (phase %d)", phase.Load())
	}
	said := s.ui.said()
	got := false
	for _, m := range said {
		got = got || strings.Contains(m, told)
	}
	if !got {
		t.Fatalf("exit %d: the message about the agent the closing reply started was never sent (model told the user wrote or an agent came up: %v); said %q", code, refusedTold.Load(), said)
	}
}
