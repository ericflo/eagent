package harness

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// The decision complete asks after every failed call, row by row.
func TestAFailedCallIsDecidedByOneTable(t *testing.T) {
	setFor(t, &offlineSwitch, time.Minute)
	under, past := 30*time.Second, 2*time.Minute
	for _, c := range []struct {
		name   string
		down   time.Duration
		others bool
		last   failKind
		told   bool
		want   callStep
	}{
		{"another failure is the caller's", 0, true, failOther, false, stepGiveUp},
		{"another failure after an outage", past, true, failOther, true, stepGiveUp},
		{"unroutable with a route after it", 0, true, failUnroutable, false, stepNext},
		{"unroutable at the end of the chain", 0, false, failUnroutable, false, stepGiveUp},
		{"unreachable for the first time", 0, true, failNetwork, false, stepRetry},
		{"unreachable under the bound, a fallback set", under, true, failNetwork, false, stepRetry},
		{"unreachable under the bound, alone", under, false, failNetwork, false, stepRetry},
		{"unreachable at the bound, a route not asked", time.Minute, true, failNetwork, false, stepNext},
		{"past the bound, a route not asked", past, true, failNetwork, false, stepNext},
		{"past the bound, told, a route not asked", past, true, failNetwork, true, stepNext},
		{"past the bound, every route asked", past, false, failNetwork, false, stepTell},
		{"past the bound, every route asked, told", past, false, failNetwork, true, stepRetry},
	} {
		if got := netStep(c.down, c.others, c.last, c.told); got != c.want {
			t.Errorf("%s: step %d, want %d", c.name, got, c.want)
		}
	}
}

// downAddr is a loopback address nothing listens on: a call to it is
// refused, as when a local model server is stopped.
func downAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// serveAt starts h on addr (one downAddr gave) until the test ends.
func serveAt(t *testing.T, addr string, h http.Handler) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
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
	rt.sync(func() { told = m.told || !m.downSince.IsZero() })
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
	for _, deadline := range []bool{false, true} {
		rt, _ := netRuntime(t, "http://"+downAddr(t), "http://"+downAddr(t))
		setFor(t, &offlineWait, time.Hour)
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
		}
		ch := netCallAsync(rt, ctx)
		if !deadline {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}
		start := time.Now()
		o := netWait(t, ch)
		if o.err == nil || time.Since(start) > time.Second {
			t.Fatalf("deadline %v: %v after %s", deadline, o.err, time.Since(start))
		}
		cancel()
	}
}
