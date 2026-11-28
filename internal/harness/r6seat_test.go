package harness

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// stoppedWithSends runs a session whose Claude takes one turn, records two
// sends it has not confirmed (one typed, one held for the end of a turn)
// and one it has, and stops eagent with the agent still running. It
// returns the session's record and the two unconfirmed ids.
func stoppedWithSends(t *testing.T) (path, project string, model *scripted, open []string) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	project = t.TempDir()
	useFakeRelays(t, newFakeSeat("Working on it."))
	model = newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	rt, err := New(testConfig(model.srv.URL), Options{Project: project, Prompt: "keep it running", Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the agent's turn", func() bool {
		n := 0
		rt.sync(func() { n = rt.st.Relays["claude"].Turns })
		return n == 1
	})
	rt.sync(func() {
		for _, s := range []struct{ text, mode string }{{"check the version", "queue"}, {"then run the tests", "after_turn"}, {"and push", "queue"}} {
			id := rt.st.NextRelaySendID()
			rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: id, From: "eagent", Mode: s.mode, Text: s.text}))
			if s.text == "and push" {
				rt.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: id, OK: true}))
				continue
			}
			open = append(open, id)
		}
	})
	cancel()
	<-done
	return rt.sess.Path, project, model, open
}

// captureAttach makes reattaching hand back a fake seat (or fail with err)
// and keeps the spec each reattach was given.
func captureAttach(t *testing.T, fail error) func() []relay.Spec {
	t.Helper()
	var mu sync.Mutex
	var specs []relay.Spec
	old := attachRelay
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		mu.Lock()
		defer mu.Unlock()
		specs = append(specs, spec)
		if fail != nil {
			return nil, fail
		}
		return newFakeSeat("again"), nil
	}
	t.Cleanup(func() { attachRelay = old })
	return func() []relay.Spec { mu.Lock(); defer mu.Unlock(); return slices.Clone(specs) }
}

// A resumed session hands the reattached seat the sends its record still
// waits on, with their words, mode and time, oldest first, so the seat can
// confirm them from the transcript. A second resume in a row, with nothing
// confirmed between, hands it the same ones.
func TestAReattachedAgentIsHandedTheSendsTheRecordStillWaitsOn(t *testing.T) {
	path, project, model, open := stoppedWithSends(t)
	specs := captureAttach(t, nil)
	for round := 1; round <= 2; round++ {
		rt, err := Resume(testConfig(model.srv.URL), Options{Project: project}, &fakeUI{}, path)
		if err != nil {
			t.Fatal(err)
		}
		rt.sess.Close()
		got := specs()
		if len(got) != round {
			t.Fatalf("round %d: %d reattaches", round, len(got))
		}
		r := got[round-1].Restore
		var ids []string
		for _, s := range r {
			ids = append(ids, s.ID)
		}
		if !slices.Equal(ids, open) {
			t.Fatalf("round %d: restored %v, want %v", round, ids, open)
		}
		if r[0].Text != "check the version" || r[0].Mode != "queue" || r[1].Text != "then run the tests" || r[1].Mode != "after_turn" || r[0].At.IsZero() || r[1].At.Before(r[0].At) {
			t.Fatalf("round %d: restored %+v", round, r)
		}
	}
}

// An agent that exited while eagent was down can confirm nothing: what the
// record still waited on is reported not confirmed, once, and no longer
// counts as on its way (which held a session with no terminal from its
// idle check for ever).
func TestSendsToAnAgentThatExitedWhileEagentWasDownAreSettled(t *testing.T) {
	path, project, model, open := stoppedWithSends(t)
	captureAttach(t, errNotRunning)
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	if n := len(rt.st.Relays["claude"].Undelivered); n != 0 {
		t.Fatalf("%d sends still wait on an agent that is gone", n)
	}
	var failed []string
	for _, ev := range rt.st.Events {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && !d.OK {
			failed = append(failed, d.ID)
			if !strings.Contains(d.Detail, "exited") || d.Outcome != event.DeliveryUnconfirmed {
				t.Errorf("detail %q", d.Detail)
			}
		}
	}
	if !slices.Equal(failed, open) {
		t.Fatalf("reported %v not confirmed, want %v", failed, open)
	}
}

// unconfirmedIDs lists, in order, the sends the record reports not
// confirmed, and fails on one reported twice.
func unconfirmedIDs(t *testing.T, evs []event.Event) []string {
	t.Helper()
	var ids []string
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && !d.OK {
			if slices.Contains(ids, d.ID) {
				t.Fatalf("%s reported not confirmed twice", d.ID)
			}
			ids = append(ids, d.ID)
		}
	}
	return ids
}

// The agent exited while eagent ran (the record says exited) with sends
// still unconfirmed, and eagent was then stopped. The resume settles them,
// once; a second resume says nothing more.
func TestSendsToAnAgentRecordedExitedAreSettledAtTheResume(t *testing.T) {
	path, project, model, open := stoppedWithSends(t)
	captureAttach(t, nil)
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	// Recorded as the harness records a seat's exit, but with nothing to
	// settle it (as a record written before this rule): the next resume
	// must.
	rt.append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "exited", Detail: "the pane is gone"}))
	rt.sess.Close()
	for round := 1; round <= 2; round++ {
		rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project}, &fakeUI{}, path)
		if err != nil {
			t.Fatal(err)
		}
		n := len(rt2.st.Relays["claude"].Undelivered)
		got := unconfirmedIDs(t, rt2.st.Events)
		rt2.sess.Close()
		if n != 0 || !slices.Equal(got, open) {
			t.Fatalf("round %d: %d still wait; reported %v, want %v", round, n, got, open)
		}
	}
}

// The agent exits while eagent runs: the sends it still waited on are
// reported not confirmed when the exit is recorded, once, so a session
// with no terminal can reach its idle check.
func TestSendsToAnAgentThatExitsLiveAreSettledWhenTheExitIsRecorded(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Working on it.")
	useFakeRelays(t, seat)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "keep it running", Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the agent's turn", func() bool {
		n := 0
		rt.sync(func() { n = rt.st.Relays["claude"].Turns })
		return n == 1
	})
	var id string
	rt.sync(func() {
		id = rt.st.NextRelaySendID()
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: id, From: "eagent", Mode: "queue", Text: "sort the mail"}))
	})
	seat.emit(relay.Event{Kind: relay.KindState, State: "exited", Detail: "the pane is gone"})
	waitFor(t, "the send settled", func() bool {
		n := 1
		rt.sync(func() { n = len(rt.st.Relays["claude"].Undelivered) })
		return n == 0
	})
	var got []string
	rt.sync(func() { got = unconfirmedIDs(t, rt.st.Events) })
	if !slices.Equal(got, []string{id}) {
		t.Fatalf("reported %v, want %s once", got, id)
	}
}

// A restart that cannot start records the relay exited; what the old agent
// was still waited on for is settled there, as at every other exit.
func TestAFailedRestartSettlesTheSendsTheOldAgentWasWaitedOnFor(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), nil}, []error{nil, errors.New("codex is not installed (not on PATH)")})
	rt := newLaunchRuntime(t)
	var id string
	rt.sync(func() {
		id = rt.st.NextRelaySendID()
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: id, From: "eagent", Mode: "queue", Text: "tidy the shelves"}))
	})
	rt.sync(func() {
		// The restart a model change makes (a message on its way now holds
		// a change asked back, item 3; this test is about the restart).
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if rel.State != "exited" {
			t.Fatalf("relay after a failed restart: %s", rel.State)
		}
		if _, waiting := rel.Undelivered[id]; waiting || rt.st.SendOutcome(id) != state.SendUnconfirmed {
			t.Errorf("still waited on %v, outcome %q, want settled unconfirmed", waiting, rt.st.SendOutcome(id))
		}
	})
}

var errNotRunning = errString("not running")

type errString string

func (e errString) Error() string { return string(e) }
