package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/settings"
)

func TestConnectorRetainsIdentityOnRetryAndReleasesOnShutdown(t *testing.T) {
	for _, failure := range []string{"heartbeat response", "command poll"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("EAGENT_FINALECHAT", "on")
			t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
			project := t.TempDir()
			grant := (&settings.Service{Project: project}).Grant()
			ready, released := make(chan string, 1), make(chan string, 1)
			var mu sync.Mutex
			var instance string
			failed, beats := false, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && r.URL.Path == "/api/v1/connectors/connector" {
					_ = json.NewEncoder(w).Encode(map[string]any{"connector": map[string]any{"id": "connector", "state": "active", "grants": []any{grant}}})
					return
				}
				if r.Header.Get("Authorization") != "Bearer fcc_fixture" {
					t.Error("unexpected account request")
					w.WriteHeader(403)
					return
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				current, _ := body["instance"].(string)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case strings.HasSuffix(r.URL.Path, "/heartbeat"):
					if instance != "" && instance != current {
						t.Error("reconnect changed process identity")
						w.WriteHeader(409)
						return
					}
					instance = current
					beats++
					if failure == "heartbeat response" && !failed {
						failed = true
						w.WriteHeader(503)
						return
					}
				case strings.Contains(r.URL.Path, "/resources/"):
					if current != instance {
						t.Error("published without owning lease")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": map[string]any{"id": "resource", "key": grant.Key}})
					return
				case strings.HasSuffix(r.URL.Path, "/commands/claim"):
					if failure == "command poll" && !failed {
						failed = true
						w.WriteHeader(503)
						return
					}
					ready <- instance
					mu.Unlock()
					<-r.Context().Done()
					mu.Lock()
					return
				case strings.HasSuffix(r.URL.Path, "/release"):
					if r.Context().Err() != nil {
						t.Error("release used cancelled context")
					}
					released <- current
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"base_url": srv.URL}}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(project, "connector.json")
			if err := writeJSONAtomic(path, connectorConfig{ID: "connector", Secret: "fcc_fixture", BaseURL: srv.URL}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runConnectorAt(ctx, project, "", path, nil) }()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("retry did not recover")
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("shutdown: %v", err)
			}
			select {
			case got := <-released:
				mu.Lock()
				if got == "" || got != instance || beats != 2 {
					t.Errorf("release=%q instance=%q heartbeats=%d", got, instance, beats)
				}
				mu.Unlock()
			default:
				t.Fatal("shutdown did not release the process lease")
			}
		})
	}
}
