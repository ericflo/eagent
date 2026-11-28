package harness

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The closing turn sends the closing report and, in the same reply, reads
// something; the call after the read is refused (a 400 that the same
// record gets again). At 59a2069 the session ended "done" with the report
// as the last word. Since bbfcd11 the "error" turn end owes a call, that
// call is refused too, and the user is told after the closing report that
// eagent cannot reach its model and "keeps trying", and the session ends
// awaiting input.
func TestRV10ClosingReportSentThenFailedCallEndsClean(t *testing.T) {
	var phase atomic.Int32
	var after atomic.Int32
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Done: #42 and #44 are merged.")
	useFakeRelays(t, seat)
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case phase.Load() >= 3:
			after.Add(1)
			return reply{status: 400, body: `{"error":{"message":"bad request","type":"invalid_request_error"}}`}
		case strings.Contains(tail, "finished a turn") && last != "tool" && phase.Load() == 0:
			phase.Store(1)
			return stopsWhenDone()
		case strings.Contains(allText(msgs), "The session is ending now") && last != "tool" && phase.Load() == 1:
			phase.Store(2)
			args := `{"text":"Closing report: #42 and #44 are merged."}`
			return reply{calls: []event.ToolCall{tc("send_message", args), tc("session_search", `{"query":"merged"}`)}}
		case last == "tool" && phase.Load() == 2:
			phase.Store(3)
			return reply{status: 400, body: `{"error":{"message":"bad request","type":"invalid_request_error"}}`}
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: brief + " Stop when done."}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}()
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(20 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	said := ui.said()
	t.Logf("exit %d; calls after the refusal %d; said %q", code, after.Load(), said)
	if code != 0 || len(said) != 1 {
		t.Fatalf("exit %d, said %q; want exit 0 and the closing report alone", code, said)
	}
}
