package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

func TestAgentsListTheDefaultFirst(t *testing.T) {
	s, project := newTestServer(t)
	old := agentInstalled
	agentInstalled = func(agent string) bool { return agent == "pi" || agent == "codex" }
	t.Cleanup(func() { agentInstalled = old })
	var agents []agentView
	w := do(s, "GET", "/api/agents", nil, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &agents) != nil || len(agents) < 4 || agents[0].Name != "codex" || !agents[0].Default || agents[1].Default {
		t.Fatalf("agents = %d %s", w.Code, w.Body)
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"relay":{"default":"pi"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agents = nil
	w = do(s, "GET", "/api/agents", nil, nil)
	if json.Unmarshal(w.Body.Bytes(), &agents) != nil || agents[0].Name != "pi" || !agents[0].Default || agents[0].Efforts == nil {
		t.Fatalf("with relay.default = %s", w.Body)
	}
}

func TestScreenReadsOnlyARunningAgent(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.ActorEagent: "m"}}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Agent: "claude", AgentLabel: "Claude", Dir: project, Host: "tmux", Target: "eagent-test-target"}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	var asked string
	old := captureScreen
	captureScreen = func(host, target string, lines int) (string, error) {
		asked = host + " " + target
		return "$ make test", nil
	}
	t.Cleanup(func() { captureScreen = old })
	w := do(s, "GET", "/api/sessions/"+sess.ID+"/agents/claude/screen", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "$ make test") || asked != "tmux eagent-test-target" {
		t.Fatalf("screen = %d %s (asked %q)", w.Code, w.Body, asked)
	}
	if w = do(s, "GET", "/api/sessions/"+sess.ID+"/agents/codex/screen", nil, nil); w.Code != 404 {
		t.Fatalf("unknown agent = %d", w.Code)
	}
	var d SessionDetail
	w = do(s, "GET", "/api/sessions/"+sess.ID, nil, nil)
	if json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Agents) != 1 || d.Agents[0].Who != "Claude" || d.Legacy {
		t.Fatalf("detail = %s", w.Body)
	}
}

// A session from before eagent was one loop opens, says so, and shows its
// history.
func TestLegacySessionOpens(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.LegacyOrchestrator: "a", event.LegacyTask: "b", event.LegacyNarrator: "c"}}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "old work"}),
		event.New(event.LegacyTaskCreate, event.LegacyOrchestrator, map[string]any{"id": "t1", "title": "Read"}),
		event.New(event.Assistant, event.LegacyNarrator, event.AssistantData{Model: "c", Usage: event.Usage{Input: 10}}),
		event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "done"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	var d SessionDetail
	w := do(s, "GET", "/api/sessions/"+sess.ID, nil, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || !d.Legacy || d.FirstMessage != "old work" || d.Status != "done" {
		t.Fatalf("legacy detail = %d %s", w.Code, w.Body)
	}
	var evs []EventView
	w = do(s, "GET", "/api/sessions/"+sess.ID+"/events", nil, nil)
	if json.Unmarshal(w.Body.Bytes(), &evs) != nil || len(evs) < 5 {
		t.Fatalf("legacy events = %s", w.Body)
	}
}

func TestStartSessionRefusesAMissingFolder(t *testing.T) {
	s, project := newTestServer(t)
	w := do(s, "POST", "/api/sessions", map[string]any{"prompt": "hi", "dir": filepath.Join(project, "nope")}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "not a directory") {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
}
