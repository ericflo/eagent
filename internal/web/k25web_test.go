package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

type sr12Host struct{ resumed chan integration.PhoneMessage }

func (h *sr12Host) Hosted(string) bool { return false }
func (h *sr12Host) Resume(_ string, m integration.PhoneMessage) error {
	select {
	case h.resumed <- m:
	default:
	}
	return nil
}

// A serve exited before it finished a stop the user asked for; while
// nothing ran, the user replied from the phone. The next serve finishes the
// stop (kills the agent, records relay.end), but the phone service answers
// its /me with a 503 at that moment, so the phone is told nothing (an
// accepted outcome). The thread is then not left with the reply watcher
// (LeftForNextRun runs only after a successful tell), and the stop's own
// writes made the session's record newer than the reply, so the watcher's
// sweep (thread newer than the record's last write) passes it over for
// good, once the service is back: the reply waits unread until the user
// writes again. Had the tell succeeded, the watcher would have looked.
func TestSR12AStopThatCouldNotTellLeavesAWaitingReplyUnread(t *testing.T) {
	sr12StopThenWatch(t, true)
}

// The control: the same steps with the service up at the stop; the stop
// tells, leaves the thread with the watcher, and the reply is taken.
func TestSR12ControlAStopThatToldLeavesTheReplyToTheWatcher(t *testing.T) {
	sr12StopThenWatch(t, false)
}

func sr12StopThenWatch(t *testing.T, downAtStop bool) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", "")
	old := killAgent
	killAgent = func(string, string) error { return nil }
	t.Cleanup(func() { killAgent = old })

	var down atomic.Bool
	var mu sync.Mutex
	var replyAt time.Time
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		at := replyAt.UTC().Format(time.RFC3339Nano)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"down for a moment"}}`))
			return
		}
		q := r.URL.Query()
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case strings.HasSuffix(r.URL.Path, "/me"):
			_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
		case r.URL.Path == "/api/v1/threads":
			sid := strings.TrimPrefix(os.Getenv("SR12_SID"), "")
			_, _ = w.Write([]byte(`{"threads":[{"id":"t","external_id":"eagent:` + sid + `","preview_sender":"user","last_activity_at":"` + at + `"}]}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			if q.Get("before") != "" || q.Get("after") == "u1" {
				_, _ = w.Write([]byte(`{"messages":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"messages":[{"id":"u1","sender":"user","body":"please also run the tests","created_at":"` + at + `"}]}`))
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
		default:
			_, _ = w.Write([]byte(`{"thread":{"id":"t"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"base_url": "`+srv.URL+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := newSession(t, project)
	t.Setenv("SR12_SID", sess.ID)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, PhoneLedger: true}),
		event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + sess.ID, BaseURL: srv.URL}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "a", Agent: "claude", Dir: project}),
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
	// The reply, sent while nothing ran the session.
	mu.Lock()
	replyAt = time.Now()
	mu.Unlock()
	for !time.Now().After(replyAt) {
	}

	// The next serve finishes the stop while the service has a bad moment.
	down.Store(downAtStop)
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if _, _, err := stopAgents(project, info.Path, s.stopConfigs(info), logf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(logged, "\n"), "not told") != downAtStop {
		t.Fatalf("setup: the stop should have told nothing; log %q", logged)
	}
	down.Store(false)

	// The project's reply watcher, with the service back.
	host := &sr12Host{resumed: make(chan integration.PhoneMessage, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, project, host, nil)
	defer func() { cancel(); stop() }()
	listed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range seen {
			if strings.HasPrefix(s, "GET /api/v1/threads?") {
				return true
			}
		}
		return false
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-host.resumed:
			if m.ID != "u1" {
				t.Fatalf("resumed with %q, want the waiting reply u1", m.ID)
			}
			return
		case <-deadline:
			ran := listed()
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("the reply sent while the session was stopped was never taken: the stop could not tell the phone, did not leave the thread with the watcher, and its writes made the record newer than the reply, so the sweep passes it over (sweep ran: %v); requests %q", ran, seen)
		}
	}
}
