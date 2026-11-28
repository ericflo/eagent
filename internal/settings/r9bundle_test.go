package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// r9MainBundle is a bundle as main (three model routes) saved it: its
// BundleBytes of a configuration with a changed orchestrator effort and
// call limit.
const r9MainBundle = `{
  "name": "mine",
  "orchestrator": {
    "protocol": "openai-chat",
    "base_url": "https://api.together.xyz/v1",
    "model": "zai-org/GLM-5.3",
    "api_key_env": "TOGETHER_API_KEY",
    "reasoning_effort": "medium",
    "max_tokens": 32768,
    "context_tokens": 200000,
    "fallback": null
  },
  "task": {
    "protocol": "openai-chat",
    "base_url": "https://api.together.xyz/v1",
    "model": "zai-org/GLM-5.3-Flash",
    "api_key_env": "TOGETHER_API_KEY",
    "reasoning_effort": "medium",
    "max_tokens": 32768,
    "context_tokens": 200000,
    "fallback": null
  },
  "narrator": {
    "protocol": "openai-chat",
    "base_url": "https://api.together.xyz/v1",
    "model": "deepseek-ai/DeepSeek-V4.1-Flash-1017",
    "api_key_env": "TOGETHER_API_KEY",
    "reasoning_effort": "low",
    "max_tokens": 4096,
    "context_tokens": 1048576,
    "fallback": null
  },
  "task_concurrency": 3,
  "max_task_turns": 150,
  "max_orchestrator_calls_per_turn": 40,
  "narrator_tick_seconds": 30,
  "narrator_quiet_seconds": 180,
  "rollover_tokens": 150000,
  "bash_wait_seconds": 20,
  "bash_timeout_seconds": 600,
  "tool_output_max_chars": 24000,
  "finalechat": {
    "artifacts": true
  },
  "allow_outside_project": false
}
`

// r9OnDisk is what config.LoadBundle gives for raw saved as bundle name,
// with the fields BundleConfig clears cleared.
func r9OnDisk(t *testing.T, project, name, raw string) config.Config {
	t.Helper()
	path := config.BundlePath(project, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", name)
	if err != nil {
		t.Fatal(err)
	}
	return r9Plain(cfg)
}

func r9Plain(cfg config.Config) config.Config {
	cfg.Name, cfg.NameFrom, cfg.DefaultConfig, cfg.Instructions, cfg.Migrated = "", "", "", "", nil
	return cfg
}

func TestR9BundleConfigTakesBundleSavedUnderMain(t *testing.T) {
	// A save is held to today's keys, as at 12aec43; only an undo, of
	// eagent's own bytes, carries main's over (TestR9UndoRestoresBundleSavedUnderMain).
	s := &Service{Project: t.TempDir()}
	if _, err := s.BundleConfig(BundleRequest{Name: "mine", Config: json.RawMessage(r9MainBundle)}); err == nil || !strings.Contains(err.Error(), "orchestrator") {
		t.Fatalf("a save in main's keys: %v, want it refused as at 12aec43", err)
	}
}

func TestR9UndoRestoresBundleSavedUnderMain(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	// The inverse of deleting "mine": save its former bytes back.
	change := ResourceChange{Kind: "bundle", Name: "mine", Before: []byte(r9MainBundle), BeforeExists: true}
	inverse, err := change.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, view.Snapshot.Version
	restore, err := s.PrepareRestore(editor, inverse, s.Grant(), []byte(r9MainBundle))
	if err != nil {
		t.Fatalf("undo refused a bundle main saved: %v", err)
	}
	if !restore.AfterExists || restore.Kind != "bundle" || restore.Name != "mine" {
		t.Fatalf("restore = %+v", restore)
	}
}

func TestR9BundleConfigNullsClearAsOnDisk(t *testing.T) {
	// LoadBundle, the reference, also reads the environment; BundleConfig
	// does not. CI switches the phone off with EAGENT_FINALECHAT=off.
	t.Setenv("EAGENT_FINALECHAT", "")
	cfg := config.Defaults()
	cfg.Model.ReasoningEffort, cfg.Model.MaxTokens, cfg.Model.ContextTokens = "", 0, 0
	raw, err := config.BundleBytes("cleared", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"reasoning_effort": null`, `"max_tokens": null`, `"context_tokens": null`} {
		if !strings.Contains(string(raw), k) {
			t.Fatalf("fixture lacks %s:\n%s", k, raw)
		}
	}
	s := &Service{Project: t.TempDir()}
	want := r9OnDisk(t, t.TempDir(), "cleared", string(raw))
	got, err := s.BundleConfig(BundleRequest{Name: "cleared", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.ReasoningEffort != "" || got.Model.MaxTokens != 0 || got.Model.ContextTokens != 0 {
		t.Fatalf("nulls stamped with defaults: effort %q max_tokens %d context_tokens %d", got.Model.ReasoningEffort, got.Model.MaxTokens, got.Model.ContextTokens)
	}
	if got = r9Plain(got); !reflect.DeepEqual(got, want) {
		t.Fatalf("BundleConfig = %+v\nLoadBundle  = %+v", got, want)
	}
}

// A bundle as eagent saves it today gives what it gave before: today's
// keys decoded over the defaults.
func TestR9BundleConfigTodaysShapeUnchanged(t *testing.T) {
	// LoadBundle, the reference, also reads the environment; BundleConfig
	// does not. CI switches the phone off with EAGENT_FINALECHAT=off.
	t.Setenv("EAGENT_FINALECHAT", "")
	cfg := config.Defaults()
	config.Presets["openai-high"](&cfg)
	cfg.Preset, cfg.MaxCallsPerTurn, cfg.Model.ReasoningEffort = "openai-high", 50, "high"
	raw, err := config.BundleBytes("today", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := config.Defaults()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&before); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: t.TempDir()}
	got, err := s.BundleConfig(BundleRequest{Name: "today", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got, before = r9Plain(got), r9Plain(before); !reflect.DeepEqual(got, before) {
		t.Fatalf("BundleConfig = %+v\nbefore      = %+v", got, before)
	}
	if want := r9OnDisk(t, t.TempDir(), "today", string(raw)); !reflect.DeepEqual(got, want) {
		t.Fatalf("BundleConfig = %+v\nLoadBundle  = %+v", got, want)
	}
	for _, bad := range []string{`{"bogus": 1}`, `{"model": {"bogus": 1}}`, `{"orchestrator": {"bogus": 1}}`} {
		if _, err := s.BundleConfig(BundleRequest{Name: "bad", Config: json.RawMessage(bad)}); err == nil || err.(*Error).Status != 400 {
			t.Fatalf("%s: err = %v, want a 400", bad, err)
		}
	}
}
