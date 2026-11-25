package projection

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

type logBuilder struct {
	seq    int64
	t      time.Time
	events []event.Event
}

func newLog() *logBuilder {
	b := &logBuilder{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	b.add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	return b
}

func (b *logBuilder) add(ev event.Event) {
	b.seq++
	b.t = b.t.Add(time.Second)
	ev.Seq, ev.Time = b.seq, b.t
	b.events = append(b.events, ev)
}

func (b *logBuilder) state() *state.State { return state.Replay(b.events) }

func detailJSON(t *testing.T, st *state.State) map[string]any {
	t.Helper()
	raw, err := json.Marshal(Detail(Metadata{ID: "s1"}, st))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The session detail carries the timers that will fire again, in main's
// shape: id, kind, spec, note, next, fires.
func TestDetailCarriesSchedules(t *testing.T) {
	b := newLog()
	if s, ok := detailJSON(t, b.state())["schedules"].([]any); !ok || len(s) != 0 {
		t.Fatalf("a session without timers has schedules %v, want []", detailJSON(t, b.state())["schedules"])
	}
	next := time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)
	b.add(event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s1", Kind: "loop", Spec: "10m", Note: "check the build", Next: next}))
	b.add(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: "s1", Note: "check the build", Next: next.Add(10 * time.Minute)}))
	b.add(event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s2", Kind: "once", Spec: "x", Note: "gone", Next: next}))
	b.add(event.New(event.ScheduleCancel, event.ActorEagent, event.ScheduleCancelData{ID: "s2"}))
	s := detailJSON(t, b.state())["schedules"].([]any)
	if len(s) != 1 {
		t.Fatalf("schedules = %v, want only s1", s)
	}
	got := s[0].(map[string]any)
	want := map[string]any{"id": "s1", "kind": "loop", "spec": "10m", "note": "check the build", "next": "2026-09-06T12:40:00Z", "fires": float64(1)}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("schedule %s = %v, want %v (%v)", k, got[k], v, got)
		}
	}
}

func legacyLog() *logBuilder {
	b := &logBuilder{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	b.add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p", Models: map[string]string{event.LegacyOrchestrator: "a", event.LegacyTask: "b", event.LegacyNarrator: "c"}}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix it"}))
	return b
}

// An old session's detail says how it stopped (its orchestrator's last
// yield), as main's did; a new session's carries neither field.
func TestDetailCarriesTheClosingReasonOfAnOldSession(t *testing.T) {
	b := legacyLog()
	b.add(event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: false, Reason: "waiting on the tests"}))
	b.add(event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: true, Reason: "fixed and pushed"}))
	d := detailJSON(t, b.state())
	if d["done"] != true || d["last_reason"] != "fixed and pushed" || d["idle"] != true || d["status"] != "idle" {
		t.Fatalf("old session detail: done=%v last_reason=%v idle=%v status=%v", d["done"], d["last_reason"], d["idle"], d["status"])
	}
	b.add(event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: false, Reason: "asked the user"}))
	if d := detailJSON(t, b.state()); d["done"] != false || d["last_reason"] != "asked the user" {
		t.Fatalf("a waiting yield: done=%v last_reason=%v", d["done"], d["last_reason"])
	}
	// The user wrote after it: the orchestrator was woken, as in main.
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "and the docs"}))
	if d := detailJSON(t, b.state()); d["done"] != nil || d["last_reason"] != nil || d["idle"] != false || d["status"] != "running" {
		t.Fatalf("after a later message: done=%v last_reason=%v idle=%v status=%v", d["done"], d["last_reason"], d["idle"], d["status"])
	}

	n := newLog()
	n.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hi"}))
	raw, _ := json.Marshal(Detail(Metadata{}, n.state()))
	if strings.Contains(string(raw), `"done"`) || strings.Contains(string(raw), `"last_reason"`) {
		t.Fatalf("a new session carries done or last_reason: %s", raw)
	}
}

// A session with work under way is not idle: eagent's own turn open, or an
// agent starting, working or stopped on a dialog.
func TestIdleOnlyWhenNothingIsUnderWay(t *testing.T) {
	b := newLog()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hi"}))
	if !Idle(b.state()) {
		t.Fatalf("a session with nothing under way is idle")
	}
	b.add(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: "user"}))
	if Idle(b.state()) || Summary(Metadata{}, b.state()).Status != "running" {
		t.Fatalf("eagent's open turn reads as idle")
	}
	b.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "done"}))
	if !Idle(b.state()) {
		t.Fatalf("after its turn the session is idle")
	}
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Agent: "claude", Dir: "/p"}))
	if Idle(b.state()) {
		t.Fatalf("a starting agent reads as idle")
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}))
	if Idle(b.state()) {
		t.Fatalf("a working agent reads as idle")
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: "trust this folder?"}))
	if Idle(b.state()) || Summary(Metadata{}, b.state()).Status != "running" || detailJSON(t, b.state())["idle"] != false {
		t.Fatalf("an agent stopped on a dialog reads as idle")
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "idle"}))
	if !Idle(b.state()) || Summary(Metadata{}, b.state()).Status != "idle" {
		t.Fatalf("an idle agent leaves the session idle")
	}
}
