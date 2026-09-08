package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

func TestKillSwitchOverridesArtifactOptInAndStoredPairing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected outbound request", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	project := t.TempDir()
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"artifacts": true, "enabled": false, "base_url": server.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(connectorPath(project), connectorConfig{ID: "fixture", Secret: "fcc_fixture", BaseURL: server.URL, ApprovalURL: server.URL + "/approve"}); err != nil {
		t.Fatal(err)
	}
	session, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(runtimeConnectorDir(project), session.ID+".json"), connectorConfig{ID: "live-fixture", Secret: "fcc_fixture", BaseURL: server.URL, Session: session.ID}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	for _, value := range []string{"off", "0", "false", "no", "OFF", " False "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("EAGENT_FINALECHAT", value)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stopPublisher := StartPublisher(ctx, project, "test", nil, session.ID)
			stopConnector := StartConnector(ctx, project, nil)
			stopConnector()
			stopPublisher() // Includes the normal final publication attempt.
			for _, force := range []bool{false, true} {
				if _, err := Publish(ctx, project, session.ID, "test", force); !errors.Is(err, finalechat.ErrDisabled) {
					t.Fatalf("Publish(force=%v): %v", force, err)
				}
			}
			if _, err := RecreatePublication(ctx, project, session.ID, "test"); !errors.Is(err, finalechat.ErrDisabled) {
				t.Fatalf("RecreatePublication: %v", err)
			}
			if _, err := Pair(ctx, project, "fixture"); !errors.Is(err, finalechat.ErrDisabled) {
				t.Fatalf("Pair: %v", err)
			}
			if _, err := PairSession(ctx, project, session.ID); !errors.Is(err, finalechat.ErrDisabled) {
				t.Fatalf("PairSession: %v", err)
			}
			if err := runConnectorAt(ctx, project, session.ID, filepath.Join(runtimeConnectorDir(project), session.ID+".json"), nil); !errors.Is(err, finalechat.ErrDisabled) {
				t.Fatalf("runtime connector: %v", err)
			}
			if err := RunConnector(ctx, project, nil); !errors.Is(err, finalechat.ErrDisabled) {
				t.Fatalf("RunConnector: %v", err)
			}
			if n := calls.Load(); n != 0 {
				t.Fatalf("disabled integration made %d HTTP requests", n)
			}
		})
	}
	// Disabling only chat mirroring must still allow a separately paired
	// settings connector to reach its configured server.
	t.Setenv("EAGENT_FINALECHAT", "")
	if !IsArtifactEnabled(project) {
		t.Fatal("chat mirroring preference disabled artifacts")
	}
	if err := RunConnector(context.Background(), project, nil); err == nil || errors.Is(err, finalechat.ErrDisabled) {
		t.Fatalf("expected the fixture server's response, got %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected one settings request with mirroring disabled, got %d", n)
	}
}
