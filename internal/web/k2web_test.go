package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// k2Page runs script after the page's send() in node, with what send()
// reads stubbed; script ends the process with an error when a check fails.
func k2Page(t *testing.T, script string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	send := regexp.MustCompile(`(?s)async function send\(\) \{.*?\n\}\n`).FindString(string(src))
	if send == "" {
		t.Fatal("app.js has no send()")
	}
	if out, err := exec.Command("node", "-e", send+script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
}

// k2Stubs stand in for the page around send(): the box holds box, and the
// session's detail is detail.
func k2Stubs(box, detail string) string {
	return `
const ta = {value: ` + box + `, style: {}, dataset: {sid: 's1'}};
const $ = () => ta;
const S = {sid: 's1', detail: ` + detail + `, drafts: {}};
const clientCaps = () => ({});
const sent = [], screens = [];
const toast = () => {};
const api = async (path) => { sent.push(path); return {}; };
const showScreen = (name) => { screens.push(name); };
`
}

// Finding 15 of the fifteenth review: /screen typed in the web composer
// shows the agent's terminal there, as the Screen button does; it used to
// be sent, and the session showed the screen only at eagent's own
// terminal and on the phone, so the page showed nothing.
func TestK2ScreenFromTheWebShowsTheAgentsTerminal(t *testing.T) {
	k2Page(t, k2Stubs(`'/screen'`, `{alive: true, agents: [{name: 'old', state: 'stopped'}, {name: 'codex', state: 'idle'}]}`)+`
send().then(() => {
  if (screens.join() !== 'codex' || sent.length !== 0 || ta.value !== '') { console.error('screens ' + JSON.stringify(screens) + ', sent ' + JSON.stringify(sent) + ', box ' + JSON.stringify(ta.value)); process.exit(1); }
});
`)
}

// The Resume button (postResume) for a session a message from the browser
// is resuming at the same moment waits for that resume, and then finds the
// session running. It used to resume it too, and one of the two failed on
// the session's lock.
func TestK2ResumeButtonWaitsForTheBrowsersResume(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	entered, gate := make(chan struct{}), make(chan struct{})
	browser := make(chan error, 1)
	go func() {
		_, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "from the browser", From: "web"}, func() error {
			close(entered)
			<-gate // the message's resume is under way
			return s.host(info.Path, harness.Options{Project: project, Interactive: true, Prompt: "from the browser", PromptSource: "web"}, "", "", true)
		})
		browser <- err
	}()
	<-entered
	button, done := make(chan *httptest.ResponseRecorder, 1), make(chan struct{})
	go func() {
		button <- do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{}, nil)
		close(done)
	}()
	k1Parked(t, "web.(*Server).postResume", done)
	close(gate)
	if err := <-browser; err != nil {
		w := <-button
		t.Fatalf("the browser's words were refused (%v): the Resume button resumed the session meanwhile (%d %s)", err, w.Code, w.Body)
	}
	if w := <-button; w.Code != http.StatusConflict {
		t.Fatalf("the Resume button answered %d %s for a session the browser had just resumed", w.Code, w.Body)
	}
}

// No run starts here once the server's shutdown has begun, whatever asks
// for it: the Resume button, a new session from the browser or from the
// phone, a reply from the phone. Each used to start a run after Shutdown
// had taken its list of runs to stop, which the process exit then killed.
func TestK2NothingStartsARunOnceTheServersShutdownHasBegun(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	info := r11Session(t, project)
	s.Shutdown(5 * time.Second)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{}, nil); w.Code != http.StatusServiceUnavailable || s.isHosted(info.ID) {
		t.Errorf("the Resume button: %d %s, hosted %v", w.Code, w.Body, s.isHosted(info.ID))
	}
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "m1", Text: "from the phone"}); !errors.Is(err, errStopping) || s.isHosted(info.ID) {
		t.Errorf("a reply from the phone: %v, hosted %v", err, s.isHosted(info.ID))
	}
	if w := do(s, "POST", "/api/sessions", map[string]any{"prompt": "a new one"}, nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a new session from the browser: %d %s", w.Code, w.Body)
	}
	// What a new session from the phone runs on (RegisterSessionStarter).
	if id, err := s.hostNewCfg(harness.Options{Project: project, Interactive: true, Prompt: "a new one from the phone"}, cfg); !errors.Is(err, errStopping) {
		t.Errorf("a new session from the phone: %q, %v", id, err)
	}
	s.mu.Lock()
	n := len(s.running)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d run(s) started after the server's shutdown", n)
	}
}

// k2Hosted runs the session here with a terminal the test types into, once
// its first look at its inbox is behind it.
func k2Hosted(t *testing.T, s *Server, cfg config.Config, project string, info store.Info) (k1Terminal, *hosted) {
	t.Helper()
	ui := k1Terminal{newWebUI(func(string, ...any) {}), make(chan string, 1), make(chan struct{}, 1)}
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.run(rt, ui.webUI, false)
	<-ui.looked
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	return ui, h
}

// k2Types counts the events of type typ on record.
func k2Types(t *testing.T, info store.Info, typ string) int {
	t.Helper()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// A dismissal or a stop from the browser for a session this server runs,
// which then ends before its next look at its inbox, is taken first: the
// dismissal is on record, and nothing is left in the inbox. They used to
// stay there, answered as delivered: a dismissal left so let the next
// resume's message answer the question the user had dismissed.
func TestK2ADismissalOrAStopForARunHereThatThenEndsIsTaken(t *testing.T) {
	for _, what := range []string{"dismiss", "stop"} {
		t.Run(what, func(t *testing.T) {
			s, project := newTestServer(t)
			cfg := k1Model(t, s, project)
			info := r11Session(t, project)
			sess, _, err := store.Open(info.Path, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sess.Append(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Merge it?"})); err != nil {
				t.Fatal(err)
			}
			_ = sess.Close()
			ui, h := k2Hosted(t, s, cfg, project, info)
			if w := do(s, "POST", "/api/sessions/"+info.ID+"/"+what, map[string]any{"question_id": "q1"}, nil); w.Code != http.StatusOK {
				t.Fatalf("answered %d %s", w.Code, w.Body)
			}
			ui.in <- "/quit" // before the run's next look at its inbox
			<-h.done
			if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
				t.Fatalf("the %s was left unread in the inbox of a run that has ended (%d file(s))", what, len(left))
			}
			if n := k2Types(t, info, event.UserDismiss); what == "dismiss" && n != 1 {
				t.Fatalf("%d dismissals on record", n)
			}
		})
	}
}

// One for a run here that is ending is not given to it, and the browser is
// told: the run reads its inbox no more.
func TestK2ADismissalForAnEndingRunHereIsRefused(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	info := r11Session(t, project)
	rt, ui := r11Ended(t, cfg, project, info)
	lock, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	s.mu.Lock()
	s.running[info.ID] = &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Unlock()
	t.Cleanup(func() { s.mu.Lock(); delete(s.running, info.ID); s.mu.Unlock() })
	for _, what := range []string{"dismiss", "stop"} {
		w := do(s, "POST", "/api/sessions/"+info.ID+"/"+what, map[string]any{"question_id": "q1"}, nil)
		if left, _ := os.ReadDir(harness.InboxDir(info.Path)); w.Code != http.StatusConflict || len(left) != 0 {
			t.Fatalf("%s: answered %d %s, %d file(s) left in the inbox of a run that reads it no more", what, w.Code, w.Body, len(left))
		}
	}
}

// With no agent running it is sent as before: the session says so in the
// conversation.
func TestK2ScreenWithNoAgentRunningIsSent(t *testing.T) {
	k2Page(t, k2Stubs(`'/screen'`, `{alive: true, agents: [{name: 'old', state: 'stopped'}]}`)+`
send().then(() => {
  if (screens.length !== 0 || sent.join() !== '/api/sessions/s1/message') { console.error('screens ' + JSON.stringify(screens) + ', sent ' + JSON.stringify(sent)); process.exit(1); }
});
`)
}
