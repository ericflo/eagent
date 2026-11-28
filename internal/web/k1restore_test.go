package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
)

// A reply from the phone whose files the reply watcher could not fetch
// resumes the session with them refused: the resumed session records them
// (phone.refused) and its mirror tells the sender.
func TestK1APhoneResumeCarriesTheRefusedFiles(t *testing.T) {
	s, _ := newTestServer(t)
	refused := []integration.RefusedFile{{AttachmentID: "att-1", Text: "eagent could not fetch shot.png from the phone"}}
	o := s.phoneResumeOptions(integration.PhoneMessage{ID: "m1", Text: "see the screenshot", Refused: refused})
	if len(o.PromptRefused) != 1 || o.PromptRefused[0] != refused[0] || o.PromptPhoneID != "m1" {
		t.Fatalf("resumed with refused %#v", o.PromptRefused)
	}
}

// With the phone off in the project's file (finalechat.enabled false), a
// restart still brings a session back with the reply that waited in its
// thread, as on main and as the reply watcher takes one.
func TestK4ARestoreWithThePhoneOffStillTakesTheReply(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "") // the configuration, not the kill switch, turns the phone off
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": false}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := newSession(t, project)
	_ = sess.Close()
	t.Setenv("FINALECHAT_URL", k4Phone(t))
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	replies := k4Restores(t)
	New(project, nil).restoreHosted(context.Background())
	if len(*replies) != 1 || (*replies)[0] != "are you still there?" {
		t.Fatalf("brought back with %q, want the reply that waited", *replies)
	}
}

// k4Phone is a FinaleChat with one reply waiting in every thread, written
// after the session stopped; it answers the token fc_fixture.
func k4Phone(t *testing.T) string {
	t.Helper()
	at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	phone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer fc_fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && r.URL.Query().Get("before") == "" {
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1","sender":"user","origin":"session","body":"are you still there?","created_at":"` + at + `","meta":{}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[]}`))
	}))
	t.Cleanup(phone.Close)
	return phone.URL
}

// k4Restores stubs only the resume of a restore (the reply is read for
// real) and returns the words each session came back with.
func k4Restores(t *testing.T) *[]string {
	t.Helper()
	old := restoreSession
	t.Cleanup(func() { restoreSession = old })
	var replies []string
	restoreSession = func(_ *Server, _ string, reply integration.PhoneMessage, _ bool) error {
		replies = append(replies, reply.Text)
		return nil
	}
	return &replies
}
