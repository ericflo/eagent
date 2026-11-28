package harness

// The seventeenth review's findings on a send being typed as its agent is
// stopped with the session, and on a second switch's brief.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r17HeldSeat is an agent whose terminal is still taking a message when
// it is stopped: Send returns only once the test lets it, with what the
// test says the typing came to.
type r17HeldSeat struct {
	*fakeSeat
	entered chan string
	release chan struct{}
	spool   func() string // where a message held for the turn's end waits
	err     error
}

func (s *r17HeldSeat) Send(id, text, mode string) (string, error) {
	if mode == "after_turn" {
		dir := s.spool()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id)), []byte(text), 0o644); err != nil {
			return "", err
		}
	}
	s.entered <- id
	<-s.release
	if s.err != nil {
		return "", s.err
	}
	return "typed", nil
}

// Finding 3. Stop session comes while a message of the user's is being
// typed into the agent. The typing ends after the loop has closed: typed
// in part, typed whole into the agent that is now stopped, or (held for
// the end of its turn) written where its hook would have read it. Nothing
// recorded how it went: the record a resume reads still said the message
// waits for an agent that is stopped, and the held one stayed on disk.
func TestAMessageBeingTypedAsTheSessionIsStoppedHasAnOutcome(t *testing.T) {
	for _, c := range []struct {
		name, mode string
		err        error
		want       state.SendOutcome
	}{
		{"its typing was cut off part way", "queue", &relay.TypingError{Err: errors.New("the terminal closed before the message was entered")}, state.SendUnconfirmed},
		{"it was typed whole", "queue", nil, state.SendUnconfirmed},
		{"it was held for the end of the turn", "after_turn", nil, state.SendNotSent},
	} {
		t.Run(c.name, func(t *testing.T) {
			var rt *Runtime
			seat := &r17HeldSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 1), release: make(chan struct{}), err: c.err}
			seat.spool = func() string { return filepath.Join(rt.sess.Path, "relays", "claude", "after_turn") }
			useGatedLaunches(t, []relaySeat{seat}, nil)
			t.Setenv("EAGENT_TEST_KEY", "x")
			relayAgentHomes(t)
			rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude", Interactive: true}, &fakeUI{input: make(chan string, 16)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			t.Cleanup(cancel)
			done := make(chan int, 1)
			go func() { done <- rt.Run(ctx) }()
			var seq int64
			rt.sync(func() {
				rt.st.Relays["claude"].State = "working"
				d := event.UserMessageData{Text: "then run the linter", Delivery: c.mode}
				ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
				seq = ev.Seq
				rel, why := rt.routeUserMessage(d)
				if rel == nil {
					t.Errorf("not routed: %s", why)
					return
				}
				rt.forwardUserMessage(rel, ev.Seq, d)
			})
			var id string
			select {
			case id = <-seat.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the message was never being typed")
			}
			spool := seat.spool()
			rt.post(func() { rt.stopSession("stopped") })
			// The typing ends once the loop takes nothing more.
			waitFor(t, "the loop's end", func() bool { return !rt.enqueue(func() {}) })
			close(seat.release)
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the session did not end")
			}
			evs, err := store.Read(rt.sess.Path)
			if err != nil {
				t.Fatal(err)
			}
			st := state.Replay(evs)
			if o := st.SendOutcome(id); o != c.want {
				t.Errorf("the record a resume reads says of the message: %q, want %q", o, c.want)
			}
			if n := len(st.Relays["claude"].Undelivered); n != 0 {
				t.Errorf("%d message(s) still wait on the agent that was stopped", n)
			}
			if left, _ := filepath.Glob(filepath.Join(spool, "*-"+id+".txt")); len(left) != 0 {
				t.Errorf("the held message is still on disk: %v", left)
			}
			if got := sendsReceipt(st, map[string]int64{id: seq}, seq); c.want == state.SendNotSent && got != "failed" {
				t.Errorf("the phone shows the message as %q", got)
			}
			var ended bool
			for _, ev := range evs {
				var d event.RelayDeliveredData
				switch {
				case ev.Type == event.SessionEnd:
					ended = true
				case ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id && ended:
					t.Errorf("its outcome was written after the session's end")
				}
			}
		})
	}
}

// Finding 4. The user's message was typed into Codex and not yet seen
// arriving when eagent switched to Claude: the brief quoted it whole, the
// brief arrived, and Claude finished a turn on it. eagent then switches to
// pi, and that brief does not go (pi's terminal takes nothing, or the
// session ends before it is typed). The message reached Claude: it was
// recorded "not sent" all the same, the phone offered to send it again, and
// relay_forward would have.
func TestABriefThatDoesNotGoDoesNotFailAMessageAnEarlierBriefGave(t *testing.T) {
	for _, how := range []string{"the terminal takes nothing", "the session ends before the brief is typed"} {
		t.Run(how, func(t *testing.T) {
			codex := newK2FeedSeat(t)
			claude := &k2FeedSeat{fakeSeat: newFakeSeat("two"), feed: make(chan relay.Event), gone: make(chan struct{})}
			t.Cleanup(claude.end)
			waiting := &r12dWaitingSeat{fakeSeat: newFakeSeat("three"), entered: make(chan string, 4), closed: make(chan struct{})}
			var pi relaySeat = &r16RefusingSeat{newFakeSeat("three")}
			if how != "the terminal takes nothing" {
				pi = waiting
			}
			rt, g, done := r10Session(t, codex, claude, pi)
			seq, id := k2Forward(t, rt, codex, "then run the linter", "queue")
			g.release <- struct{}{}
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			codex.waitGone(t)
			recv(t, switched, "switch")
			codex.end()
			waitFor(t, "the brief typed into Claude", func() bool { return len(claude.sentTexts()) > 0 })
			if brief := claude.sentTexts()[0]; !strings.Contains(brief, "- then run the linter\n") {
				t.Fatalf("setup: the brief does not quote the message:\n%s", brief)
			}
			k1QueuesIdle(t, rt)
			claude.say(
				relay.Event{Kind: relay.KindDelivered, SendID: lastSendID(rt), OK: true},
				relay.Event{Kind: relay.KindTurn, Text: "The linter is clean."},
			)
			rt.sync(func() {})
			g.release <- struct{}{}
			again := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi"})
			claude.waitGone(t)
			if pi == waiting {
				select {
				case <-waiting.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("pi's brief was never begun")
				}
			} else {
				recv(t, again, "the second switch")
				k1QueuesIdle(t, rt)
				if o := k2Outcome(rt, lastSendID(rt)); o != state.SendNotSent {
					t.Fatalf("setup: pi's brief is %q", o)
				}
			}
			claude.end()
			st := state.Replay(r10Quit(t, rt, g, 0, done))
			if o := st.SendOutcome(id); o == state.SendNotSent {
				t.Errorf("Claude had the message in its brief and finished a turn on it, and the record says of it: %q", o)
			}
			if got := sendsReceipt(st, map[string]int64{id: seq}, seq); got == "failed" {
				t.Errorf("the phone shows the message as %q, which offers to send it again", got)
			}
			if sent, _ := forwardedAs(st, "codex", seq, ""); sent == "" {
				t.Errorf("relay_forward would type the message again")
			}
		})
	}
}

// The same two switches with Claude replaced before it finished a turn,
// whether or not its brief was seen arriving: Claude was stopped part way,
// so the message is still quoted whole for pi, however long it is, and is
// still pi's brief's to lose. When that brief does not go, no agent that
// runs has the message, and eagent is told to pass it on.
func TestABriefThatDoesNotGoStillFailsAMessageNoAgentFinishedATurnOn(t *testing.T) {
	long := "then run the linter" + strings.Repeat(", and say what it found in each file", 100)
	for _, read := range []string{"Claude had read nothing", "Claude's brief had arrived"} {
		t.Run(read, func(t *testing.T) {
			codex := newK2FeedSeat(t)
			claude := &k2FeedSeat{fakeSeat: newFakeSeat("two"), feed: make(chan relay.Event), gone: make(chan struct{})}
			t.Cleanup(claude.end)
			rt, g, done := r10Session(t, codex, claude, &r16RefusingSeat{newFakeSeat("three")})
			_, id := k2Forward(t, rt, codex, long, "queue")
			g.release <- struct{}{}
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			codex.waitGone(t)
			recv(t, switched, "switch")
			codex.end()
			k1QueuesIdle(t, rt)
			if read == "Claude's brief had arrived" {
				claude.say(relay.Event{Kind: relay.KindDelivered, SendID: lastSendID(rt), OK: true})
				rt.sync(func() {})
			}
			g.release <- struct{}{}
			again := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi"})
			claude.waitGone(t)
			recv(t, again, "the second switch")
			claude.end()
			k1QueuesIdle(t, rt)
			var brief string
			last := lastSendID(rt)
			rt.sync(func() {
				if d, ok := sendRecord(rt.st, last); ok {
					brief = d.Text
				}
			})
			if !strings.Contains(brief, "- "+long+"\n") {
				t.Errorf("pi's brief does not quote the message whole (%d characters of brief)", len(brief))
			}
			if o := k2Outcome(rt, id); o != state.SendNotSent {
				t.Errorf("no agent that runs has the message, and the record says of it: %q", o)
			}
			r10Quit(t, rt, g, 0, done)
		})
	}
}

// A switch leaves a message of the user's that its brief quotes without an
// outcome of its own (the brief is its way to the new agent). The session
// then ends: the record still says so, as after a resume, and nothing is
// told to send the message again.
func TestAMessageLeftToABriefIsStillLeftToItWhenTheSessionEnds(t *testing.T) {
	codex := newK2FeedSeat(t)
	rt, g, done := r10Session(t, codex, newFakeSeat("two"))
	_, id := k2Forward(t, rt, codex, "then run the linter", "queue")
	g.release <- struct{}{}
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	codex.waitGone(t)
	recv(t, switched, "switch")
	codex.end()
	k1QueuesIdle(t, rt)
	st := state.Replay(r10Quit(t, rt, g, 0, done))
	if o := st.SendOutcome(id); o != state.SendWaiting {
		t.Errorf("the brief gave Claude the message, and the record says of it: %q", o)
	}
}
