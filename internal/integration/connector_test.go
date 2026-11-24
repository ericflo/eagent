package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func fixtureCommand(t *testing.T) (*settings.Service, control.Grant, command) {
	t.Helper()
	s := &settings.Service{Project: t.TempDir()}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(6)}}}
	raw, _ := json.Marshal(p)
	return s, g, command{ID: randomID(), UserID: "test-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
}

func TestDuplicateCommandReturnsOriginalResultWithoutWriting(t *testing.T) {
	s, g, q := fixtureCommand(t)
	first, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || first.Status != "succeeded" {
		t.Fatal(first.Status, err)
	}
	old, err := os.Stat(config.File(s.Project))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := executeCommand(context.Background(), s, g, q, true)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first.Result)
	b, _ := json.Marshal(retry.Result)
	if string(a) != string(b) {
		t.Fatal("retry changed original result")
	}
	now, _ := os.Stat(config.File(s.Project))
	if !old.ModTime().Equal(now.ModTime()) {
		t.Fatal("duplicate command repeated file write")
	}
	raw, err := os.ReadFile(filepath.Join(s.Project, ".agents/eagent/settings-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if json.Unmarshal(raw, &event) != nil || event["event"] != "settings.succeeded" {
		t.Fatal("audit not recorded")
	}
}

func TestReconcileSaveBeforeAcknowledgement(t *testing.T) {
	s, g, q := fixtureCommand(t)
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	desired, etag, _, err := s.Prepare(q.Proposal, g)
	if err != nil {
		t.Fatal(err)
	}
	j := commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: g.Key, Status: "prepared", BeforeETag: etag, Desired: desired, DesiredETag: artifact.Digest([]byte(desired))}
	if err := writeJSONAtomic(filepath.Join(stateDir(s.Project), "commands", q.ID+".json"), j); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.SaveRaw(desired, etag, true); err != nil {
		t.Fatal(err)
	}
	editor.Close()
	got, err := executeCommand(context.Background(), s, g, q, true)
	if err != nil || got.Status != "succeeded" {
		t.Fatal("did not reconcile completed write", got.Status, err)
	}
}

func TestMissingJournalNeverRepeatsAnUncertainCommand(t *testing.T) {
	s, g, q := fixtureCommand(t)
	got, err := executeCommand(context.Background(), s, g, q, true)
	if err != nil || got.Status != "unknown" {
		t.Fatal(got.Status, err)
	}
	if _, err := os.Stat(config.File(s.Project)); !os.IsNotExist(err) {
		t.Fatal("uncertain delivery changed configuration")
	}
}

func TestConnectorAddsAndRefreshesLocalServer(t *testing.T) {
	modelName := "local-v1"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"` + modelName + `","context_length":65536}]}`))
		case "/v1/chat/completions", "/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing field"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	s := &settings.Service{Project: t.TempDir()}
	g := s.Grant()
	v0, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	// Add server via connector settings.apply (/add_server_url).
	p := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   settings.SchemaVersion,
		ExpectedVersion: v0.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/add_server_url", Value: mock.URL}},
	}
	raw, _ := json.Marshal(p)
	q := command{ID: randomID(), UserID: "u1", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
	res, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("add server command: status=%s result=%v err=%v", res.Status, res.Result, err)
	}
	servers, _ := config.LoadServers(s.Project)
	if len(servers) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	// Swap the model on the mock server and invoke settings.refresh.
	modelName = "local-v2"
	v1, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	rp := control.Proposal{Operation: "settings.refresh", SchemaVersion: settings.SchemaVersion, ExpectedVersion: v1.Snapshot.Version}
	rraw, _ := json.Marshal(rp)
	rq := command{ID: randomID(), UserID: "u1", Proposal: rp, Digest: artifact.Digest(rraw), Expires: time.Now().Add(time.Minute)}
	rres, err := executeCommand(context.Background(), s, g, rq, false)
	if err != nil || rres.Status != "succeeded" {
		t.Fatalf("refresh command: status=%s result=%v err=%v", rres.Status, rres.Result, err)
	}
	v2, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range v2.Descriptor.Suggestions["models"] {
		if m.Value == "local-v2" && m.Group == servers[0].ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("settings.refresh did not discover newly loaded local model: %+v", v2.Descriptor.Suggestions["models"])
	}
}
