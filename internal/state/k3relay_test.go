package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A9. How far the relay's transcript was read comes from its newest event
// that says (a line's event, relay.read), with the transcript it was then;
// a record written before events said it has none (a reattach then cuts
// by time, as it always did); and a move to another transcript leaves the
// position with the transcript it was read in.
func TestK3AReadPositionIsTheNewestTheRecordSays(t *testing.T) {
	old := newBuilder()
	old.startClaude()
	old.add(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: "claude", InnerID: "a", Transcript: "/w/a.jsonl"}))
	old.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Done."}))
	if rel := Replay(old.events).Relays["claude"]; rel.Read != 0 || rel.ReadIn != "" {
		t.Fatalf("an older record: read %d in %q; want none", rel.Read, rel.ReadIn)
	}
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: "claude", InnerID: "a", Transcript: "/w/a.jsonl"}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Done.", Read: 900}))
	b.add(event.New(event.RelayRead, event.ActorHarness, event.RelayReadData{Name: "claude", Read: 1200}))
	if rel := Replay(b.events).Relays["claude"]; rel.Read != 1200 || rel.ReadIn != "/w/a.jsonl" {
		t.Fatalf("read %d in %q; want 1200 in /w/a.jsonl", rel.Read, rel.ReadIn)
	}
	b.add(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: "claude", InnerID: "b", Transcript: "/w/b.jsonl"}))
	if rel := Replay(b.events).Relays["claude"]; rel.ReadIn == rel.Transcript {
		t.Fatalf("after a move: read in %q, transcript %q; the position is the old transcript's", rel.ReadIn, rel.Transcript)
	}
}
