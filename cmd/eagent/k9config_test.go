package main

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A built-in preset chosen as the bundle (EAGENT_CONFIG=anthropic-med, or a
// file's default_config naming one: "a bundle name may also be a built-in
// preset", as the loader says) is the configuration in force: a session
// runs it, `eagent config` shows it. On main `eagent presets save copy`
// saved it (LoadBundle). settings.CurrentLayers asks the loader for the
// bundle in force only by its Name, which a built-in leaves empty, so the
// copy now holds the built-in defaults' model.
func TestSR4PresetsSaveKeepsABuiltInChosenAsTheBundle(t *testing.T) {
	for _, viaEnv := range []bool{true, false} {
		name := "default_config"
		if viaEnv {
			name = "EAGENT_CONFIG"
		}
		t.Run(name, func(t *testing.T) {
			r3NoEnv(t)
			project := t.TempDir()
			if viaEnv {
				r3WriteConfig(t, project, `{"tick_seconds":45}`)
				t.Setenv("EAGENT_CONFIG", "anthropic-med")
			} else {
				r3WriteConfig(t, project, `{"tick_seconds":45,"default_config":"anthropic-med"}`)
			}
			inForce, err := config.LoadBundle(project, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if inForce.Model.Model != "claude-opus-5-5" {
				t.Fatalf("setup: in force %q", inForce.Model.Model)
			}
			var code int
			out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"save", "copy"}) })
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			saved, err := config.LoadBundle(project, "", "copy")
			if err != nil {
				t.Fatal(err)
			}
			if saved.Model.Model != inForce.Model.Model {
				t.Fatalf("saved copy runs %q; the configuration in force runs %q", saved.Model.Model, inForce.Model.Model)
			}
		})
	}
}

// `EAGENT_CONFIG=anthropic-med eagent config --write` in a project with no
// config.json: `eagent config` shows Claude Opus, and on main the written
// file held it (preset anthropic-med, LoadBundle). The file now holds the
// built-in defaults' model, so once the variable is gone the project runs
// GLM. A saved bundle named the same way is written (CurrentLayers takes
// it); only a built-in is dropped.
func TestSR4ConfigWriteKeepsABuiltInChosenAsTheBundle(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	t.Setenv("EAGENT_CONFIG", "anthropic-med")
	inForce, err := config.LoadBundle(project, "", "")
	if err != nil || inForce.Model.Model != "claude-opus-5-5" {
		t.Fatalf("setup: in force %q, %v", inForce.Model.Model, err)
	}
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", true, nil) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	t.Setenv("EAGENT_CONFIG", "")
	written, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if written.Model.Model != inForce.Model.Model {
		t.Fatalf("written config.json runs %q; the configuration in force when it was written ran %q", written.Model.Model, inForce.Model.Model)
	}
}
