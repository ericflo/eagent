package integration

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The phone deletes the bundle its host was started on (eagent serve
// --config handmade). The refusal (728d3f6) tells the user to select a
// different default; the phone selects another saved preset as the
// default, as told, and the same refusal comes back: the bundle is in
// force through the server's flag, which nothing the phone can select
// changes.
func TestSR8BTheRefusalSaysWhatKeepsTheBundle(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	s, g, path := handmadeBundle(t)
	if _, err := config.SaveBundle(s.Project, "other", "", config.Defaults()); err != nil {
		t.Fatal(err)
	}
	restore := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{}, nil
	}, func(project string) (config.Config, error) { return config.LoadBundle(project, "", "handmade") })
	defer restore()
	executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/default_config", Value: "other"}}}), "succeeded")
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}})
	j, err := executeCommand(context.Background(), s, g, q, false)
	if bundleBytes(path) != handmade {
		t.Fatalf("setup: the host's bundle was deleted: %s %v (%v)", j.Status, j.Result, err)
	}
	msg, _ := j.Result["message"].(string)
	if msg == "select a different default before deleting its bundle" {
		t.Fatalf("default_config is other, not handmade; the phone is told %q, which selecting any default cannot satisfy (handmade is the server's --config)", msg)
	}
}
