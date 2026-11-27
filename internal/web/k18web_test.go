package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// sr9PhoneOff is the k17 setting: the project file has the phone on (the
// server's own configuration), the saved preset "nophone" has it off, and
// a session mirrored once (its thread on the record) was last resumed from
// the page on "nophone" and stopped. It returns the fake phone's count of
// closing notes.
func sr9PhoneOff(t *testing.T) (*Server, string, store.Info, func() int) {
	t.Helper()
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	cfg := r11Model(t)
	cfg.Finalechat.Enabled = nil // the project's file: the phone on
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "nophone"), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	info := r11Session(t, project)
	k16Mirrored(t, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{"config": "nophone"}, nil); w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	sr9Stop(t, s, info, "the page's resume")
	notes := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.notes)
	}
	return s, project, info, notes
}

// The user replies from the phone to a stopped session while `eagent serve`
// is stopping (rememberHosted has set stopping; Shutdown waits on the runs,
// and the reply watcher stops only after). The watcher resumes it for the
// reply (Server.Resume); host answers errStopping, whose words are meant
// for the page ("send it again once it is back"), and the watcher posts
// them to the thread as its note: "eagent could not resume this session
// for your reply yet: eagent is stopping; send it again once it is back.
// It keeps trying every minute." It keeps the reply and writes it down for
// the next watcher (keptFile), which resumes the session with it. The note
// tells the user to send again words eagent kept: done as told, the
// session takes the reply twice (the kept one, then the copy). Nothing of
// this is on main, which had no stopping.
func TestSR9WebAPhoneReplyDuringShutdownIsToldToSendAgainAndKept(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	posted := make(chan string, 8)
	phone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/api/v1/threads":
			_, _ = w.Write([]byte(`{"threads":[{"id":"t1","external_id":"eagent:` + sess.ID + `","preview_sender":"user","last_activity_at":"` + at + `"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			text, _ := body["body"].(string)
			posted <- text
			_, _ = w.Write([]byte(`{"message":{"id":"n"},"thread":{"id":"t1"}}`))
		case strings.HasSuffix(r.URL.Path, "/messages") && r.URL.Query().Get("before") == "":
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1","sender":"user","origin":"session","body":"are you still there?","created_at":"` + at + `","meta":{}}]}`))
		default:
			_, _ = w.Write([]byte(`{"messages":[]}`))
		}
	}))
	t.Cleanup(phone.Close)
	t.Setenv("FINALECHAT_URL", phone.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	// The serve's shutdown has begun.
	s.rememberHosted()
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, project, s, func(string, ...any) {})
	var note string
	select {
	case note = <-posted:
	case <-time.After(20 * time.Second):
		cancel()
		stop()
		t.Fatal("setup: the watcher posted nothing")
	}
	cancel()
	stop() // the watcher stops: it writes down the replies it keeps
	raw, _ := os.ReadFile(filepath.Join(project, ".agents", "eagent", "finalechat-state", "resume-watch-kept.json"))
	kept := strings.Contains(string(raw), `"m1"`)
	if strings.Contains(note, "send it again") && kept {
		t.Fatalf("the phone was told %q, and the reply was kept for the next watcher (kept file: %s): the user is told to send again words eagent resumes the session with after the restart, and sending them again doubles them", note, strings.TrimSpace(string(raw)))
	}
	t.Logf("note %q kept %v", note, kept)
}

// sr9Stop stops the run of the session s hosts and waits for its end.
func sr9Stop(t *testing.T, s *Server, info store.Info, what string) {
	t.Helper()
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	if h == nil {
		t.Fatalf("setup: %s: the session is not hosted", what)
	}
	h.cancel()
	<-h.done
}

// The user replies on the phone; the reply watcher resumes the session
// with the reply, and that run mirrors to the phone, so the conversation
// goes on there. Then `eagent serve` restarts (rememberHosted, Shutdown):
// the session is on the list of sessions to bring back, with no reply
// waiting, and the next server brought it back on the configuration the
// list named for it ("nophone"), the mirror off: the user's next replies
// in its thread waited unread. On main the phone's resume and the restore
// both ran on the server's own configuration (host: loadConfig("", "")),
// the phone on, so the restored run kept mirroring; so they do again.
// (Adapted: the control's line naming the session's configuration on the
// next server is gone with that configuration.)
func TestSR9WebARestoreTurnsOffThePhoneAReplyTurnedOn(t *testing.T) {
	s, project, info, notes := sr9PhoneOff(t)
	oldReply := pendingReply
	t.Cleanup(func() { pendingReply = oldReply })
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, nil // no reply waits at the restart
	}
	// The reply from the phone: the run mirrors.
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "reply-1", Text: "what is the status?"}); err != nil {
		t.Fatal(err)
	}
	before := notes()
	// The serve restarts.
	s.rememberHosted()
	s.Shutdown(5 * time.Second)
	if notes() == before {
		t.Fatal("setup: the phone's run did not mirror (no closing note at the shutdown)")
	}
	next := New(project, nil)
	t.Cleanup(func() { next.Shutdown(5 * time.Second) })
	next.restoreHosted(context.Background())
	if !next.isHosted(info.ID) {
		t.Fatal("setup: the session was not brought back")
	}
	mark := notes()
	sr9Stop(t, next, info, "the restored run")
	restoredMirrored := notes() > mark

	// Control, as on main: the same restore on the server's own
	// configuration mirrors.
	if err := next.Resume(info.ID, integration.PhoneMessage{ID: "reply-2", Text: "and now?"}); err != nil {
		t.Fatal(err)
	}
	next.rememberHosted()
	next.Shutdown(5 * time.Second)
	control := New(project, nil)
	t.Cleanup(func() { control.Shutdown(5 * time.Second) })
	control.restoreHosted(context.Background())
	if !control.isHosted(info.ID) {
		t.Fatal("setup: control: the session was not brought back")
	}
	mark = notes()
	sr9Stop(t, control, info, "the control's restored run")
	if notes() == mark {
		t.Fatal("setup: control: a restore on the server's own configuration did not mirror either")
	}
	if !restoredMirrored {
		t.Fatal("the session ran mirrored to the phone (resumed for a reply) when the serve restarted, and the next serve brought it back with the mirror off (the list's configuration, \"nophone\"): the user's replies in its thread wait unread while it runs, and nothing it says reaches the phone; on main the restore ran on the server's own configuration, the phone on")
	}
}

// sr9Session is a stopped session run from the terminal on the project's
// own configuration (its session.start names none), whose record shows its
// thread on the phone service at base.
func sr9Session(t *testing.T, project, base string) store.Info {
	t.Helper()
	sess := newSession(t, project)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
		event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + sess.ID, BaseURL: base}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// The stop half of the review's TestSR9WebAPhoneReplyOnTheProjectsAccount-
// IsAnsweredOnTheServesBundle: the project's file has the phone at a,
// where the session (run from the terminal, on the project's own
// configuration) has its thread; the serve runs the bundle "work", whose
// phone is at b. The stop tells a.
func TestSR9WebAStopOnTheProjectsAccountTellsIt(t *testing.T) {
	const a, b = "https://phone-a.invalid", "https://phone-b.invalid"
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", "")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"base_url": "`+a+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"finalechat": {"base_url": "`+b+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Bundle = "work"
	info := sr9Session(t, project, a)
	stop := s.stopConfig(info)
	stopAt := "nothing"
	if stop != nil {
		stopAt = stop.Finalechat.BaseURL
	}
	if stopAt != a {
		t.Fatalf("thread at %s (the project file's account): the stop tells %s; want %s", a, stopAt, a)
	}
}

// The stop half of the review's TestSR9WebTwoTokensOnOneServiceAnswerOn-
// TheWrongAccount: two accounts on one service (two tokens on
// finale.chat). The project's file reads the work token, where the
// session's thread is; the serve runs the bundle "personal", with the
// personal token on the same service. The stop tells with the work token.
func TestSR9WebTwoTokensOnOneServiceStopOnTheWorkAccount(t *testing.T) {
	const u = "https://phone.invalid"
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", u)
	t.Setenv("SR9_WORK", "fc_work")
	t.Setenv("SR9_PERSONAL", "fc_personal")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"token_env": "SR9_WORK"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "personal"), []byte(`{"finalechat": {"token_env": "SR9_PERSONAL"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Bundle = "personal"
	info := sr9Session(t, project, u)
	stop := s.stopConfig(info)
	stopTok := "nothing"
	if stop != nil {
		stopTok = stop.Finalechat.TokenEnvName()
	}
	if stopTok != "SR9_WORK" {
		t.Fatalf("thread on the work account: the stop tells with %s; want $SR9_WORK", stopTok)
	}
}

// The project's phone is turned off in its file (a token is still set).
// A session mirrored to the phone (its thread is on the record) is
// stopped from outside any run. The record decides whether the phone is
// told, the configuration only the account; TellPhoneStopped returned at
// once on the configuration's enabled: false: no closing note, no header,
// no receipts, no owed notes.
func TestSR9WebAStopOfAMirroredSessionWithThePhoneOffTellsNothing(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": false}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k5Kills(t)
	info := srAgentSession(t, project)
	k16Mirrored(t, info)
	cfg := s.stopConfig(info)
	if cfg == nil {
		t.Fatal("setup: no configuration for the stop")
	}
	if _, _, err := stopAgents(info.Path, cfg, nil); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.notes) != 1 {
		t.Fatalf("a stop of a session whose record shows it mirrored posted %d closing notes, want 1 (the configuration's phone is off: %v)", len(f.notes), !cfg.Finalechat.Wanted())
	}
}

// k18Accounts is a phone service with two accounts (tokens fc_work and
// fc_personal) on which only the work account has the thread "t". It
// keeps each request's method, path and token.
type k18Accounts struct {
	mu   sync.Mutex
	seen []string
}

func (f *k18Accounts) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+tok)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/me"):
		_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
	case strings.HasPrefix(r.URL.Path, "/api/v1/threads/t") && tok != "fc_work":
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
	case r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
	default:
		_, _ = w.Write([]byte(`{"thread":{"id":"t"}}`))
	}
}

// A stop never makes a thread: it posts only to the thread the record
// names, and tells nothing (saying why) on an account that does not have
// it, even one on the same service; on the account that has it, it posts
// there. It used to post by the session's external id, which makes a
// thread on an account that has none.
func TestK18AStopNeverMakesAThread(t *testing.T) {
	_, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &k18Accounts{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_URL", srv.URL)
	t.Setenv("K18_WORK", "fc_work")
	t.Setenv("K18_PERSONAL", "fc_personal")
	info := sr9Session(t, project, srv.URL)
	posts := func(tok string) []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, s := range f.seen {
			if strings.HasPrefix(s, "POST ") && strings.HasSuffix(s, " "+tok) {
				out = append(out, s)
			}
		}
		return out
	}
	on := func(env string) *config.Config {
		cfg := config.Defaults()
		cfg.Finalechat.TokenEnv = env
		return &cfg
	}
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if _, _, err := stopAgents(info.Path, on("K18_PERSONAL"), logf); err != nil {
		t.Fatal(err)
	}
	if got := posts("fc_personal"); len(got) != 0 {
		t.Fatalf("the stop posted on an account without the session's thread: %q", got)
	}
	if !slices.ContainsFunc(logged, func(l string) bool {
		return strings.Contains(l, "not told") && strings.Contains(l, "not on the phone account")
	}) {
		t.Fatalf("the log does not say why the phone is not told: %q", logged)
	}
	if _, _, err := stopAgents(info.Path, on("K18_WORK"), logf); err != nil {
		t.Fatal(err)
	}
	got := posts("fc_work")
	if len(got) == 0 {
		t.Fatal("the stop told the account with the thread nothing")
	}
	for _, p := range got {
		if !strings.HasPrefix(p, "POST /api/v1/threads/t/") {
			t.Fatalf("the stop posted outside the record's thread: %q", got)
		}
	}
}

// A reply from the phone during this server's shutdown resumes nothing,
// and its answer says what is true for the phone: the reply is kept, and
// there is nothing to send again (errStopping's words are the page's).
func TestK18APhoneReplyDuringTheShutdownIsToldItIsKept(t *testing.T) {
	s, project := newTestServer(t)
	info := r11Session(t, project)
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	err := s.Resume(info.ID, integration.PhoneMessage{ID: "reply-1", Text: "are you there?"})
	if err == nil || strings.Contains(err.Error(), "send it again") || !strings.Contains(err.Error(), "kept") || !strings.Contains(err.Error(), "nothing to send again") {
		t.Fatalf("answered %v", err)
	}
	if s.isHosted(info.ID) {
		t.Fatal("the session was resumed during the shutdown")
	}
}

// A list of sessions to bring back written with the names of their
// configurations ("configs", and on its stop marks), as the last head
// wrote it, still reads: the marked stop is finished and the listed
// session brought back.
func TestK18AListWrittenWithConfigurationNamesStillReads(t *testing.T) {
	_, project := newTestServer(t)
	k5Kills(t)
	listed, marked := r11Session(t, project), r11Session(t, project)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	list := fmt.Sprintf(`{"sessions": [%q], "at": %q, "configs": {%q: {"config": "nophone", "preset": "-"}}, "stop": [{"session": %q, "at": %q, "config": "nophone", "preset": "-"}]}`, listed.ID, at, listed.ID, marked.ID, at)
	if err := os.MkdirAll(filepath.Dir(hostedFile(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostedFile(project), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	var back []string
	old := restoreSession
	restoreSession = func(_ *Server, id string, _ integration.PhoneMessage) error { back = append(back, id); return nil }
	t.Cleanup(func() { restoreSession = old })
	oldReply := pendingReply
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, nil
	}
	t.Cleanup(func() { pendingReply = oldReply })
	s := New(project, nil)
	s.restoreHosted(context.Background())
	if !slices.Equal(back, []string{listed.ID}) {
		t.Fatalf("brought back %q, want the listed session", back)
	}
	s.mu.Lock()
	still := s.listed(marked.ID)
	s.mu.Unlock()
	if still {
		t.Fatal("the marked stop was not finished")
	}
}
