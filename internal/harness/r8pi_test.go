package harness

import (
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/relay"
)

// After pi's /new the seat reports the new session before any file for it
// exists (an inner event with no transcript). The record then names the
// new session and no transcript, and a restart opens the new session, not
// the conversation the user left.
func TestR8PiMoveWithoutFileRecordedAndRestartedThere(t *testing.T) {
	first, next := newFakeSeat("one"), newFakeSeat("two")
	var mu sync.Mutex
	var ids []string
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		mu.Lock()
		n := len(ids)
		ids = append(ids, innerID)
		mu.Unlock()
		seat := first
		if n > 0 {
			seat = next
		}
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay = old })
	rt := newLaunchRuntime(t)

	first.emit(relay.Event{Kind: relay.KindInner, InnerID: "old-session", Transcript: "/sessions/old.jsonl"})
	waitFor(t, "the first session recorded", func() bool {
		var ok bool
		rt.sync(func() { ok = rt.st.Relays["codex"].InnerID == "old-session" })
		return ok
	})
	first.emit(relay.Event{Kind: relay.KindInner, InnerID: "new-session"})
	waitFor(t, "the move recorded", func() bool {
		var ok bool
		rt.sync(func() { ok = rt.st.Relays["codex"].InnerID == "new-session" })
		return ok
	})
	rt.sync(func() {
		if tr := rt.st.Relays["codex"].Transcript; tr != "" {
			t.Errorf("the new session keeps the old transcript %q", tr)
		}
	})

	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	waitFor(t, "the restart", func() bool { mu.Lock(); defer mu.Unlock(); return len(ids) == 2 })
	waitRelayStarts(t, rt)
	mu.Lock()
	defer mu.Unlock()
	if ids[1] != "new-session" {
		t.Fatalf("restarted with session %q, want the new one", ids[1])
	}
}
