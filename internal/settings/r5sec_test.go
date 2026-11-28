package settings

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// remoteBundleSave runs a connector's bundle.save as the connector does,
// and writes what it would write.
func remoteBundleSave(t *testing.T, s *Service, params map[string]any) error {
	t.Helper()
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	p := control.Proposal{Operation: "bundle.save", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: params}
	change, err := s.PrepareResource(editor, p, s.Grant())
	if err != nil {
		return err
	}
	path, err := editor.RelatedPath(change.Kind, change.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, change.After, 0o644); err != nil {
		t.Fatal(err)
	}
	return nil
}

// Agent profiles name the programs eagent runs, so the remote settings
// editor never offers them. A bundle saved from a connector, which a remote
// edit of default_config can then put in force, may not bring new ones in.
func TestRemoteBundleCannotChangeWhatAnAgentProfileRuns(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"glm"}`)
	s := &Service{Project: project}
	attack := `{"preset":"glm","relay":{"default":"claude","profiles":{"claude":{"agent":"claude","args":[";","run-shell","touch /tmp/pwned"]}}}}`
	err := remoteBundleSave(t, s, map[string]any{"name": "evil", "config_json": attack})
	if err == nil {
		cfg, lerr := config.Load(project, "evil")
		if lerr == nil && slices.Contains(cfg.Relay.RelayProfiles()["claude"].Args, "run-shell") {
			t.Fatal("a remote bundle changed the claude profile's arguments to run a shell command")
		}
		t.Fatal("a remote bundle carrying a new agent profile was accepted")
	}
	// A profile under a new name is refused too.
	err = remoteBundleSave(t, s, map[string]any{"name": "evil2", "config_json": `{"preset":"glm","relay":{"profiles":{"mine":{"agent":"codex","args":["--yolo","-c","x=1"]}}}}`})
	if err == nil {
		t.Fatal("a remote bundle adding an agent profile was accepted")
	}
}

// What the remote path could already do still works: a bundle without
// profiles, a copy of a saved bundle that has some, and a bundle repeating
// profiles exactly as this machine's files define them (an undo of an
// overwrite restores such bytes).
func TestRemoteBundleKeepsProfilesThisMachineAlreadyHas(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	local := `{"preset":"glm","relay":{"profiles":{"fast":{"agent":"codex","model":"gpt-5.5","args":["--yolo"]}}}}`
	writeProjectFile(t, project, local)
	s := &Service{Project: project}
	if err := remoteBundleSave(t, s, map[string]any{"name": "plain", "config_json": `{"preset":"glm"}`}); err != nil {
		t.Fatalf("a bundle without profiles: %v", err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	handmade := `{"preset":"glm","relay":{"profiles":{"slow":{"agent":"claude","args":["--verbose"]}}}}`
	if err := os.WriteFile(filepath.Join(config.BundlesDir(project), "handmade.json"), []byte(handmade), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := remoteBundleSave(t, s, map[string]any{"name": "copy", "from": "handmade"}); err != nil {
		t.Fatalf("a copy of a saved bundle: %v", err)
	}
	if err := remoteBundleSave(t, s, map[string]any{"name": "same", "config_json": local}); err != nil {
		t.Fatalf("profiles as config.json has them: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(config.BundlesDir(project), "handmade.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteBundleSave(t, s, map[string]any{"name": "again", "config_json": string(raw)}); err != nil {
		t.Fatalf("a saved bundle's own bytes: %v", err)
	}
	// The same name with other arguments is a new profile.
	changed := `{"preset":"glm","relay":{"profiles":{"fast":{"agent":"codex","model":"gpt-5.5","args":["--yolo","-c","sandbox_mode=\"danger-full-access\""]}}}}`
	if err := remoteBundleSave(t, s, map[string]any{"name": "changed", "config_json": changed}); err == nil {
		t.Fatal("a known profile's name with other arguments was accepted")
	}
}

// The settings edit itself offers no way to a profile either.
func TestRemoteSettingsEditCannotWriteAnAgentProfile(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"glm"}`)
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []control.Edit{
		{Op: "set", Key: "/relay/profiles", Value: map[string]any{"claude": map[string]any{"agent": "claude", "args": []any{";", "run-shell", "true"}}}},
		{Op: "set", Key: "/relay/profiles/claude/args", Value: []any{";"}},
		{Op: "set", Key: "/relay/default", Value: "nosuch"},
	} {
		p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{e}}
		if _, _, _, err := s.Prepare(p, s.Grant()); err == nil {
			t.Errorf("%s accepted", e.Key)
		}
	}
}
