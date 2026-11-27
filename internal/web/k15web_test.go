package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/llm"
)

// k15Model is a configuration whose model is a test server that tells
// calls on its channel and answers none.
func k15Model(t *testing.T, calls chan struct{}) config.Config {
	t.Helper()
	quit := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case calls <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-quit:
		}
		http.Error(w, "no answers in this test", http.StatusServiceUnavailable)
	}))
	t.Cleanup(model.Close)
	t.Cleanup(func() { close(quit) })
	t.Setenv("EAGENT_TEST_KEY", "x")
	cfg := config.Defaults()
	cfg.Model.BaseURL, cfg.Model.APIKeyEnv, cfg.Model.Protocol, cfg.Model.Model, cfg.Model.Fallback = model.URL, "EAGENT_TEST_KEY", llm.ProtocolChat, "m", nil
	off := false
	cfg.Finalechat.Enabled, cfg.Finalechat.Artifacts = &off, false
	return cfg
}

// k15Project writes the project's own configuration on model a and a
// saved one, "other", on model b.
func k15Project(t *testing.T, project string) (a, b chan struct{}) {
	t.Helper()
	a, b = make(chan struct{}, 64), make(chan struct{}, 64)
	if _, err := config.Write(project, k15Model(t, a)); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveBundle(project, "other", "", k15Model(t, b)); err != nil {
		t.Fatal(err)
	}
	return a, b
}

// k15Calls waits for a call to model want; a call to other fails.
func k15Calls(t *testing.T, want, other chan struct{}, what string) {
	t.Helper()
	select {
	case <-want:
	case <-other:
		t.Fatalf("%s called the other configuration's model", what)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s called no model", what)
	}
}

// k15Drain empties the channels of calls made so far.
func k15Drain(chs ...chan struct{}) {
	for _, ch := range chs {
		for len(ch) > 0 {
			<-ch
		}
	}
}

// k15Start starts a session from the page with body.
func k15Start(t *testing.T, s *Server, body map[string]any) string {
	t.Helper()
	w := do(s, "POST", "/api/sessions", body, nil)
	var out struct {
		ID string `json:"id"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ID == "" {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	return out.ID
}

// A session started in the browser on a picked configuration ("other"),
// brought back after a restart, runs on that configuration, not on the
// server's own. It used to come back on the server's own.
func TestK15ASessionStartedOnAPickedBundleIsRestoredOnIt(t *testing.T) {
	s, project := newTestServer(t)
	a, b := k15Project(t, project)
	id := k15Start(t, s, map[string]any{"prompt": "hello", "config": "other"})
	s.rememberHosted()
	s.Shutdown(5 * time.Second)
	k15Drain(a, b)
	next := New(project, nil)
	t.Cleanup(func() { next.Shutdown(5 * time.Second) })
	next.restoreHosted(context.Background())
	if !next.isHosted(id) {
		t.Fatal("setup: the session was not brought back")
	}
	if w := do(next, "POST", "/api/sessions/"+id+"/message", map[string]any{"text": "go on"}, nil); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	k15Calls(t, b, a, "the session brought back after the restart")
}

// The same session resumed from the phone (a reply in its thread) runs on
// the configuration it was started on: on the server that ran it.
func TestK15ASessionStartedOnAPickedBundleIsResumedFromThePhoneOnIt(t *testing.T) {
	s, project := newTestServer(t)
	a, b := k15Project(t, project)
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	id := k15Start(t, s, map[string]any{"prompt": "hello", "config": "other"})
	s.mu.Lock()
	h := s.running[id]
	s.mu.Unlock()
	h.cancel()
	<-h.done
	k15Drain(a, b)
	if err := s.Resume(id, integration.PhoneMessage{Text: "from the phone"}); err != nil {
		t.Fatal(err)
	}
	k15Calls(t, b, a, "the session resumed from the phone")
}

// And on the next server, which knows it from the session's record.
func TestK15ASessionStartedOnAPickedBundleIsResumedFromThePhoneOnItAfterARestart(t *testing.T) {
	s, project := newTestServer(t)
	a, b := k15Project(t, project)
	id := k15Start(t, s, map[string]any{"prompt": "hello", "config": "other"})
	s.Shutdown(5 * time.Second)
	k15Drain(a, b)
	next := New(project, nil)
	t.Cleanup(func() { next.Shutdown(5 * time.Second) })
	if err := next.Resume(id, integration.PhoneMessage{Text: "from the phone again"}); err != nil {
		t.Fatal(err)
	}
	k15Calls(t, b, a, "the session resumed from the phone by the next server")
}

// A session started with no configuration picked runs on the server's own
// and, brought back by a server whose own is another, on that one, as
// before: what it was started on is not pinned.
func TestK15ASessionStartedWithNoPickIsRestoredOnTheServersOwn(t *testing.T) {
	s, project := newTestServer(t)
	a, b := k15Project(t, project)
	s.Bundle = "other" // this server's own: eagent serve --config other
	id := k15Start(t, s, map[string]any{"prompt": "hello"})
	s.rememberHosted()
	s.Shutdown(5 * time.Second)
	k15Drain(a, b)
	next := New(project, nil) // its own: the project's
	t.Cleanup(func() { next.Shutdown(5 * time.Second) })
	next.restoreHosted(context.Background())
	if !next.isHosted(id) {
		t.Fatal("setup: the session was not brought back")
	}
	if w := do(next, "POST", "/api/sessions/"+id+"/message", map[string]any{"text": "go on"}, nil); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	k15Calls(t, a, b, "the session brought back after the restart")
}
