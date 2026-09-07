package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// fakePhone is a minimal Finalechat: one thread, messages, questions, and
// the long-poll shape the runtime relies on.
type fakePhone struct {
	srv       *httptest.Server
	mu        sync.Mutex
	messages  []map[string]any
	questions map[string]map[string]any
	posts     []map[string]any // bodies of POST …/messages
	asks      []map[string]any // bodies of POST …/questions
	cancelled []string
	seq       int
	remote    bool
	cond      *sync.Cond
}

func newFakePhone(remote bool) *fakePhone {
	f := &fakePhone{questions: map[string]map[string]any{}, remote: remote}
	f.cond = sync.NewCond(&f.mu)
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakePhone) nextID() string { f.seq++; return fmt.Sprintf("id-%04d", f.seq) }

func (f *fakePhone) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer fc_test" {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := r.URL.Path
	switch {
	case path == "/api/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": "token", "base_url": f.srv.URL, "push_enabled": true, "version": "test", "user": map[string]any{"display_name": "Tester", "email": "t@x", "settings": map[string]any{"notify_all_messages": false, "remote_mode": f.remote}}})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodPost:
		f.mu.Lock()
		sender, _ := body["sender"].(string)
		if sender == "" {
			sender = "agent"
		}
		m := map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": sender, "body": body["body"], "format": "markdown", "importance": body["importance"], "meta": body["meta"], "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		if m["importance"] == nil {
			m["importance"] = "normal"
		}
		f.messages = append(f.messages, m)
		f.posts = append(f.posts, body)
		f.cond.Broadcast()
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": m, "thread": map[string]any{"id": "t1", "external_id": "eagent:x", "title": body["title"], "agent": body["agent"]}})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodGet:
		after := r.URL.Query().Get("after")
		sender := r.URL.Query().Get("sender")
		wait := 0
		fmt.Sscanf(r.URL.Query().Get("wait"), "%d", &wait)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		f.mu.Lock()
		var out []map[string]any
		for {
			out = nil
			seen := after == ""
			for _, m := range f.messages {
				if !seen {
					seen = m["id"] == after
					continue
				}
				if sender == "" || m["sender"] == sender {
					out = append(out, m)
				}
			}
			if len(out) > 0 || wait == 0 || time.Now().After(deadline) {
				break
			}
			// wake on new messages or every 200ms to re-check the deadline
			go func() { time.Sleep(200 * time.Millisecond); f.mu.Lock(); f.cond.Broadcast(); f.mu.Unlock() }()
			f.cond.Wait()
			if r.Context().Err() != nil {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		if out == nil {
			out = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out, "has_more": false, "thread_id": "t1", "timed_out": len(out) == 0})
	case strings.HasSuffix(path, "/questions") && r.Method == http.MethodPost:
		f.mu.Lock()
		q := map[string]any{"id": f.nextID(), "thread_id": "t1", "prompt": body["prompt"], "options": body["options"], "allow_freeform": true, "multi_select": false, "status": "pending", "answer": nil, "meta": body["meta"], "created_at": time.Now().UTC().Format(time.RFC3339Nano), "expires_at": nil}
		f.questions[q["id"].(string)] = q
		f.asks = append(f.asks, body)
		f.cond.Broadcast()
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q, "thread": map[string]any{"id": "t1"}})
	case strings.HasPrefix(path, "/api/v1/questions/") && strings.HasSuffix(path, "/cancel"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/questions/"), "/cancel")
		f.mu.Lock()
		f.cancelled = append(f.cancelled, id)
		if q := f.questions[id]; q != nil && q["status"] == "pending" {
			q["status"] = "cancelled"
		}
		f.cond.Broadcast()
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"question": f.questions[id]})
	case strings.HasPrefix(path, "/api/v1/questions/"):
		id := strings.TrimPrefix(path, "/api/v1/questions/")
		wait := 0
		fmt.Sscanf(r.URL.Query().Get("wait"), "%d", &wait)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		f.mu.Lock()
		q := f.questions[id]
		for q != nil && q["status"] == "pending" && wait > 0 && time.Now().Before(deadline) {
			go func() { time.Sleep(200 * time.Millisecond); f.mu.Lock(); f.cond.Broadcast(); f.mu.Unlock() }()
			f.cond.Wait()
			if r.Context().Err() != nil {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		if q == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no question"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q})
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"` + path + `"}}`))
	}
}

// userReply is the user typing on the phone.
func (f *fakePhone) userReply(text string) {
	f.mu.Lock()
	f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "body": text, "format": "text", "importance": "normal", "meta": map[string]any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	f.cond.Broadcast()
	f.mu.Unlock()
}

// answer is the user tapping an option: the question resolves and a user
// message records the choice, as the real service does.
func (f *fakePhone) answer(label, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.questions {
		if q["status"] != "pending" {
			continue
		}
		q["status"] = "answered"
		q["answer"] = map[string]any{"selected": []string{label}, "text": text}
		body := label
		if text != "" {
			body = label + " — " + text
		}
		f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "body": body, "format": "text", "importance": "normal", "meta": map[string]any{"kind": "answer", "question_id": q["id"]}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
		f.cond.Broadcast()
		return true
	}
	return false
}

func (f *fakePhone) pendingQuestions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, q := range f.questions {
		if q["status"] == "pending" {
			n++
		}
	}
	return n
}

func (f *fakePhone) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.posts) }

func (f *fakePhone) postsWhere(pred func(map[string]any) bool) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, p := range f.posts {
		if pred(p) {
			out = append(out, p)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A batch (-p) session with the phone mirror on: the narrator asks, the
// session waits instead of ending, the user taps an answer on the phone, the
// work finishes, and the final report is posted as important.
func TestPhoneAnswersQuestionInBatchSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "answered question") && strings.Contains(all, "blue"):
				return reply{calls: []event.ToolCall{tc("note", `{"text":"Painted it blue."}`), tc("yield", `{"done":true,"reason":"painted blue"}`)}}
			default:
				return reply{calls: []event.ToolCall{
					tc("note", `{"text":"I need the user to pick a colour: red or blue."}`),
					tc("yield", `{"done":false,"reason":"waiting for the colour decision"}`),
				}}
			}
		default: // narrator
			switch {
			case strings.Contains(all, "Blue it is"): // already said; a real narrator holds here too
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			case strings.Contains(all, "painted blue"):
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Blue it is; done."}`)}}
			case strings.Contains(all, "pick a colour") && !strings.Contains(all, "asked;") && !strings.Contains(all, "asked on"):
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(true)
	defer fp.srv.Close()
	cfg := testConfig(s.srv.URL)
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "paint it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	// The session must not end while the phone question is open.
	select {
	case code := <-done:
		_, _, logs := ui.snapshot()
		t.Fatalf("session ended (%d) while waiting on the phone; logs=%v", code, logs)
	case <-time.After(1500 * time.Millisecond):
	}
	if !fp.answer("blue", "") {
		t.Fatal("no pending question to answer")
	}
	select {
	case code := <-done:
		if code != 0 {
			_, _, logs := ui.snapshot()
			t.Fatalf("exit %d logs=%v", code, logs)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("session did not finish after the phone answer")
	}
	evs, _ := store.Read(rt.sess.Path)
	for _, ev := range evs {
		t.Logf("%d %-12s %s %s", ev.Seq, ev.Actor, ev.Type, clipTail(string(ev.Data), 90))
	}
	st := state.Replay(evs)
	if st.EndReason != "done" {
		t.Fatalf("end reason = %s", st.EndReason)
	}
	if st.Phone == nil || st.Phone.ThreadID != "t1" || !st.Phone.RemoteMode {
		t.Fatalf("phone thread not recorded: %+v", st.Phone)
	}
	var answer event.UserAnswerData
	var final event.NarratorMessageData
	for _, ev := range evs {
		switch ev.Type {
		case event.UserAnswer:
			_ = ev.Decode(&answer)
		case event.NarratorMessage:
			_ = ev.Decode(&final)
		}
	}
	if answer.Text != "blue" || answer.Source != "finalechat" || answer.QuestionID != "q1" {
		t.Fatalf("answer = %+v", answer)
	}
	if !final.Important {
		t.Fatalf("final report should be important: %+v", final)
	}
	// The phone saw: the prompt (as the user), the question, the final report as important, and the end note.
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "user" }); len(got) != 1 || got[0]["body"] != "paint it" || got[0]["title"] == "" {
		t.Fatalf("prompt mirror = %v", got)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["importance"] == "important" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "Blue it is") {
		t.Fatalf("important posts = %v", got)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "system" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "finished") {
		t.Fatalf("end note = %v", got)
	}
	if len(fp.asks) != 1 || fp.asks[0]["prompt"] != "Which colour?" {
		t.Fatalf("asks = %v", fp.asks)
	}
}

// An interactive session: a reply typed on the phone becomes a user message,
// terminal input is mirrored to the phone, and a terminal answer withdraws
// the phone question.
func TestPhoneReplyAndTerminalAnswerMirror(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			switch {
			case strings.Contains(all, "answered question"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"colour chosen"}`)}}
			case strings.Contains(all, "from the phone"):
				return reply{calls: []event.ToolCall{tc("note", `{"text":"Need a colour."}`), tc("yield", `{"done":false,"reason":"waiting for the colour"}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for instructions"}`)}}
			}
		default:
			switch {
			case strings.Contains(all, "colour chosen"):
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done.","important":true}`)}}
			case strings.Contains(all, "Need a colour") && !strings.Contains(all, "asked;") && !strings.Contains(all, "asked on"):
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	cfg := testConfig(s.srv.URL)
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: true, Prompt: "hello"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, "the thread to exist", func() bool { return fp.postCount() >= 1 })
	fp.userReply("this comes from the phone")
	waitFor(t, "the question", func() bool { return ui.askedCount() == 1 })
	if fp.pendingQuestions() != 1 {
		waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	}
	ui.input <- "1" // answered in the terminal
	waitFor(t, "the phone question to be withdrawn", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.cancelled) == 1 })
	waitFor(t, "the final message", func() bool { return ui.messageCount() == 1 })
	ui.input <- "/quit"
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("did not quit")
	}
	evs, _ := store.Read(rt.sess.Path)
	var fromPhone, mirroredAnswer bool
	for _, ev := range evs {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Source == "finalechat" && d.Text == "this comes from the phone" {
				fromPhone = true
			}
		}
	}
	for _, p := range fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "user" }) {
		if p["body"] == "red" {
			mirroredAnswer = true
		}
	}
	if !fromPhone {
		t.Fatal("phone reply was not recorded as a user message with source finalechat")
	}
	if !mirroredAnswer {
		t.Fatalf("terminal answer was not mirrored to the phone: %v", fp.posts)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["importance"] == "important" }); len(got) != 1 {
		t.Fatalf("important posts = %v", got)
	}
}

// With enabled: false nothing touches the phone, token or not.
func TestPhoneDisabledInConfig(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"nothing to do"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Nothing to do."}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	cfg := testConfig(s.srv.URL)
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	off := false
	cfg.Finalechat.Enabled = &off
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "hi"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if fp.postCount() != 0 {
		t.Fatalf("phone was contacted while disabled: %v", fp.posts)
	}
	evs, _ := store.Read(rt.sess.Path)
	if state.Replay(evs).Phone != nil {
		t.Fatal("phone thread recorded while disabled")
	}
}

// A session interrupted while a question is open re-asks it on the phone
// when resumed, and the phone answer then finishes the work.
func TestPhoneReasksPendingQuestionOnResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			if strings.Contains(all, "answered question") {
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"colour chosen"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("note", `{"text":"Pick a colour: red or blue."}`), tc("yield", `{"done":false,"reason":"waiting for the colour"}`)}}
		default:
			switch {
			case strings.Contains(all, "Done."):
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			case strings.Contains(all, "colour chosen"):
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done."}`)}}
			case strings.Contains(all, "Pick a colour") && !strings.Contains(all, "asked;") && !strings.Contains(all, "asked on"):
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(true)
	defer fp.srv.Close()
	cfg := testConfig(s.srv.URL)
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "paint it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the first phone question", func() bool { return fp.pendingQuestions() == 1 })
	cancel() // interrupted with the question open
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("did not stop")
	}
	// The phone question outlives the process here (in reality it may have expired).
	sessionPath := rt.sess.Path

	rt2, err := Resume(cfg, Options{Project: project, Interactive: false}, ui, sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel2()
	done2 := make(chan int)
	go func() { done2 <- rt2.Run(ctx2) }()
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) == 2 })
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	select {
	case code := <-done2:
		if code != 0 {
			_, _, logs := ui.snapshot()
			t.Fatalf("exit %d logs=%v", code, logs)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("resumed session did not finish")
	}
	evs, _ := store.Read(sessionPath)
	st := state.Replay(evs)
	if st.EndReason != "done" || st.Question != nil {
		t.Fatalf("end=%s question=%v", st.EndReason, st.Question)
	}
}
