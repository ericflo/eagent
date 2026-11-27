package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A session to bring back after a restart has a reply waiting, and cannot
// come back with it (restoreSession fails): the restore gives the reply
// back to the reply watcher (integration.ReleaseReply), which otherwise
// leaves it to the restore for good.
func TestK1ARestoreThatFailsGivesTheReplyBack(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	oldRestore, oldReply, oldRelease := restoreSession, pendingReply, releaseReply
	t.Cleanup(func() { restoreSession, pendingReply, releaseReply = oldRestore, oldReply, oldRelease })
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{ID: "m1", Text: "/quit"}, true, nil
	}
	restoreSession = func(*Server, string, integration.PhoneMessage) error {
		return errors.New("config.json is being edited")
	}
	var released []string
	releaseReply = func(_ string, info store.Info, msg integration.PhoneMessage) error {
		released = append(released, info.ID+" "+msg.ID)
		return nil
	}
	New(project, nil).restoreHosted(context.Background())
	if len(released) != 1 || released[0] != sess.ID+" m1" {
		t.Fatalf("released %q, want the reply of %s", released, sess.ID)
	}
}

// A session to bring back whose thread was read, but whose reply could not
// be left for the reply watcher (integration.ErrHandOver): the terminal is
// not told its thread could not be checked.
func TestK1ARestoreWhoseHandOverFailsSaysSo(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	oldRestore, oldReply := restoreSession, pendingReply
	t.Cleanup(func() { restoreSession, pendingReply = oldRestore, oldReply })
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, fmt.Errorf("%w: disk full", integration.ErrHandOver)
	}
	restoreSession = func(*Server, string, integration.PhoneMessage) error {
		t.Fatal("brought back although its reply could not be left for the watcher")
		return nil
	}
	var lines []string
	New(project, func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }).restoreHosted(context.Background())
	said := strings.Join(lines, "\n")
	if strings.Contains(said, "could not check its thread") || !strings.Contains(said, "disk full") {
		t.Fatalf("said %q", said)
	}
}

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

// With the phone off for the sessions the server resumes (the project's
// finalechat.enabled false), a restart brings a session back as it was:
// no reply is taken from its thread, as the reply watcher takes none.
func TestK4ARestoreWithThePhoneOffTakesNoReply(t *testing.T) {
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
	if len(*replies) != 1 || (*replies)[0] != "" {
		t.Fatalf("brought back with %q, want the session with no reply", *replies)
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
	restoreSession = func(_ *Server, _ string, reply integration.PhoneMessage) error {
		replies = append(replies, reply.Text)
		return nil
	}
	return &replies
}

// The server runs a bundle that names the phone's server and token
// variable; the project's own file names none. A restart brings a session
// back with the reply that waited in its thread on that account.
func TestK4ARestoreReadsTheThreadOnTheServesAccount(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", "")
	t.Setenv("FINALECHAT_TOKEN", "")
	t.Setenv("K4_PHONE_TOKEN", "fc_fixture")
	sess := newSession(t, project)
	_ = sess.Close()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "phone2"), []byte(`{"finalechat": {"base_url": "`+k4Phone(t)+`", "token_env": "K4_PHONE_TOKEN"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	replies := k4Restores(t)
	next := New(project, nil)
	next.Bundle = "phone2"
	next.restoreHosted(context.Background())
	if len(*replies) != 1 || (*replies)[0] != "are you still there?" {
		t.Fatalf("brought back with %q, want the reply on the serve's account", *replies)
	}
}
