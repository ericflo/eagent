package harness

// The dismissal tests against a real FinaleChat server, ported from the old
// phone_e2e_test.go. They skip unless EAGENT_E2E_FINALECHAT_URL names a
// local server with open sign-up (loopback only: the package's guard
// refuses anything else):
//
//	EAGENT_E2E_FINALECHAT_URL=http://127.0.0.1:18080 go test ./internal/harness -run RealFinaleChat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// phoneReal is the app's side of a real FinaleChat server: a signed-in
// browser session and an agent token for the same account.
type phoneReal struct {
	t       *testing.T
	base    string
	browser *http.Client
	token   string
}

func newPhoneReal(t *testing.T) *phoneReal {
	t.Helper()
	base := strings.TrimRight(os.Getenv("EAGENT_E2E_FINALECHAT_URL"), "/")
	if base == "" {
		t.Skip("set EAGENT_E2E_FINALECHAT_URL to a local FinaleChat server with open sign-up")
	}
	jar, _ := cookiejar.New(nil)
	p := &phoneReal{t: t, base: base, browser: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
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
func (p *phoneReal) call(asAgent bool, method, path string, body any, out any) int {
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

type phoneRealQuestion struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	Status   string `json:"status"`
	Answer   any    `json:"answer"`
}

func (p *phoneReal) questions(status string) []phoneRealQuestion {
	var out struct {
		Questions []phoneRealQuestion `json:"questions"`
	}
	p.call(false, "GET", "/api/v1/questions?status="+status, nil, &out)
	return out.Questions
}

func (p *phoneReal) userBodies(thread string) []string {
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

// start runs a session against the real server, the mirror switched on for
// this test alone.
func (p *phoneReal) start(t *testing.T, interactive bool, brain func(string, []map[string]any) reply) *phoneRig {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "on")
	t.Setenv("EAGENT_E2E_FC", p.token)
	g := phoneStart(t, nil, "paint it", interactive, brain, func(cfg *config.Config) {
		on := true
		cfg.Finalechat.Enabled = &on
		cfg.Finalechat.BaseURL = p.base
		cfg.Finalechat.TokenEnv = "EAGENT_E2E_FC"
		cfg.Finalechat.Artifacts = false
	})
	return g
}

// Old TestPhoneDismissAgainstRealFinaleChat: the card's Dismiss, against a
// real server, records a dismissal and nothing else: no answer is made up,
// the model is not called, and no message is written to the thread in the
// user's name. What the user sends next is an ordinary message.
func TestPhoneDismissAgainstRealFinaleChat(t *testing.T) {
	phone := newPhoneReal(t)
	g := phone.start(t, false, phoneColourBrain(true))
	waitFor(t, "the question on the real server", func() bool { return len(phone.questions("pending")) == 1 })
	q := phone.questions("pending")[0]
	waitFor(t, "the loop to settle", func() bool {
		busy := true
		g.rt.sync(func() { busy = g.rt.turns.busy })
		return !busy
	})
	before := g.model.count(testModel)
	if code := phone.call(false, "POST", "/api/v1/questions/"+q.ID+"/dismiss", map[string]any{}, nil); code != 200 {
		t.Fatalf("dismiss: %d", code)
	}
	g.running(3*time.Second, "on the dismissal")
	if got := g.model.count(testModel); got != before {
		t.Fatalf("the dismissal called the model: %d calls before, %d after", before, got)
	}
	if bodies := phone.userBodies(q.ThreadID); len(bodies) != 1 || bodies[0] != "paint it" {
		t.Fatalf("after the dismissal the thread holds these user messages, want only the prompt: %q", bodies)
	}
	if code := phone.call(false, "POST", "/api/v1/threads/"+q.ThreadID+"/messages", map[string]any{"body": "actually, make it green"}, nil); code != 201 {
		t.Fatalf("message: %d", code)
	}
	if code := g.exit(40 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	evs := g.log()
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

// Old TestSlashDismissAgainstRealFinaleChat: /dismiss in the terminal,
// against a real server, closes the card as dismissed (not withdrawn) with
// the agent's token.
func TestSlashDismissAgainstRealFinaleChat(t *testing.T) {
	phone := newPhoneReal(t)
	g := phone.start(t, true, phoneColourBrain(false))
	waitFor(t, "the question on the real server", func() bool { return len(phone.questions("pending")) == 1 })
	q := phone.questions("pending")[0]
	g.ui.input <- "/dismiss"
	waitFor(t, "the card to close as dismissed", func() bool {
		got := phone.questions("dismissed")
		return len(got) == 1 && got[0].ID == q.ID
	})
	if n := len(phone.questions("cancelled")); n != 0 {
		t.Fatal("the card was withdrawn instead of dismissed")
	}
	g.ui.input <- "/quit"
	g.exit(20 * time.Second)
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatal("an answer was recorded for a dismissed question")
		}
	}
}
