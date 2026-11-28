package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// downSockets holds, by address, the sockets downAddr bound.
var downSockets sync.Map

// downAddr is a loopback address nothing listens on: a call to it is
// refused, as when a local model server is stopped. The port is held by a
// socket bound and not listening (which refuses connections), so no other
// process can take it while the test runs; serveAt turns that same socket
// into the listener.
func downAddr(t *testing.T) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		syscall.Close(fd)
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", sa.(*syscall.SockaddrInet4).Port)
	downSockets.Store(addr, fd)
	t.Cleanup(func() {
		if _, ok := downSockets.LoadAndDelete(addr); ok {
			syscall.Close(fd)
		}
	})
	return addr
}

// serveAt starts h on addr (one downAddr gave) until the test ends.
func serveAt(t *testing.T, addr string, h http.Handler) {
	t.Helper()
	v, ok := downSockets.LoadAndDelete(addr)
	if !ok {
		t.Fatalf("%s is not an address downAddr gave", addr)
	}
	fd := v.(int)
	if err := syscall.Listen(fd, 128); err != nil {
		syscall.Close(fd)
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), addr)
	l, err := net.FileListener(f) // a copy of the socket; the file is closed
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
}

// netModel answers every call with its model's name, whichever address
// it is served on: the primary asks for testModel, the fallback for
// eagent-fallback.
func netModel(t *testing.T) *scripted {
	s := newScripted(func(model string, _ []map[string]any) reply { return reply{text: "from " + model} })
	t.Cleanup(s.srv.Close)
	return s
}

// netRuntime is a runtime whose primary is at primary and, when fallback
// is not "", has one fallback there, with its loop served and every wait
// of an outage shortened: rounds 5 to 20 ms apart, the user told after
// 40 ms.
func netRuntime(t *testing.T, primary, fallback string) (*Runtime, *fakeUI) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	setFor(t, &offlineWait, 5*time.Millisecond)
	setFor(t, &offlineSwitch, 40*time.Millisecond)
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig(primary)
	if fallback != "" {
		cfg.Model.Fallback = &config.ModelRoute{Protocol: "openai-chat", BaseURL: fallback, Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY"}
	}
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: t.TempDir()}, ui)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	rt.models[event.ActorEagent].client.MaxAttempts = 1
	relayServeLoop(t, rt)
	return rt, ui
}

func netCall(rt *Runtime, ctx context.Context) (*llm.Response, error) {
	return rt.complete(ctx, event.ActorEagent, llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}}, nil)
}

// netCallAsync makes a call on its own goroutine; the channel gets its
// outcome.
func netCallAsync(rt *Runtime, ctx context.Context) <-chan netOutcome {
	ch := make(chan netOutcome, 1)
	go func() {
		resp, err := netCall(rt, ctx)
		ch <- netOutcome{resp, err}
	}()
	return ch
}

type netOutcome struct {
	resp *llm.Response
	err  error
}

func netWait(t *testing.T, ch <-chan netOutcome) netOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not come back")
	}
	return netOutcome{}
}

// outageNotices are the automatic messages that say the model cannot be
// reached; netRoutes the Route events.
func outageNotices(rt *Runtime) []string {
	var out []string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.MessageData
			if ev.Type == event.Message && ev.Decode(&d) == nil && d.Auto && strings.Contains(d.Text, "can't reach my own model") {
				out = append(out, d.Text)
			}
		}
	})
	return out
}

func netRoutes(rt *Runtime) []event.RouteData {
	var out []event.RouteData
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RouteData
			if ev.Type == event.Route && ev.Decode(&d) == nil {
				out = append(out, d)
			}
		}
	})
	return out
}

// rounds waits until n more rounds of an outage could have gone by.
func rounds(n int) { time.Sleep(time.Duration(n) * 4 * offlineWait) }

// Both down: the user is told once, however many rounds go by. The
// primary comes back first: the session stays on it, with no route event.
func TestBothRoutesDownThenThePrimaryBackStaysOnThePrimary(t *testing.T) {
	model := netModel(t)
	pAddr, fAddr := downAddr(t), downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, "http://"+fAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	rounds(5)
	serveAt(t, pAddr, model.srv.Config.Handler)
	o := netWait(t, ch)
	if o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	if n := outageNotices(rt); len(n) != 1 {
		t.Fatalf("told %d times: %q", len(n), n)
	}
	if r := netRoutes(rt); len(r) != 0 {
		t.Fatalf("route events %+v", r)
	}
}

// One route, down past the bound: told once, waited for, and the call
// goes through when it is back. A later outage is told again.
func TestASingleRouteDownForLongIsToldOnceAndWaitedFor(t *testing.T) {
	model := netModel(t)
	addr := downAddr(t)
	rt, _ := netRuntime(t, "http://"+addr, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	rounds(5)
	serveAt(t, addr, model.srv.Config.Handler)
	o := netWait(t, ch)
	if o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	n := outageNotices(rt)
	if len(n) != 1 {
		t.Fatalf("told %d times: %q", len(n), n)
	}
	var m *modelRoute
	rt.sync(func() { m = rt.models[event.ActorEagent] })
	var told bool
	rt.sync(func() { told = m.told })
	if told {
		t.Fatal("the outage is over, but its state stands")
	}
}

// A primary out of reach for less than the bound (a local server
// reloading) is waited for: the fallback is not asked.
func TestABriefOutageDoesNotLeaveThePrimary(t *testing.T) {
	model := netModel(t)
	addr := downAddr(t)
	rt, ui := netRuntime(t, "http://"+addr, model.srv.URL)
	setFor(t, &offlineSwitch, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the first refusal", func() bool { return strings.Contains(strings.Join(ui.logged(), "\n"), "waiting for the network") })
	serveAt(t, addr, model.srv.Config.Handler)
	o := netWait(t, ch)
	if o.err != nil || o.resp.Text != "from "+testModel || model.count("eagent-fallback") != 0 {
		t.Fatalf("call: %v %+v; fallback calls %d", o.err, o.resp, model.count("eagent-fallback"))
	}
	if r, n := netRoutes(rt), outageNotices(rt); len(r) != 0 || len(n) != 0 {
		t.Fatalf("route events %+v, notices %q", r, n)
	}
}

// A call waiting out an outage returns at once when it is cancelled (the
// session stopping) or runs out of its time (the closing word). The
// fallback is not asked for an outage.
func TestACallWaitingOnTheNetworkReturnsWhenItsContextEnds(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			waitingCallReturns(t, deadline)
		})
	}
}

func waitingCallReturns(t *testing.T, deadline bool) {
	var asked atomic.Int32
	stop := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(func() { close(stop); hung.Close() })
	rt, _ := netRuntime(t, "http://"+downAddr(t), hung.URL)
	setFor(t, &offlineWait, time.Hour) // held in the wait between rounds
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline {
		ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()
	ch := netCallAsync(rt, ctx)
	time.Sleep(20 * time.Millisecond)
	if !deadline {
		cancel()
	}
	start := time.Now()
	o := netWait(t, ch)
	if o.err == nil || time.Since(start) > time.Second {
		t.Fatalf("%v after %s", o.err, time.Since(start))
	}
	if asked.Load() != 0 {
		t.Fatalf("the fallback was asked for an outage")
	}
}

// A change of effort alone keeps an outage going: told once, not again.
func TestAnEffortChangeDuringAnOutageDoesNotTellTheUserAgain(t *testing.T) {
	model := netModel(t)
	addr := downAddr(t)
	rt, _ := netRuntime(t, "http://"+addr, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	rt.sync(func() {
		ep := rt.models[event.ActorEagent].client.Endpoint
		ep.ReasoningEffort = "low"
		rt.swapRoute(routeSwap{who: event.ActorEagent, eps: []llm.Endpoint{ep}, conf: config.ModelRoute{BaseURL: ep.BaseURL, Model: ep.Model}})
		rt.models[event.ActorEagent].client.MaxAttempts = 1
	})
	rounds(8)
	serveAt(t, addr, model.srv.Config.Handler)
	if o := netWait(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	if n := outageNotices(rt); len(n) != 1 {
		t.Fatalf("told %d times: %q", len(n), n)
	}
}

// From the second reader of the outage handling (S/r6w-net), renamed.

// A call on the route in use has a picture refused and goes through
// without it; while it is out the user changes the effort only (the same
// model at the same address). The refusal is the route's own and is
// written down, so the next call does not upload the picture again.
func TestARefusalOfTheRouteInUseSurvivesAnEffortChange(t *testing.T) {
	model := netModel(t)
	var imageCalls atomic.Int32
	held := make(chan struct{}, 1)
	release := make(chan struct{})
	var hold atomic.Bool
	hold.Store(true)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if strings.Contains(string(raw), "image_url") {
			imageCalls.Add(1)
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"image too large","code":"image_too_large"}}`))
			return
		}
		if hold.CompareAndSwap(true, false) {
			held <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		model.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(primary.Close)
	rt, _ := netRuntime(t, primary.URL, "")
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
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the call without the picture never came")
	}
	rt.sync(func() {
		ep := rt.models[event.ActorEagent].client.Endpoint
		ep.ReasoningEffort = "low"
		rt.swapRoute(routeSwap{who: event.ActorEagent, eps: []llm.Endpoint{ep}, conf: config.ModelRoute{BaseURL: ep.BaseURL, Model: ep.Model}})
	})
	close(release)
	if o := netWait(t, ch); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	var refused bool
	rt.sync(func() { refused = rt.st.RefusedImages[shot] })
	// The next call, as the record renders it.
	go func() {
		resp, err := rt.complete(ctx, event.ActorEagent, req.WithRefused(nil), nil)
		ch <- netOutcome{resp, err}
	}()
	if o := netWait(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	if !refused || imageCalls.Load() != 1 {
		t.Fatalf("the route in use refused the picture, but the record says refused=%v and it was uploaded %d times", refused, imageCalls.Load())
	}
}
