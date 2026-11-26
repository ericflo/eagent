package relay

import (
	"testing"
	"time"
)

// pi took the hand-over but its run starts more than 5 s later (a login
// check that goes to the network, another extension's slow hook). pi
// reports idle with nothing pending all that time. The message is not
// taken back and handed over again: pi would run it twice.
func TestPiExtensionWaitsForARunPiIsSlowToStart(t *testing.T) {
	t.Parallel()
	// Handed over at the first look (1 s), run at 8 s: 7 s later, past the
	// short give-back (5 s) by a second, and settled 2 s before the end.
	list, wait, _ := runPiExtension(t, piFast, true, 10*time.Second, "PREFLIGHT_MS=7000")
	sent := wait()
	if len(sent) != 1 || sent[0].Text != "the held message" {
		t.Fatalf("pi was handed the message %d times, want once: %+v", len(sent), sent)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("the spool still holds %v after pi ran the message", got)
	}
}
