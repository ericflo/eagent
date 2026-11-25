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
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(60)}}}
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

func TestConnectorSavePresetStandaloneCombinedAndUndo(t *testing.T) {
	s := &settings.Service{Project: t.TempDir()}
	g := s.Grant()
	v0, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Standalone /save_preset runs through journal.Resource and offers undo.
	p1 := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   settings.SchemaVersion,
		ExpectedVersion: v0.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/save_preset", Value: "snap-1"}},
	}
	raw1, _ := json.Marshal(p1)
	q1 := command{ID: randomID(), UserID: "u1", Proposal: p1, Digest: artifact.Digest(raw1), Expires: time.Now().Add(time.Minute)}
	res1, err := executeCommand(context.Background(), s, g, q1, false)
	if err != nil || res1.Status != "succeeded" {
		t.Fatalf("standalone save_preset: status=%s result=%v err=%v", res1.Status, res1.Result, err)
	}
	if _, err := os.Stat(config.BundlePath(s.Project, "snap-1")); err != nil {
		t.Fatalf("snap-1 not written: %v", err)
	}
	if _, ok := res1.Result["undo"].(map[string]any); !ok {
		t.Fatalf("expected undo offer on standalone /save_preset: %#v", res1.Result)
	}

	// 2. Combined config edit + /save_preset saves both without leaking save_preset
	// into config.json or undo.
	v1, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p2 := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   settings.SchemaVersion,
		ExpectedVersion: v1.Snapshot.Version,
		Edits: []control.Edit{
			{Op: "set", Key: "/max_calls_per_turn", Value: float64(8)},
			{Op: "set", Key: "/save_preset", Value: "snap-8"},
		},
	}
	raw2, _ := json.Marshal(p2)
	q2 := command{ID: randomID(), UserID: "u1", Proposal: p2, Digest: artifact.Digest(raw2), Expires: time.Now().Add(time.Minute)}
	res2, err := executeCommand(context.Background(), s, g, q2, false)
	if err != nil || res2.Status != "succeeded" {
		t.Fatalf("combined save_preset: status=%s result=%v err=%v", res2.Status, res2.Result, err)
	}
	loaded, err := config.Load(s.Project, "snap-8")
	if err != nil || loaded.MaxCallsPerTurn != 8 {
		t.Fatalf("snap-8 = %+v err=%v", loaded, err)
	}
	rawCfg, _ := os.ReadFile(config.File(s.Project))
	if string(rawCfg) == "" || config.Resolve(s.Project, "", "").Effective.MaxCallsPerTurn != 8 {
		t.Fatalf("config.json = %s", rawCfg)
	}
	// Undo reverses the config.json edit cleanly (and does not choke on /save_preset).
	undo := reviewedUndo(t, s, g, res2)
	executeExpected(t, s, g, undo, "succeeded")
	if got := config.Resolve(s.Project, "", "").Effective.MaxCallsPerTurn; got != config.Defaults().MaxCallsPerTurn {
		t.Fatalf("after undo max_calls_per_turn = %d", got)
	}
}
