package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// Finding 25: a JSON null in config.json is a deliberate "cleared"
// override. Saving an unrelated field while the environment pins those
// fields carries the nulls over unchanged and says so.
func TestSaveUnderEnvKeepsNullOverrides(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), `{"preset":"glm","model":{"reasoning_effort":null,"max_tokens":null}}`)
	t.Setenv("EAGENT_REASONING_EFFORT", "low")
	t.Setenv("EAGENT_MAX_TOKENS", "1000")
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	cfg := res.Effective
	cfg.Persona = "terse"
	raw, _ := json.Marshal(cfg)
	out, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, BasePreset: "glm"})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Model map[string]json.RawMessage `json:"model"`
	}
	if err := json.Unmarshal(disk, &file); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reasoning_effort", "max_tokens"} {
		if v, ok := file.Model[key]; !ok || string(v) != "null" {
			t.Fatalf("model.%s after the save = %q (present %v); file:\n%s", key, v, ok, disk)
		}
	}
	kept := map[string]bool{}
	for _, f := range out.Kept {
		kept[f.Pointer] = true
	}
	if !kept["/model/reasoning_effort"] || !kept["/model/max_tokens"] {
		t.Fatalf("Kept = %+v", out.Kept)
	}
	if !strings.Contains(string(disk), `"terse"`) {
		t.Fatalf("persona not saved:\n%s", disk)
	}
}

// Finding 26: each built-in preset's suggestion describes that preset,
// whatever EAGENT_CONFIG names as the configuration in use.
func TestPresetSuggestionsIgnoreEnvConfig(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.BundlePath(project, "cheap"), `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"OPENROUTER_API_KEY"}}`)
	t.Setenv("EAGENT_CONFIG", "cheap")
	want := config.Defaults()
	config.Presets["anthropic-high"](&want)
	lists := map[string][]control.Suggestion{"project": PresetSuggestions(project), "session": SessionConfigSuggestions(project, true)}
	for list, suggestions := range lists {
		found := false
		for _, s := range suggestions {
			if s.Value != "anthropic-high" {
				continue
			}
			found = true
			for _, key := range []string{"/model/model", "/routes/eagent/model"} {
				if v, ok := s.Implies[key]; ok && v != want.Model.Model {
					t.Fatalf("%s list: anthropic-high implies %s = %v", list, key, v)
				}
			}
		}
		if !found {
			t.Fatalf("%s list has no anthropic-high", list)
		}
	}
}

// Finding 28: an undo of a pick of a fallback's model puts back the
// fields the pick gave the fallback route, as for a primary route.
func TestRemoteUndoOfFallbackModelPick(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct {
		name, before string
		edits        []control.Edit
	}{
		{"change a fallback", `{"preset":"openai-high"}`, []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "z-ai/glm-5.3"}}},
		{"add a fallback", `{"preset":"glm"}`, []control.Edit{{Op: "set", Key: "/model/fallback/base_url", Value: "https://openrouter.ai/api/v1"}, {Op: "set", Key: "/model/fallback/model", Value: "z-ai/glm-5.3"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			writeProjectFile(t, project, c.before)
			s := &Service{Project: project}
			view := apply(t, s, c.edits...)
			desired, _ := os.ReadFile(config.File(project))
			after, err := s.RemoteSnapshot(s.Grant())
			if err != nil {
				t.Fatal(err)
			}
			inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: c.edits}, c.before, string(desired))
			if err != nil {
				t.Fatal(err)
			}
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
			raw, _, _, err := s.PrepareUndo(inverse, c.before, string(desired), s.Grant())
			if err != nil {
				t.Fatalf("undo: %v\nafter the edit:\n%s", err, desired)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			writeProjectFile(t, project, c.before)
			want, _ := config.Load(project, "")
			g, _ := json.Marshal(got.Model.Fallback)
			w, _ := json.Marshal(want.Model.Fallback)
			if string(g) != string(w) {
				t.Fatalf("fallback after undo %s, want %s\nedit wrote:\n%s\nundo wrote:\n%s", g, w, desired, raw)
			}
		})
	}
}
