package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

func sessionViewFor(t *testing.T, project string, grant control.Grant) control.Descriptor {
	t.Helper()
	values := runtimecontrol.FromConfigFor(project, config.Defaults())
	values.Relays = map[string]runtimecontrol.RelayRoute{
		"claude": {Agent: "claude"}, "codex": {Agent: "codex"}, "pi": {Agent: "pi"}, "antigravity": {Agent: "antigravity"},
	}
	view, err := runtimecontrol.View(project, "session", "gen", values, true)
	if err != nil {
		t.Fatal(err)
	}
	g := grant
	g.Key, g.Scope = view.Key, "session"
	decorateSessionView(project, &view, g)
	return view.Descriptor
}

// A grant that leaves out a class of the session's settings gets those
// settings read only, and the rest still writable: a reader would refuse
// the whole resource for one writable field it may not grant.
func TestSessionViewFollowsEachFieldsClass(t *testing.T) {
	project := t.TempDir()
	grant := control.Grant{Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference"}}
	d := sessionViewFor(t, project, grant)
	grant.Key, grant.Scope = "", "session"
	if err := d.Validate(grant); err != nil {
		t.Fatalf("descriptor for a preference-only grant: %v", err)
	}
	var route, preference bool
	for _, f := range d.Fields {
		switch f.Class {
		case "route":
			route = true
			if f.Writable || f.LockedReason == "" {
				t.Errorf("%s is writable under a grant without its class", f.Key)
			}
		case "preference":
			preference = preference || f.Writable
		}
	}
	if !route || !preference {
		t.Fatalf("route fields %v, writable preferences %v", route, preference)
	}
}

// However many models the providers list, a session's settings document
// stays within the document cap beside the agents' lists and the rest.
func TestSessionViewWithHugeModelListFits(t *testing.T) {
	project := t.TempDir()
	var models []config.Model
	for i := range 6000 {
		models = append(models, config.Model{Provider: "openrouter", ID: fmt.Sprintf("vendor-%04d/a-rather-long-model-name-for-testing-%d", i, i), Label: fmt.Sprintf("A Rather Long Model Name %d", i), Price: &config.Price{In: 1.25, Out: 10}, Context: 1_000_000, Live: true})
	}
	if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: models}); err != nil {
		t.Fatal(err)
	}
	grant := control.Grant{Operations: []string{"settings.apply", "settings.refresh", runtimecontrol.StopOperation}, Classes: []string{"preference", "route"}}
	d := sessionViewFor(t, project, grant)
	grant.Scope = "session"
	if err := d.Validate(grant); err != nil {
		t.Fatalf("descriptor: %v (%d lists)", err, len(d.Suggestions))
	}
	if len(d.Suggestions) > 8 {
		t.Fatalf("%d suggestion lists", len(d.Suggestions))
	}
	if len(d.Suggestions["models"]) < 100 {
		t.Fatalf("%d models offered", len(d.Suggestions["models"]))
	}
}
