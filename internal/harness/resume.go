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
		r.adoptLegacy()
	}
}

// adoptLegacy continues a session that was written when eagent ran three
// models (an orchestrator, task workers and a narrator). Its record stays
// as it is and stays readable; the one loop starts a new subsession from a
// briefing the harness assembles, so its conversation never has to render
// events it has no words for. Called before the loop starts.
func (r *Runtime) adoptLegacy() {
	covers := r.st.LastSeq()
	var b strings.Builder
	b.WriteString("(This session was started by an earlier eagent, which did the work itself with helpers of its own. That way of working is gone: now a coding agent in a terminal does the work and you stand between it and the user. eagent assembled this briefing from the old record; session_search and session_read reach all of it.)\n")
	var told, notes []string
	for _, ev := range r.st.Events {
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
	body := strings.TrimSpace(b.String()) + "\n\n" + r.dossierFacts(covers)
	if err := r.openSubsession("resumed by an eagent with one loop", "upgrade", r.st.ContextTokens()); err != nil {
		r.ui.Log("resume: %v", err)
		return
	}
	r.append(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: body, CoversSeq: covers, By: "harness"}))
	r.turns.promptTokens = 0
	r.ui.Log("this session was written by an earlier eagent; it continues in a new subsession from a briefing")
}
