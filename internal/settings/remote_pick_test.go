package settings

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func writeProjectFile(t *testing.T, project, raw string) {
	t.Helper()
	if err := os.MkdirAll(project+"/.agents/eagent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fieldOf(view RemoteView, key string) (control.Field, bool) {
	i := slices.IndexFunc(view.Descriptor.Fields, func(f control.Field) bool { return f.Key == key })
	if i < 0 {
		return control.Field{}, false
	}
	return view.Descriptor.Fields[i], true
}

func apply(t *testing.T, s *Service, edits ...control.Edit) RemoteView {
	t.Helper()
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	if _, _, err := s.Apply(context.Background(), p, s.Grant()); err != nil {
		t.Fatalf("apply %+v: %v", edits, err)
	}
	return view
}

// A route's model is offered by provider, grouped by the base_url picking it
// gives the route, and applying a pick of a model at another provider gives
// the route that provider's protocol, key variable and the model's window.
func TestRemoteModelPickAtAnotherProvider(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high"}`)
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/model", "/summarizer/model"} {
		f, ok := fieldOf(view, route+"/model")
		if !ok || f.Suggest == nil || f.Suggest.List != "models" || f.Suggest.GroupBy != route+"/base_url" {
			t.Fatalf("%s/model suggest = %+v", route, f.Suggest)
		}
		b, _ := fieldOf(view, route+"/base_url")
		if b.Suggest == nil || b.Suggest.List != "providers" || !slices.Contains(b.Shape.Enum, any("https://api.anthropic.com/v1")) {
			t.Fatalf("%s/base_url = %+v", route, b)
		}
	}
	models := view.Descriptor.Suggestions["models"]
	if !slices.ContainsFunc(models, func(s control.Suggestion) bool {
		return s.Value == "claude-opus-5-5" && s.Group == "https://api.anthropic.com/v1"
	}) {
		t.Fatal("no Claude Opus 5.5 in Anthropic's group")
	}
	providers := view.Descriptor.Suggestions["providers"]
	if !slices.ContainsFunc(providers, func(s control.Suggestion) bool {
		return s.Value == "https://api.anthropic.com/v1" && s.Label == "Anthropic"
	}) {
		t.Fatalf("providers = %+v", providers)
	}
	// What FinaleChat sends for a pick from another provider's group.
	apply(t, s, control.Edit{Op: "set", Key: "/model/model", Value: "claude-opus-5-5"}, control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.anthropic.com/v1"})
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Model
	if m.Protocol != "anthropic" || m.APIKeyEnv != "ANTHROPIC_API_KEY" || m.ContextTokens != 1_000_000 || m.Model != "claude-opus-5-5" {
		t.Fatalf("route = %+v", m)
	}
	if lm, ok := config.Lookup(m.BaseURL, m.Model); !ok || !slices.Contains(lm.Efforts, m.ReasoningEffort) {
		t.Fatalf("effort %q is not one the model takes", m.ReasoningEffort)
	}
	// A pick within the same provider sends the model alone.
	apply(t, s, control.Edit{Op: "set", Key: "/model/model", Value: "claude-haiku-5-5"})
	if cfg, err = config.Load(project, ""); err != nil || cfg.Model.Protocol != "anthropic" || cfg.Model.Model != "claude-haiku-5-5" {
		t.Fatalf("same-provider pick: %+v %v", cfg.Model, err)
	}
	// A model no list knows keeps the rest of the route; its window is
	// unknown, not the old model's.
	apply(t, s, control.Edit{Op: "set", Key: "/model/model", Value: "claude-unlisted"})
	if cfg, err = config.Load(project, ""); err != nil || cfg.Model.APIKeyEnv != "ANTHROPIC_API_KEY" || cfg.Model.ContextTokens != 0 {
		t.Fatalf("unlisted pick: %+v %v", cfg.Model, err)
	}
}

// A dedicated summarizer with no route of its own calls the main model's;
// picking its model alone starts it from that route, so the save loads.
func TestRemoteDedicatedSummarizerPickWithoutOwnRoute(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"summarizer":{"mode":"dedicated"}}`)
	s := &Service{Project: project}
	apply(t, s, control.Edit{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"})
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Summarizer.Route(cfg.Model)
	if r.Model != "zai-org/GLM-5.3-Flash" || r.BaseURL != cfg.Model.BaseURL || r.Protocol != cfg.Model.Protocol || r.Fallback != nil {
		t.Fatalf("summarizer route = %+v", r)
	}
	// And at another provider, as FinaleChat sends it.
	apply(t, s, control.Edit{Op: "set", Key: "/summarizer/model/model", Value: "gpt-6-luna"}, control.Edit{Op: "set", Key: "/summarizer/model/base_url", Value: "https://api.openai.com/v1"})
	if cfg, err = config.Load(project, ""); err != nil {
		t.Fatal(err)
	}
	if r := cfg.Summarizer.Route(cfg.Model); r.Protocol != "openai-responses" || r.APIKeyEnv != "OPENAI_API_KEY" || r.ContextTokens != 1_050_000 {
		t.Fatalf("summarizer route = %+v", r)
	}
	if cfg.Model.Model != "zai-org/GLM-5.3" {
		t.Fatalf("the main route changed: %+v", cfg.Model)
	}
}

// The summarizer's route is described while it is inline, out of the way,
// so choosing dedicated can give it one; and an undo of a save that made it
// inline while editing its route still applies.
func TestRemoteSummarizerRouteWhileInline(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	before := `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY","reasoning_effort":"high"}}}`
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	edits := []control.Edit{{Op: "set", Key: "/summarizer/mode", Value: "inline"}, {Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}}
	view := apply(t, s, edits...)
	after, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := fieldOf(after, "/summarizer/model/model")
	if !ok || !f.Writable || !f.Advanced {
		t.Fatalf("inline summarizer model field = %+v (present %v)", f, ok)
	}
	desired, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	original := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	inverse, err := ConfigInverse(original, before, string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	raw, _, _, err := s.PrepareUndo(inverse, string(desired), s.Grant())
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !strings.Contains(raw, `"dedicated"`) || !strings.Contains(raw, `"high"`) {
		t.Fatalf("undo gives %s", raw)
	}
}

// An undo of a model pick puts back the fields the pick gave the route,
// and an undo of a summarizer model picked with no route of its own
// removes the route it wrote.
func TestRemoteUndoOfModelPick(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct {
		name, before string
		edits        []control.Edit
	}{
		{"main route", `{"preset":"openai-high"}`, []control.Edit{{Op: "set", Key: "/model/model", Value: "claude-opus-5-5"}, {Op: "set", Key: "/model/base_url", Value: "https://api.anthropic.com/v1"}}},
		{"summarizer route", `{"summarizer":{"mode":"dedicated"}}`, []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"}}},
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
			raw, _, _, err := s.PrepareUndo(inverse, string(desired), s.Grant())
			if err != nil {
				t.Fatalf("undo: %v", err)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			writeProjectFile(t, project, c.before)
			want, _ := config.Load(project, "")
			if got.Model.Model != want.Model.Model || got.Model.Protocol != want.Model.Protocol || got.Model.APIKeyEnv != want.Model.APIKeyEnv || got.Model.ContextTokens != want.Model.ContextTokens || (got.Summarizer.Model == nil) != (want.Summarizer.Model == nil) {
				t.Fatalf("after undo %+v / %+v, want %+v / %+v\n%s", got.Model, got.Summarizer.Model, want.Model, want.Summarizer.Model, raw)
			}
		})
	}
}

// eagent's settings document keeps to the lists a reader accepts, and the
// protocol takes up to 16.
func TestSuggestionListLimits(t *testing.T) {
	d := control.Descriptor{Format: control.Format, SchemaVersion: "x", AdapterVersion: "1", Fields: []control.Field{}, Suggestions: map[string][]control.Suggestion{}}
	for i := range 16 {
		d.Suggestions[string(rune('a'+i))] = []control.Suggestion{{Value: "v"}}
	}
	if err := d.Validate(control.Grant{}); err != nil {
		t.Fatalf("16 lists: %v", err)
	}
	d.Suggestions["q"] = []control.Suggestion{{Value: "v"}}
	if err := d.Validate(control.Grant{}); err == nil {
		t.Fatal("17 lists accepted")
	}
}

// A remote pick of another model on a route whose effort is "" (send none)
// keeps it "": an empty effort is not one the new model lacks.
func TestRemoteModelPickKeepsAnEmptyEffortEmpty(t *testing.T) {
	project := t.TempDir()
	writeProjectFile(t, project, `{"model":{"reasoning_effort":null}}`)
	s := &Service{Project: project}
	apply(t, s,
		control.Edit{Op: "set", Key: "/model/base_url", Value: "https://openrouter.ai/api/v1"},
		control.Edit{Op: "set", Key: "/model/model", Value: "z-ai/glm-5.3"})
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "z-ai/glm-5.3" || cfg.Model.ReasoningEffort != "" {
		t.Fatalf("after the pick: %s at effort %q", cfg.Model.Model, cfg.Model.ReasoningEffort)
	}
}
