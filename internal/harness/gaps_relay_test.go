package harness

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// Behaviours of main that this branch does not have (relay group). Run
// with -tags gaps. See ledger-relay.md for the details of each.

// relayCancelUI cancels the run the first time the user is told something:
// a kill that lands between two tool calls of one reply.
type relayCancelUI struct {
	*fakeUI
	cancel func()
}

func (u *relayCancelUI) Say(text string) {
	u.fakeUI.Say(text)
	if u.cancel != nil {
		u.cancel()
		// Say runs on the loop: hold it a moment so the shutdown the
		// cancel asks for is queued ahead of the turn's next step, and the
		// kill lands between the two calls every time.
		time.Sleep(50 * time.Millisecond)
	}
}

// GAP 1, deterministic. eagent decided, in one reply, to tell the user and
// stop the agent; the process was killed after the first call ran. On
// main the resumed session told the orchestrator "[Session resumed ... after
// an interruption.]" and it acted again. Here the reply's SeenSeq marks
// everything before it as looked at, the second call's result is
// "interrupted", and nothing wakes eagent after the resume: the agent is
// never stopped and a batch session never ends.
func TestGapInterruptedActionsAreTakenUpAfterResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	f := useRelayFactory(t, "Done: #42 and #44 are merged.")
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now"):
			return say("All done.")
		case strings.Contains(all, "finished a turn") && !strings.Contains(all, "Stopped Codex."):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Codex merged the PRs; stopping it."}`),
				tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
			}}
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	cfg := testConfig(model.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := &relayCancelUI{fakeUI: &fakeUI{}, cancel: cancel}
	rt, err := New(cfg, Options{Project: project, Prompt: brief + " Stop when done."}, ui)
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.Run(ctx); code != 130 {
		t.Fatalf("first run exit %d, want 130 (killed)", code)
	}
	evs, _ := store.Read(rt.sess.Path)
	interrupted := false
	for _, ev := range evs {
		var d event.ToolResultData
		if ev.Type == event.ToolResult && ev.Decode(&d) == nil && d.Name == "relay_control" && d.Output == "interrupted" {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatal("setup: the stop was not cut off by the kill")
	}
	ui2 := &fakeUI{}
	rt2, err := Resume(cfg, Options{Project: project}, ui2, rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	code := rt2.Run(ctx2)
	seats := f.all()
	st := state.Replay(mustRead(t, rt.sess.Path))
	if code != 0 || st.EndReason != "done" || !seats[len(seats)-1].wasKilled() {
		t.Fatalf("after the resume: exit %d, end %q, agent stopped %v: the interrupted stop was never redone", code, st.EndReason, seats[len(seats)-1].wasKilled())
	}
}

func mustRead(t *testing.T, path string) []event.Event {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// Old: TestChaosCrashResumeLoop. The runner is killed at random points and
// resumed, with provider faults on top; the session still finishes, every
// run leaves a consistent log, and a killed run exits 130. The agents
// outlive each run and are reattached; a reattached agent replays the turn
// its transcript holds that the record does not.
func TestChaosCrashResumeLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("chaos suite skipped with -short")
	}
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}
	t.Cleanup(func() { errorRetryDelays = old })
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Setenv("EAGENT_TEST_KEY", "x")
			// On the disk, not in memory: each event is fsynced, which takes
			// the milliseconds that let a kill land between a call's message
			// and its result. In memory a turn crosses that in microseconds
			// and the kills miss it.
			project := diskDir(t)
			rng := rand.New(rand.NewSource(seed))
			c := newRelayChaos(seed+100, 0.2, relayChaosBrain)
			defer c.srv.Close()
			cfg := testConfig(c.srv.URL)
			var sessionPath string
			f := useRelayFactory(t, "Wrote part1.txt, part2.txt and part3.txt.")
			f.onAttach = func(s *fakeSeat) {
				// The agent's transcript backlog: its answer to the brief, if
				// the record does not have it yet.
				evs, _ := store.Read(sessionPath)
				for _, ev := range evs {
					if ev.Type == event.RelayTurn {
						return
					}
				}
				s.emit(relay.Event{Kind: relay.KindState, State: "working"}, relay.Event{Kind: relay.KindTurn, Text: s.reply, DurationMS: 1000, Tools: 3}, relay.Event{Kind: relay.KindState, State: "idle"})
			}
			resumes := 0
			for attempt := 1; attempt <= 16; attempt++ {
				ui := &fakeUI{}
				var rt *Runtime
				var err error
				if sessionPath == "" {
					rt, err = New(cfg, Options{Project: project, Prompt: relayChaosBrief}, ui)
				} else {
					rt, err = Resume(cfg, Options{Project: project}, ui, sessionPath)
				}
				if err != nil {
					t.Fatal(err)
				}
				if attempt > 1 {
					resumes++
				}
				sessionPath = rt.sess.Path
				ctx, cancel := context.WithCancel(context.Background())
				if attempt < 13 {
					time.AfterFunc(time.Duration(10+rng.Intn(250))*time.Millisecond, cancel)
				} else {
					time.AfterFunc(60*time.Second, cancel)
				}
				code := rt.Run(ctx)
				cancel()
				st := relayCheckLog(t, sessionPath)
				if st.EndReason == "done" && code == 0 {
					if n := relayBriefSends(st, relayChaosBrief); n != 1 {
						t.Fatalf("the brief went to the agent %d times", n)
					}
					if resumes == 0 {
						t.Fatalf("the first run finished before any kill landed; the test did not exercise resume")
					}
					t.Logf("seed %d: done after %d runs (%d resumes), %d events, faults %v", seed, attempt, resumes, len(st.Events), c.faults())
					return
				}
				if code != 130 {
					t.Fatalf("attempt %d: unexpected exit %d (%s); logs %v", attempt, code, st.EndReason, ui.logged())
				}
			}
			t.Fatal("never finished")
		})
	}
}

// diskTmp is the temporary directory the run started with, before TestMain
// moved temporary files to memory.
var diskTmp string

// diskDir is a temporary directory on diskTmp, for a test whose timing
// rests on writes taking as long as they do on a disk.
func diskDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(diskTmp, "eagent-disk-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}
