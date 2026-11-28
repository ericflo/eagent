package harness

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// C13 (not changed): a route that answers 500, 502, 503, 504 or 529 has
// answered; it is not out of reach. The call comes back to the turn with
// that failure after the client's own attempts, as main's completeActor
// returns it, the fallback is not asked, and the session stays on the
// route (the turn retries it and tells the user after the second failure).
func TestK1AServerErrorIsTheTurnsNotAnOutage(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 529} {
		var fbCalls atomic.Int32
		fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fbCalls.Add(1) }))
		primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		}))
		rt, _ := netRuntime(t, primary.URL, fb.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		o := netWait(t, netCallAsync(rt, ctx))
		cancel()
		var ae *llm.APIError
		if !errors.As(o.err, &ae) || ae.Status != status || fbCalls.Load() != 0 || len(netRoutes(rt)) != 0 {
			t.Fatalf("%d: err %v, fallback asked %d times, routes %+v", status, o.err, fbCalls.Load(), netRoutes(rt))
		}
		primary.Close()
		fb.Close()
	}
}

// Nor is a stream that stalls (no bytes for the idle limit): the call
// comes back to the turn with the stall, as on main, and the fallback is
// not asked.
func TestK1AStalledStreamIsTheTurnsNotAnOutage(t *testing.T) {
	var fbCalls atomic.Int32
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fbCalls.Add(1) }))
	t.Cleanup(fb.Close)
	release := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(primary.Close)
	t.Cleanup(func() { close(release) })
	rt, _ := netRuntime(t, primary.URL, fb.URL)
	rt.sync(func() { rt.models[event.ActorEagent].client.IdleTimeout = 50 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := netWait(t, netCallAsync(rt, ctx))
	if !errors.Is(o.err, llm.ErrIdle) || fbCalls.Load() != 0 || len(netRoutes(rt)) != 0 {
		t.Fatalf("err %v, fallback asked %d times, routes %+v", o.err, fbCalls.Load(), netRoutes(rt))
	}
}
