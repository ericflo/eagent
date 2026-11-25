package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// The list says a session is busy, not idle, while eagent is in the middle
// of something: a model call the log does not show yet, a briefing being
// written, its own open turn, or an agent stopped on a dialog.
func TestSessionWithWorkUnderWayIsNotIdle(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	appendAll := func(evs ...event.Event) {
		for _, ev := range evs {
			if _, err := sess.Append(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendAll(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: project}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hi"}),
	)
	h := fakeHosted()
	h.ui = newWebUI(func(string, ...any) {})
	s.mu.Lock()
	s.running[sess.ID] = h
	s.mu.Unlock()
	status := func() (string, bool) {
		t.Helper()
		var d SessionDetail
		w := do(s, "GET", "/api/sessions/"+sess.ID, nil, nil)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil {
			t.Fatalf("detail = %d %s", w.Code, w.Body)
		}
		var list []SessionSummary
		w = do(s, "GET", "/api/sessions", nil, nil)
		if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].Status != d.Status {
			t.Fatalf("list = %s, detail status %s", w.Body, d.Status)
		}
		return d.Status, d.Idle
	}
	if st, idle := status(); st != "idle" || !idle {
		t.Fatalf("nothing under way: %s idle=%v", st, idle)
	}
	h.ui.Status(harness.Status{Busy: true})
	if st, idle := status(); st != "running" || idle {
		t.Fatalf("eagent thinking: %s idle=%v", st, idle)
	}
	h.ui.Status(harness.Status{Compacting: true})
	if st, idle := status(); st != "running" || idle {
		t.Fatalf("eagent writing a briefing: %s idle=%v", st, idle)
	}
	h.ui.Status(harness.Status{})
	appendAll(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: "user"}))
	if st, _ := status(); st != "running" {
		t.Fatalf("eagent's turn open: %s", st)
	}
	appendAll(
		event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "done"}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Agent: "claude", Dir: project}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: "trust this folder?"}),
	)
	if st, _ := status(); st != "running" {
		t.Fatalf("agent stopped on a dialog: %s", st)
	}
	appendAll(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "idle"}))
	if st, _ := status(); st != "idle" {
		t.Fatalf("agent idle: %s", st)
	}
}

// An old session's detail says how it stopped, from its last yield.
func TestLegacySessionCarriesItsClosingReason(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.LegacyOrchestrator: "a", event.LegacyTask: "b", event.LegacyNarrator: "c"}}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "old work"}),
		event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: true, Reason: "all fixed"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	var d SessionDetail
	w := do(s, "GET", "/api/sessions/"+sess.ID, nil, nil)
	if json.Unmarshal(w.Body.Bytes(), &d) != nil || d.Done == nil || !*d.Done || d.LastReason != "all fixed" || d.Schedules == nil {
		t.Fatalf("legacy detail = %s", w.Body)
	}
}
