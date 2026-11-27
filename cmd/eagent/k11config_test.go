package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// sr6TwoPresets saves "work" (the built-in defaults' model) and "other"
// (another model) in a project with no config.json.
func sr6TwoPresets(t *testing.T) string {
	t.Helper()
	r3NoEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	if _, err := config.SaveBundle(project, "work", "", config.Defaults()); err != nil {
		t.Fatal(err)
	}
	other := config.Defaults()
	config.Presets["openai-high"](&other)
	if _, err := config.SaveBundle(project, "other", "", other); err != nil {
		t.Fatal(err)
	}
	return project
}

// `eagent --config work config --write` writes the file from work's layers,
// with work's "name" in it (as main's did). `eagent presets use other` then
// makes other the default: sessions run other's model. `eagent presets
// save copy` should save what runs, as main's LoadBundle did; CurrentLayers
// takes the file's "name" for a bundle already laid and leaves the default
// one out, so copy gets work's model.
func TestSR6PresetsSaveAfterConfigWriteSavesWhatRuns(t *testing.T) {
	project := sr6TwoPresets(t)
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "work", true, nil) })
	if code != 0 {
		t.Fatalf("config --write: exit %d\n%s", code, out)
	}
	raw, _ := os.ReadFile(config.File(project))
	if !strings.Contains(string(raw), `"name"`) {
		t.Logf("note: the file written has no name key:\n%s", raw)
	}
	if out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "other"}) }); code != 0 {
		t.Fatalf("presets use other: exit %d\n%s", code, out)
	}
	runs, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if runs.Model.Model == config.Defaults().Model.Model {
		t.Fatalf("setup: sessions do not run other's model (%s)", runs.Model.Model)
	}
	if out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"save", "copy"}) }); code != 0 {
		t.Fatalf("presets save copy: exit %d\n%s", code, out)
	}
	saved, err := config.LoadBundle(project, "", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Model != runs.Model.Model {
		t.Fatalf("eagent presets save copy saved model %q; sessions run %q (default_config other)\nconfig.json:\n%s", saved.Model.Model, runs.Model.Model, raw)
	}
}

// 95382c0's sibling: EAGENT_PRESET naming a saved preset loads it as the
// bundle (LoadBundleWithFile), as EAGENT_CONFIG does, and the web and phone
// deletes refuse it (Resolve makes it the active bundle), but `eagent
// presets delete` deletes it; the next session under the same environment
// then fails to start ("unknown preset").
func TestSR6PresetsDeleteKeepsTheOneEAGENTPRESETNames(t *testing.T) {
	project := sr6TwoPresets(t)
	t.Setenv("EAGENT_PRESET", "other")
	if cfg, err := config.LoadBundle(project, "", ""); err != nil || cfg.Name != "other" {
		t.Fatalf("setup: sessions under EAGENT_PRESET=other load %q (%v)", cfg.Name, err)
	}
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"delete", "other"}) })
	if _, err := os.Stat(config.BundlePath(project, "other")); err != nil || code == 0 {
		_, loadErr := config.LoadBundle(project, "", "")
		t.Fatalf("eagent presets delete other under EAGENT_PRESET=other: exit %d, file %v; a session now: %v\n%s", code, err, loadErr, out)
	}
}
