package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// C12 (not changed): a fallback asked in place of an unreachable primary
// that refuses the conversation as too long does not take over: it is
// asked again once a round, as a fallback that fails any other way is, and
// the session waits for the primary as it does with no fallback (as on
// main). Nothing is lost: the primary, once back, answers the call, and
// the session stays on it.
func TestK1AProbeRefusedAsTooLongWaitsForThePrimary(t *testing.T) {
	primary := netModel(t)
	var fbCalls atomic.Int32
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fbCalls.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 200000 tokens","code":"context_length_exceeded"}}`))
	}))
	t.Cleanup(fb.Close)
	pAddr := downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, fb.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := make(chan netOutcome, 1)
	go func() {
		resp, err := netCall(rt, ctx)
		ch <- netOutcome{resp, err}
	}()
	waitFor(t, "the fallback to be asked in two rounds", func() bool { return fbCalls.Load() >= 2 })
	serveAt(t, pAddr, primary.srv.Config.Handler)
	if o := netWait(t, ch); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	if routes := netRoutes(rt); len(routes) != 0 {
		t.Fatalf("the session moved: %+v", routes)
	}
}
