package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func routeFixture(t *testing.T, handler http.HandlerFunc) (*settings.Service, control.Grant, command) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	project := t.TempDir()
	cfg := config.Defaults()
	actor := config.Actor{BaseURL: server.URL + "/v1", Model: "fixture", Protocol: llm.ProtocolChat, APIKeyEnv: "EAGENT_ROUTE_FIXTURE_KEY"}
	cfg.Orchestrator, cfg.Task, cfg.Narrator = actor, actor, actor
	t.Setenv("EAGENT_ROUTE_FIXTURE_KEY", "fixture-private-key")
	if err := writeJSONAtomic(config.File(project), cfg); err != nil {
		t.Fatal(err)
	}
	s := &settings.Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "route.test", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"actor": "task", "route": "primary"}}
	raw, _ := json.Marshal(p)
	return s, g, command{ID: randomID(), UserID: "fixture-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
}

func TestRemoteRouteUsesSharedProbeWithoutHoldingSettingsWriter(t *testing.T) {
	var calls atomic.Int32
	var service *settings.Service
	handlerError := make(chan error, 1)
	s, g, q := routeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		editor, err := config.LockEditor(ctx, service.Project)
		if err == nil {
			editor.Close()
		}
		handlerError <- err
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != float64(4096) || r.Header.Get("Authorization") != "Bearer fixture-private-key" {
			http.Error(w, "bad fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"ping-1","type":"function","function":{"name":"ping","arguments":"{\"message\":\"pong\"}"}}]}}],"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	})
	service = s
	first, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || first.Status != "succeeded" {
		t.Fatal(first.Status, first.Result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("probe did not make exactly one call: %d", calls.Load())
	}
	if err := <-handlerError; err != nil {
		t.Fatalf("probe held the settings writer during HTTP: %v", err)
	}
	result, ok := first.Result["route_test"].(settings.RouteTestResult)
	if !ok || result.Status != "tool_call" || result.Usage.Input != 8 {
		t.Fatalf("wrong route result: %#v", first.Result)
	}
	retry, err := executeCommand(context.Background(), s, g, q, true)
	if err != nil || retry.Status != "succeeded" || calls.Load() != 1 {
		t.Fatal("acknowledgement retry repeated a paid test", err)
	}
}

func TestRemoteRouteRequiresCostGrantAndNeverRepeatsUnknownCall(t *testing.T) {
	var calls atomic.Int32
	s, g, q := routeFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) })
	restricted := g
	restricted.Classes = []string{"preference"}
	got, err := executeCommand(context.Background(), s, restricted, q, false)
	if err != nil || got.Status != "rejected" || calls.Load() != 0 {
		t.Fatal("route did not require the cost grant", got.Status, err)
	}
	q.ID = randomID()
	route, err := s.PrepareRoute(q.Proposal, g)
	if err != nil {
		t.Fatal(err)
	}
	journal := commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: g.Key, Status: "prepared", Route: &route, RouteStarted: true}
	if err := writeJSONAtomic(filepath.Join(stateDir(s.Project), "commands", q.ID+".json"), journal); err != nil {
		t.Fatal(err)
	}
	got, err = executeCommand(context.Background(), s, g, q, true)
	if err != nil || got.Status != "unknown" || calls.Load() != 0 {
		t.Fatal("uncertain paid request was repeated", got.Status, err)
	}
	q.ID = randomID()
	q.Proposal.Parameters["actor"] = "http://127.0.0.1/private"
	raw, _ := json.Marshal(q.Proposal)
	q.Digest = artifact.Digest(raw)
	got, err = executeCommand(context.Background(), s, g, q, false)
	if err != nil || got.Status != "rejected" || calls.Load() != 0 {
		t.Fatal("route accepted an arbitrary target", got.Status, err)
	}
}

func TestRemoteRouteRedactsEchoedCredentialFromResultAndAudit(t *testing.T) {
	var calls atomic.Int32
	s, g, q := routeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"fixture-private-key must not be exported"}}`, 500)
	})
	got, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || got.Status != "succeeded" || calls.Load() != 1 {
		t.Fatal(got.Status, err)
	}
	raw, _ := json.Marshal(got.Result)
	audit, err := os.ReadFile(filepath.Join(s.Project, ".agents/eagent/settings-audit.jsonl"))
	if err != nil || strings.Contains(string(raw)+string(audit), "fixture-private-key") {
		t.Fatal("route result leaked its credential", err)
	}
	result := got.Result["route_test"].(settings.RouteTestResult)
	if result.Status != "failed" || result.HTTPStatus != 500 {
		t.Fatal("failed provider call was reported as working", result)
	}
}
