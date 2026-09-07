package harness

// Jepsen-flavoured tests: the provider misbehaves at random (5xx, truncated
// streams, malformed arguments, empty replies, latency) and the runner is
// killed and resumed at random points. The work must still finish, and the
// log must stay consistent: every file parses, sequence numbers are strictly
// increasing without gaps, replayed state matches the live outcome, and no
// tool call is left without a result.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// chaosServer wraps the scripted brain with random faults.
type chaosServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	rng   *rand.Rand
	rate  float64 // probability of a fault per request
	brain func(model string, msgs []map[string]any) reply
	stats map[string]int
}

func newChaos(seed int64, rate float64, brain func(string, []map[string]any) reply) *chaosServer {
	c := &chaosServer{rng: rand.New(rand.NewSource(seed)), rate: rate, brain: brain, stats: map[string]int{}}
	c.srv = httptest.NewServer(http.HandlerFunc(c.handle))
	return c
}

func (c *chaosServer) handle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string           `json:"model"`
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c.mu.Lock()
	roll := c.rng.Float64()
	fault := ""
	if roll < c.rate {
		fault = []string{"503", "429", "truncate", "malformed", "empty", "slow", "garbage"}[c.rng.Intn(7)]
	}
	c.stats[fault]++
	c.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	switch fault {
	case "503":
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"message":"no available server"}}`)
		return
	case "429":
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
		return
	case "truncate":
		// Dies mid tool call: no finish_reason.
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`)
		return
	case "garbage":
		fmt.Fprint(w, "data: {not json\n\ndata: [DONE]\n\n")
		return
	case "empty":
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	case "slow":
		time.Sleep(300 * time.Millisecond)
	default:
		// Real providers take time; without this, kills never land mid-work.
		c.mu.Lock()
		d := time.Duration(20+c.rng.Intn(60)) * time.Millisecond
		c.mu.Unlock()
		time.Sleep(d)
	}
	rep := c.brain(body.Model, body.Messages)
	if fault == "malformed" && len(rep.calls) > 0 {
		// Cut the first call's arguments in half.
		a := string(rep.calls[0].Args)
		rep.calls[0].Args = json.RawMessage(a[:len(a)/2])
	}
	writeReply(w, rep)
}

func writeReply(w http.ResponseWriter, rep reply) {
	if rep.text != "" {
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": rep.text}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	for i, c := range rep.calls {
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": c.ID + fmt.Sprint(i), "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Args)},
		}}}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	finish := "stop"
	if len(rep.calls) > 0 {
		finish = "tool_calls"
	}
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 10}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
}

// workBrain drives a three-part build to completion whatever happens: the
// orchestrator delegates three tasks, waits for whatever is running,
// re-delegates parts that never landed, verifies, and finishes.
func workBrain(project string) func(string, []map[string]any) reply {
	started := regexp.MustCompile(`Task (t\d+) started`)
	ended := regexp.MustCompile(`\[Task (t\d+) (completed|failed|interrupted|cancelled)\]`)
	return func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			for _, n := range []string{"1", "2", "3"} {
				if !strings.Contains(all, fmt.Sprintf(`"part %s"`, n)) && !strings.Contains(all, "part "+n+" started") && !strings.Contains(all, "Task t"+n+" started") {
					return reply{calls: []event.ToolCall{tc("delegate", fmt.Sprintf(`{"title":"part %s","description":"write part%s.txt"}`, n, n))}}
				}
			}
			open := map[string]bool{}
			for _, m := range started.FindAllStringSubmatch(all, -1) {
				open[m[1]] = true
			}
			for _, m := range ended.FindAllStringSubmatch(all, -1) {
				delete(open, m[1])
			}
			if len(open) > 0 {
				return reply{calls: []event.ToolCall{tc("wait", `{"timeout_seconds":20}`)}}
			}
			for _, n := range []string{"1", "2", "3"} {
				if _, err := os.Stat(filepath.Join(project, "part"+n+".txt")); err != nil {
					// Each redo gets a distinct title so it is delegated once per round.
					round := strings.Count(all, "redo part "+n) + 1
					return reply{calls: []event.ToolCall{tc("delegate", fmt.Sprintf(`{"title":"redo part %s (%d)","description":"write part%s.txt"}`, n, round, n))}}
				}
			}
			if !strings.Contains(all, "all parts present") {
				return reply{calls: []event.ToolCall{tc("bash", `{"command":"ls part1.txt part2.txt part3.txt && echo all parts present"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("note", `{"text":"three parts written"}`), tc("yield", `{"done":true,"reason":"three parts written"}`)}}
		case "task":
			n := "1"
			if i := strings.Index(all, "write part"); i >= 0 && i+10 < len(all) {
				n = all[i+10 : i+11]
			}
			if !strings.Contains(all, "created part"+n+".txt") && !strings.Contains(all, "overwrote part"+n+".txt") {
				return reply{calls: []event.ToolCall{tc("write_file", fmt.Sprintf(`{"path":"part%s.txt","content":"part %s"}`, n, n))}}
			}
			return reply{calls: []event.ToolCall{tc("complete_task", fmt.Sprintf(`{"status":"completed","summary":"wrote part%s.txt"}`, n))}}
		default:
			if strings.Contains(all, "DECLARED THE WORK DONE") || strings.Contains(lastUserText(msgs), "final") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"All three parts are written."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
}

// checkLog verifies the structural invariants of a session directory.
func checkLog(t *testing.T, path string) *state.State {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatalf("log unreadable: %v", err)
	}
	var last int64
	for _, ev := range evs {
		if ev.Seq != last+1 {
			t.Fatalf("sequence gap or duplicate: %d after %d (%s)", ev.Seq, last, ev.Source)
		}
		last = ev.Seq
	}
	st := state.Replay(evs)
	// Every assistant tool call has a result (after resume repairs).
	if st.Ended {
		for _, actor := range []string{event.ActorOrchestrator, event.ActorNarrator} {
			if d := st.DanglingCalls(actor, ""); len(d) > 0 {
				t.Fatalf("%s has %d dangling tool calls after the session ended", actor, len(d))
			}
		}
		for _, id := range st.TaskOrder {
			if st.Tasks[id].Running() {
				t.Fatalf("task %s still running after the session ended", id)
			}
		}
	}
	return st
}

func TestChaosProviderFaults(t *testing.T) {
	if testing.Short() {
		t.Skip("chaos suite skipped with -short")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			project := t.TempDir()
			c := newChaos(seed, 0.3, workBrain(project))
			defer c.srv.Close()
			cfg := testConfig(c.srv.URL)
			ui := &fakeUI{}
			rt, err := New(cfg, Options{Project: project, Prompt: "build the three parts"}, ui)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			code := rt.Run(ctx)
			st := checkLog(t, rt.sess.Path)
			if code != 0 || st.EndReason != "done" {
				t.Fatalf("exit %d reason %s; faults %v; logs %v", code, st.EndReason, c.stats, ui.logs)
			}
			for _, n := range []string{"1", "2", "3"} {
				if _, err := os.Stat(filepath.Join(project, "part"+n+".txt")); err != nil {
					t.Fatalf("part%s.txt missing", n)
				}
			}
			if len(ui.messages) == 0 {
				t.Fatal("user heard nothing")
			}
			t.Logf("seed %d: faults injected %v, orchestrator calls %d, events %d", seed, c.stats, st.Calls[event.ActorOrchestrator], len(st.Events))
		})
	}
}

func TestChaosCrashResumeLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("chaos suite skipped with -short")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			project := t.TempDir()
			rng := rand.New(rand.NewSource(seed))
			c := newChaos(seed+100, 0.2, workBrain(project))
			defer c.srv.Close()
			cfg := testConfig(c.srv.URL)
			var sessionPath string
			resumes := 0
			for attempt := 1; attempt <= 16; attempt++ {
				ui := &fakeUI{}
				var rt *Runtime
				var err error
				if sessionPath == "" {
					rt, err = New(cfg, Options{Project: project, Prompt: "build the three parts"}, ui)
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
				// Kill at a random moment, or let it run on the last attempts.
				ctx, cancel := context.WithCancel(context.Background())
				if attempt < 13 {
					kill := time.Duration(30+rng.Intn(500)) * time.Millisecond
					time.AfterFunc(kill, cancel)
				} else {
					time.AfterFunc(60*time.Second, cancel)
				}
				code := rt.Run(ctx)
				cancel()
				st := checkLog(t, sessionPath)
				if st.EndReason == "done" && code == 0 {
					for _, n := range []string{"1", "2", "3"} {
						if _, err := os.Stat(filepath.Join(project, "part"+n+".txt")); err != nil {
							t.Fatalf("part%s.txt missing", n)
						}
					}
					if resumes == 0 {
						t.Fatalf("the first run finished before any kill landed; the test did not exercise resume")
					}
					t.Logf("seed %d: done after %d runs (%d resumes), %d events, faults %v", seed, attempt, resumes, len(st.Events), c.stats)
					return
				}
				if code != 130 {
					t.Fatalf("attempt %d: unexpected exit %d (%s); logs %v", attempt, code, st.EndReason, ui.logs)
				}
			}
			t.Fatal("never finished")
		})
	}
}
