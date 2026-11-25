package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: before.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/max_calls_per_turn", Value: float64(4)}}}
	if _, _, _, err := s.Prepare(p, grant); err != nil {
		t.Fatal("permitted edit could not use the captured configuration version", err)
	}
	p.Edits = []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENROUTER_API_KEY"}}
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
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/max_calls_per_turn", Value: float64(4)}}}
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
	if res.Effective.MaxCallsPerTurn != 4 {
		t.Fatal("edit not persisted")
	}
	old := res.File.ETag
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"max_calls_per_turn": json.RawMessage("5")}, old, true); err != nil {
		t.Fatal(err)
	}
	p.ExpectedVersion = view.Snapshot.Version
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("remote edit overwrote local change")
	}
}

func TestRemoteRejectsEnvironmentOverridesAndUnknownRoutes(t *testing.T) {
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "90000")
	s := &Service{Project: t.TempDir()}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range view.Descriptor.Fields {
		if field.Key == "/rollover_tokens" && field.Writable {
			t.Fatal("environment field advertised writable")
		}
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/rollover_tokens", Value: float64(40000)}}}
	if _, _, err := s.Apply(context.Background(), p, g); err == nil {
		t.Fatal("environment overridden")
	}
	p.Edits = []control.Edit{{Op: "set", Key: "/model/base_url", Value: "https://unapproved.invalid"}}
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
	if _, err := config.SaveRaw(s.Project, "{\"max_calls_per_turn\":7,\"persona\":\"concise\",\"private_unknown\":\"secret-never-export\"}", "", true); err != nil {
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
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: v.Snapshot.Version, Edits: []control.Edit{{Op: "unset", Key: "/max_calls_per_turn"}}}
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
	if _, ok := saved["max_calls_per_turn"]; ok {
		t.Fatal("unset did not remove override")
	}
	if saved["persona"] != "concise" || saved["private_unknown"] != "secret-never-export" {
		t.Fatal("unrelated settings lost")
	}
}

func TestSnapshotDetailsCarryEffectiveRoutesOverPreset(t *testing.T) {
	project := t.TempDir()
	// Project file builds on preset muse but lowers the model's output budget
	// and gives the summarizer a model of its own.
	raw := `{"preset":"muse","model":{"max_tokens":4096},"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"deepseek/deepseek-v4.1-flash-1017","api_key_env":"OPENROUTER_API_KEY","reasoning_effort":"low"}}}`
	if err := os.MkdirAll(filepath.Join(project, ".agents", "eagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "eagent", "config.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	routes, ok := view.Snapshot.Details["effective_routes"].(map[string]any)
	if !ok {
		t.Fatalf("no effective_routes in details: %v", view.Snapshot.Details)
	}
	if model := routes["model"].(map[string]any); model["model"] != "muse-spark-1.3-contributor" {
		t.Fatalf("the model should come from the preset: %v", model)
	}
	sum := routes["summarizer"].(map[string]any)
	if sum["model"] != "deepseek/deepseek-v4.1-flash-1017" || sum["reasoning_effort"] != "low" {
		t.Fatalf("file's summarizer not reflected: %v", sum)
	}
	// The file is the top active layer; the preset it builds on is /preset.
	if view.Snapshot.Effective["/preset"] != "muse" {
		t.Fatalf("effective preset = %v", view.Snapshot.Effective["/preset"])
	}
	if view.Snapshot.Saved["/summarizer/model/model"] != "deepseek/deepseek-v4.1-flash-1017" {
		t.Fatalf("saved summarizer model = %v", view.Snapshot.Saved["/summarizer/model/model"])
	}
}

// A file still in the keys of eagent's earlier design reads, and saves, in
// today's: the phone sees /model/..., and its first edit rewrites the file.
func TestRemoteEditsMigrateLegacyFile(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"orchestrator":{"model":"z-ai/glm-5.3","base_url":"https://openrouter.ai/api/v1","api_key_env":"OPENROUTER_API_KEY"},"narrator":{"reasoning_effort":"medium"},"narrator_tick_seconds":20,"task_concurrency":3,"_note":"keep"}`
	if err := os.WriteFile(config.File(project), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	// The narrator's effort alone is not carried over: the one model makes
	// the orchestrator's decisions too.
	if _, has := view.Snapshot.Saved["/model/reasoning_effort"]; has || view.Snapshot.Saved["/model/model"] != "z-ai/glm-5.3" || view.Snapshot.Saved["/tick_seconds"] != float64(20) {
		t.Fatalf("saved = %v", view.Snapshot.Saved)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/model/reasoning_effort", Value: "high"}}}
	if _, _, err := s.Apply(context.Background(), p, g); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(config.File(project))
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if config.HasLegacyKeys(saved) || string(saved["_note"]) != `"keep"` {
		t.Fatalf("after a remote edit:\n%s", raw)
	}
	cfg, err := config.Load(project, "")
	if err != nil || cfg.Model.Model != "z-ai/glm-5.3" || cfg.Model.ReasoningEffort != "high" || cfg.TickSeconds != 20 || len(cfg.Migrated) != 0 {
		t.Fatalf("loaded: %v %+v", err, cfg)
	}
}

// route.test names a saved route by target: the model, or the summarizer's
// route (the model's own unless the file gives it one).
func TestPrepareRouteTargets(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY","reasoning_effort":"none"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	propose := func(target string) control.Proposal {
		return control.Proposal{Operation: "route.test", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"target": target}}
	}
	b, err := s.PrepareRoute(propose("model"), g)
	if err != nil || b.Model.Model != "zai-org/GLM-5.3" || b.Model.ReasoningEffort != "medium" {
		t.Fatalf("model: %v %+v", err, b.Model)
	}
	b, err = s.PrepareRoute(propose("summarizer"), g)
	if err != nil || b.Model.ReasoningEffort != "none" {
		t.Fatalf("summarizer: %v %+v", err, b.Model)
	}
	if _, err := s.PrepareRoute(propose("orchestrator"), g); err == nil {
		t.Fatal("an unknown target was accepted")
	}
}

// A raw save in the earlier design's keys has its routes checked as they
// will load.
func TestSaveRawChecksLegacyRoutes(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	_, err := s.SaveRaw(context.Background(), RawRequest{Raw: `{"orchestrator":{"base_url":"https://unregistered.invalid/v1","api_key_env":"OPENAI_API_KEY"}}`})
	if err == nil {
		t.Fatal("an unregistered host was saved under orchestrator")
	}
	if _, err := os.Stat(config.File(s.Project)); !os.IsNotExist(err) {
		t.Fatal("a refused raw save wrote the file")
	}
}
