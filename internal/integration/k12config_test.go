package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The bundle an `eagent serve --config work` starts and resumes sessions
// on is in force for the phone too: deleting it from there is refused, as
// the server's own editor refuses it.
func TestK12ThePhoneCannotDeleteTheBundleItsHostRuns(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	s, g, path := handmadeBundle(t)
	restore := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{}, nil
	}, func(project string) (config.Config, error) { return config.LoadBundle(project, "", "handmade") })
	defer restore()
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}})
	if j, err := executeCommand(context.Background(), s, g, q, false); err == nil && j.Status == "succeeded" || bundleBytes(path) != handmade {
		t.Fatalf("the phone deleted the bundle its host runs: %s %v (%v)", j.Status, j.Result, err)
	}
}

// An eagent server started with --config work runs phone-started sessions
// on bundle work, and since fe74da8 the phone's agent picker
// (relay_profiles) lists that bundle's agents. The session.start action
// the same snapshot declares still takes its relay choices from the
// project's configuration (relayChoices), so an agent the picker offers
// is refused when the user picks it.
func TestSR7AnAgentThePhoneIsOfferedCanBeStarted(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	s, g, _ := routeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no provider call expected") })
	bundle := config.BundlePath(s.Project, "work")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte(`{"relay":{"profiles":{"review":{"agent":"codex","label":"Codex reviewer"}},"default":"review"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got SessionRequest
	restore := SetSessionStarter(func(_ context.Context, _ string, req SessionRequest) (SessionStart, error) {
		got = req
		return SessionStart{ID: "1788000000077", Host: "in_process", Interactive: true}, nil
	}, func(project string) (config.Config, error) { return config.LoadBundle(project, "", "work") })
	defer restore()
	offered := false
	for _, p := range relayProfiles(s.Project) {
		offered = offered || p["name"] == "review"
	}
	if !offered {
		t.Fatalf("setup: the phone is not offered the host's review agent: %v", relayProfiles(s.Project))
	}
	q := sessionStartCommand(t, s, g, "review the diff")
	q.Proposal.Parameters["relay"] = "review"
	raw, _ := json.Marshal(q.Proposal)
	q.Digest = artifact.Digest(raw)
	res, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || res.Status != "succeeded" || got.Relay != "review" {
		t.Fatalf("the phone offered review and the start was %s: %v (%v); the host would run it", res.Status, res.Result, err)
	}
}
