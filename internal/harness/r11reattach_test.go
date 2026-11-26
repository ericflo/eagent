package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// r11At is ev recorded at t.
func r11At(ev event.Event, t time.Time) event.Event {
	ev.Time = t
	return ev
}

// r11Since writes a record holding evs (with their own times), resumes it,
// and returns the time the reattach was told the transcript was read up to.
func r11Since(t *testing.T, evs ...event.Event) time.Time {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range append([]event.Event{event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: project})}, evs...) {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	path := sess.Path
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	var got []time.Time
	old := attachRelay
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		got = append(got, since)
		return newFakeSeat("again"), nil
	}
	t.Cleanup(func() { attachRelay = old })
	rt, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	rt.sess.Close()
	if len(got) != 1 {
		t.Fatalf("%d reattaches, want 1", len(got))
	}
	return got[0]
}

func r11Start(agent string, at time.Time) event.Event {
	return r11At(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: agent, Profile: agent, Agent: agent, Host: "tmux", Target: "eagent-relay-r11-" + agent, InnerID: "i1"}), at)
}

// The finding's precision cases. A send is recorded, the agent's reply to
// it is written in the send's own millisecond (pi) or second (Antigravity,
// which stamps whole seconds), and eagent stops before the seat reads it.
// The send was recorded without reading the transcript, so the reattach
// reads on from the last event that came from it (the turn before): the
// reply's stamp is after that, and is news.
func TestAReattachReadsOnFromTheLastEventTheTranscriptGave(t *testing.T) {
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, c := range []struct {
		agent string
		unit  time.Duration // what the agent's transcript stamps to
	}{{"pi", time.Millisecond}, {"antigravity", time.Second}} {
		t.Run(c.agent, func(t *testing.T) {
			turnAt := base.Add(time.Second + 200*time.Millisecond)
			sent := base.Add(5*time.Second + 123456*time.Microsecond)
			since := r11Since(t,
				r11Start(c.agent, base),
				r11At(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: c.agent, Text: "Ready."}), turnAt),
				r11At(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: c.agent, State: "idle"}), turnAt.Add(time.Millisecond)),
				r11At(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: c.agent, ID: "r1", From: "user", Mode: "queue", Text: "what is 2+2"}), sent),
			)
			reply := sent.Truncate(c.unit)
			if !reply.After(since) {
				t.Fatalf("since %s: the reply stamped %s is read as history and its turn is dropped", since.Format(time.RFC3339Nano), reply.Format(time.RFC3339Nano))
			}
			if !since.Equal(turnAt) {
				t.Fatalf("since %s, want the turn's %s: what the seat read before it would be read again", since.Format(time.RFC3339Nano), turnAt.Format(time.RFC3339Nano))
			}
		})
	}
}

// The finding's Claude case: the agent's turn is written after the arrival
// the seat recorded, then a send, its failure and an autopilot order are
// recorded before the seat reads the turn, and eagent stops. None of them
// read the transcript; the turn is after the arrival, and is news.
func TestAReattachReadsATurnWrittenBeforeLaterHarnessEvents(t *testing.T) {
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	arrived := base.Add(3 * time.Second)
	turn := base.Add(30 * time.Second)
	since := r11Since(t,
		r11Start("claude", base),
		r11At(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Mode: "queue", Text: "run the tests"}), base.Add(2*time.Second)),
		r11At(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: true}), arrived),
		r11At(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r2", From: "eagent", Mode: "queue", Text: "and push"}), turn.Add(100*time.Millisecond)),
		r11At(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r2", OK: false, Outcome: event.DeliveryNotSent, Detail: "x"}), turn.Add(150*time.Millisecond)),
		r11At(event.New(event.RelayAutopilot, event.ActorHarness, event.RelayAutopilotData{Name: "claude", Instruction: "keep going"}), turn.Add(200*time.Millisecond)),
	)
	if !turn.After(since) {
		t.Fatalf("since %s: the turn stamped %s is read as history and dropped", since.Format(time.RFC3339Nano), turn.Format(time.RFC3339Nano))
	}
	if !since.Equal(arrived) {
		t.Fatalf("since %s, want the arrival's %s", since.Format(time.RFC3339Nano), arrived.Format(time.RFC3339Nano))
	}
}
