package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// A9. What the seat read is recorded with how far it read the transcript,
// on the event a line gave and as relay.read for a read the record has
// nothing else of; a reattach to the same transcript is told the newest
// (spec.Read), and reads on from there in place of the last event's time.
func TestK3AReattachIsToldHowFarTheTranscriptWasRead(t *testing.T) {
	seat := newFakeSeat("one")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	const path = "/w/session.jsonl"
	seat.emit(relay.Event{Kind: relay.KindInner, InnerID: "abc", Transcript: path},
		relay.Event{Kind: relay.KindTurn, Text: "Built it.", Read: 1234})
	k1Relay(t, rt, "the turn recorded", func(rel *state.Relay) bool { return rel.Turns == 1 && rel.Read == 1234 && rel.ReadIn == path })
	seat.emit(relay.Event{Kind: relay.KindRead, Read: 2000})
	k1Relay(t, rt, "the read recorded", func(rel *state.Relay) bool { return rel.Read == 2000 })
	var got int64 = -1
	old := attachRelay
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		got = spec.Read
		if transcript != path {
			t.Errorf("reattached to %q, want %q", transcript, path)
		}
		return newFakeSeat("again"), nil
	}
	t.Cleanup(func() { attachRelay = old })
	rt.sync(func() { rt.reattachRelays() })
	if got != 2000 {
		t.Fatalf("the reattach was told %d, want 2000 (the newest read)", got)
	}
	var reads int
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			if ev.Type == event.RelayRead {
				reads++
			}
		}
	})
	if reads != 1 {
		t.Errorf("%d relay.read on the record, want the one", reads)
	}
}
