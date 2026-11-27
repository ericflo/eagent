package state

import (
	"maps"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Each agent that held a name waits for what it was sent itself. A fresh
// start under a stopped agent's name takes what was held for that start,
// and no more: what the stopped one was typed stays on it (Holders), where
// a settle of the record can still find it, and an outcome ends the wait
// whichever of them the send was to. (Only the newest agent under a name
// was looked at, so a send to an older one was never settled.)
func TestK5EachHolderOfANameWaitsForItsOwnSends(t *testing.T) {
	b := newBuilder()
	start := func() {
		b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "t"}))
	}
	send := func(id string, held bool) {
		b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: id, From: "user", Mode: "queue", Text: "text of " + id, Held: held}))
	}
	waiting := func(st *State) [][]string {
		var out [][]string
		for _, h := range st.Holders("codex") {
			out = append(out, slices.Sorted(maps.Keys(h.Undelivered)))
		}
		return out
	}
	start()
	send("r1", false)
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "codex", Reason: "stopped"}))
	send("r2", true) // held for the next start under the name
	start()
	send("r3", false)
	st := Replay(b.events)
	if got := waiting(st); len(got) != 2 || !slices.Equal(got[0], []string{"r1"}) || !slices.Equal(got[1], []string{"r2", "r3"}) {
		t.Fatalf("the holders wait for %v; want [[r1] [r2 r3]]", got)
	}
	if cur := st.Relays["codex"]; cur != st.Holders("codex")[1] {
		t.Fatal("the newest holder is not the relay under the name")
	}
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: false, Outcome: event.DeliveryUnconfirmed}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r2", OK: true}))
	if got := waiting(Replay(b.events)); len(got) != 2 || len(got[0]) != 0 || !slices.Equal(got[1], []string{"r3"}) {
		t.Fatalf("after the outcomes the holders wait for %v; want [[] [r3]]", got)
	}
}
