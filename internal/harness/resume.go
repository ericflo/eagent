package harness

import (
	"fmt"
	"strings"

	"github.com/ericflo/eagent/internal/event"
)

// closeInterrupted repairs the record after a restart: the agents that kept
// running in their terminals are picked up again, tool calls that never got
// a result get one, a turn left open is closed, and a session written by
// the three-actor design is given a briefing to continue from. Called
// before the loop starts.
func (r *Runtime) closeInterrupted() {
	// The agents run in terminals of their own and survive eagent stopping;
	// pick them up again, and say which are gone.
	closed := r.reattachRelays()
	for _, tc := range r.st.DanglingCalls(event.ActorEagent) {
		r.append(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{
			CallID: tc.ID, Name: tc.Name, Output: "interrupted: eagent restarted before this finished", IsError: true,
		}))
	}
	if r.st.Turns[event.ActorEagent] > 0 {
		r.append(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "interrupted"}))
	}
	legacy := r.st.Legacy() && r.st.Calls[event.ActorEagent] == 0
	r.append(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: r.opts.Interactive, Closed: closed}))
	if legacy {
		// A session written when eagent ran three models (an orchestrator,
		// task workers and a narrator). Its record stays as it is and stays
		// readable. Before anything else the one loop writes a dossier from
		// that record (dossier.go, with a map over every old file, the old
		// dossiers included) and continues in a new subsession, so its
		// conversation never has to render events it has no words for.
		r.turns.adopt = true
	}
}

// legacyBriefing stands in for the model's part of the dossier that a
// session of the three-actor design continues from, when the model could
// not write it: what the user was last told, how the old session left
// things, the last report of each agent, and the last dossier the old
// session wrote, in their own words. partial is whatever the model did
// write. Loop goroutine only.
func (r *Runtime) legacyBriefing(partial string) string {
	var b strings.Builder
	b.WriteString("(This session was started by an earlier eagent, which did the work itself with helpers of its own. That way of working is gone: now a coding agent in a terminal does the work and you stand between it and the user. Your model could not write this briefing, so eagent assembled it from the old record. It has no MAP by topic of its own; the old session's last briefing, below, has one, and session_search and session_read reach all of the record.)\n")
	var told, notes []string
	var last *event.Event
	for i, ev := range r.st.Events {
		switch ev.Type {
		case event.Message:
			var d event.MessageData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				told = append(told, fmt.Sprintf("- [%s] %s", r.when(ev.Time), clipRunes(oneLine(d.Text, 700), 700)))
			}
		case event.LegacyYield:
			var d event.LegacyYieldData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Reason) != "" {
				state := "it stopped to wait"
				if d.Done {
					state = "it called the work done"
				}
				notes = append(notes, fmt.Sprintf("- [%s] %s: %s", r.when(ev.Time), state, clipRunes(oneLine(d.Reason, 500), 500)))
			}
		case event.Dossier:
			last = &r.st.Events[i]
		}
	}
	if len(told) > 0 {
		b.WriteString("\nWHAT THE USER WAS LAST TOLD, oldest first:\n" + strings.Join(told[max(0, len(told)-6):], "\n") + "\n")
	}
	if len(notes) > 0 {
		b.WriteString("\nHOW THE OLD SESSION LEFT THINGS:\n" + strings.Join(notes[max(0, len(notes)-3):], "\n") + "\n")
	}
	for _, name := range r.st.RelayOrder {
		if rel := r.st.Relays[name]; rel != nil && rel.LastTurn != "" {
			fmt.Fprintf(&b, "\n%s'S LAST REPORT (%s):\n%s\n", strings.ToUpper(rel.Who()), r.when(rel.LastTurnAt), clipRunes(rel.LastTurn, 3000))
		}
	}
	if last != nil {
		var d event.DossierData
		if last.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
			fmt.Fprintf(&b, "\nTHE OLD SESSION'S LAST BRIEFING (%s, written %s; what happened after it is in the files that follow):\n%s\n", last.Source, r.when(last.Time), clipRunes(strings.TrimSpace(d.Text), 16000))
		}
	}
	if strings.TrimSpace(partial) != "" {
		b.WriteString("\nWHAT THE MODEL DID WRITE:\n" + strings.TrimSpace(partial) + "\n")
	}
	return b.String()
}
