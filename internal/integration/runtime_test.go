package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

func TestRuntimeConnectorPublishesDeliversAndReconciles(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "on")
	project, session := t.TempDir(), "1788800000000"
	var applied atomic.Int32
	owner, err := runtimecontrol.New(project, session, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error {
		applied.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ownerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				ownerDone <- nil
				return
			case <-ticker.C:
				if err := owner.Poll(); err != nil {
					ownerDone <- err
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		if err := <-ownerDone; err != nil {
			t.Error(err)
		}
	}()
	grant, _ := runtimecontrol.Grant(project, session)
	view, err := runtimeSnapshot(project, session, grant)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, Generation: view.Generation, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(7)}}}
	raw, _ := json.Marshal(p)
	q := command{ID: randomID(), UserID: "owner", ResourceID: "resource", Proposal: p, Digest: artifact.Digest(raw), ClaimToken: "claim", Expires: time.Now().Add(time.Minute)}
	claimed := false
	results := make(chan map[string]any, 1)
	publications := make(chan control.Snapshot, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fcc_test" {
			t.Error("wrong credential")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var response any = map[string]any{}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/connectors/connector":
			response = map[string]any{"connector": connectorInfo{ID: "connector", State: "active", Grants: []control.Grant{grant}}}
		case strings.HasSuffix(r.URL.Path, "/heartbeat"), strings.HasSuffix(r.URL.Path, "/renew"), strings.HasSuffix(r.URL.Path, "/release"):
		case strings.Contains(r.URL.Path, "/resources/"):
			var body struct {
				Descriptor control.Descriptor `json:"descriptor"`
				Snapshot   control.Snapshot   `json:"snapshot"`
				Generation string             `json:"generation"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Generation != p.Generation || body.Descriptor.Validate(grant) != nil || !body.Snapshot.RuntimeKnown {
				t.Error("invalid live resource")
			}
			publications <- body.Snapshot
			response = map[string]any{"resource": resourceInfo{ID: "resource", Key: grant.Key, Generation: p.Generation}}
		case strings.HasSuffix(r.URL.Path, "/commands/claim"):
			if claimed {
				<-r.Context().Done()
				return
			}
			claimed = true
			response = map[string]any{"command": q, "resource": resourceInfo{ID: "resource", Key: grant.Key, Generation: p.Generation}}
		case strings.HasSuffix(r.URL.Path, "/result"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			results <- body
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	path := filepath.Join(runtimeConnectorDir(project), session+".json")
	if err := writeJSONAtomic(path, connectorConfig{ID: "connector", Secret: "fcc_test", BaseURL: srv.URL, Session: session}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runConnectorAt(ctx, project, session, path, nil); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case result := <-results:
		if result["status"] != "succeeded" || applied.Load() != 1 {
			t.Fatalf("not acknowledged by runtime: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("connector timed out")
	}
	for i := 0; i < 2; i++ {
		select {
		case snapshot := <-publications:
			if i == 1 && snapshot.Saved["/task_concurrency"] != float64(7) {
				t.Fatal("new runtime snapshot was not published")
			}
		case <-ctx.Done():
			t.Fatal("publication timed out")
		}
	}
	cancel()
	<-done
	// Simulate a connector crash after owner acknowledgement but before its
	// own outcome commit. Reconciliation must read the original native result.
	journalPath := filepath.Join(stateDir(project), "runtime-commands", q.ID+".json")
	if err := writeJSONAtomic(journalPath, commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: grant.Key, Status: "prepared"}); err != nil {
		t.Fatal(err)
	}
	recovered, err := executeRuntimeCommand(context.Background(), project, session, grant, q, true)
	if err != nil || recovered.Status != "succeeded" || applied.Load() != 1 {
		t.Fatalf("reconciliation: %+v %v", recovered, err)
	}
	q.ID = randomID()
	unknown, err := executeRuntimeCommand(context.Background(), project, session, grant, q, true)
	if err != nil || unknown.Status != "unknown" || applied.Load() != 1 {
		t.Fatal("unconfirmed command was repeated", unknown, err)
	}
	if _, err := os.Stat(config.File(project)); !os.IsNotExist(err) {
		t.Fatal("runtime command wrote project defaults")
	}
}

func TestSessionRoutesReflectActualLog(t *testing.T) {
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{
		Session: sess.ID, Cwd: project,
		Models:    map[string]string{"orchestrator": "reflection/beam-1.0-501b-a23b", "task": "z-ai/glm-5.3", "narrator": "deepseek/deepseek-v4.1-flash-1017"},
		Endpoints: map[string]string{"orchestrator": "https://openrouter.ai/api/v1", "task": "https://openrouter.ai/api/v1", "narrator": "https://openrouter.ai/api/v1"},
		Efforts:   map[string]string{"orchestrator": "xhigh", "task": "high", "narrator": "low"},
		Config:    "muse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// A fallback switched the narrator to another host mid-session.
	_, err = sess.Append(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: "narrator", BaseURL: "https://api.together.xyz/v1", Model: "deepseek-ai/DeepSeek-V4.1-Flash-1017", Reason: "fallback"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	routes, cfgName, ok := sessionRoutes(project, sess.ID)
	if !ok {
		t.Fatal("no routes")
	}
	if cfgName != "muse" {
		t.Fatalf("config = %q", cfgName)
	}
	orch := routes["orchestrator"].(map[string]any)
	if orch["model"] != "reflection/beam-1.0-501b-a23b" || orch["effort"] != "xhigh" || orch["host"] != "https://openrouter.ai/api/v1" {
		t.Fatalf("orchestrator route = %v", orch)
	}
	narr := routes["narrator"].(map[string]any)
	if narr["model"] != "deepseek-ai/DeepSeek-V4.1-Flash-1017" || narr["host"] != "https://api.together.xyz/v1" {
		t.Fatalf("fallback route not reflected: %v", narr)
	}

	// And the runtime snapshot for a paired session carries them in details.
	owner, err := runtimecontrol.New(project, sess.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	grant, _ := runtimecontrol.Grant(project, sess.ID)
	view, err := runtimeSnapshot(project, sess.ID, grant)
	if err != nil {
		t.Fatal(err)
	}
	rd, ok := view.Snapshot.Details["routes"].(map[string]any)
	if !ok || rd["orchestrator"].(map[string]any)["model"] != "reflection/beam-1.0-501b-a23b" {
		t.Fatalf("runtime snapshot routes = %v", view.Snapshot.Details["routes"])
	}
	if view.Snapshot.Details["config"] != "muse" {
		t.Fatalf("runtime snapshot config = %v", view.Snapshot.Details["config"])
	}
}

func TestRunningSessionsDiscoversLiveHeartbeatOnly(t *testing.T) {
	project := t.TempDir()
	// A session with no runtime status is not "running".
	dead, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	if got := runningSessions(project); len(got) != 0 {
		t.Fatalf("runningSessions = %v, want none", got)
	}
	// A session whose harness started the runtime control heartbeats.
	live, err := store.Create(store.Root(project), time.UnixMilli(1788800000001))
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := runtimecontrol.New(project, live.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := runningSessions(project)
	if len(got) != 1 || got[0] != live.ID {
		t.Fatalf("runningSessions = %v, want [%s]", got, live.ID)
	}
	// When the harness stops, the heartbeat lapses and the session drops out.
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	// Close heartbeats with AvailableUntil zeroed; ReadStatus still returns it,
	// but AvailableUntil.After(now) is false.
	if got := runningSessions(project); len(got) != 0 {
		t.Fatalf("after shutdown runningSessions = %v, want none", got)
	}
}

func TestRequestSessionGrantWidensConnectorGrants(t *testing.T) {
	project := t.TempDir()
	// Account token for settingsAccount.
	t.Setenv("FINALECHAT_TOKEN", "fc_test_account")
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/connect") {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	// settingsAccount resolves the base URL from the token's server; point it at the fake.
	t.Setenv("FINALECHAT_URL", srv.URL)
	stored := []control.Grant{{Key: "proj", Scope: "project", Operations: []string{"settings.apply"}, Classes: []string{"preference"}}}
	want := control.Grant{Key: "proj-session-123", Scope: "session", Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference", "route"}}
	if err := requestSessionGrant(context.Background(), project, "fcc_local_secret", "/api/v1/connectors/abc", stored, want); err != nil {
		t.Fatal(err)
	}
	if gotBody["secret"] != "fcc_local_secret" {
		t.Fatalf("connect body missing the connector secret: %v", gotBody)
	}
	grants, ok := gotBody["requested_grants"].([]any)
	if !ok || len(grants) != 2 {
		t.Fatalf("requested_grants = %v", gotBody)
	}
	second, _ := grants[1].(map[string]any)
	if second["key"] != "proj-session-123" || second["scope"] != "session" {
		t.Fatalf("appended grant = %v", second)
	}
	classes, _ := second["classes"].([]any)
	if len(classes) != 2 {
		t.Fatalf("classes = %v", classes)
	}
}

// End-to-end: an auto-discovered session worker (no pairing file) widens the
// connector grant via /connect, publishes the session-scoped resource with
// the session's actual routes, and binds a session settings website to it.
func TestAutoDiscoveredSessionWorkerPublishesSessionResourceAndSite(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "on")
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	project := t.TempDir()
	// A running session whose log says it runs custom routes.
	sess, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{
		Session: sess.ID, Cwd: project,
		Models:    map[string]string{"orchestrator": "anthropic/claude-sonnet-5-5", "task": "deepseek/deepseek-v4.1-flash-1017", "narrator": "anthropic/claude-haiku-5-5"},
		Endpoints: map[string]string{"orchestrator": "https://api.anthropic.com/v1", "task": "https://openrouter.ai/api/v1", "narrator": "https://api.anthropic.com/v1"},
		Efforts:   map[string]string{"orchestrator": "high", "task": "high", "narrator": "low"},
		Config:    "muse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := runtimecontrol.New(project, sess.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	projectGrant := (&settings.Service{Project: project}).Grant()
	sessionGrant, err := runtimecontrol.Grant(project, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	widened := false
	var connectGrants []any
	var resourceBody map[string]any
	var bindingBody map[string]any
	resourceSeen := make(chan struct{}, 1)
	bindingSeen := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && path == "/api/v1/connectors/connector":
			grants := []any{projectGrant}
			if widened {
				grants = append(grants, sessionGrant)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"connector": map[string]any{"id": "connector", "state": "active", "grants": grants}})
		case r.Method == "POST" && strings.HasSuffix(path, "/connect"):
			if body["secret"] != "fcc_fixture" {
				w.WriteHeader(403)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "forbidden", "message": "You do not have access to this resource."}})
				return
			}
			widened = true
			connectGrants, _ = body["requested_grants"].([]any)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.Method == "POST" && strings.HasSuffix(path, "/heartbeat"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.Method == "POST" && strings.HasSuffix(path, "/release"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.Method == "GET" && strings.Contains(path, "/commands/claim"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.Method == "PUT" && strings.Contains(path, "/resources/"):
			resourceBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": map[string]any{"id": "sess-resource", "key": sessionGrant.Key}})
			select {
			case resourceSeen <- struct{}{}:
			default:
			}
		case r.Method == "PUT" && strings.Contains(path, "/bindings/"):
			bindingBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{})
			select {
			case bindingSeen <- struct{}{}:
			default:
			}
		case r.Method == "PUT" && strings.Contains(path, "/settings-resources/") && strings.HasSuffix(path, "/website"):
			_ = json.NewEncoder(w).Encode(map[string]any{"artifact": map[string]any{"id": "art-1"}})
		case r.Method == "GET" && strings.HasPrefix(path, "/api/v1/artifacts/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"artifact": map[string]any{"id": "art-1", "current_revision_id": nil}, "revision": nil})
		case r.Method == "POST" && strings.Contains(path, "/blobs/check"):
			_ = json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})
		case r.Method == "PUT" && strings.Contains(path, "/blobs/"):
			w.WriteHeader(200)
		case r.Method == "POST" && strings.HasSuffix(path, "/revisions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": map[string]any{"id": "rev-1"}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("FINALECHAT_URL", srv.URL)
	// Project connector credential file; the session worker derives from it.
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(connectorConfig{ID: "connector", Secret: "fcc_fixture", BaseURL: srv.URL, Session: ""})
	if err := os.WriteFile(filepath.Join(dir, "finalechat-connector.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stop := startConnectorAt(ctx, project, sess.ID, "", filepath.Join(stateDir(project), "auto-"+sess.ID+".lock"), t.Logf)
	defer stop()
	select {
	case <-resourceSeen:
	case <-ctx.Done():
		t.Fatal("session resource was not published")
	}
	select {
	case <-bindingSeen:
	case <-ctx.Done():
		t.Fatal("session settings website was not bound")
	}
	mu.Lock()
	defer mu.Unlock()
	if !widened || len(connectGrants) != 2 {
		t.Fatalf("grant not widened: widened=%v grants=%v", widened, connectGrants)
	}
	details, _ := resourceBody["snapshot"].(map[string]any)["details"].(map[string]any)
	routes, _ := details["routes"].(map[string]any)
	orch, _ := routes["orchestrator"].(map[string]any)
	if orch["model"] != "anthropic/claude-sonnet-5-5" || orch["effort"] != "high" {
		t.Fatalf("session resource routes = %v", routes)
	}
	if details["config"] != "muse" {
		t.Fatalf("config = %v", details["config"])
	}
	desc, _ := resourceBody["descriptor"].(map[string]any)
	fields, _ := desc["fields"].([]any)
	hasRouteField := false
	for _, f := range fields {
		if fm, ok := f.(map[string]any); ok && fm["key"] == "/routes/orchestrator/model" {
			hasRouteField = true
		}
	}
	if !hasRouteField {
		t.Fatal("session resource descriptor lacks /routes/orchestrator/model")
	}
	if bindingBody["resource_id"] != "sess-resource" || bindingBody["revision_id"] != "rev-1" {
		t.Fatalf("binding = %v", bindingBody)
	}
}
