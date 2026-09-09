package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

func sessionStartCommand(t *testing.T, s *settings.Service, g control.Grant, prompt string) command {
	t.Helper()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "session.start", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"prompt": prompt}}
	raw, _ := json.Marshal(p)
	return command{ID: randomID(), UserID: "fixture-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
}

func TestSessionStartIsAdvertisedAsACostAction(t *testing.T) {
	s, g, _ := routeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no provider call expected") })
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	var found *control.Action
	for i := range view.Descriptor.Actions {
		if view.Descriptor.Actions[i].Operation == "session.start" {
			found = &view.Descriptor.Actions[i]
		}
	}
	if found == nil || found.Class != "cost" || found.Parameters.Properties["prompt"].MaxLength != 32768 {
		t.Fatalf("session.start not advertised as expected: %#v", view.Descriptor.Actions)
	}
	if err := view.Descriptor.Validate(g); err != nil {
		t.Fatalf("descriptor rejected by its own grant: %v", err)
	}
	narrow := g
	narrow.Operations = []string{"settings.refresh"}
	view, err = s.RemoteSnapshot(narrow)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range view.Descriptor.Actions {
		if a.Operation == "session.start" {
			t.Fatal("session.start advertised without the operation granted")
		}
	}
}

func TestSessionStartRunsTheRegisteredStarterExactlyOnce(t *testing.T) {
	s, g, _ := routeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no provider call expected") })
	var calls atomic.Int32
	restore := SetSessionStarter(func(ctx context.Context, project string, req SessionRequest) (SessionStart, error) {
		calls.Add(1)
		if project != s.Project || req.Prompt != "Build the thing" || req.Dir != "" {
			t.Errorf("starter received %#v in %q", req, project)
		}
		return SessionStart{ID: "1788000000000", Host: "in_process", Interactive: true, Message: "hosted"}, nil
	})
	defer restore()
	q := sessionStartCommand(t, s, g, "Build the thing")
	first, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || first.Status != "succeeded" {
		t.Fatal(first.Status, first.Result, err)
	}
	if first.Result["session_id"] != "1788000000000" || first.Result["thread"] != "ext:eagent:1788000000000" || first.Result["interactive"] != true {
		t.Fatalf("unexpected result: %#v", first.Result)
	}
	// Redelivery of the same command returns the durable result without a second start.
	again, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || again.Status != "succeeded" || again.Result["session_id"] != "1788000000000" {
		t.Fatal(again.Status, again.Result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("starter ran %d times", calls.Load())
	}
	// A claim whose local journal is missing is reconciled, never re-run.
	fresh := sessionStartCommand(t, s, g, "Build the thing")
	unknown, err := executeCommand(context.Background(), s, g, fresh, true)
	if err != nil || unknown.Status != "unknown" {
		t.Fatal(unknown.Status, unknown.Result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("reconciliation started a session: %d calls", calls.Load())
	}
}

func TestSessionStartRejectsAnEmptyPromptAndReportsStarterFailures(t *testing.T) {
	s, g, _ := routeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no provider call expected") })
	restore := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{}, errors.New("no model key")
	})
	defer restore()
	blank := sessionStartCommand(t, s, g, "   ")
	if got, err := executeCommand(context.Background(), s, g, blank, false); err != nil || got.Status != "rejected" {
		t.Fatal(got.Status, got.Result, err)
	}
	failing := sessionStartCommand(t, s, g, "Do it")
	got, err := executeCommand(context.Background(), s, g, failing, false)
	if err != nil || got.Status != "rejected" || got.Result["message"] != "no model key" {
		t.Fatal(got.Status, got.Result, err)
	}
	// A settings save that landed after the phone loaded its page does not
	// block starting a session; only a runtime generation would.
	stale := sessionStartCommand(t, s, g, "Do it")
	stale.Proposal.ExpectedVersion = "older"
	raw, _ := json.Marshal(stale.Proposal)
	stale.Digest = artifact.Digest(raw)
	restore2 := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{ID: "1788000000001", Host: "in_process", Interactive: true}, nil
	})
	defer restore2()
	if got, err := executeCommand(context.Background(), s, g, stale, false); err != nil || got.Status != "succeeded" {
		t.Fatal(got.Status, got.Result, err)
	}
}

func TestDetachedSpawnReportsTheAnnouncedSession(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required")
	}
	project := t.TempDir()
	previous := spawnCommand
	defer func() { spawnCommand = previous }()
	spawnCommand = func(project string, req SessionRequest, announce string) (*exec.Cmd, error) {
		cmd := exec.Command("sh", "-c", `printf '%s\n' "$PROMPT" >&2; printf '1788000000042\n' > "$EAGENT_ANNOUNCE_SESSION"; sleep 2`)
		cmd.Env = append(cmd.Env, "PROMPT="+req.Prompt, AnnounceEnv+"="+announce, "PATH="+os.Getenv("PATH"))
		return cmd, nil
	}
	started, err := spawnDetachedSession(context.Background(), project, SessionRequest{Prompt: "Build the thing"})
	if err != nil || started.ID != "1788000000042" || started.Host != "detached" || started.Interactive {
		t.Fatalf("%#v %v", started, err)
	}
	spawnCommand = func(project string, req SessionRequest, announce string) (*exec.Cmd, error) {
		cmd := exec.Command("sh", "-c", `echo "no TOGETHER_API_KEY" >&2; exit 3`)
		cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))
		return cmd, nil
	}
	_, err = spawnDetachedSession(context.Background(), project, SessionRequest{Prompt: "Build the thing"})
	if err == nil || !strings.Contains(err.Error(), "no TOGETHER_API_KEY") {
		t.Fatalf("expected the child's output in the error, got %v", err)
	}
}

func TestReconnectWidensAnExistingGrantWithoutDroppingAnything(t *testing.T) {
	want := (&settings.Service{Project: t.TempDir()}).Grant()
	old := want
	old.Operations = []string{"settings.apply", "settings.refresh", "custom.extra"}
	old.Classes = []string{"preference"}
	other := control.Grant{Key: "session-abc", Label: "session", Scope: "session", Operations: []string{"settings.apply"}, Classes: []string{"preference"}}
	wider, changed := widenGrant([]control.Grant{other, old}, want)
	if !changed {
		t.Fatal("a grant missing session.start was reported as complete")
	}
	for _, op := range append(want.Operations, "custom.extra") {
		if !slices.Contains(wider.Operations, op) {
			t.Fatalf("operation %s missing from widened grant %v", op, wider.Operations)
		}
	}
	if !slices.Contains(wider.Classes, "cost") || !slices.Contains(wider.Classes, "preference") {
		t.Fatalf("classes not widened: %v", wider.Classes)
	}
	replaced := replaceGrant([]control.Grant{other, old}, wider)
	if len(replaced) != 2 || replaced[0].Key != "session-abc" || len(replaced[1].Operations) != len(wider.Operations) {
		t.Fatalf("replaceGrant changed the wrong entry: %#v", replaced)
	}
	if _, changed := widenGrant([]control.Grant{wider}, want); changed {
		t.Fatal("a complete grant was reported as needing widening")
	}
	if got, changed := widenGrant(nil, want); !changed || got.Key != want.Key {
		t.Fatal("a missing grant should be requested whole")
	}
}

func TestSessionStartDirectoryIsCheckedAndPassedThrough(t *testing.T) {
	s, g, _ := routeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no provider call expected") })
	var got SessionRequest
	restore := SetSessionStarter(func(_ context.Context, _ string, req SessionRequest) (SessionStart, error) {
		got = req
		return SessionStart{ID: "1788000000007", Host: "in_process", Interactive: true}, nil
	})
	defer restore()
	elsewhere := t.TempDir()
	q := sessionStartCommand(t, s, g, "Look around")
	q.Proposal.Parameters["cwd"] = elsewhere
	raw, _ := json.Marshal(q.Proposal)
	q.Digest = artifact.Digest(raw)
	first, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || first.Status != "succeeded" || got.Dir != elsewhere || first.Result["cwd"] != elsewhere {
		t.Fatalf("%s %v %#v %v", first.Status, first.Result, got, err)
	}
	for _, bad := range []string{"relative/path", filepath.Join(elsewhere, "missing")} {
		q := sessionStartCommand(t, s, g, "Look around")
		q.Proposal.Parameters["cwd"] = bad
		raw, _ := json.Marshal(q.Proposal)
		q.Digest = artifact.Digest(raw)
		got = SessionRequest{}
		res, err := executeCommand(context.Background(), s, g, q, false)
		if err != nil || res.Status != "rejected" || got.Prompt != "" {
			t.Fatalf("cwd %q: %s %v (starter ran: %v)", bad, res.Status, res.Result, got.Prompt != "")
		}
	}
}

func TestProjectDirectoriesListsRootRecentChildrenAndSiblings(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "proj")
	for _, d := range []string{filepath.Join(project, "src"), filepath.Join(project, ".git"), filepath.Join(project, "node_modules"), filepath.Join(base, "sibling"), filepath.Join(base, ".hidden")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.CwdChange, event.ActorOrchestrator, event.CwdChangeData{Path: filepath.Join(base, "sibling")})); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.CwdChange, event.ActorTask, event.CwdChangeData{Path: filepath.Join(project, "src")}).WithTask("t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.CwdChange, event.ActorOrchestrator, event.CwdChangeData{Path: filepath.Join(base, "gone")})); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	dirs := projectDirectories(project)
	real, _ := filepath.EvalSymlinks(project)
	if dirs["root"] != real {
		t.Fatalf("root = %v", dirs["root"])
	}
	if got := dirs["children"].([]string); len(got) != 1 || filepath.Base(got[0]) != "src" {
		t.Fatalf("children = %v", got)
	}
	if got := dirs["siblings"].([]string); len(got) != 1 || filepath.Base(got[0]) != "sibling" {
		t.Fatalf("siblings = %v", got)
	}
	if got := dirs["recent"].([]string); len(got) != 1 || got[0] != filepath.Join(base, "sibling") {
		t.Fatalf("recent = %v (a worker's move and a vanished directory do not count)", got)
	}
}
