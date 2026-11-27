package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// sr9ServerError is a provider's 500: a failure that is not a refusal.
var sr9ServerError = reply{status: 500, body: `{"error":{"message":"overloaded","type":"api_error"}}`}

// sr9ClosingRefusedRun runs a batch session whose closing word is refused
// (400) when refuseClosing, else held. During its last title and summary a
// timer is set (the session stops ending) and fires (it is ending again),
// and that call fails with a 500: not a refusal. It returns the phase
// reached (4: the timer's notice got its look) and the exit code.
func sr9ClosingRefusedRun(t *testing.T, refuseClosing bool) (int32, int) {
	return sr9ClosingRefusedRunWith(t, refuseClosing, sr9ServerError)
}

// sr9ClosingRefusedRunWith is sr9ClosingRefusedRun with the summary's
// failure given (fail).
func sr9ClosingRefusedRunWith(t *testing.T, refuseClosing bool, fail reply) (int32, int) {
	coreSummariesOn(t, false)
	setFor(t, &wakeSettle, time.Millisecond)
	var phase atomic.Int32
	var fired atomic.Bool
	var rt atomic.Pointer[Runtime]
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			if refuseClosing {
				return k1BadRequest
			}
			return hold()
		case summary && phase.CompareAndSwap(2, 3):
			r := rt.Load()
			r.sync(func() {
				r.append(event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s1", Kind: "once", Spec: "1s", Note: "check the build again", Next: time.Now().Add(time.Second)}))
			})
			r.sync(func() {
				r.append(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: "s1", Note: "check the build again"}))
			})
			fired.Store(true)
			return fail
		case summary:
			return fail
		case fired.Load() && phase.CompareAndSwap(3, 4):
			return hold() // the timer's look
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: the build is green.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("The build is green."), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1
		rt.Store(r)
	})
	code := coreExit(s)
	return phase.Load(), code
}

// Control: with the closing word answered, the timer's notice (fired
// during the last title and summary, whose call failed with a 500) gets
// its look.
func TestSR9LoopControlTimerNoticeLookedAtAfterSummaryFails(t *testing.T) {
	if p, code := sr9ClosingRefusedRun(t, false); p != 4 {
		t.Fatalf("control: phase %d, exit %d; want the timer's look (phase 4)", p, code)
	}
}

// The closing word is refused (closingRefused). During the last title and
// summary a timer is set (the session stops ending) and fires (ending
// again), and the summary's call fails with a 500, not a refusal, while
// the session is ending: the closed-out path (w.wait > 0 && closing) does
// not go through turnFailed, so the refusal kept from the closing that
// stopped is not cleared (9b02490 clears it only in turnFailed). The
// session ends at once and the timer's notice gets no look.
func TestSR9LoopKeptRefusalSurvivesASummaryFailureThatIsNotARefusal(t *testing.T) {
	p, code := sr9ClosingRefusedRun(t, true)
	if p != 4 {
		t.Fatalf("phase %d, exit %d: the timer's notice got no look; want its look (phase 4), as without the earlier refusal", p, code)
	}
}

// The same with the last title and summary running out of its own time
// (summaryFinalWait): the turn breaks on ctx.Err() with no step on the
// loop, so the kept refusal is not cleared either.
func TestSR9LoopKeptRefusalSurvivesASummaryThatTimesOut(t *testing.T) {
	setFor(t, &summaryFinalWait, 300*time.Millisecond)
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	p, code := sr9ClosingRefusedRunWith(t, true, reply{block: never, calls: hold().calls})
	if p != 4 {
		t.Fatalf("phase %d, exit %d: the timer's notice got no look after the summary timed out; want its look (phase 4)", p, code)
	}
}

// Probe (not a bar finding): an ordinary /quit of an interactive session
// with an agent up. closeRelays closes the seat after endQueue, so the
// seat watcher's last post is refused; post logs that refusal.
func TestSR9LoopProbeOrdinaryQuitLogsRefusedLoopWork(t *testing.T) {
	seat := newFakeSeat("ok")
	rt, _, done := r10Session(t, seat)
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	for _, l := range rt.ui.(*fakeUI).logged() {
		if strings.Contains(l, "loop work after the session's end was not run") {
			t.Logf("PROBE: an ordinary quit logged %q", l)
			return
		}
	}
	t.Logf("PROBE: no refused-work line on an ordinary quit")
}

// Control for the timeout: with the closing word answered, the notice is
// looked at.
func TestSR9LoopControlTimerNoticeLookedAtAfterSummaryTimesOut(t *testing.T) {
	setFor(t, &summaryFinalWait, 300*time.Millisecond)
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	if p, code := sr9ClosingRefusedRunWith(t, false, reply{block: never, calls: hold().calls}); p != 4 {
		t.Fatalf("control: phase %d, exit %d; want the timer's look (phase 4)", p, code)
	}
}
