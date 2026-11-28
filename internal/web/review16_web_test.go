package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
)

// The server's own configuration has the phone off, and the user replies
// from the app in the thread of a stopped session: the reply watcher
// brings the session back with the reply (as on main). That run mirrors
// to the thread the reply came from: it is where the reply's refused
// files are told and where what the user writes next is read. It ran with
// no mirror, hosted for good: the refusal went untold, and the next
// replies were left to a mirror it did not have.
func TestASessionBroughtBackByAPhoneReplyMirrors(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	cfg := r11Model(t) // the project's file: the phone off
	if cfg.Finalechat.Wanted() {
		t.Fatal("setup: the project's file has the phone on")
	}
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	info := r11Session(t, project)
	k16Mirrored(t, info)
	notes := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.notes)
	}
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "reply-1", Text: "what is the status?"}); err != nil {
		t.Fatal(err)
	}
	sr9Stop(t, s, info, "the run for the phone's reply")
	if notes() == 0 {
		t.Fatal("the run a reply from the phone started did not mirror (no closing note in its thread)")
	}
	// A restore after a restart with no reply waiting, and the page's own
	// resume, run on the configuration as it is: the phone off.
	before := notes()
	if err := s.Resume(info.ID, integration.PhoneMessage{}); err != nil {
		t.Fatal(err)
	}
	sr9Stop(t, s, info, "the run with no reply")
	if notes() != before {
		t.Fatal("a resume with no reply from the phone mirrored, with the phone off")
	}
}
