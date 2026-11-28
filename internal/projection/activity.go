package projection

import (
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// ScheduleView is one timer eagent set for itself that will fire again.
type ScheduleView struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Spec  string    `json:"spec"`
	Note  string    `json:"note"`
	Next  time.Time `json:"next"`
	Fires int       `json:"fires"`
}

func schedulesOf(st *state.State) []ScheduleView {
	out := []ScheduleView{}
	for _, sc := range st.ActiveSchedules() {
		out = append(out, ScheduleView{ID: sc.ID, Kind: sc.Kind, Spec: sc.Spec, Note: sc.Note, Next: sc.Next, Fires: sc.Fires})
	}
	return out
}

// LegacyClose is how a log from before eagent was one loop stopped: the
// orchestrator's last yield, as long as nothing came after it that woke the
// session again (the user writing, or the one loop taking the session
// over). ok is false for any other log.
func LegacyClose(st *state.State) (done bool, reason string, ok bool) {
	var last *event.LegacyYieldData
	for _, ev := range st.Events {
		switch {
		case ev.Type == event.LegacyYield && ev.Actor == event.LegacyOrchestrator && ev.Task == "":
			var d event.LegacyYieldData
			if ev.Decode(&d) == nil {
				last = &d
			}
		case ev.Type == event.UserMessage || ev.Type == event.UserAnswer || ev.Actor == event.ActorEagent:
			last = nil
		}
	}
	if last == nil {
		return false, "", false
	}
	return last.Done, last.Reason, true
}

// legacyOnly says the log was written by the three-actor design and the one
// loop has not taken it over since.
func legacyOnly(st *state.State) bool {
	if !st.Legacy() {
		return false
	}
	for _, ev := range st.Events {
		if ev.Actor == event.ActorEagent {
			return false
		}
		if ev.Type == event.SubsessionStart {
			var d event.SubsessionStartData
			if ev.Decode(&d) == nil && d.Reason == "upgrade" {
				return false
			}
		}
	}
	return true
}

// Idle reports, from the log alone, that nothing more is expected until the
// user writes. It is false while eagent's own turn is open and while any
// agent is starting, working, stopped on a dialog (eagent has to deal with
// it) or has a message on its way. An old log is idle once its orchestrator
// yielded, as it was then.
//
// A briefing being written for a fresh conversation leaves nothing in the
// log until it is done; only the process running the session knows (see
// the web server's hosted status).
func Idle(st *state.State) bool {
	if legacyOnly(st) {
		_, _, ok := LegacyClose(st)
		return ok
	}
	if st.Turns[event.ActorEagent] > 0 || !st.Waiting() {
		return false
	}
	for _, r := range st.ActiveRelays() {
		if r.State == "blocked" {
			return false
		}
	}
	return true
}
