package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
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

// A call sent before the route answered another one, failing after it
// did, says nothing about the route now: the user is not told it cannot
// be reached.
func TestR6VerifyAStaleFailureAfterTheRouteAnsweredDoesNotTellTheUser(t *testing.T) {
	model := netModel(t)
	primary, n := dropFirst(t, 80*time.Millisecond, model.srv.Config.Handler)
	rt, _ := netRuntime(t, primary.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := netCallAsync(rt, ctx)
	waitFor(t, "the first call to be out", func() bool { return n.Load() == 1 })
	if o := netWait(t, netCallAsync(rt, ctx)); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("second call: %v %+v", o.err, o.resp)
	}
	if o := netWait(t, a); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("first call: %v %+v", o.err, o.resp)
	}
	if got := outageNotices(rt); len(got) != 0 {
		t.Fatalf("the model answered before and after, but the user was told %q", got)
	}
}

// A fallback asked in place of an unreachable primary refuses a picture
// and then fails: it does not take over, so its refusal is not written
// down, and the primary, once back, is sent the picture.
func TestR6VerifyAPictureRefusedByAProbeDoesNotLeaveThePrimaryWithoutIt(t *testing.T) {
	primary := netModel(t)
	var fbCalls atomic.Int32
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fbCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "image_url") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"image too large","code":"image_too_large"}}`))
			return
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
	}))
	t.Cleanup(fb.Close)
	pAddr := downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, fb.URL)
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, audit2PNG(t, 8, 6), 0o644); err != nil {
		t.Fatal(err)
	}
	req := llm.Request{Messages: []llm.Message{{Role: "user", Text: "look at this", Images: []llm.Image{{Path: shot, MediaType: "image/png"}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := make(chan netOutcome, 1)
	go func() {
		resp, err := rt.complete(ctx, event.ActorEagent, req, nil)
		ch <- netOutcome{resp, err}
	}()
	waitFor(t, "the fallback to be asked", func() bool { return fbCalls.Load() >= 2 })
	serveAt(t, pAddr, primary.srv.Config.Handler)
	if o := netWait(t, ch); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	var refused map[string]bool
	rt.sync(func() { refused = rt.st.RefusedImages })
	reqs := primary.requests(testModel)
	sent := len(reqs) > 0 && strings.Contains(string(reqs[len(reqs)-1].Messages[0]), "image_url")
	if len(refused) != 0 || !sent {
		t.Fatalf("the session stayed on the primary, yet the record refuses %v and the primary got the picture: %v", refused, sent)
	}
}
