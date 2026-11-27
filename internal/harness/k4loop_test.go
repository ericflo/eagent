package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

var sr2TooLong = reply{status: 400, body: `{"error":{"message":"prompt is too long: 250000 tokens > 200000 maximum","type":"invalid_request_error"}}`}

// The sibling of 17912f6: a session with no terminal is ending, a notice
// of eagent's own asks for a look during the closing look, and the
// provider refuses that look as too long. maybeTurn no longer restarts a
// closing session, but turnFailed still takes the first refusal for a
// restart (restartRuns has no stillClosing gate): no failure is counted,
// no pass is made, and the second failure tells the user (important,
// buzzing the phone) that the model refuses the conversation "even after
// I restarted it from my notes". No restart was made.
func TestSR2LoopAnEndingSessionSaysItRestartedWhenItDidNot(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var passes, refused atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	r0, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case injected.Load():
			refused.Add(1)
			return sr2TooLong
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if r := rt.Load(); r != nil && !injected.Load() {
				r.sync(func() {
					r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] A note of your own came due."}))
				})
				injected.Store(true)
			}
			return hold()
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
	if !injected.Load() || refused.Load() == 0 {
		t.Fatalf("setup: notice %v, refused calls %d (exit %d)", injected.Load(), refused.Load(), code)
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("setup: %d dossier passes", n)
	}
	for _, m := range ui.said() {
		if strings.Contains(m, "even after I restarted it from my notes") {
			t.Fatalf("told %q after %d refused calls and no restart at all (exit %d)", m, refused.Load(), code)
		}
	}
}

// The review's suspicion: a run stopped between running a call (here a
// send_message, whose message is on the record) and writing its result.
// The resumed run marks the call interrupted and tells eagent so; what it
// says must hold for a call that may have run: not that it did not run.
func TestK4AnInterruptedCallIsNotSaidToHaveNotRun(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	sess, _, err := store.Open(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := tc("send_message", `{"text":"The build is green."}`)
	for _, ev := range []event.Event{
		event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: "the user wrote"}),
		event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call}}),
		event.New(event.Message, event.ActorEagent, event.MessageData{Text: "The build is green."}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	sess.Close()
	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
	s2.settled()
	for _, ev := range s2.events(event.HarnessMessage, event.ToolResult) {
		var d struct {
			Text   string `json:"text"`
			Output string `json:"output"`
		}
		_ = ev.Decode(&d)
		if text := d.Text + d.Output; strings.Contains(text, "did not run") {
			t.Fatalf("a call whose message is on the record is said not to have run: %q", text)
		}
	}
}
