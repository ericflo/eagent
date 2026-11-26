package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
