package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

func TestAntigravityUsesSessionSpecificStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id := "23124aa2-ad6d-4418-b186-4c4c21fa3001"
	path := filepath.Join(antigravityHome(), "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a := antigravityAgent{}
	spec := Spec{StatusFile: filepath.Join(t.TempDir(), "status.json")}
	if found, _ := a.locate(spec, time.Now(), ""); found != "" {
		t.Fatal("claimed a different terminal's transcript")
	}
	var out bytes.Buffer
	input := strings.NewReader(`{"conversation_id":"` + id + `","email":"private@example.org","agent_state":"idle"}`)
	if WriteAntigravityStatus(spec.StatusFile, input, &out) != 0 || !a.ready(out.String()) {
		t.Fatal(out.String())
	}
	saved, err := os.ReadFile(spec.StatusFile)
	if err != nil || bytes.Contains(saved, []byte("private@example.org")) {
		t.Fatal("invalid private status record", err)
	}
	if found, inner := a.locate(spec, time.Now(), ""); found != path || inner != id {
		t.Fatal(found, inner)
	}
	args := a.command(config.BuiltinRelayProfiles()["antigravity"].With("gemini-test-high", "low"), id, true, "hook")
	if !slices.Equal(args[:7], []string{"agy", "--conversation", id, "--model", "gemini-test-high", "--effort", "low"}) || !slices.Contains(args, "--dangerously-skip-permissions") {
		t.Fatal(args)
	}
}

func TestAntigravityCatalogAndEffortVariants(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PATH", root)
	if err := os.WriteFile(filepath.Join(root, "agy"), []byte("#!/bin/sh\n[ \"$1\" = models ] || exit 1\nprintf 'model-low\\tModel (Low)\\nmodel-high\\tModel (High)\\nfixed\\tFixed\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Keep the fixture independent of any listing fetched by other tests.
	oldModels, oldRetry := agyCatalog.models, agyCatalog.retry
	agyCatalog.models, agyCatalog.retry = nil, time.Time{}
	t.Cleanup(func() { agyCatalog.models, agyCatalog.retry = oldModels, oldRetry })
	if err := os.MkdirAll(antigravityHome(), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"model": "Model (High)"})
	if err := os.WriteFile(filepath.Join(antigravityHome(), "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if model, effort := AgentDefaults("antigravity"); model != "model-high" || effort != "high" {
		t.Fatal(model, effort)
	}
	if label := Label("antigravity", root, "model-low", ""); label != "Antigravity · Model low" {
		t.Fatal(label)
	}
	if CheckAntigravityEffort("model-low", "high") != nil || CheckAntigravityEffort("model-low", "max") == nil || CheckAntigravityEffort("fixed", "low") == nil {
		t.Fatal("wrong model-specific efforts")
	}
}
