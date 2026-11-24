package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// realPhone is the app's side of a real FinaleChat server (a local one with
// open sign-up): a signed-in browser session and an agent token for the
// same account.
type realPhone struct {
	t       *testing.T
	base    string
	browser *http.Client
	token   string
}

func newRealPhone(t *testing.T) *realPhone {
	t.Helper()
	base := strings.TrimRight(os.Getenv("EAGENT_E2E_FINALECHAT_URL"), "/")
	if base == "" {
		t.Skip("set EAGENT_E2E_FINALECHAT_URL to a local FinaleChat server with open sign-up")
	}
	jar, _ := cookiejar.New(nil)
	p := &realPhone{t: t, base: base, browser: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	if s := p.call(false, "POST", "/api/v1/auth/register", map[string]any{"email": email, "password": "correct-horse-battery"}, nil); s != 201 {
		t.Fatalf("register: %d", s)
	}
	var token struct {
		Secret string `json:"secret"`
	}
	if s := p.call(false, "POST", "/api/v1/tokens", map[string]any{"name": "e2e"}, &token); s != 201 {
		t.Fatalf("token: %d", s)
	}
	p.token = token.Secret
	return p
}

// call makes a request as the app (the signed-in browser) or, with asAgent,
// with the agent's token.
func (p *realPhone) call(asAgent bool, method, path string, body any, out any) int {
	p.t.Helper()
	raw := []byte(nil)
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, p.base+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	client := p.browser
	if asAgent {
		req.Header.Set("Authorization", "Bearer "+p.token)
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	res, err := client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

type realQuestion struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	Status   string `json:"status"`
	Answer   any    `json:"answer"`
}

func (p *realPhone) questions(status string) []realQuestion {
	var out struct {
		Questions []realQuestion `json:"questions"`
	}
	p.call(false, "GET", "/api/v1/questions?status="+status, nil, &out)
	return out.Questions
}

func (p *realPhone) userBodies(thread string) []string {
	var out struct {
		Messages []struct {
			Sender string `json:"sender"`
			Body   string `json:"body"`
		} `json:"messages"`
	}
	p.call(false, "GET", "/api/v1/threads/"+thread+"/messages?limit=200", nil, &out)
	var bodies []string
	for _, m := range out.Messages {
		if m.Sender == "user" {
			bodies = append(bodies, m.Body)
		}
	}
	return bodies
}

func (p *realPhone) config(t *testing.T, project, modelURL string) {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "on")
	t.Setenv("EAGENT_TEST_FC", p.token)
	raw, _ := json.Marshal(map[string]any{"finalechat": map[string]any{"artifacts": false}})
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func colourBrain(model string, msgs []map[string]any) reply {
	all := allText(msgs)
	switch model {
	case "orch":
		if strings.Contains(all, "make it green") {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"painted green"}`)}}
		}
		return reply{calls: []event.ToolCall{
			tc("note", `{"text":"I need the user to pick a colour: red or blue."}`),
			tc("yield", `{"done":false,"reason":"waiting for the colour decision"}`),
		}}
	default:
		switch {
		case strings.Contains(all, "Painted it green"):
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		case strings.Contains(all, "painted green"):
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Painted it green."}`)}}
		case strings.Contains(all, "pick a colour") && !strings.Contains(all, "asked;") && !strings.Contains(all, "asked on"):
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
		default:
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
}

// The card's Dismiss, against a real FinaleChat server: the app dismisses
// the question the way the web app does, and the session records a
// dismissal and nothing else. No answer is made up, the orchestrator is not
// woken, and no message is written to the thread in the user's name. What
// the user sends next is an ordinary message. Fakes cannot show what the
// server does with a dismissal, so this runs against the real thing:
//
//	EAGENT_E2E_FINALECHAT_URL=http://127.0.0.1:18080 go test ./internal/harness -run RealFinaleChat
func TestPhoneDismissAgainstRealFinaleChat(t *testing.T) {
	phone := newRealPhone(t)
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	s := newScripted(colourBrain)
	defer s.srv.Close()
	phone.config(t, project, s.srv.URL)
	cfg := testConfig(s.srv.URL)
	on := true
	cfg.Finalechat.Enabled = &on
	cfg.Finalechat.BaseURL = phone.base
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	cfg.Finalechat.Artifacts = false
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "paint it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the question on the real server", func() bool { return len(phone.questions("pending")) == 1 })
	q := phone.questions("pending")[0]
	orchBefore := s.count("orch")
	if code := phone.call(false, "POST", "/api/v1/questions/"+q.ID+"/dismiss", map[string]any{}, nil); code != 200 {
		t.Fatalf("dismiss: %d", code)
	}
	select {
	case code := <-done:
		t.Fatalf("session ended (%d) on the dismissal", code)
	case <-time.After(3 * time.Second):
	}
	if got := s.count("orch"); got != orchBefore {
		t.Fatalf("the dismissal woke the orchestrator: %d calls before, %d after", orchBefore, got)
	}
	if bodies := phone.userBodies(q.ThreadID); len(bodies) != 1 || bodies[0] != "paint it" {
		t.Fatalf("after the dismissal the thread holds these user messages, want only the prompt: %q", bodies)
	}
	if code := phone.call(false, "POST", "/api/v1/threads/"+q.ThreadID+"/messages", map[string]any{"body": "actually, make it green"}, nil); code != 201 {
		t.Fatalf("message: %d", code)
	}
	select {
	case code := <-done:
		if code != 0 {
			_, _, logs := ui.snapshot()
			t.Fatalf("exit %d logs=%v", code, logs)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("session did not finish after the user's message")
	}
	evs, _ := store.Read(rt.sess.Path)
	if st := state.Replay(evs); st.EndReason != "done" {
		t.Fatalf("end reason = %s", st.EndReason)
	}
	dismissals, messages := 0, 0
	for _, ev := range evs {
		switch ev.Type {
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			t.Fatalf("an answer was recorded for a dismissed question: %+v", d)
		case event.UserDismiss:
			var d event.UserDismissData
			_ = ev.Decode(&d)
			if d.QuestionID != "q1" || d.Source != "finalechat" {
				t.Fatalf("dismissal = %+v", d)
			}
			dismissals++
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if strings.Contains(d.Text, "green") && d.Source == "finalechat" {
				messages++
			}
		}
	}
	if dismissals != 1 || messages != 1 {
		t.Fatalf("dismissals = %d, messages after = %d", dismissals, messages)
	}
	if got := phone.questions("dismissed"); len(got) != 1 || got[0].ID != q.ID || got[0].Answer != nil {
		t.Fatalf("the server's record of the question = %+v", got)
	}
	if n := len(phone.questions("pending")); n != 0 {
		t.Fatalf("%d question(s) pending at the end", n)
	}
}

// /dismiss in the terminal, against a real server: the card closes as
// dismissed (not withdrawn) with the agent's token.
func TestSlashDismissAgainstRealFinaleChat(t *testing.T) {
	phone := newRealPhone(t)
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	s := newScripted(colourBrain)
	defer s.srv.Close()
	phone.config(t, project, s.srv.URL)
	cfg := testConfig(s.srv.URL)
	on := true
	cfg.Finalechat.Enabled = &on
	cfg.Finalechat.BaseURL = phone.base
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	cfg.Finalechat.Artifacts = false
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: true, Prompt: "paint it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the question on the real server", func() bool { return len(phone.questions("pending")) == 1 })
	q := phone.questions("pending")[0]
	ui.input <- "/dismiss"
	waitFor(t, "the card to close as dismissed", func() bool {
		got := phone.questions("dismissed")
		return len(got) == 1 && got[0].ID == q.ID
	})
	if n := len(phone.questions("cancelled")); n != 0 {
		t.Fatalf("the card was withdrawn instead of dismissed")
	}
	ui.input <- "/quit"
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("session did not quit")
	}
	evs, _ := store.Read(rt.sess.Path)
	for _, ev := range evs {
		if ev.Type == event.UserAnswer {
			t.Fatal("an answer was recorded for a dismissed question")
		}
	}
}
