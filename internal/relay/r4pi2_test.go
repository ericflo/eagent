package relay

import (
	"testing"
	"time"
)

// pi refuses the hand-over after its input hooks (no model, no key, a
// login check that failed): seen is set and the message waits for the
// long retry. Then an unrelated typed run starts (others is counted) and
// is interrupted. pi 1.6.1 settles any interrupted run with aborted true
// (agent-session.js abort sets _agentRunAbortRequested; Escape restores the
// queue and aborts even with nothing queued). agent_settled then takes
// "seen && !pending && aborted" for the hand-over having been put back in
// the editor and deletes its files, though pi never had it: the held
// message is lost. Here a second typed message is queued so the stand-in
// interrupts the run; the editor gets that message, not the held one.
func TestPiHandOverRefusedAfterTheHookSurvivesAnUnrelatedInterruptedRun(t *testing.T) {
	t.Parallel()
	// The hooks see it at the extension's first look (1 s); the typed run
	// goes from 2 s to 4.5 s, the second typed message at 3 s: a second or
	// more between each.
	list, wait, _ := runPiPromptPath(t, piFast, 8*time.Second,
		`[[2000, "type", "a typed message"], [3000, "type", "a second typed message"]]`,
		"REFUSE_AFTER=1", "RUN_EACH_MS=2500", "ABORT_QUEUED=1")
	r := wait()
	if r.count("sent", held) != 1 || r.count("ran", "a typed message") != 1 {
		t.Fatalf("the scenario did not run as meant:\n%s", r.out)
	}
	if r.count("ran", held) == 0 && len(list()) == 0 {
		t.Fatalf("the held message never reached pi and its spool file is gone:\n%s", r.out)
	}
}
