package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// srAgentSession is a stopped session whose record shows its agent
// running, as a run the server's shutdown ended leaves it: the agents run
// in terminals of their own and outlive the run.
func srAgentSession(t *testing.T, project string) store.Info {
	t.Helper()
	sess := newSession(t, project)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-sr-claude"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func srActiveAgents(t *testing.T, info store.Info) []string {
	t.Helper()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range state.Replay(evs).ActiveRelays() {
		names = append(names, r.Name)
	}
	return names
}

// A Stop during the shutdown for a session this server was not running
// gets main's answer, and stops nothing.
func TestK5AStopDuringTheShutdownForASessionNotRunningIsRefusedAsOnMain(t *testing.T) {
	s, project := newTestServer(t)
	info := srAgentSession(t, project)
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "session is not running") {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if got := srActiveAgents(t, info); len(got) != 1 {
		t.Fatalf("agents %q on record", got)
	}
}
