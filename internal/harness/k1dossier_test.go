package harness

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// k1BadRequest is a failure that is not a refusal as too long, and that
// the client does not retry on its own.
var k1BadRequest = reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}

// Every turn's first call goes through (it makes a lookup) and the call
// after it fails, not as too long. The turn that failed is owed again, so
// the next one starts; its first call coming back is no sign that the
// turn's calls go through, so the waits between those turns grow as for
// any failing call, where each turn once started the next at once, paid
// for its first call, and said it was trying again now, without end.
func TestK1ASecondCallThatFailsAfterTheFirstWentThroughWaitsLongerEachTime(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var ready atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return k1BadRequest
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "a wait of an hour, or calls going on without one", func() bool {
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "model call failed") > 30
	})
	if n := r4Logged(s, "trying again in"); n != 1 {
		t.Fatalf("a second call that failed after the first went through was tried again at once each time, the waits never growing (%d failed calls logged)", r4Logged(s, "model call failed"))
	}
	if n := r4Logged(s, "model call failed"); n != 2 {
		t.Fatalf("%d failed calls logged before the wait of an hour, want 2", n)
	}
}
