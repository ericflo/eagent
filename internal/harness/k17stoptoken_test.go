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

// The project's file and the server's own configuration name one phone
// service with different tokens (the case 52c42f9 set out to handle). The
// project file's token is a stale one the service no longer accepts (401);
// the server's own token is the one the session's run mirrored with, and
// its account has the thread. A stop from outside any run should tell the
// thread on the server's account. stoppedThread returns the first
// account's /me failure instead of moving on, so the phone is told
// nothing: it still shows the agent running.
func TestSR11StopWithAStaleFirstTokenTellsTheSecondAccount(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_CONFIG", "")
	t.Setenv("EAGENT_PRESET", "")
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/me"):
			_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
		case r.Method == http.MethodPost:
			mu.Lock()
			posts = append(posts, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t1"}}`))
		default:
			_, _ = w.Write([]byte(`{"thread":{"id":"t1"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SR11_FILE_TOKEN", "stale")
	t.Setenv("SR11_OWN_TOKEN", "good")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat":{"enabled":true,"base_url":"`+srv.URL+`","token_env":"SR11_FILE_TOKEN"}}`), 0o644); err != nil {
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
	file, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	own := file
	own.Finalechat.TokenEnv = "SR11_OWN_TOKEN"
	why := TellPhoneStopped([]config.Config{file, own}, path, nil)
	mu.Lock()
	defer mu.Unlock()
	if len(posts) == 0 {
		t.Fatalf("the thread on the server's own account (which has it) was told nothing of the stop, because the project file's stale token failed first: %v", why)
	}
}

// The session was mirrored on two accounts of one service at different
// times, and the user deleted its live thread, which the run made again
// (the record still names the deleted one). Each account has a thread
// under the session's reference: the stop tells the one active last, on
// its account, and not the older thread on the account asked first.
func TestK17StopTellsTheRecreatedThreadActiveLast(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_CONFIG", "")
	t.Setenv("EAGENT_PRESET", "")
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		own := map[string]string{"first": "old", "second": "live"}[tok]
		at := map[string]string{"first": "2026-11-01T10:00:00Z", "second": "2026-11-20T10:00:00Z"}[tok]
		switch {
		case strings.HasSuffix(r.URL.Path, "/me"):
			_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/threads/ext:"):
			_, _ = w.Write([]byte(`{"thread":{"id":"` + own + `","last_activity_at":"` + at + `"}}`))
		case r.Method == http.MethodGet && !strings.Contains(r.URL.Path, "/threads/"+own):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
		case r.Method == http.MethodPost:
			mu.Lock()
			posts = append(posts, tok+" "+r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"` + own + `"}}`))
		default:
			_, _ = w.Write([]byte(`{"thread":{"id":"` + own + `"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("K17_FIRST", "first")
	t.Setenv("K17_SECOND", "second")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat":{"enabled":true,"base_url":"`+srv.URL+`","token_env":"K17_FIRST"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "deleted", ExternalID: "eagent:" + sess.ID, BaseURL: srv.URL})); err != nil {
		t.Fatal(err)
	}
	path := sess.Path
	_ = sess.Close()
	first, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Finalechat.TokenEnv = "K17_SECOND"
	why := TellPhoneStopped([]config.Config{first, second}, path, nil)
	mu.Lock()
	defer mu.Unlock()
	if len(posts) == 0 {
		t.Fatalf("nothing was told: %v", why)
	}
	for _, p := range posts {
		if !strings.HasPrefix(p, "second /api/v1/threads/live/") {
			t.Fatalf("the stop posted %q; want only the thread active last (live, on the second account)", posts)
		}
	}
}
