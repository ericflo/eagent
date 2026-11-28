package settings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// leakServer is a model server that answers like mockLocalServer and counts
// every request that carries secret in any header.
func leakServer(t *testing.T, secret string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	inner := mockLocalServer(t)
	t.Cleanup(inner.Close)
	var leaks atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, secret) {
					leaks.Add(1)
				}
			}
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, inner.URL+r.URL.Path, r.Body)
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &leaks
}

func applyServers(t *testing.T, s *Service, edits ...control.Edit) error {
	t.Helper()
	v, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Apply(context.Background(), control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: v.Snapshot.Version, Edits: edits}, s.Grant())
	return err
}

// A remote edit of the server list cannot send a local key to an address of
// its choosing: not by probing it, not by saving the pairing and letting a
// later refresh or route use it, and not by moving a registered server that
// has a key to a new address.
func TestRemoteServerEditNeverSendsALocalKeyToANewAddress(t *testing.T) {
	const secret = "sk-canary-must-not-leave"
	t.Setenv("OPENAI_API_KEY", secret)
	t.Setenv("LOCAL_BOX_KEY", secret)
	attacker, leaks := leakServer(t, secret)
	project := t.TempDir()
	s := &Service{Project: project}

	server := func(fields map[string]any) control.Edit {
		return control.Edit{Op: "set", Key: "/servers", Value: []any{fields}}
	}
	if err := applyServers(t, s, server(map[string]any{"url": attacker.URL, "key_env": "OPENAI_API_KEY"})); err == nil {
		t.Error("a remote edit registered a new address with a cloud key variable")
	}
	if err := applyServers(t, s, server(map[string]any{"url": attacker.URL, "key_env": "OPENAI_API_KEY", "skip_probe": true})); err == nil {
		t.Error("a remote edit saved a new address with a cloud key variable unprobed")
	}

	// A server the local user registered with a key of its own.
	own := mockLocalServer(t)
	defer own.Close()
	reg, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: own.URL, ID: "box", KeyEnv: "LOCAL_BOX_KEY"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Moving it to another address keeps neither its key nor its id's trust.
	if err := applyServers(t, s, server(map[string]any{"id": "box", "url": attacker.URL, "key_env": "LOCAL_BOX_KEY"})); err == nil {
		t.Error("a remote edit moved a keyed server to a new address with its key")
	}
	if err := applyServers(t, s, server(map[string]any{"id": "box", "url": reg.Server.BaseURL, "key_env": "OPENAI_API_KEY"})); err == nil {
		t.Error("a remote edit gave a registered server a different key variable")
	}
	// Whatever was saved, nothing later sends the key there either.
	_, _, _ = config.RefreshAllServers(context.Background(), project)
	if n := leaks.Load(); n != 0 {
		t.Fatalf("the fake server received the key %d times", n)
	}
	servers, _ := config.LoadServers(project)
	for _, srv := range servers {
		if strings.HasPrefix(srv.BaseURL, attacker.URL) && srv.KeyEnv != "" {
			t.Fatalf("servers.json pairs %s with %s", srv.BaseURL, srv.KeyEnv)
		}
	}

	// What stays allowed: editing the registered server with its own key,
	// and adding a keyless server.
	if err := applyServers(t, s, server(map[string]any{"id": "box", "url": reg.Server.BaseURL, "key_env": "LOCAL_BOX_KEY", "label": "Renamed"})); err != nil {
		t.Fatalf("editing a registered server with its own key: %v", err)
	}
	if err := applyServers(t, s, control.Edit{Op: "set", Key: "/servers", Value: []any{
		map[string]any{"id": "box", "url": reg.Server.BaseURL, "key_env": "LOCAL_BOX_KEY"},
		map[string]any{"url": attacker.URL},
	}}); err != nil {
		t.Fatalf("adding a keyless server: %v", err)
	}
	if n := leaks.Load(); n != 0 {
		t.Fatalf("the fake server received the key %d times", n)
	}
}
