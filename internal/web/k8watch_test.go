package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

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

// The serve runs the bundle "work", whose phone is a self-hosted service b
// that is down when the serve restarts; the project's file has the phone
// at a, where the session's thread is (no reply waits in it). main read
// the waiting reply on the project file's account (a) and brought the
// session back. Here the restore reads it on the serve's own account (b)
// first: the read fails, and the session that was running before the
// restart is not brought back ("it comes back on your next message").
func TestSR9WebADownServePhoneKeepsTheRestoreFromBringingSessionsBack(t *testing.T) {
	_, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srvA := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srvA.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	b := down.URL
	down.Close()
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srvA.URL)
	t.Setenv("SR9_WORK_TOKEN", "fc_work")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"finalechat": {"base_url": "`+b+`", "token_env": "SR9_WORK_TOKEN"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := sr9Session(t, project, srvA.URL)
	first := New(project, nil)
	first.Bundle = "work"
	first.writeHosted([]string{info.ID}, time.Now().UTC())
	var back []string
	old := restoreSession
	restoreSession = func(_ *Server, id string, _ integration.PhoneMessage) error { back = append(back, id); return nil }
	t.Cleanup(func() { restoreSession = old })
	var logged []string
	next := New(project, func(f string, args ...any) { logged = append(logged, fmt.Sprintf(f, args...)) })
	next.Bundle = "work"
	next.restoreHosted(context.Background())
	if len(back) != 1 {
		t.Fatalf("the session running before the restart was brought back %d times, want 1; log: %q", len(back), logged)
	}
}
