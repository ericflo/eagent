package settings

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The providers list names the groups every model picker shows; with every
// agent's model list and a saved preset in the project it still has its
// place among the eight lists a document carries.
func TestProvidersListKeepsItsPlaceBesideSavedPresets(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset":"glm","description":"saved"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	lists := view.Descriptor.Suggestions
	if len(lists) > suggestionLists {
		t.Fatalf("%d lists", len(lists))
	}
	if len(lists["providers"]) == 0 {
		names := []string{}
		for k := range lists {
			names = append(names, k)
		}
		t.Fatalf("no providers list; lists are %v", names)
	}
	// Every agent's models keep theirs; the saved presets' list is the one
	// left out, and /default_config still offers their names.
	for _, agent := range []string{"claude", "codex", "pi", "antigravity"} {
		if lists[agent+"_models"] == nil {
			t.Errorf("no %s_models list", agent)
		}
	}
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/model/base_url" && (f.Suggest == nil || f.Suggest.List != "providers") {
			t.Fatalf("/model/base_url suggest = %+v", f.Suggest)
		}
		if f.Suggest != nil && lists[f.Suggest.List] == nil {
			t.Fatalf("%s suggests the missing list %q", f.Key, f.Suggest.List)
		}
	}
}
