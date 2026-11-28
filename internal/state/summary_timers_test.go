package state

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// /status and `eagent replay` list each timer that will fire again, as
// they did before eagent was one loop: id, kind, spec and the next firing.
func TestSummaryListsEachTimer(t *testing.T) {
	next := time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)
	st := Replay([]event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p", TZ: "UTC"}),
		event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s1", Kind: "loop", Spec: "10m", Note: "check the build", Next: next}),
		event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s2", Kind: "once", Spec: "2026-09-06T13:00:00Z", Note: "gone", Next: next}),
		event.New(event.ScheduleCancel, event.ActorEagent, event.ScheduleCancelData{ID: "s2"}),
	})
	out := st.Summary()
	if !strings.Contains(out, "timers: 1\n") || !strings.Contains(out, "  s1 loop \"10m\" next 12:30:00\n") {
		t.Fatalf("summary does not list the timer:\n%s", out)
	}
	if strings.Contains(out, "s2") {
		t.Fatalf("a cancelled timer is listed:\n%s", out)
	}
}
