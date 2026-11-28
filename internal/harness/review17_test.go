package harness

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// r17Pick moves eagent's calls to another model at url, as a pick in the
// live settings does.
func r17Pick(t *testing.T, rt *Runtime, url string) {
	t.Helper()
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: url, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 1024}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	rt.sync(func() {
		rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
	})
}

// A call of eagent's is out on one model when the user picks another, and
// the call is then cut off: its time ran out while the provider held it,
// or while it waited for a network that did not come back. Like a call the
// provider refused, it was made on the old model, and its failure says so:
// it is not read as the failure of the model in use.
func TestACallCutOffAfterAModelChangeIsNotTheNewModelsFailure(t *testing.T) {
	for _, how := range []string{"held by the provider", "waiting for the network"} {
		t.Run(how, func(t *testing.T) {
			addr := downAddr(t)
			entered := make(chan struct{}, 1)
			if how == "held by the provider" {
				serveAt(t, addr, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					_, _ = io.Copy(io.Discard, req.Body) // so the server notices the call given up
					entered <- struct{}{}
					<-req.Context().Done()
				}))
			}
			rt, ui := netRuntime(t, "http://"+addr, "")
			setFor(t, &offlineWait, time.Minute) // the wait the call is cut off in
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch := netCallAsync(rt, ctx)
			if how == "held by the provider" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("setup: the call was never made")
				}
			} else {
				waitFor(t, "the call waiting for the network", func() bool {
					return strings.Contains(strings.Join(ui.logged(), "\n"), "waiting for the network")
				})
			}
			r17Pick(t, rt, "http://"+downAddr(t))
			cancel()
			o := netWait(t, ch)
			var counted bool
			rt.sync(func() { counted = rt.onRouteInUse(o.err) })
			if o.err == nil || counted {
				t.Fatalf("the call made before the pick failed with %v, and is read as the picked model's failure: %v", o.err, counted)
			}
		})
	}
}

// A session with no terminal is ending, and its closing call is out on one
// model when the user picks another. The provider holds the call until the
// closing word's time runs out. The closing word is then asked of the model
// in use, and the user is not told that no closing report could be written.
func TestAClosingCallThatRunsOutOfTimeAfterAModelChangeIsAskedOfTheNewModel(t *testing.T) {
	setFor(t, &finalReportTimeout, 300*time.Millisecond)
	other := newScripted(func(_ string, m []map[string]any) reply {
		if m[len(m)-1]["role"] == "tool" {
			return hold()
		}
		return say("Closing: the build is green and Codex is stopped.")
	})
	t.Cleanup(other.srv.Close)
	never := make(chan struct{})
	defer close(never) // before the model's server is closed, which waits for the held call
	var rtp atomic.Pointer[Runtime]
	var reported, asked atomic.Bool
	rt, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		switch {
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if !asked.Swap(true) {
				// The pick, made while this call is out.
				r17Pick(t, rtp.Load(), other.srv.URL)
			}
			return reply{block: never}
		case m[len(m)-1]["role"] == "tool" || reported.Swap(true):
			return hold()
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	rtp.Store(rt)
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatal("the session did not end")
	}
	told := strings.Join(ui.said(), "\n---\n")
	if n := other.count("other-model"); n == 0 || strings.Contains(told, "could not write a proper closing report") || !strings.Contains(told, "Closing: the build is green") {
		t.Fatalf("exit %d: the model picked while the closing call was out got %d calls; the user was told:\n%s", code, n, told)
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

// Codex is starting again with a model the user picked, and the same pick
// arrives again while it starts (the phone's settings saved twice). It is
// what the start runs: the live settings go on showing it as Codex's
// model, where they fell back to the one Codex ran before, and an effort
// picked alone after that was laid over that old model.
func TestTheSamePickAgainWhileAnAgentStartsKeepsWhatTheStartRuns(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	pick := relayPick{"gpt-6.2-astra", "high"}
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("codex", pick) })
	if err != nil || !strings.Contains(msg, "starting again") {
		t.Fatalf("repick: %q %v", msg, err)
	}
	g.waitEntered(t)
	rt.sync(func() { msg, err = rt.repickRelay("codex", pick) })
	if err != nil || !strings.Contains(msg, "already starting again with gpt-6.2-astra at high") {
		t.Fatalf("the same pick again: %q %v", msg, err)
	}
	rt.sync(func() {
		if got := rt.runsNext(rt.st.Relays["codex"]); got != pick {
			t.Errorf("while it starts, Codex is shown as running %+v next, want %+v", got, pick)
		}
		if got := rt.relayRoutes()["codex"]; got.Model != pick.Model || got.Effort != pick.Effort {
			t.Errorf("while it starts, the live settings show %+v", got)
		}
	})
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if _, waiting := rt.waitingPick("codex"); rel.Model != pick.Model || rel.Effort != pick.Effort || waiting {
			t.Errorf("after the start: %s at %s, waiting %v", rel.Model, rel.Effort, rt.relayNext)
		}
	})
}
