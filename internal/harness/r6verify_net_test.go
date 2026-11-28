package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Written by the independent reader of the outage handling (S/r6v-net),
// taken over unchanged.

// dropFirst serves h, except that its first request is held for hold and
// then has its connection closed with no answer: a long reply cut off by
// the route going away under it. Every later request is answered.
func dropFirst(t *testing.T, hold time.Duration, h http.Handler) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			select {
			case <-time.After(hold):
			case <-r.Context().Done():
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// One long call that drops is not a minute of outage: the primary is
// tried again, answers, and the session stays on it.
func TestR6VerifyALongCallThatDropsOnceIsNotAMinuteOutage(t *testing.T) {
	model := netModel(t)
	primary, _ := dropFirst(t, 80*time.Millisecond, model.srv.Config.Handler)
	rt, _ := netRuntime(t, primary.URL, model.srv.URL) // offlineSwitch is 40 ms here
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := netCall(rt, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "from "+testModel || model.count("eagent-fallback") != 0 || len(netRoutes(rt)) != 0 {
		t.Fatalf("one dropped call (the primary answers its next one) moved the session: reply %q, fallback asked %d times, routes %+v",
			resp.Text, model.count("eagent-fallback"), netRoutes(rt))
	}
}
