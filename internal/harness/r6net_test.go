package harness

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// The decision complete asks after every failed call, row by row.
func TestAFailedCallIsDecidedByOneTable(t *testing.T) {
	for _, c := range []struct {
		name   string
		past   bool
		others bool
		last   failKind
		told   bool
		want   callStep
	}{
		{"another failure is the caller's", false, true, failOther, false, stepGiveUp},
		{"another failure after an outage", true, true, failOther, true, stepGiveUp},
		{"unroutable with a route after it", false, true, failUnroutable, false, stepNext},
		{"unroutable at the end of the chain", false, false, failUnroutable, false, stepGiveUp},
		{"a failure older than the route's last answer", false, true, failStale, false, stepAgain},
		{"a failure older than the last answer, past", true, false, failStale, false, stepAgain},
		{"unreachable under the bound, a fallback set", false, true, failNetwork, false, stepRetry},
		{"unreachable under the bound, alone", false, false, failNetwork, false, stepRetry},
		{"past the bound, a route not asked", true, true, failNetwork, false, stepNext},
		{"past the bound, told, a route not asked", true, true, failNetwork, true, stepNext},
		{"past the bound, every route asked", true, false, failNetwork, false, stepTell},
		{"past the bound, every route asked, told", true, false, failNetwork, true, stepRetry},
	} {
		if got := netStep(c.past, c.others, c.last, c.told); got != c.want {
			t.Errorf("%s: step %d, want %d", c.name, got, c.want)
		}
	}
}

// What an outage is known to be, from the calls that came back, in order.
func TestAnOutageIsTimedFromWhatTheRouteDidLast(t *testing.T) {
	setFor(t, &offlineSwitch, time.Minute)
	t0 := time.Unix(1_000_000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	err := errors.New("refused")
	type call struct {
		sent, back int
		answered   bool // false: a network failure
		counts     bool // for a failure: whether it counts
	}
	for _, c := range []struct {
		name  string
		calls []call
		now   int
		down  bool
		past  bool
	}{
		{"nothing yet", nil, 0, false, false},
		{"one long call that drops", []call{{0, 200, false, true}}, 200, true, false},
		{"tried again soon after", []call{{0, 31, false, true}, {46, 77, false, true}}, 77, true, false},
		{"tried again, out past the bound", []call{{0, 31, false, true}, {46, 92, false, true}}, 92, true, true},
		{"a call sent before the first failure is no second try", []call{{0, 31, false, true}, {10, 200, false, true}}, 200, true, false},
		{"answered since: the outage is over", []call{{0, 31, false, true}, {46, 92, false, true}, {100, 101, true, false}}, 101, false, false},
		{"a failure sent before the last answer counts for nothing", []call{{0, 10, true, false}, {5, 200, false, false}}, 200, false, false},
		{"a failure sent after the last answer counts", []call{{0, 10, true, false}, {11, 40, false, true}}, 40, true, false},
	} {
		var o outage
		for _, k := range c.calls {
			if k.answered {
				o.answer(at(k.back))
			} else if got := o.failed(at(k.sent), at(k.back), err); got != k.counts {
				t.Errorf("%s: the failure sent at %d counts %v", c.name, k.sent, got)
			}
		}
		if o.down() != c.down || o.past(at(c.now)) != c.past {
			t.Errorf("%s: down %v past %v, want %v %v", c.name, o.down(), o.past(at(c.now)), c.down, c.past)
		}
	}
}

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
// of an outage shortened: rounds 5 to 20 ms apart, the others asked after
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

// A stopped primary with a fallback that answers: past the bound the
// fallback is asked and takes over as one that answers for an unroutable
// primary does (the log, the route event, the settings), with no alarm,
// and the next call goes to it.
func TestAStoppedPrimaryHandsTheCallToAFallbackThatAnswers(t *testing.T) {
	model := netModel(t)
	rt, ui := netRuntime(t, "http://"+downAddr(t), model.srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := netCall(rt, ctx)
	if err != nil || resp.Text != "from eagent-fallback" {
		t.Fatalf("fallback asked %d times; call: %v %+v", model.count("eagent-fallback"), err, resp)
	}
	routes := netRoutes(rt)
	if len(routes) != 1 || routes[0].Model != "eagent-fallback" || !strings.Contains(routes[0].Reason, "cannot be reached") {
		t.Fatalf("route events %+v", routes)
	}
	if n := outageNotices(rt); len(n) != 0 {
		t.Fatalf("a fallback that answers is no outage, but the user was told %q", n)
	}
	var pick runtimecontrol.Route
	rt.sync(func() { pick = rt.activeSettings.Routes[event.ActorEagent] })
	if pick.Model != "eagent-fallback" {
		t.Fatalf("the settings show %+v", pick)
	}
	if !strings.Contains(strings.Join(ui.logged(), "\n"), "cannot be reached") {
		t.Fatalf("no switch in the log: %q", ui.logged())
	}
	if resp, err := netCall(rt, ctx); err != nil || resp.Text != "from eagent-fallback" || model.count("eagent-fallback") != 2 {
		t.Fatalf("the next call: %v %+v, fallback calls %d", err, resp, model.count("eagent-fallback"))
	}
}

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

// Both down, then the fallback comes back first: it takes over.
func TestBothRoutesDownThenTheFallbackBackTakesOver(t *testing.T) {
	model := netModel(t)
	pAddr, fAddr := downAddr(t), downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, "http://"+fAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	serveAt(t, fAddr, model.srv.Config.Handler)
	o := netWait(t, ch)
	if o.err != nil || o.resp.Text != "from eagent-fallback" {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	if r := netRoutes(rt); len(r) != 1 || r[0].Model != "eagent-fallback" {
		t.Fatalf("route events %+v", r)
	}
	if n := outageNotices(rt); len(n) != 1 {
		t.Fatalf("told %d times: %q", len(n), n)
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
	rt.sync(func() { told = m.out.told || m.out.down() })
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

// Two calls in the same outage: one notice, one switch, and both come
// back from the fallback.
func TestTwoCallsInOneOutageSwitchOnceAndTellOnce(t *testing.T) {
	model := netModel(t)
	pAddr, fAddr := downAddr(t), downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, "http://"+fAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := netCallAsync(rt, ctx), netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	rounds(3)
	serveAt(t, fAddr, model.srv.Config.Handler)
	for _, ch := range []<-chan netOutcome{a, b} {
		if o := netWait(t, ch); o.err != nil || o.resp.Text != "from eagent-fallback" {
			t.Fatalf("call: %v %+v", o.err, o.resp)
		}
	}
	if r, n := netRoutes(rt), outageNotices(rt); len(r) != 1 || len(n) != 1 {
		t.Fatalf("route events %+v, notices %q", r, n)
	}
}

// A call waiting out an outage returns at once when it is cancelled (the
// session stopping) or runs out of its time (the closing word), whether it
// waits between rounds or is asking the other routes.
func TestACallWaitingOnTheNetworkReturnsWhenItsContextEnds(t *testing.T) {
	for _, probing := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("probing=%v,deadline=%v", probing, deadline), func(t *testing.T) {
				waitingCallReturns(t, probing, deadline)
			})
		}
	}
}

func waitingCallReturns(t *testing.T, probing, deadline bool) {
	// The fallback takes the call and never answers: a call that is
	// asking it is held there until its context ends.
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
	if !probing {
		setFor(t, &offlineWait, time.Hour) // held in the wait between rounds
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline {
		ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()
	ch := netCallAsync(rt, ctx)
	if probing {
		waitFor(t, "the fallback to be asked", func() bool { return asked.Load() > 0 })
	} else {
		time.Sleep(20 * time.Millisecond)
	}
	if !deadline {
		cancel()
	}
	start := time.Now()
	o := netWait(t, ch)
	if o.err == nil || time.Since(start) > time.Second {
		t.Fatalf("%v after %s", o.err, time.Since(start))
	}
	if !probing && asked.Load() != 0 {
		t.Fatalf("the fallback was asked during the first wait")
	}
}

// A route asked in place of another has the limits any call to it has, the
// idle limit among them (which also bounds the wait for a reply to begin:
// llm's TestIdleTimeoutCoversTheWaitForAResponse), but one attempt. A
// reply it has begun is not cut, however slow.
func TestARouteAskedInPlaceOfAnotherHasTheOrdinaryLimits(t *testing.T) {
	t.Run("limits", func(t *testing.T) {
		rt, _ := netRuntime(t, "http://"+downAddr(t), "http://"+downAddr(t))
		var ep llm.Endpoint
		rt.sync(func() { ep = rt.models[event.ActorEagent].routes[1] })
		probe, plain := rt.probeClient(ep, event.ActorEagent), rt.newClient(ep, event.ActorEagent)
		if probe.MaxAttempts != 1 || probe.IdleTimeout != plain.IdleTimeout || probe.CallTimeout != plain.CallTimeout || probe.IdleTimeout <= 0 {
			t.Fatalf("asked in place of another: attempts %d, idle %s, call %s; an ordinary call: idle %s, call %s",
				probe.MaxAttempts, probe.IdleTimeout, probe.CallTimeout, plain.IdleTimeout, plain.CallTimeout)
		}
	})
	t.Run("slow", func(t *testing.T) {
		model := netModel(t)
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read before the answer begins: the body cannot be read after.
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond) // begun, then slow
			model.srv.Config.Handler.ServeHTTP(w, r)
		}))
		t.Cleanup(slow.Close)
		rt, _ := netRuntime(t, "http://"+downAddr(t), slow.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if o := netWait(t, netCallAsync(rt, ctx)); o.err != nil || o.resp.Text != "from eagent-fallback" {
			t.Fatalf("call: %v %+v", o.err, o.resp)
		}
		if r := netRoutes(rt); len(r) != 1 {
			t.Fatalf("routes %+v", r)
		}
	})
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

// After a fallback took over, picking the primary again in the settings
// brings the session back to it: once the outage is over, and during one of
// the fallback.
func TestPickingThePrimaryAgainLeavesTheFallback(t *testing.T) {
	model := netModel(t)
	pAddr, fAddr := downAddr(t), downAddr(t)
	rt, _ := netRuntime(t, "http://"+pAddr, "http://"+fAddr)
	serveAt(t, fAddr, model.srv.Config.Handler)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if o := netWait(t, netCallAsync(rt, ctx)); o.err != nil || o.resp.Text != "from eagent-fallback" {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	serveAt(t, pAddr, model.srv.Config.Handler)
	switchTo(rt, "http://"+pAddr, testModel)
	if o := netWait(t, netCallAsync(rt, ctx)); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("after picking the primary: %v %+v", o.err, o.resp)
	}
	// On the fallback again, which goes away under a call: the pick moves
	// the waiting call to the primary at once.
	gone := downAddr(t)
	switchTo(rt, "http://"+gone, "eagent-fallback")
	// A try in flight finishes on its route (here one attempt, not the
	// client's half minute of retries); the next goes to the pick.
	rt.sync(func() { rt.models[event.ActorEagent].client.MaxAttempts = 1 })
	ch := netCallAsync(rt, ctx)
	time.Sleep(20 * time.Millisecond)
	switchTo(rt, "http://"+pAddr, testModel)
	if o := netWait(t, ch); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("picking the primary during an outage: %v %+v", o.err, o.resp)
	}
}

// From the second reader of the outage handling (S/r6w-net), renamed.

// A fallback asked during an outage past the bound is slow to answer.
// Meanwhile the primary answers another call (it is back), then fails one
// call once (a blip, under the bound). The fallback's late answer does not
// move the session: the outage it was asked for is over, and the one now
// open is not past the bound.
func TestAFallbackAskedForAnOutageThatEndedDoesNotTakeOverOnANewBlip(t *testing.T) {
	model := netModel(t)
	var answer atomic.Bool // the primary: false drops the connection, true answers
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !answer.Load() {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		model.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(primary.Close)
	var asked atomic.Int32
	release := make(chan struct{})
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		model.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fb.Close)
	rt, _ := netRuntime(t, primary.URL, fb.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a := netCallAsync(rt, ctx)
	waitFor(t, "the fallback to be asked", func() bool { return asked.Load() > 0 })

	// The primary is back and answers a call.
	answer.Store(true)
	if o := netWait(t, netCallAsync(rt, ctx)); o.err != nil || o.resp.Text != "from "+testModel {
		t.Fatalf("the primary's answer: %v %+v", o.err, o.resp)
	}
	// Then one call of it fails once; it is not tried again here.
	setFor(t, &offlineWait, time.Hour)
	answer.Store(false)
	cctx, ccancel := context.WithCancel(ctx)
	c := netCallAsync(rt, cctx)
	waitFor(t, "the blip to be counted", func() bool {
		var down bool
		rt.sync(func() { down = rt.models[event.ActorEagent].out.down() })
		return down
	})
	var past bool
	rt.sync(func() { past = rt.models[event.ActorEagent].out.past(time.Now()) })
	if past {
		t.Fatal("setup: one failure is past the bound")
	}
	ccancel()
	netWait(t, c)
	answer.Store(true)

	close(release)
	if o := netWait(t, a); o.err != nil {
		t.Fatal(o.err)
	}
	if r := netRoutes(rt); len(r) != 0 {
		t.Fatalf("the primary answered after the outage the fallback was asked for, and has failed once since, yet the session moved: %+v", r)
	}
}

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
