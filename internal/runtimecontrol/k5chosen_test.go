package runtimecontrol

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// A preset saved in a change that chooses a configuration asks the session
// for its routes with that configuration's name (ChosenRoutes), the /config
// edit's, though the values then name the preset saved; any other save
// asks with the routes alone (Routes).
func TestK5ASaveWithAChosenConfigurationAsksWithItsName(t *testing.T) {
	cleanPresetEnv(t)
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "y")
	t.Setenv("TOGETHER_API_KEY", "z")
	project := t.TempDir()
	cfg := config.Defaults()
	c, err := New(project, "s-chosen", FromConfigFor(project, cfg), func(Request, Values, map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	asked := map[string]string{}
	c.Routes = func(map[string]Route) (map[string]config.ModelRoute, error) {
		asked["routes"] = "yes"
		return map[string]config.ModelRoute{RouteEagent: cfg.Model}, nil
	}
	c.ChosenRoutes = func(_ map[string]Route, chosen string) (map[string]config.ModelRoute, error) {
		asked["chosen"] = chosen
		return map[string]config.ModelRoute{RouteEagent: cfg.Model}, nil
	}
	defer c.Close()
	defer runLoop(t, c)()
	seen := ""
	for _, edits := range [][]control.Edit{
		{{Op: "set", Key: "/tick_seconds", Value: float64(45)}, {Op: "set", Key: "/save_preset", Value: "plain"}},
		{{Op: "set", Key: "/config", Value: "openai-high"}, {Op: "set", Key: "/save_preset", Value: "picked"}},
	} {
		// The version a client saves against is the published status's,
		// which the session writes right after it acknowledges a save.
		var view settings.RemoteView
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if status, err := ReadStatus(project, "s-chosen"); err == nil {
				if view, err = View(project, "s-chosen", status.Generation, status.Values, true); err != nil {
					t.Fatal(err)
				}
				if view.Snapshot.Version != seen {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("the status never showed the save before")
			}
		}
		seen = view.Snapshot.Version
		grant, _ := Grant(project, "s-chosen")
		prop := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Edits: edits}
		raw, _ := json.Marshal(prop)
		req := Request{ID: newID(), UserID: "u1", Proposal: prop, Digest: artifact.Digest(raw), Expires: time.Now().Add(5 * time.Second), Grant: grant}
		if outcome, err := Deliver(context.Background(), project, "s-chosen", req, false); err != nil || outcome.Status != "succeeded" {
			t.Fatalf("save: status=%s result=%v err=%v", outcome.Status, outcome.Result, err)
		}
	}
	if asked["routes"] != "yes" || asked["chosen"] != "openai-high" {
		t.Fatalf("asked %v, want Routes for the plain save and ChosenRoutes with openai-high for the other", asked)
	}
}
