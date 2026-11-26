package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// The service's ids are UUIDv7, whose first eight characters change about
// once a minute. Two files of one name sent within that minute are both
// kept, each with its own bytes.
func TestTwoSavedFilesOfOneNameSentTogetherAreBothKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("file " + r.URL.Path))
	}))
	defer srv.Close()
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_fixture"}
	session := t.TempDir()
	a, err := saveAttachment(context.Background(), client, session, finalechat.Attachment{ID: "019abc12-0000-7000-8000-000000000001", Filename: "image.png", URL: "/f/1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := saveAttachment(context.Background(), client, session, finalechat.Attachment{ID: "019abc12-0000-7000-8000-000000000002", Filename: "image.png", URL: "/f/2"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path {
		t.Fatalf("both saved at %s", a.Path)
	}
	for path, want := range map[string]string{a.Path: "file /f/1", b.Path: "file /f/2"} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != want {
			t.Fatalf("%s holds %q (%v), want %q", path, raw, err, want)
		}
	}
}

// A file over the limit is refused and nothing of it is left on disk.
func TestSavedFileOverTheLimitIsRefusedNotCutShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("x", 1<<20))
		for i := 0; i < 10; i++ {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write([]byte("tail"))
	}))
	defer srv.Close()
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_fixture"}
	session := t.TempDir()
	_, err := saveAttachment(context.Background(), client, session, finalechat.Attachment{ID: "019abc12-0000-7000-8000-00000000000b", Filename: "big.bin", URL: "/f/big"})
	if !errors.Is(err, finalechat.ErrTooLarge) {
		t.Fatalf("err = %v, want too large", err)
	}
	if entries, _ := os.ReadDir(session + "/attachments"); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}

// A reply that resumes a stopped session with a file too large to take
// brings the session back with its words, and the sender is told in the
// thread why the file is missing.
func TestReplyWatcherTellsThePhoneAFileWasTooLarge(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			notes = append(notes, body["body"].(string))
			mu.Unlock()
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"message":{"id":"n1"},"thread":{"id":"t1"}}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Minute))
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := &resumeWatcher{project: project, host: host, client: &finalechat.Client{BaseURL: srv.URL, Token: "fc_fixture"}, logf: t.Logf, retries: &replyRetries{replies: map[string]pendingReply{}}, handled: map[string]time.Time{}}
	big := finalechat.Attachment{ID: "019abc12-0000-7000-8000-00000000000b", Filename: "clip.mov", Size: finalechat.MaxAttachmentSize + 1, URL: "/api/v1/attachments/x"}
	w.consider(context.Background(), finalechat.Thread{ID: "t1", ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "here it is", Attachments: []finalechat.Attachment{big}})
	if host.calls() != 1 || host.resumed[0].Text != "here it is" || len(host.resumed[0].Attachments) != 0 {
		t.Fatalf("resumed %#v", host.resumed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 1 || !strings.Contains(notes[0], "clip.mov") || !strings.Contains(notes[0], "10 MB") {
		t.Fatalf("notes = %q", notes)
	}
}

// The session's summary names its thread a moment after it starts. The
// first publication must not put the project's folder name back over it.
func TestFirstPublicationKeepsTheThreadsSummaryTitle(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	f.mu.Lock()
	f.title = "Fixing the login redirect"
	f.mu.Unlock()
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.title != "Fixing the login redirect" {
		t.Fatalf("title = %q", f.title)
	}
	if f.patchAgent != "eagent" {
		t.Fatalf("agent = %q", f.patchAgent)
	}
}

// The service keeps a file's name up to 255 characters. Saved under the
// whole id, a name that long must still make a file that opens as what it
// is, as the running session's copy does.
func TestALongPhoneFileNameStillSaves(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("png bytes"))
	}))
	t.Cleanup(srv.Close)
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	for _, name := range []string{strings.Repeat("a", 216) + ".png", strings.Repeat("a", 251) + ".png"} {
		a := finalechat.Attachment{ID: "019abc12-0000-7000-8000-000000000001", Filename: name, URL: "/f/1", Size: 9}
		saved, err := saveAttachment(context.Background(), client, t.TempDir(), a)
		if err != nil {
			t.Fatalf("a %d-byte name (the service allows 255) was refused: %v", len(name), err)
		}
		if raw, err := os.ReadFile(saved.Path); err != nil || string(raw) != "png bytes" {
			t.Fatalf("saved %q, %v", raw, err)
		}
		if !strings.HasSuffix(saved.Path, ".png") || len(filepath.Base(saved.Path)) > 255 {
			t.Fatalf("saved as %q (%q)", saved.Path, saved.Name)
		}
	}
}
