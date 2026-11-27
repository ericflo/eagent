package harness

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// SR1: a session with no terminal stops its agent; the call after that
// fails, and so does the retry's first call: two failures in a row, the
// second with no call of its turn coming back. The user is told "I keep
// trying and will pick up where I left off", and in the same moment the
// session ends as one whose model is down (maybeEnd's down case): nothing
// keeps trying. What the user is told must not promise a retry the
// session is about to drop.
func TestSRLoopDownNoticePromisesRetriesTheEndingSessionNeverMakes(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var calls atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if calls.Add(1) == 1 {
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
		}
		return k1BadRequest
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	var promise, closing string
	for _, m := range ui.said() {
		if strings.Contains(m, "I keep trying") {
			promise = m
		}
		if strings.HasPrefix(m, "This session is ending") {
			closing = m
		}
	}
	if promise != "" && closing != "" {
		t.Fatalf("exit %d: told %q, then the session ended at once: %q", code, promise, closing)
	}
}

// SR2: the conversation passes its restart size while the session's
// directory takes no new file (a directory it may no longer write, inodes
// used up), though its open record still takes appends. The dossier pass
// was paid for in full (up to 40 calls over the whole conversation) and
// only then did finishDossier find the next file cannot be made; the
// briefing was thrown away, and dossierRetry later the same paid pass ran
// again, without end. main made the file first (startRollover) and paid
// no model call for a restart that could not be made. Here dossierRetry is
// shortened so the tries show in a fraction of a second: each gives the
// restart up without a pass.
func TestSRLoopARestartWhoseFileCannotBeMadePaysForAPassAgainAndAgain(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	setFor(t, &dossierRetry, 50*time.Millisecond)
	var ready atomic.Bool
	var passes atomic.Int32
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			if last, _ := m[len(m)-1]["content"].(string); strings.Contains(last, dossierAsk) {
				passes.Add(1) // a pass's first call
			}
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		r := hold()
		if ready.Load() {
			r.prompt = 30000 // past the 20,000 restart size of testConfig
		}
		return r
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	if err := os.Chmod(s.rt.sess.Path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.rt.sess.Path, 0o755) })
	ready.Store(true)
	r14AgentTurn(s, "The build is green.")
	r6Until(t, "three restarts given up, or four passes", func() bool {
		return r4Logged(s, "the conversation continues as it is for now") >= 3 || passes.Load() >= 4
	})
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d paid dossier passes for a restart whose file cannot be made (%d briefings thrown away so far)", n, r4Logged(s, "the conversation continues as it is for now"))
	}
}

// The reader's suspicion on cd6c922: a session with no terminal is
// ending, its closing look puts the conversation past the restart size,
// and a notice of eagent's own (as a timer of the harness's leaves one)
// asks for a look meanwhile. The look a wake asks for still restarted
// the conversation first: a paid pass nothing would read.
func TestK3AnEndingBatchSessionPaysForNoDossierForAWake(t *testing.T) {
	var passes atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	r0, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if r := rt.Load(); r != nil && !injected.Load() {
				injected.Store(true)
				r.sync(func() {
					r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] A note of your own came due."}))
				})
			}
			r := hold()
			r.prompt = 30000 // past the 20,000 restart size of testConfig
			return r
		case stopped.CompareAndSwap(false, true):
			return stopsAndReports("The build is green; stopping Codex.")
		}
		return hold()
	})
	rt.Store(r0)
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	if !injected.Load() {
		t.Fatalf("setup: no notice came during the closing look (exit %d)", code)
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier passes paid for by a session that was ending (exit %d)", n, code)
	}
}
