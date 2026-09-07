package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// fakeUI records what the user would see.
type fakeUI struct {
	mu       sync.Mutex
	messages []string
	asked    []string
	logs     []string
	input    chan string
}

func (u *fakeUI) Narrate(text string) {
	u.mu.Lock()
	u.messages = append(u.messages, text)
	u.mu.Unlock()
}
func (u *fakeUI) Ask(id, text string, options []string) {
	u.mu.Lock()
	u.asked = append(u.asked, text)
	u.mu.Unlock()
}
func (u *fakeUI) Status(Status)                          {}
func (u *fakeUI) Trace(event.Event)                      {}
func (u *fakeUI) Stream(actor, task, kind, delta string) {}
func (u *fakeUI) Log(format string, args ...any) {
	u.mu.Lock()
	u.logs = append(u.logs, fmt.Sprintf(format, args...))
	u.mu.Unlock()
}
func (u *fakeUI) Input() <-chan string { return u.input }
func (u *fakeUI) askedCount() int      { u.mu.Lock(); defer u.mu.Unlock(); return len(u.asked) }
func (u *fakeUI) messageCount() int    { u.mu.Lock(); defer u.mu.Unlock(); return len(u.messages) }
func (u *fakeUI) snapshot() ([]string, []string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string{}, u.messages...), append([]string{}, u.asked...), append([]string{}, u.logs...)
}
func (u *fakeUI) Idle(bool) {}

// scripted is a fake chat-completions server. brain decides each reply from
// the model name and the messages sent so far.
type scripted struct {
	srv   *httptest.Server
	brain func(model string, msgs []map[string]any) reply
	mu    sync.Mutex
	calls map[string]int
}

type reply struct {
	text   string
	calls  []event.ToolCall
	prompt int // reported prompt tokens
	block  <-chan struct{}
}

func tc(name string, args string) event.ToolCall {
	return event.ToolCall{ID: fmt.Sprintf("c%d", time.Now().UnixNano()%1_000_000), Name: name, Args: json.RawMessage(args)}
}

func newScripted(brain func(model string, msgs []map[string]any) reply) *scripted {
	s := &scripted{brain: brain, calls: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model    string           `json:"model"`
			Messages []map[string]any `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.calls[body.Model]++
		s.mu.Unlock()
		rep := s.brain(body.Model, body.Messages)
		if rep.block != nil {
			select {
			case <-rep.block:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
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
		prompt := rep.prompt
		if prompt == 0 {
			prompt = 1000
		}
		usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": 10}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	}))
	return s
}

func (s *scripted) count(model string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[model]
}

func testConfig(url string) config.Config {
	cfg := config.Defaults()
	for _, a := range []*config.Actor{&cfg.Orchestrator, &cfg.Task, &cfg.Narrator} {
		a.BaseURL = url
		a.APIKeyEnv = "EAGENT_TEST_KEY"
		a.Protocol = llm.ProtocolChat
	}
	cfg.Orchestrator.Model = "orch"
	cfg.Task.Model = "task"
	cfg.Narrator.Model = "narr"
	cfg.NarratorTickSeconds = 3600
	cfg.RolloverTokens = 20000
	off := false
	cfg.Finalechat.Enabled = &off // never the real phone from a test
	return cfg
}

// TestMain keeps every test in this package away from a real Finalechat
// account: the kill switch wins over any token on the machine. Tests that
// exercise the mirror point it at a fake server and flip the switch back.
func TestMain(m *testing.M) {
	os.Setenv("EAGENT_FINALECHAT", "off")
	os.Exit(m.Run())
}

// lastUserText returns the text of the last user message in a request.
func lastUserText(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			s, _ := msgs[i]["content"].(string)
			return s
		}
	}
	return ""
}

func allText(msgs []map[string]any) string {
	var b strings.Builder
	for _, m := range msgs {
		switch c := m["content"].(type) {
		case string:
			b.WriteString(c + "\n")
		case []any: // text parts next to images
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok {
						b.WriteString(t + "\n")
					}
				}
			}
		}
	}
	return b.String()
}

func countRole(msgs []map[string]any, role string) int {
	n := 0
	for _, m := range msgs {
		if m["role"] == role {
			n++
		}
	}
	return n
}

func TestEndToEndDelegationAndFinish(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case !strings.Contains(all, "Task t1 started"):
				return reply{calls: []event.ToolCall{tc("delegate", `{"title":"Write hello","description":"Create hello.txt containing hello"}`)}}
			case !strings.Contains(all, "Task t1 completed"):
				return reply{calls: []event.ToolCall{tc("wait", `{"tasks":["t1"],"timeout_seconds":30}`)}}
			default:
				return reply{calls: []event.ToolCall{
					tc("note", `{"text":"hello.txt is written and verified"}`),
					tc("yield", `{"done":true,"reason":"hello.txt exists"}`),
				}}
			}
		case "task":
			if !strings.Contains(all, "created hello.txt") {
				return reply{calls: []event.ToolCall{tc("write_file", `{"path":"hello.txt","content":"hello"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("complete_task", `{"status":"completed","summary":"wrote hello.txt; verified with read"}`)}}
		default: // narrator
			if strings.Contains(all, "DECLARED THE WORK DONE") || strings.Contains(lastUserText(msgs), "final") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done: hello.txt was created."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{"reason":"nothing yet"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "make hello.txt"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code := rt.Run(ctx)
	if code != 0 {
		t.Fatalf("exit code %d; logs: %v", code, ui.logs)
	}
	if raw, err := os.ReadFile(filepath.Join(project, "hello.txt")); err != nil || string(raw) != "hello" {
		t.Fatalf("hello.txt = %q %v", raw, err)
	}
	if len(ui.messages) == 0 || !strings.Contains(ui.messages[len(ui.messages)-1], "Done") {
		t.Fatalf("narrator messages = %v", ui.messages)
	}
	// Replay reconstructs the same picture.
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(evs)
	if !st.Ended || st.EndReason != "done" || st.Tasks["t1"].Status != "completed" || !st.Idle() || !st.LastYield.Done {
		t.Fatalf("replayed state: ended=%v reason=%s task=%+v", st.Ended, st.EndReason, st.Tasks["t1"])
	}
	if got := s.count("orch"); got != 3 {
		t.Fatalf("orchestrator calls = %d, want 3", got)
	}
	// The narrator must not have been woken without anything new to see.
	if n := s.count("narr"); n > 3 {
		t.Fatalf("narrator called %d times", n)
	}
}

func TestTextOnlyOrchestratorIsNudgedThenYields(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		switch model {
		case "orch":
			return reply{text: "I think we are done."} // never calls a tool
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"final words"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, _ := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "hi"}, ui)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code := rt.Run(ctx)
	if code != 2 {
		t.Fatalf("exit code %d (want 2: awaiting input), logs %v", code, ui.logs)
	}
	if s.count("orch") != 3 {
		t.Fatalf("expected 2 nudges then a forced yield; orchestrator calls = %d", s.count("orch"))
	}
	if len(ui.messages) == 0 {
		t.Fatal("the user must still hear from the narrator")
	}
}

func TestRolloverProducesDossierAndNewSubsession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			if strings.Contains(all, "Dossier: your previous context filled up") {
				if !strings.Contains(all, "THE DOSSIER TEXT") {
					return reply{text: "dossier missing"}
				}
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"finished after rollover"}`)}}
			}
			// First subsession: one huge-context call that keeps working.
			return reply{prompt: 25000, calls: []event.ToolCall{tc("bash", `{"command":"echo working"}`)}}
		case "task":
			// The dossier task.
			if !strings.Contains(all, "session_list") || !strings.Contains(all, "1 lines") && !strings.Contains(all, "lines") {
				return reply{calls: []event.ToolCall{tc("session_list", `{}`)}}
			}
			return reply{calls: []event.ToolCall{tc("complete_task", `{"status":"completed","summary":"THE DOSSIER TEXT. `+strings.Repeat("Details of the work so far. ", 12)+` MAP: goal at 0000000000001.jsonl:3-4"}`)}}
		default:
			if strings.Contains(lastUserText(msgs), "final") || strings.Contains(all, "DECLARED THE WORK DONE") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"done"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d logs %v", code, ui.logs)
	}
	files, _ := store.ReadOnly(rt.sess.Path).Files()
	if len(files) != 2 {
		t.Fatalf("expected 2 subsession files, got %v", files)
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if len(st.Subsessions) != 2 || st.Subsessions[1].Reason != "rollover" || !strings.Contains(st.Subsessions[1].Dossier, "THE DOSSIER TEXT") {
		t.Fatalf("subsessions = %+v", st.Subsessions)
	}
	// The dossier task's events live in the new file.
	for _, ev := range evs {
		if ev.Type == event.TaskCreate && ev.Source.File != files[1] {
			t.Fatalf("dossier task created in %s, want %s", ev.Source.File, files[1])
		}
	}
	if st.EndReason != "done" {
		t.Fatalf("end reason %s", st.EndReason)
	}
}

func TestInterruptAndResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	block := make(chan struct{})
	var phase sync.Map
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "Task t2 completed"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"done after resume"}`)}}
			case strings.Contains(all, "interrupted"):
				if !strings.Contains(all, "Task t2 started") {
					return reply{calls: []event.ToolCall{tc("delegate", `{"title":"again","description":"redo"}`)}}
				}
				return reply{calls: []event.ToolCall{tc("wait", `{"tasks":["t2"]}`)}}
			case !strings.Contains(all, "Task t1 started"):
				return reply{calls: []event.ToolCall{tc("delegate", `{"title":"slow","description":"slow work"}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("wait", `{"tasks":["t1"]}`)}}
			}
		case "task":
			if _, resumed := phase.Load("resumed"); !resumed {
				return reply{block: block} // hangs until the runner is interrupted
			}
			return reply{calls: []event.ToolCall{tc("complete_task", `{"status":"completed","summary":"redone"}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"status"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait until the task worker is mid-call, then pull the plug.
		for i := 0; i < 200 && s.count("task") == 0; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	code := rt.Run(ctx)
	if code != 130 {
		t.Fatalf("interrupted exit = %d", code)
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	// The task is either recorded as interrupted at shutdown (normal) or
	// still marked running (if the worker did not stop within the grace).
	if st.EndReason != "interrupted" || (st.Tasks["t1"].Status != "running" && st.Tasks["t1"].Status != "interrupted") {
		t.Fatalf("after interrupt: reason=%s task=%s", st.EndReason, st.Tasks["t1"].Status)
	}

	phase.Store("resumed", true)
	close(block)
	ui2 := &fakeUI{}
	rt2, err := Resume(testConfig(s.srv.URL), Options{Project: project}, ui2, rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if code := rt2.Run(ctx2); code != 0 {
		t.Fatalf("resumed exit %d logs %v", code, ui2.logs)
	}
	evs, _ = store.Read(rt.sess.Path)
	st = state.Replay(evs)
	if st.Tasks["t1"].Status != "interrupted" || st.Tasks["t2"].Status != "completed" || st.EndReason != "done" {
		t.Fatalf("after resume: t1=%s t2=%v end=%s", st.Tasks["t1"].Status, st.Tasks["t2"], st.EndReason)
	}
	if st.Resumes != 1 {
		t.Fatalf("resumes = %d", st.Resumes)
	}
}

func TestInteractiveQuestionAndAnswer(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "answered question") && strings.Contains(all, "blue"):
				return reply{calls: []event.ToolCall{tc("note", `{"text":"Using blue as requested."}`), tc("yield", `{"done":true,"reason":"colour chosen: blue"}`)}}
			case strings.Contains(all, "answered question"):
				return reply{text: "unexpected answer"}
			default:
				return reply{calls: []event.ToolCall{
					tc("note", `{"text":"I need the user to pick a colour: red or blue."}`),
					tc("yield", `{"done":false,"reason":"waiting for the colour decision"}`),
				}}
			}
		default: // narrator
			switch {
			case strings.Contains(all, "colour chosen"):
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Blue it is; done."}`)}}
			case strings.Contains(all, "pick a colour") && !strings.Contains(all, "asked;"):
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Interactive: true, Prompt: "paint it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()

	// Wait for the question, answer by option number, then quit once done.
	deadline := time.Now().Add(20 * time.Second)
	for ui.askedCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_, asked, logs := ui.snapshot()
	if len(asked) != 1 || !strings.Contains(asked[0], "Which colour") {
		t.Fatalf("asked = %v logs=%v", asked, logs)
	}
	ui.input <- "2"
	for ui.messageCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	messages, _, logs := ui.snapshot()
	if len(messages) == 0 || !strings.Contains(messages[0], "Blue") {
		t.Fatalf("messages = %v logs=%v", messages, logs)
	}
	ui.input <- "/quit"
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not quit")
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if st.EndReason != "quit" || st.Question != nil {
		t.Fatalf("end=%s question=%v", st.EndReason, st.Question)
	}
	var answered bool
	for _, ev := range evs {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answered = d.Text == "blue" && d.QuestionID == "q1"
		}
	}
	if !answered {
		t.Fatal("numbered option was not mapped to its text")
	}
}

// fileContains reports whether the session log on disk has the text.
func fileContains(sessionPath, needle string) bool {
	evs, err := store.Read(sessionPath)
	if err != nil {
		return false
	}
	for _, ev := range evs {
		if strings.Contains(ev.Type, needle) {
			return true
		}
	}
	return false
}

func TestWakeDuringYieldingCallIsNotLost(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	var sessionPath string
	var mu sync.Mutex
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "Task t1 completed"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"saw the result"}`)}}
			case strings.Contains(all, "Task t1 started"):
				// Yield "waiting for t1", but only deliver the reply once t1's
				// result is already on disk, so the yield is stale.
				mu.Lock()
				sp := sessionPath
				mu.Unlock()
				for i := 0; i < 400 && !fileContains(sp, "task.end"); i++ {
					time.Sleep(10 * time.Millisecond)
				}
				return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for t1"}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("delegate", `{"title":"w","description":"do w"}`)}}
			}
		case "task":
			return reply{calls: []event.ToolCall{tc("complete_task", `{"status":"completed","summary":"w done"}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"ok"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	sessionPath = rt.sess.Path
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d (the stale yield ended the session early); logs %v", code, ui.logs)
	}
	if s.count("orch") != 3 {
		t.Fatalf("orchestrator calls = %d, want 3 (delegate, stale yield, real yield)", s.count("orch"))
	}
}

func TestOneShotScheduleFiresOnce(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "fired at"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"timer fired"}`)}}
			case strings.Contains(all, "schedule s1 created"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for the timer"}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"in 5s","note":"check back"}`)}}
			}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"ok"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	// Speed the timer up: the harness arms from the stored Next, so rewrite it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	code := rt.Run(ctx)
	if code != 0 {
		t.Fatalf("exit %d after %s; logs %v", code, time.Since(start), ui.logs)
	}
	evs, _ := store.Read(rt.sess.Path)
	fires := 0
	for _, ev := range evs {
		if ev.Type == event.ScheduleFire {
			fires++
		}
	}
	if fires != 1 {
		t.Fatalf("one-shot schedule fired %d times", fires)
	}
	st := state.Replay(evs)
	if len(st.ActiveSchedules()) != 0 {
		t.Fatal("one-shot schedule still active after firing")
	}
}

func TestFastCommandsDoNotNotify(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			n := strings.Count(all, "exited with code 0")
			if n < 3 {
				return reply{calls: []event.ToolCall{tc("bash", `{"command":"echo hi"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"ran commands"}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"ok"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d logs %v", code, ui.logs)
	}
	evs, _ := store.Read(rt.sess.Path)
	exits := 0
	for _, ev := range evs {
		if ev.Type == event.ProcExit {
			exits++
			var d event.ProcExitData
			_ = ev.Decode(&d)
			if d.Notify {
				t.Fatalf("fast command %s was reported as a notification although its result was delivered inline", d.Handle)
			}
		}
	}
	if exits != 3 {
		t.Fatalf("expected 3 process exits, got %d", exits)
	}
	if s.count("orch") != 4 {
		t.Fatalf("orchestrator calls = %d, want 4 (no spurious wakes)", s.count("orch"))
	}
}

func TestResumeRepairsTornLog(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	block := make(chan struct{})
	var phase sync.Map
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			if strings.Contains(all, "created done.txt") {
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"done"}`)}}
			}
			if _, resumed := phase.Load("resumed"); !resumed {
				return reply{block: block}
			}
			return reply{calls: []event.ToolCall{tc("write_file", `{"path":"done.txt","content":"ok"}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"done"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "go"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 200 && s.count("orch") == 0; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	if code := rt.Run(ctx); code != 130 {
		t.Fatalf("exit %d", code)
	}
	// Simulate a crash mid-write: a partial JSON line at the end of the file.
	files, _ := store.ReadOnly(rt.sess.Path).Files()
	f, _ := os.OpenFile(filepath.Join(rt.sess.Path, files[len(files)-1]), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":999,"type":"user.message","actor":"user","data":{"text":"trunc`)
	f.Close()

	phase.Store("resumed", true)
	close(block)
	ui := &fakeUI{}
	rt2, err := Resume(testConfig(s.srv.URL), Options{Project: project}, ui, rt.sess.Path)
	if err != nil {
		t.Fatalf("resume over a torn log failed: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if code := rt2.Run(ctx2); code != 0 {
		t.Fatalf("resumed exit %d logs %v", code, ui.logs)
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	for _, ev := range evs {
		if ev.Seq != last+1 {
			t.Fatalf("sequence gap after repair: %d after %d", ev.Seq, last)
		}
		last = ev.Seq
	}
	if _, err := os.Stat(filepath.Join(rt.sess.Path, files[len(files)-1]+".torn")); err != nil {
		t.Fatal("torn bytes were not preserved")
	}
	if _, err := os.Stat(filepath.Join(project, "done.txt")); err != nil {
		t.Fatal("work did not finish after the repair")
	}
}

func TestTwoSessionsInOneProject(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			name := "a"
			if strings.Contains(all, "write b") {
				name = "b"
			}
			if !strings.Contains(all, "created "+name+".txt") {
				return reply{calls: []event.ToolCall{tc("write_file", fmt.Sprintf(`{"path":"%s.txt","content":"%s"}`, name, name))}}
			}
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"written"}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"ok"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	cfg := testConfig(s.srv.URL)
	rtA, err := New(cfg, Options{Project: project, Prompt: "write a"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	rtB, err := New(cfg, Options{Project: project, Prompt: "write b"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	if rtA.SessionID() == rtB.SessionID() {
		t.Fatal("two sessions got the same id")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	codes := make(chan int, 2)
	go func() { codes <- rtA.Run(ctx) }()
	go func() { codes <- rtB.Run(ctx) }()
	if a, b := <-codes, <-codes; a != 0 || b != 0 {
		t.Fatalf("exit codes %d %d", a, b)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(project, n+".txt")); err != nil {
			t.Fatalf("%s.txt missing", n)
		}
	}
	infos, _ := store.List(store.Root(project))
	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, found %d", len(infos))
	}
	for _, info := range infos {
		evs, err := store.Read(info.Path)
		if err != nil {
			t.Fatal(err)
		}
		if st := state.Replay(evs); st.EndReason != "done" {
			t.Fatalf("session %s ended %s", info.ID, st.EndReason)
		}
	}
}

func TestCleanNarrationStripsLeakedTokens(t *testing.T) {
	in := "Done. The file is in place.</｜DSML｜parameter>"
	if got := cleanNarration(in); got != "Done. The file is in place." {
		t.Fatalf("got %q", got)
	}
	if got := cleanNarration("ok<|im_end|>"); got != "ok" {
		t.Fatalf("got %q", got)
	}
	if got := cleanNarration("a < b and b > c"); got != "a < b and b > c" {
		t.Fatalf("ordinary text altered: %q", got)
	}
}
