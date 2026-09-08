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
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
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
