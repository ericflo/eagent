package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
)

// Helpers of the relay porting group. Every top-level name starts with
// "relay" so the other groups' helpers cannot collide with them.

// relayRunBatch runs a session with no terminal to its end with the given
// configuration and options, and returns it.
func relayRunBatch(t *testing.T, cfg config.Config, opts Options) (*Runtime, *fakeUI) {
	t.Helper()
	ui := &fakeUI{}
	rt, err := New(cfg, opts, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})
	select {
	case code := <-done:
		done <- code
		if code != 0 {
			t.Fatalf("exit code %d; said %q; logs %v", code, ui.said(), ui.logged())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the session did not end; said %q; logs %v", ui.said(), ui.logged())
	}
	return rt, ui
}

// relayRunLive runs a session in the background until the test ends.
func relayRunLive(t *testing.T, rt *Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("the session did not stop")
		}
	})
}

// relaySaid waits until the user has been told something containing want.
func relaySaid(t *testing.T, ui *fakeUI, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(ui.said(), " | "), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never said %q: %q (logs %q)", want, ui.said(), ui.logged())
}

// relayFactory gives every launch and every reattach a fresh fake seat, and
// records what was launched.
type relayFactory struct {
	mu       sync.Mutex
	reply    string
	seats    []*fakeSeat
	specs    []relay.Spec
	attached []string
	// onAttach, when set, runs on each reattached seat before it is
	// returned (a backlog the agent's transcript would replay).
	onAttach func(*fakeSeat)
}

func useRelayFactory(t *testing.T, reply string) *relayFactory {
	t.Helper()
	f := &relayFactory{reply: reply}
	oldLaunch, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		s := newFakeSeat(f.reply)
		f.seats = append(f.seats, s)
		f.specs = append(f.specs, spec)
		return s, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		f.mu.Lock()
		s := newFakeSeat(f.reply)
		f.seats = append(f.seats, s)
		f.attached = append(f.attached, target)
		hook := f.onAttach
		f.mu.Unlock()
		if hook != nil {
			hook(s)
		}
		return s, nil
	}
	t.Cleanup(func() { launchRelay, attachRelay = oldLaunch, oldAttach })
	return f
}

func (f *relayFactory) all() []*fakeSeat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeSeat{}, f.seats...)
}

func (f *relayFactory) launched() []relay.Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]relay.Spec{}, f.specs...)
}

// relayPhone is a minimal FinaleChat: one thread, its messages, thread
// meta patches, and the long poll the mirror relies on.
type relayPhone struct {
	srv      *httptest.Server
	mu       sync.Mutex
	cond     *sync.Cond
	messages []map[string]any
	posts    []map[string]any // bodies of POST …/messages
	metas    []map[string]any // PATCH thread meta bodies
	keys     map[string]string
	seq      int
	gets     int
	title    string
}

func newRelayPhone() *relayPhone {
	f := &relayPhone{keys: map[string]string{}}
	f.cond = sync.NewCond(&f.mu)
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *relayPhone) nextID() string { f.seq++; return fmt.Sprintf("id-%04d", f.seq) }

func (f *relayPhone) thread() map[string]any {
	return map[string]any{"id": "t1", "external_id": "eagent:x", "title": f.title}
}

func (f *relayPhone) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer fc_test" {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	path := r.URL.Path
	switch {
	case path == "/api/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": "token", "base_url": f.srv.URL, "push_enabled": true, "version": "test", "features": []string{"activity", "idempotency", "dismiss", "attachments"}, "user": map[string]any{"display_name": "Tester", "email": "t@x", "settings": map[string]any{"notify_all_messages": false, "remote_mode": true}}})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodPost:
		f.mu.Lock()
		if key, _ := body["client_key"].(string); key != "" {
			if id, ok := f.keys[key]; ok {
				var orig map[string]any
				for _, m := range f.messages {
					if m["id"] == id {
						orig = m
					}
				}
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"message": orig, "thread": f.thread(), "created": false})
				return
			}
		}
		sender, _ := body["sender"].(string)
		if sender == "" {
			sender = "agent"
		}
		m := map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": sender, "body": body["body"], "format": "markdown", "importance": body["importance"], "meta": body["meta"], "origin": "token", "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		if key, _ := body["client_key"].(string); key != "" {
			f.keys[key] = m["id"].(string)
		}
		if m["importance"] == nil {
			m["importance"] = "normal"
		}
		f.messages = append(f.messages, m)
		f.posts = append(f.posts, body)
		if title, _ := body["title"].(string); f.title == "" {
			f.title = title
		}
		thread := f.thread()
		f.cond.Broadcast()
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": m, "thread": thread})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodGet:
		after := r.URL.Query().Get("after")
		sender := r.URL.Query().Get("sender")
		wait := 0
		fmt.Sscanf(r.URL.Query().Get("wait"), "%d", &wait)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		defer wakeAt(r, &f.mu, f.cond, deadline)()
		f.mu.Lock()
		f.gets++
		var out []map[string]any
		for {
			out = nil
			seen := after == ""
			for _, m := range f.messages {
				if !seen {
					seen = m["id"] == after
					continue
				}
				if sender == "" || m["sender"] == sender {
					out = append(out, m)
				}
			}
			if len(out) > 0 || wait == 0 || time.Now().After(deadline) {
				break
			}
			f.cond.Wait()
			if r.Context().Err() != nil {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		if out == nil {
			out = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out, "has_more": false, "thread_id": "t1", "timed_out": len(out) == 0})
	case strings.HasSuffix(path, "/activity"):
		_ = json.NewEncoder(w).Encode(map[string]any{"applied": true, "thread": f.thread()})
	case strings.HasPrefix(path, "/api/v1/threads/") && r.Method == http.MethodPatch:
		f.mu.Lock()
		if meta, ok := body["meta"].(map[string]any); ok {
			f.metas = append(f.metas, meta)
		}
		thread := f.thread()
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": thread})
	case strings.HasPrefix(path, "/api/v1/threads/") && r.Method == http.MethodGet:
		f.mu.Lock()
		thread, exists := f.thread(), len(f.posts) > 0
		f.mu.Unlock()
		if !exists {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": thread})
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"` + path + `"}}`))
	}
}

// userReply is the user typing on the phone.
func (f *relayPhone) userReply(text string) {
	f.mu.Lock()
	f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "origin": "session", "body": text, "format": "text", "importance": "normal", "meta": map[string]any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (f *relayPhone) lastMessageID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return ""
	}
	id, _ := f.messages[len(f.messages)-1]["id"].(string)
	return id
}

func (f *relayPhone) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.posts) }
func (f *relayPhone) getCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.gets }

func (f *relayPhone) postsWhere(pred func(map[string]any) bool) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, p := range f.posts {
		if pred(p) {
			out = append(out, p)
		}
	}
	return out
}

func (f *relayPhone) metaPatches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.metas...)
}

// relayPhoneConfig points the mirror at the fake service, lifts the
// package-wide kill switch for this test only, and pins artifact
// publication off in memory and on disk (as the old fakePhoneConfig did).
func relayPhoneConfig(t *testing.T, project, modelURL string, fp *relayPhone) config.Config {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "on")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	cfg := testConfig(modelURL)
	on := true
	cfg.Finalechat.Enabled = &on
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	cfg.Finalechat.Artifacts = false
	raw, err := json.Marshal(map[string]any{"finalechat": map[string]any{"artifacts": false}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}
