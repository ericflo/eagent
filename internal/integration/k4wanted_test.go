package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// k4OffHost is a serve whose own configuration turns the phone off, as
// web.Server says (PhoneConfig).
type k4OffHost struct {
	fakeResumeHost
}

func (h *k4OffHost) PhoneConfig() (config.Finalechat, error) {
	off := false
	return config.Finalechat{Enabled: &off}, nil
}

// k4Watch starts the project's reply watcher against a stopped session
// with a reply waiting, and returns once the watcher has either resumed it
// or said it is off for the phone.
func k4Watch(t *testing.T, host ResumeHost, configure func(project string)) *fakeResumeHost {
	t.Helper()
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	f.say(sid, "m1", "are you still there?", r6Info(t, project, sid).Modified.Add(time.Minute), nil)
	configure(project)
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	said := make(chan struct{}, 1)
	logf := func(format string, args ...any) {
		t.Logf(format, args...)
		if line := fmt.Sprintf(format, args...); strings.Contains(line, "resumed with your reply") || strings.Contains(line, "phone is off") {
			select {
			case said <- struct{}{}:
			default:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, logf)
	select {
	case <-said:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher neither resumed the session nor said it is off")
	}
	cancel()
	stop()
	switch h := host.(type) {
	case *fakeResumeHost:
		return h
	case *k4OffHost:
		return &h.fakeResumeHost
	}
	return nil
}

// A project whose phone is off (finalechat.enabled false): a reply in a
// stopped session's thread is not acted on by the reply watcher, as no run
// of the project would mirror the session.
func TestK4AWatcherLeavesAProjectWhosePhoneIsOff(t *testing.T) {
	host := &fakeResumeHost{hosted: map[string]bool{}}
	k4Watch(t, host, func(project string) {
		if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": false}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if n := host.calls(); n != 0 {
		t.Fatalf("resumed %d times with the phone off", n)
	}
}

// The same when it is the serve's own configuration (its bundle or preset)
// that turns the phone off: the sessions it would resume run without it.
func TestK4AWatcherLeavesAServeWhosePhoneIsOff(t *testing.T) {
	host := &k4OffHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}}
	h := k4Watch(t, host, func(string) {})
	if n := h.calls(); n != 0 {
		t.Fatalf("resumed %d times with the serve's phone off", n)
	}
}

// k4AccountHost is a serve whose own configuration (its bundle) names the
// phone's account: a server and a token variable of its own.
type k4AccountHost struct {
	fakeResumeHost
	fc config.Finalechat
}

func (h *k4AccountHost) PhoneConfig() (config.Finalechat, error) { return h.fc, nil }

// The serve's bundle names the phone's server and token variable, and the
// project's own file names none: the reply watcher watches the account the
// sessions it resumes are mirrored to, and resumes the session.
func TestK4AWatcherWatchesTheAccountTheServeRuns(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	f.say(sid, "m1", "are you still there?", r6Info(t, project, sid).Modified.Add(time.Minute), nil)
	t.Setenv("FINALECHAT_URL", "")
	t.Setenv("FINALECHAT_TOKEN", "")
	t.Setenv("K4_PHONE_TOKEN", "fc_fixture")
	host := &k4AccountHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, fc: config.Finalechat{BaseURL: f.srv.URL, TokenEnv: "K4_PHONE_TOKEN"}}
	said := make(chan string, 4)
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log(line)
		if strings.Contains(line, "resumed with your reply") || strings.Contains(line, "not connected") {
			select {
			case said <- line:
			default:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, logf)
	defer stop()
	defer cancel()
	select {
	case line := <-said:
		if !strings.Contains(line, "resumed with your reply") {
			t.Fatalf("the watcher said %q, want the session resumed", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher neither resumed the session nor said why")
	}
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumed %#v", host.resumed)
	}
}
