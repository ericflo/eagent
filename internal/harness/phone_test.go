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
	files     map[string]fakeFile // attachment id -> bytes served to the agent
	uploaded  []fakeFile          // files the agent posted
}

type fakeFile struct {
	name, contentType string
	data              []byte
}

func newFakePhone(remote bool) *fakePhone {
	f := &fakePhone{questions: map[string]map[string]any{}, remote: remote, files: map[string]fakeFile{}}
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
	path := r.URL.Path
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(400)
			return
		}
		body = map[string]any{}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				body[k] = v[0]
			}
		}
		var atts []map[string]any
		for _, hs := range r.MultipartForm.File {
			for _, h := range hs {
				fh, _ := h.Open()
				data, _ := io.ReadAll(fh)
				fh.Close()
				f.mu.Lock()
				f.uploaded = append(f.uploaded, fakeFile{name: h.Filename, contentType: h.Header.Get("Content-Type"), data: data})
				f.mu.Unlock()
				atts = append(atts, map[string]any{"id": "up-" + h.Filename, "kind": "file", "content_type": h.Header.Get("Content-Type"), "filename": h.Filename, "size": len(data), "url": "/api/v1/attachments/up-" + h.Filename})
			}
		}
		body["_attachments"] = atts
	} else if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/attachments/"):
		id := strings.TrimPrefix(path, "/api/v1/attachments/")
		f.mu.Lock()
		file, ok := f.files[id]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no attachment"}}`))
			return
		}
		w.Header().Set("Content-Type", file.contentType)
		w.Header().Set("Content-Disposition", `inline; filename="`+file.name+`"`)
		_, _ = w.Write(file.data)
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
		if atts, ok := body["_attachments"].([]map[string]any); ok && len(atts) > 0 {
			m["attachments"] = atts
			delete(body, "_attachments")
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
func (f *fakePhone) userReply(text string) { f.userReplyWith(text, nil) }

// userReplyWith is the user sending words and/or files from the phone.
func (f *fakePhone) userReplyWith(text string, atts []map[string]any) {
	f.mu.Lock()
	m := map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "body": text, "format": "text", "importance": "normal", "meta": map[string]any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if len(atts) > 0 {
		m["attachments"] = atts
	}
	f.messages = append(f.messages, m)
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (f *fakePhone) uploads() []fakeFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeFile{}, f.uploaded...)
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

// fakePhoneConfig points the mirror at the fake service and lifts the
// package-wide kill switch for this test only.
func fakePhoneConfig(t *testing.T, modelURL string, fp *fakePhone) config.Config {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "on")
	cfg := testConfig(modelURL)
	on := true
	cfg.Finalechat.Enabled = &on
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	return cfg
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
	cfg := fakePhoneConfig(t, s.srv.URL, fp)
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
	cfg := fakePhoneConfig(t, s.srv.URL, fp)
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
	cfg := fakePhoneConfig(t, s.srv.URL, fp)
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
	cfg := fakePhoneConfig(t, s.srv.URL, fp)
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

// Files travel both ways: a screenshot the user sends from the phone is
// saved under the session and handed to the orchestrator with its path (and
// as an image), and a file the narrator attaches goes up as multipart.
func TestPhoneAttachmentsBothWays(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	// A tiny valid PNG (1x1) the fake phone will serve as the user's screenshot.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.WriteFile(filepath.Join(project, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	var sawImagePart, sawAttachmentNote bool
	var mu sync.Mutex
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			// Look for the picture the user sent: as a note with the path and as an image part.
			for _, m := range msgs {
				if m["role"] != "user" {
					continue
				}
				if parts, ok := m["content"].([]any); ok {
					for _, p := range parts {
						if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
							mu.Lock()
							sawImagePart = true
							mu.Unlock()
						}
					}
				}
			}
			if strings.Contains(all, "The user attached a file") && strings.Contains(all, "in-") {
				mu.Lock()
				sawAttachmentNote = true
				mu.Unlock()
				return reply{calls: []event.ToolCall{tc("note", `{"text":"Got the screenshot; the result is at shot.png"}`), tc("yield", `{"done":true,"reason":"done with the picture"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for the picture"}`)}}
		default:
			switch {
			case strings.Contains(all, "Here is the shot"):
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			case strings.Contains(all, "done with the picture"):
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Here is the shot.","attachments":["shot.png"]}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
			}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	fp.files["att-1"] = fakeFile{name: "IMG_1.png", contentType: "image/png", data: png}
	cfg := fakePhoneConfig(t, s.srv.URL, fp)
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "look at what I send you"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	// The user sends a screenshot from the phone with no words.
	fp.userReplyWith("", []map[string]any{{"id": "att-1", "kind": "image", "content_type": "image/png", "filename": "IMG_1.png", "size": len(png), "width": 1, "height": 1, "url": "/api/v1/attachments/att-1"}})
	select {
	case code := <-done:
		if code != 0 {
			_, _, logs := ui.snapshot()
			evs, _ := store.Read(rt.sess.Path)
			for _, ev := range evs {
				t.Logf("%d %-12s %s %s", ev.Seq, ev.Actor, ev.Type, clipTail(string(ev.Data), 160))
			}
			t.Fatalf("exit %d logs=%v", code, logs)
		}
	case <-time.After(30 * time.Second):
		_, _, logs := ui.snapshot()
		t.Fatalf("did not finish; logs=%v", logs)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawAttachmentNote {
		t.Fatal("the orchestrator was not told about the attached file")
	}
	if !sawImagePart {
		t.Fatal("the orchestrator did not receive the image as an image part")
	}
	evs, _ := store.Read(rt.sess.Path)
	var in, out []event.Attachment
	for _, ev := range evs {
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Source == "finalechat" {
				in = d.Attachments
			}
		case event.NarratorMessage:
			var d event.NarratorMessageData
			_ = ev.Decode(&d)
			out = d.Attachments
		}
	}
	if len(in) != 1 || in[0].ContentType != "image/png" || in[0].Size != int64(len(png)) || !strings.HasPrefix(filepath.Base(in[0].Path), "in-att-1-") {
		t.Fatalf("inbound attachment = %+v", in)
	}
	if _, err := os.Stat(in[0].Path); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	if len(out) != 1 || out[0].Name != "shot.png" || out[0].Kind != "image" || !strings.HasPrefix(out[0].Path, rt.sess.Path) {
		t.Fatalf("outbound attachment = %+v", out)
	}
	ups := fp.uploads()
	if len(ups) != 1 || ups[0].name != "shot.png" || ups[0].contentType != "image/png" || len(ups[0].data) != len(png) {
		t.Fatalf("uploads = %+v", ups)
	}
}

// view_image shows a picture to the model right after the tool result, and
// a file named imperfectly is still found in the project.
func TestViewImageShowsPictureAndFindsFilesByName(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.MkdirAll(filepath.Join(project, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "out", "render.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sawImage bool
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model == "orch" {
			for _, m := range msgs {
				if parts, ok := m["content"].([]any); ok && m["role"] == "user" {
					for _, p := range parts {
						if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
							mu.Lock()
							sawImage = true
							mu.Unlock()
						}
					}
				}
			}
			if strings.Contains(all, "follows this result as an image") {
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"looked"}`)}}
			}
			// A wrong directory, the right name: the harness should find it.
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"/tmp/wrong/place/render.png"}`)}}
		}
		if strings.Contains(all, "looked") && !strings.Contains(all, "Seen.") {
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Seen."}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Interactive: false, Prompt: "look at the render"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d", code)
	}
	evs, _ := store.Read(rt.sess.Path)
	var found bool
	for _, ev := range evs {
		if ev.Type == event.ToolResult {
			var d event.ToolResultData
			_ = ev.Decode(&d)
			t.Logf("tool.result %s images=%+v err=%v out=%s", d.Name, d.Images, d.IsError, clipTail(d.Output, 120))
			if d.Name == "view_image" && len(d.Images) == 1 && d.Images[0].Width == 1 && strings.HasPrefix(d.Images[0].Path, rt.sess.Path) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("view_image result did not record the image with its size under the session")
	}
	view := state.Replay(evs).OrchestratorView()
	for _, m := range view {
		t.Logf("view %s images=%d text=%s", m.Role, len(m.Images), clipTail(m.Text, 80))
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawImage {
		t.Fatal("the model never received the image part")
	}
}

// Workers and the narrator fall back like the orchestrator: a dead primary
// route (bad key here) is abandoned for the next one, once, and the switch
// is recorded so the log says which model really answered.
func TestWorkerAndNarratorRoutesFallBack(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_DEAD_KEY", "dead")
	project := t.TempDir()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer dead.Close()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			if strings.Contains(all, "t1 completed") || strings.Contains(all, "built it") {
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"built"}`)}}
			}
			if countRole(msgs, "assistant") == 0 {
				return reply{calls: []event.ToolCall{tc("delegate", `{"title":"Build","description":"make hello.txt"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("wait", `{}`)}}
		case "task":
			return reply{calls: []event.ToolCall{tc("complete_task", `{"status":"completed","summary":"built it"}`)}}
		default:
			if strings.Contains(all, "built") && !strings.Contains(all, "Done.") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	cfg := testConfig(s.srv.URL)
	// Task worker and narrator: primary at the dead server, fallback at the scripted one.
	for _, a := range []*config.Actor{&cfg.Task, &cfg.Narrator} {
		fb := *a
		a.BaseURL, a.APIKeyEnv = dead.URL, "EAGENT_DEAD_KEY"
		a.Fallback = &fb
	}
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "go"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		_, _, logs := ui.snapshot()
		t.Fatalf("exit %d logs=%v", code, logs)
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if st.EndReason != "done" || st.Tasks["t1"] == nil || st.Tasks["t1"].Status != "completed" {
		t.Fatalf("end=%s task=%+v", st.EndReason, st.Tasks["t1"])
	}
	routed := map[string]int{}
	for _, ev := range evs {
		if ev.Type == event.Route {
			var d event.RouteData
			_ = ev.Decode(&d)
			routed[d.Actor]++
			if d.BaseURL != s.srv.URL {
				t.Fatalf("route event points at %s", d.BaseURL)
			}
		}
	}
	if routed[event.ActorTask] != 1 || routed[event.ActorNarrator] != 1 || routed[event.ActorOrchestrator] != 0 {
		t.Fatalf("route events = %v", routed)
	}
	if st.Hosts[event.ActorTask] != s.srv.URL || st.Hosts[event.ActorNarrator] != s.srv.URL {
		t.Fatalf("hosts after fallback = %v", st.Hosts)
	}
	if ui.messageCount() != 1 {
		t.Fatalf("narrator messages = %d", ui.messageCount())
	}
}

// A second read of an unchanged file, or a second look at an unchanged
// image, comes back as a note; a changed file or force=true reads again.
func TestRepeatReadsAreShortCircuited(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	path := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.WriteFile(filepath.Join(project, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	step := 0
	var mu sync.Mutex
	brain := func(model string, msgs []map[string]any) reply {
		if model != "orch" {
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		mu.Lock()
		defer mu.Unlock()
		step++
		switch step {
		case 1:
			return reply{calls: []event.ToolCall{tc("read_file", `{"path":"notes.txt"}`)}}
		case 2:
			return reply{calls: []event.ToolCall{tc("read_file", `{"path":"notes.txt"}`)}} // unchanged
		case 3:
			_ = os.WriteFile(path, []byte("second\n"), 0o644)
			// force a different mtime even on coarse filesystems
			later := time.Now().Add(2 * time.Second)
			_ = os.Chtimes(path, later, later)
			return reply{calls: []event.ToolCall{tc("read_file", `{"path":"notes.txt"}`)}} // changed
		case 4:
			return reply{calls: []event.ToolCall{tc("read_file", `{"path":"notes.txt","force":true}`)}} // forced
		case 5:
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		case 6:
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}} // unchanged
		default:
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"done"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Interactive: false, Prompt: "read"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d", code)
	}
	evs, _ := store.Read(rt.sess.Path)
	var reads, views []event.ToolResultData
	for _, ev := range evs {
		if ev.Type == event.ToolResult {
			var d event.ToolResultData
			_ = ev.Decode(&d)
			switch d.Name {
			case "read_file":
				reads = append(reads, d)
			case "view_image":
				views = append(views, d)
			}
		}
	}
	if len(reads) != 4 {
		t.Fatalf("reads = %d", len(reads))
	}
	if !strings.Contains(reads[0].Output, "first") || !strings.Contains(reads[1].Output, "unchanged since you read it") || !strings.Contains(reads[2].Output, "second") || !strings.Contains(reads[3].Output, "second") {
		t.Fatalf("read outputs = %q %q %q %q", reads[0].Output, reads[1].Output, reads[2].Output, reads[3].Output)
	}
	if len(views) != 2 || len(views[0].Images) != 1 || len(views[1].Images) != 0 || !strings.Contains(views[1].Output, "unchanged since you looked at it") {
		t.Fatalf("views = %+v", views)
	}
}

// An orchestrator that keeps editing files itself gets told to delegate.
func TestOrchestratorEditNudge(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	n := 0
	var mu sync.Mutex
	brain := func(model string, msgs []map[string]any) reply {
		if model != "orch" {
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		mu.Lock()
		defer mu.Unlock()
		n++
		if n <= orchestratorEditNudge {
			if n%2 == 0 { // every other edit goes through the shell; it must count the same
				return reply{calls: []event.ToolCall{tc("bash", fmt.Sprintf(`{"command":"cat > f%d.txt <<'EOF'\nx\nEOF"}`, n))}}
			}
			return reply{calls: []event.ToolCall{tc("write_file", fmt.Sprintf(`{"path":"f%d.txt","content":"x"}`, n))}}
		}
		return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"done"}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Interactive: false, Prompt: "write"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d", code)
	}
	evs, _ := store.Read(rt.sess.Path)
	var steers []string
	for _, ev := range evs {
		if ev.Type == event.Steer && ev.Actor == event.ActorOrchestrator {
			var d event.SteerData
			_ = ev.Decode(&d)
			steers = append(steers, d.Text)
		}
	}
	if len(steers) < orchestratorEditNudge+1 {
		t.Fatalf("steers = %d", len(steers))
	}
	if strings.Contains(steers[1], "Delegate what remains") {
		t.Fatal("nudged too early")
	}
	if !strings.Contains(steers[len(steers)-1], "Delegate what remains") {
		t.Fatalf("no nudge in the last steer: %s", steers[len(steers)-1])
	}
}

// The steer keeps the narrator on a cadence: an orientation message early,
// then a progress line whenever the user has waited past the limit while
// work continues, and nothing extra when they heard from it recently.
func TestNarratorSteerCadence(t *testing.T) {
	st := state.New()
	st.Started = time.Now().Add(-2 * time.Minute)
	// A fresh session with no narrator message yet, two minutes in.
	s := steerNarrator(st, time.Now(), wakePeriodic, false, true, "", false, 2*time.Minute, 3*time.Minute, nil)
	if !strings.Contains(s, "heard nothing from you") || !strings.Contains(s, "what you understood the job to be") {
		t.Fatalf("no orientation prompt: %s", s)
	}
	// Later, with a previous message four minutes old and a task running.
	st.Tasks["t1"] = &state.Task{ID: "t1", Status: "running"}
	st.TaskOrder = []string{"t1"}
	s = steerNarrator(st, time.Now(), wakePeriodic, false, true, "Building the parser.", false, 4*time.Minute, 3*time.Minute, nil)
	if !strings.Contains(s, "last heard from you 4m ago") || !strings.Contains(s, "send a short progress line now") {
		t.Fatalf("no progress prompt: %s", s)
	}
	// Recently spoken: just the fact, no push.
	s = steerNarrator(st, time.Now(), wakePeriodic, false, true, "Building the parser.", false, 40*time.Second, 3*time.Minute, nil)
	if !strings.Contains(s, "last heard from you 40s ago") || strings.Contains(s, "progress line now") {
		t.Fatalf("unexpected push: %s", s)
	}
	// Cadence disabled.
	s = steerNarrator(st, time.Now(), wakePeriodic, false, true, "Building the parser.", false, 10*time.Minute, 0, nil)
	if strings.Contains(s, "progress line now") {
		t.Fatalf("cadence should be off: %s", s)
	}
	// A user message asks for an acknowledgement at once.
	s = steerNarrator(st, time.Now(), wakeUser, true, false, "", true, 5*time.Second, 3*time.Minute, nil)
	if !strings.Contains(s, "Reply now") || strings.Contains(s, "heard nothing from you") {
		t.Fatalf("acknowledgement steer: %s", s)
	}
	// What is in flight is named, with the ask to say which one.
	s = steerNarrator(st, time.Now(), wakePeriodic, false, true, "Building.", false, 4*time.Minute, 3*time.Minute, []string{"`npm test` (p3, task t1) has been running for 3m10s"})
	if !strings.Contains(s, "In flight right now: `npm test`") || !strings.Contains(s, "which one, by name") {
		t.Fatalf("inflight steer: %s", s)
	}
}
