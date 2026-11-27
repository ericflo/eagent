package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
)

// sr8Host is the server as the reply watcher sees it, with its resume
// recorded instead of run.
type sr8Host struct {
	*Server
	resumed chan integration.PhoneMessage
}

func (h *sr8Host) Resume(_ string, msg integration.PhoneMessage) error {
	h.resumed <- msg
	return nil
}

// sr8Phone is a FinaleChat with one stopped session's thread, whose latest
// message is a reply the user wrote after the session stopped.
func sr8Phone(t *testing.T, sid string) string {
	t.Helper()
	at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	phone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fc_fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/api/v1/threads":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"threads":[{"id":"t1","external_id":"eagent:` + sid + `","preview_sender":"user","last_activity_at":"` + at + `"}]}`))
		case strings.HasSuffix(r.URL.Path, "/messages") && r.URL.Query().Get("before") == "":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1","sender":"user","origin":"session","body":"are you still there?","created_at":"` + at + `","meta":{}}]}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"messages":[]}`))
		}
	}))
	t.Cleanup(phone.Close)
	return phone.URL
}

// The project's own file turns the phone off; a saved bundle, "phone",
// turns it on. A session started in the browser on "phone" mirrors to the
// phone, and every resume of it runs on "phone" (runConfig), so its
// closing note says a reply in its thread resumes it while eagent serve
// runs. The user replies from the phone. The reply watcher asks the
// server's own configuration (PhoneConfig), finds the phone off there,
// and never watches: the reply resumes nothing. On main the watcher took
// the account from the project's file whether or not the phone was on
// there (settingsAccount), and resumed the session with the reply.
func TestSR8WatcherWatchesThePhoneTheSessionRunsOn(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": false}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "phone"), []byte(`{"finalechat": {"enabled": true}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Config: "phone"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	t.Setenv("FINALECHAT_URL", sr8Phone(t, sess.ID))
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &sr8Host{Server: s, resumed: make(chan integration.PhoneMessage, 4)}
	off := make(chan string, 1)
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log(line)
		if strings.Contains(line, "phone is off") {
			select {
			case off <- line:
			default:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, project, host, logf)
	defer stop()
	defer cancel()
	select {
	case msg := <-host.resumed:
		if msg.ID != "m1" {
			t.Fatalf("resumed with %+v, want m1", msg)
		}
	case line := <-off:
		t.Fatalf("the session runs on the phone (bundle %q) and its thread holds a reply, but the watcher does not watch: %s", "phone", line)
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher neither resumed the session nor said why")
	}
}
