package relay

import (
	"strings"
	"testing"
	"time"
)

// An Antigravity relay line whose state name is not "idle" (here empty, as
// before agy reports an agent_state; antigravity.go says the names are not
// documented) reads as busy; the stuck send's Enter is pressed again all
// the same, as at 80f4746: busy-ness does not gate it.
func TestAgyStuckSendIsPressedAgainWithAnUnknownStateName(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> fix the tests\n\nAntigravity relay ·  · Gemini 3 Pro"}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state, s.stateAt = "idle", time.Now().Add(-time.Minute)
	s.track("r1", "fix the tests", "queue")
	s.checkScreen(time.Now().Add(7 * time.Second))
	if len(h.keys) == 0 {
		t.Fatalf("the stuck send sat in agy's input box and Enter was not pressed again (pending %v)", s.Snapshot().Pending)
	}
}

// A held message whose first word is a directory ("/tmp is full ...") is
// handed over by the Stop hook, as every held message is.
func TestAHeldMessageStartingWithADirectoryIsHandedOver(t *testing.T) {
	t.Parallel()
	spool := t.TempDir()
	msg := "/tmp is full again; clear the old build outputs before the next run"
	if err := spoolMessage(spool, "m1", msg); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if !strings.Contains(out.String(), "clear the old build outputs") {
		t.Errorf("the Stop hook did not hand the held message over (said %q); isAgentCommand=%v", out.String(), isAgentCommand(msg))
	}
}
