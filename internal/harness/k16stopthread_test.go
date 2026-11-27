package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// The session mirrored to thread t1; the user deleted that thread in the
// app, and the run's next post recreated it under the same external id as
// t2 (rememberIn takes the new id in memory; no phone.thread is written
// for it, so the record still names t1). The session is then stopped from
// outside any run. TellPhoneStopped looks up t1 only, finds it gone, and
// tells the live thread t2 nothing: no header, no receipts, no owed note,
// no closing note.
func TestSR10StopTellsTheThreadTheRunRecreated(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_CONFIG", "")
	t.Setenv("EAGENT_PRESET", "")
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/me"):
			_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/threads/t1"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t2"}}`))
		default:
			_, _ = w.Write([]byte(`{"thread":{"id":"t2"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SR10_FC_TOKEN", "tok")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat":{"enabled":true,"base_url":"`+srv.URL+`","token_env":"SR10_FC_TOKEN"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t1", ExternalID: "eagent:" + sess.ID, BaseURL: srv.URL})); err != nil {
		t.Fatal(err)
	}
	path := sess.Path
	_ = sess.Close()
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	why := TellPhoneStopped([]config.Config{cfg}, path, nil)
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if strings.HasPrefix(s, "POST ") {
			return
		}
	}
	t.Fatalf("the live thread of the session (recreated after the user deleted the one its record names) was told nothing of the stop: %v; requests %q", why, seen)
}
