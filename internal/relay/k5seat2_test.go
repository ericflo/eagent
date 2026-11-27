package relay

// The sixteenth review's fourth pass over the seat (sr4seat).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SR4-2. A reattached seat types nothing until the screen shows the
// agent's own prompt or busy line (35c9fd8), and stays "attached" until
// the agent writes news. Codex's prompt is recognised by its composer's
// placeholder, which is gone while the composer holds text: words the user
// was typing at the terminal, or a send the eagent before typed whose
// Enter was lost (never pressed again once restored). Every send to that
// idle Codex then waits 40 s and fails as a dialog needing an answer; a
// live seat, and main, type it. (The screen is Codex 0.171.0's: its
// composer's footer, "? for shortcuts" and the context left.)
func TestSr4SeatReattachedCodexWithTextInItsComposerRefusesEverySend(t *testing.T) {
	savedWait, savedSettle := readyWait, typeSettle
	readyWait, typeSettle = 300*time.Millisecond, time.Millisecond // the 40 s wait, shortened
	t.Cleanup(func() { readyWait, typeSettle = savedWait, savedSettle })
	dir := t.TempDir()
	transcript := filepath.Join(dir, "rollout.jsonl")
	at := time.Now().Add(-2 * time.Minute)
	writeLines(t, transcript,
		k1CodexMeta(),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Fixed."}}`, stamp(at.Add(time.Second))),
	)
	st, _ := os.Stat(transcript)
	// Codex idle at its own prompt, its composer holding a draft.
	h := &fakeHost{alive: true, screen: "› and the README too\n\n  ? for shortcuts                                    100% context left\n"}
	s := attachSeat(Spec{Dir: dir, State: "idle", Read: st.Size()}, codexAgent{}, h, "sr4-codex-draft", "thread-1", transcript, at.Add(2*time.Second))
	s.poll = piLook
	done := make(chan struct{})
	go s.watch()
	go func() {
		defer close(done)
		for range s.Events() {
		}
	}()
	t.Cleanup(func() { s.Close(); <-done })
	if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.catchingUp }) {
		t.Fatal("never caught up")
	}
	res, err := s.Send("s1", "also run the tests", "queue")
	if err != nil || len(k1Typed(h)) != 1 {
		t.Fatalf("Send to an idle reattached Codex: %q, %v, typed %q; want it typed, as a live seat and main type it", res, err, k1Typed(h))
	}
}
