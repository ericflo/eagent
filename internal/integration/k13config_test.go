package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
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

// A settings.apply that makes saved preset work the default, journaled as
// prepared and cut before its write, is delivered again after work was
// deleted (allowed then: the file did not name it). b98ef21 holds a
// journaled bundle.delete to the guard again when it is written; the
// journaled file write that names the bundle is not held to it: it writes
// a default_config naming a bundle that is gone, reports succeeded, and
// every session started after it fails to load its configuration.
func TestSR8AJournaledDefaultIsNotWrittenOverADeletedBundle(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	s, g, _ := fixtureCommand(t)
	if _, err := config.SaveBundle(s.Project, "work", "", config.Defaults()); err != nil {
		t.Fatal(err)
	}
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/default_config", Value: "work"}}})
	// The first delivery prepares and journals the write, as executeCommand
	// does, and is cut before it writes.
	desired, etag, _, err := s.Prepare(q.Proposal, g)
	if err != nil {
		t.Fatalf("setup: preparing default_config work: %v", err)
	}
	res := config.Resolve(s.Project, "", "")
	raw := res.File.Raw
	if raw == "" {
		raw = "{}\n"
	}
	applied := q.Proposal
	journal := commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: g.Key, Status: "prepared",
		Applied: &applied, Desired: desired, BeforeETag: etag, DesiredETag: artifact.Digest([]byte(desired)), BeforeRaw: &raw}
	if err := writeJSONAtomic(filepath.Join(stateDir(s.Project), "commands", q.ID+".json"), journal); err != nil {
		t.Fatal(err)
	}
	// Meanwhile work is deleted: no file names it as the default yet.
	if err := s.DeleteBundle(context.Background(), "work"); err != nil {
		t.Fatalf("setup: deleting work while no default names it: %v", err)
	}
	j, err := executeCommand(context.Background(), s, g, q, false)
	b, _ := os.ReadFile(config.File(s.Project))
	var file map[string]json.RawMessage
	_ = json.Unmarshal(b, &file)
	_, loadErr := config.Load(s.Project, "")
	if string(file["default_config"]) == `"work"` && loadErr != nil {
		t.Fatalf("the replayed command (%s %v, %v) wrote default_config work over a deleted bundle; every start now fails: %v", j.Status, j.Result["message"], err, loadErr)
	}
	if msg, _ := j.Result["message"].(string); j.Status != "rejected" || !strings.Contains(msg, `"work"`) {
		t.Fatalf("the replay refused finishes %s, saying %q; want rejected, naming the missing preset", j.Status, msg)
	}
}

// Under `serve --config handmade` with default_config handmade too, the
// phone's refusal names both: selecting a different default alone would
// not free it.
func TestK13TheRefusalNamesEveryReasonAtOnce(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	s, g, path := handmadeBundle(t)
	if _, err := config.SaveRaw(s.Project, `{"default_config":"handmade"}`, "", true); err != nil {
		t.Fatal(err)
	}
	restore := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{}, nil
	}, func(project string) (config.Config, error) { return config.LoadBundle(project, "", "handmade") })
	defer restore()
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}})
	j, err := executeCommand(context.Background(), s, g, q, false)
	msg, _ := j.Result["message"].(string)
	if bundleBytes(path) != handmade || !strings.Contains(msg, "default_config") || !strings.Contains(msg, "--config") {
		t.Fatalf("the refusal (%s, %v) says %q; it should name default_config and the server's --config", j.Status, err, msg)
	}
}
