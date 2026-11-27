package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv11InverseAt2ffa8a0 is ConfigInverse exactly as it was at 2ffa8a0.
func rv11InverseAt2ffa8a0(original control.Proposal, beforeRaw string) (control.Proposal, error) {
	inverse := control.Proposal{Operation: "settings.apply"}
	if original.Operation != "settings.apply" || len(original.Edits) == 0 {
		return inverse, fmt.Errorf("this command has no reversible settings edits")
	}
	before, err := fileValues(beforeRaw)
	if err != nil {
		return inverse, err
	}
	keys := []string{}
	for _, edit := range original.Edits {
		keys = append(keys, edit.Key)
	}
	for _, route := range []string{"/model", "/model/fallback", "/summarizer/model", "/summarizer/model/fallback"} {
		picked := slices.ContainsFunc(original.Edits, func(e control.Edit) bool { return e.Key == route+"/model" || e.Key == route+"/base_url" })
		extra := []string{}
		if picked {
			extra = routePickFields
		}
		if _, had := pointerValue(before, route); !had && route == "/summarizer/model" && slices.ContainsFunc(original.Edits, func(e control.Edit) bool { return e.Op == "set" && strings.HasPrefix(e.Key, route+"/") }) {
			extra = routeLeaves
		}
		for _, leaf := range extra {
			if !slices.Contains(keys, route+"/"+leaf) {
				keys = append(keys, route+"/"+leaf)
			}
		}
	}
	for _, key := range keys {
		value, exists := pointerValue(before, key)
		reverse := control.Edit{Key: key, Op: "unset"}
		switch {
		case exists && value != nil:
			reverse.Op, reverse.Value = "set", value
		case exists && strings.HasSuffix(key, "/reasoning_effort"):
			reverse.Op, reverse.Value = "set", ""
		case exists && strings.HasSuffix(key, "/max_tokens"):
			reverse.Op, reverse.Value = "set", float64(0)
		}
		inverse.Edits = append(inverse.Edits, reverse)
	}
	return inverse, nil
}

// A project written by `eagent config --write` on a preset with no fallback
// (glm) spells "fallback": null. A remote edit that adds a fallback is then
// undone at 2ffa8a0 to a file that loads exactly as before; at HEAD the
// undo is refused.
func TestRV11UndoOfFallbackOverWrittenNullNoPresetFallback(t *testing.T) {
	clearRouteEnv(t)
	for _, preset := range []string{"glm", "deepseek", "anthropic-high"} {
		t.Run(preset, func(t *testing.T) {
			project := t.TempDir()
			cfg, err := config.Load(project, preset)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.Fallback != nil {
				t.Skipf("preset %s has a fallback", preset)
			}
			if _, err := config.Write(project, cfg); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(config.File(project))
			before := string(b)
			var m map[string]map[string]json.RawMessage
			_ = json.Unmarshal(b, &m)
			if string(m["model"]["fallback"]) != "null" {
				t.Fatalf("config --write did not spell fallback null:\n%s", before)
			}
			want, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			edits := []control.Edit{
				{Op: "set", Key: "/model/fallback/base_url", Value: "https://openrouter.ai/api/v1"},
				{Op: "set", Key: "/model/fallback/model", Value: "z-ai/glm-5.3"},
			}
			s := &Service{Project: project}
			view := apply(t, s, edits...)
			desired, _ := os.ReadFile(config.File(project))
			after, err := s.RemoteSnapshot(s.Grant())
			if err != nil {
				t.Fatal(err)
			}
			original := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}

			// 2ffa8a0: the undo goes through and loads as the file before.
			old, err := rv11InverseAt2ffa8a0(original, before)
			if err != nil {
				t.Fatal(err)
			}
			old.SchemaVersion, old.ExpectedVersion = SchemaVersion, after.Snapshot.Version
			raw, _, _, err := s.PrepareUndo(old, string(desired), s.Grant())
			if err != nil {
				t.Fatalf("2ffa8a0 undo: %v", err)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			g, _ := json.Marshal([]any{got.Model, got.Summarizer})
			w, _ := json.Marshal([]any{want.Model, want.Summarizer})
			if string(g) != string(w) {
				t.Fatalf("2ffa8a0 undo loads differently (no regression to show):\n%s\nwant %s", g, w)
			}

			// HEAD: the same undo.
			if _, err := ConfigInverse(original, before); err != nil {
				t.Fatalf("HEAD refuses an undo that 2ffa8a0 made and that loads as the file before: %v", err)
			}
		})
	}
}
