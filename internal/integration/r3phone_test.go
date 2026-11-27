package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// The reply watcher hands the session the message's meta (route, delivery,
// client capabilities), not only its words, so the resumed session can
// take it as a running one would.
func TestResumeWatcherHandsOverTheMessagesMeta(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "") // a watcher runs only with the phone on; the gate also runs with it off
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Minute))
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := &resumeWatcher{project: project, host: host, client: &finalechat.Client{BaseURL: "http://127.0.0.1:1", Token: "fc_fixture"}, logf: t.Logf, retries: &replyRetries{replies: map[string]pendingReply{}}, handled: map[string]time.Time{}}
	meta := map[string]any{"eagent.route": "eagent", "eagent.delivery": "interrupt", "eagent.client": map[string]any{"timezone": "Europe/Paris"}}
	w.consider(context.Background(), finalechat.Thread{ID: "t1", ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "/screen", Meta: meta})
	if host.calls() != 1 {
		t.Fatalf("%d resumes", host.calls())
	}
	got := host.resumed[0]
	if got.ID != "m1" || got.Meta["eagent.route"] != "eagent" || got.Meta["eagent.delivery"] != "interrupt" || got.Meta["eagent.client"] == nil {
		t.Fatalf("handed over %#v", got)
	}
}

// A phone attachment is saved under the session's attachments folder
// whatever its id and name say: "../" and "/" in either never reach the
// path.
func TestSavedAttachmentStaysInTheSessionsFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_fixture"}
	session := t.TempDir()
	dir := filepath.Join(session, "attachments")
	for _, a := range []finalechat.Attachment{
		{ID: "../../x", Filename: "../../etc/passwd", URL: srv.URL + "/f/1"},
		{ID: "a/../../b", Filename: "/abs/path.txt", URL: srv.URL + "/f/2"},
		{ID: "..", Filename: "..", URL: srv.URL + "/f/3"},
	} {
		saved, err := saveAttachment(context.Background(), client, session, a)
		if err != nil {
			t.Fatalf("%q: %v", a.ID, err)
		}
		if filepath.Dir(saved.Path) != dir {
			t.Fatalf("id %q name %q saved at %s, outside %s", a.ID, a.Filename, saved.Path, dir)
		}
		base := filepath.Base(saved.Path)
		if strings.Contains(base, "..") || strings.Contains(saved.Name, "/") {
			t.Fatalf("id %q name %q saved as %q (%q)", a.ID, a.Filename, base, saved.Name)
		}
		if raw, err := os.ReadFile(saved.Path); err != nil || string(raw) != "hello" {
			t.Fatalf("%s: %q %v", saved.Path, raw, err)
		}
	}
	entries, _ := os.ReadDir(session)
	if len(entries) != 1 {
		t.Fatalf("the session folder holds %d entries, want only attachments/", len(entries))
	}
}
