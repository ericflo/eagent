package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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

// The page asks "Stop this session and its agent?". A stop pressed once
// the server's shutdown has begun (stopForShutdown) answers "Stopped" and
// takes the session off the list the next server brings back, but nothing
// stops its agent: the shutdown's end of the run leaves agents running, and
// with the session never brought back nothing watches the agent again. A
// stop the run takes (stopSession) kills every agent and records relay.end.
func TestSRStopDuringShutdownSaysStoppedWhileTheAgentRuns(t *testing.T) {
	s, project := newTestServer(t)
	killed := k5Kills(t)
	info := srAgentSession(t, project)
	if got := srActiveAgents(t, info); len(got) != 1 {
		t.Fatalf("setup: the record shows agents %q, want claude running", got)
	}
	// The server was running it when its shutdown began (rememberHosted),
	// and the shutdown has ended the run.
	s.mu.Lock()
	s.writeHosted([]string{info.ID}, time.Now())
	s.mu.Unlock()
	s.resuming.Lock()
	s.stopping = true // the server's shutdown has begun, and has ended the run
	s.resuming.Unlock()
	w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Stopped") {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if got := srActiveAgents(t, info); len(got) > 0 {
		t.Fatalf("the user pressed Stop (session and agent) and was told %s, but nothing stopped the agent: the record still shows %q running, and the session will not be brought back to watch it", strings.TrimSpace(w.Body.String()), got)
	}
	if !slices.Equal(*killed, []string{"tmux eagent-sr-claude"}) || !strings.Contains(w.Body.String(), "with ") {
		t.Fatalf("killed %q, answered %s", *killed, w.Body)
	}
	if got := k3Listed(t, project); len(got) != 0 {
		t.Fatalf("the next server would bring back %q", got)
	}
}

// k5Kills stands in for killing agents' terminals, and lists the ones
// killed.
func k5Kills(t *testing.T) *[]string {
	t.Helper()
	var killed []string
	old := killAgent
	killAgent = func(host, target string) error { killed = append(killed, host+" "+target); return nil }
	t.Cleanup(func() { killAgent = old })
	return &killed
}

// A Stop during the shutdown for a session this server was not running
// gets main's answer, and stops nothing.
func TestK5AStopDuringTheShutdownForASessionNotRunningIsRefusedAsOnMain(t *testing.T) {
	s, project := newTestServer(t)
	killed := k5Kills(t)
	info := srAgentSession(t, project)
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "session is not running") {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if got := srActiveAgents(t, info); len(got) != 1 || len(*killed) != 0 {
		t.Fatalf("agents %q on record, killed %q", got, *killed)
	}
}
