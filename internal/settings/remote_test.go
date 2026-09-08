package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func TestArchiveAndRestrictedConnectorShareConfigurationVersion(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	grant := s.Grant()
	before, err := s.RemoteSnapshot(grant)
	if err != nil {
		t.Fatal(err)
	}
	grant.Classes = []string{"preference"}
	after, err := s.RemoteSnapshot(grant)
	if err != nil {
		t.Fatal(err)
	}
	if before.Snapshot.Version != after.Snapshot.Version {
		t.Fatal("the same configuration has different archive and connector versions")
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: before.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(4)}}}
	if _, _, _, err := s.Prepare(p, grant); err != nil {
		t.Fatal("permitted edit could not use the captured configuration version", err)
	}
	p.Edits = []control.Edit{{Op: "set", Key: "/allow_outside_project", Value: true}}
	if _, _, _, err := s.Prepare(p, grant); err == nil {
		t.Fatal("shared configuration version bypassed the narrower grant")
	}
}

func TestRemoteAndLocalEditorsShareVersionLock(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(4)}}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := s.Apply(context.Background(), p, g); results <- err }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		var stale *config.ErrConflict
		if err == nil {
			success++
		} else if errors.As(err, &stale) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("lost version check: %d success, %d conflict", success, conflict)
	}
	res := config.Resolve(s.Project, "", "")
	if res.Effective.TaskConcurrency != 4 {
		t.Fatal("edit not persisted")
	}
	old := res.File.ETag
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"task_concurrency": json.RawMessage("5")}, old, true); err != nil {
		t.Fatal(err)
	}
	p.ExpectedVersion = view.Snapshot.Version
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("remote edit overwrote local change")
	}
}

func TestRemoteRejectsEnvironmentOverridesAndUnknownRoutes(t *testing.T) {
	t.Setenv("EAGENT_TASK_CONCURRENCY", "9")
	s := &Service{Project: t.TempDir()}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range view.Descriptor.Fields {
		if field.Key == "/task_concurrency" && field.Writable {
			t.Fatal("environment field advertised writable")
		}
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(4)}}}
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("environment overridden")
	}
	p.Edits = []control.Edit{{Op: "set", Key: "/task/base_url", Value: "https://unapproved.invalid"}}
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("unknown credential destination allowed")
	}
	p.Edits = []control.Edit{{Op: "set", Key: "/unknown_secret", Value: "x"}}
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("unknown field allowed")
	}
	if _, err := os.Stat(config.File(s.Project)); !os.IsNotExist(err) {
		t.Fatal("rejected edits wrote a config")
	}
}

func TestRemoteUnsetPreservesUnrelatedFieldsAndSecrets(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	if _, err := config.SaveRaw(s.Project, "{\"task_concurrency\":7,\"persona\":\"concise\",\"private_unknown\":\"secret-never-export\"}", "", true); err != nil {
		t.Fatal(err)
	}
	g := s.Grant()
	v, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "secret-never-export") {
		t.Fatal("snapshot exposed unknown config value")
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: v.Snapshot.Version, Edits: []control.Edit{{Op: "unset", Key: "/task_concurrency"}}}
	_, _, err = s.Apply(context.Background(), p, g)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(config.File(s.Project))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	if _, ok := saved["task_concurrency"]; ok {
		t.Fatal("unset did not remove override")
	}
	if saved["persona"] != "concise" || saved["private_unknown"] != "secret-never-export" {
		t.Fatal("unrelated settings lost")
	}
}
