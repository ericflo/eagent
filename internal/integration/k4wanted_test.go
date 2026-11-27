package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

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
