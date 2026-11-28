package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
// and the reply watcher stops only after). The watcher's resume is refused
// (integration.ErrStopping): nothing is posted to the thread and nothing
// is kept, so the reply is still the thread's latest message for the next
// server's sweep. (A note used to tell the user to send it again, while
// the reply was also kept: done as told, the session took it twice.)
func TestSR9WebAPhoneReplyDuringShutdownIsLeftInItsThread(t *testing.T) {
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
	left := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, project, s, func(format string, _ ...any) {
		if strings.Contains(format, "left in its thread for the next eagent") {
			left <- struct{}{}
		}
	})
	select {
	case <-left:
	case <-time.After(20 * time.Second):
		cancel()
		stop()
		t.Fatal("the watcher never left the reply for the next eagent")
	}
	cancel()
	stop()
	select {
	case note := <-posted:
		t.Fatalf("the thread was told %q about a reply the next eagent takes", note)
	default:
	}
	if s.isHosted(sess.ID) {
		t.Fatal("the session was resumed during the shutdown")
	}
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

// k18Accounts is a phone service with two accounts (tokens fc_work and
// fc_personal) on which only the work account has the thread "t" (by its
// id, and under the session's reference). It keeps each request's method,
// path and token.
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
	case strings.HasPrefix(r.URL.Path, "/api/v1/threads/") && tok != "fc_work":
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
	case r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
	default:
		_, _ = w.Write([]byte(`{"thread":{"id":"t"}}`))
	}
}
