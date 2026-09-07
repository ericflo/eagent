package harness

import (
	"fmt"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// closeInterrupted repairs state after a restart: dangling tool calls get
// error results, running tasks and processes are marked interrupted, and the
// orchestrator is told what happened. Called before the loop starts.
func (r *Runtime) closeInterrupted() {
	var closed []string
	now := time.Now()

	// Processes cannot survive a restart.
	for _, p := range r.st.RunningProcs() {
		r.append(event.New(event.ProcExit, p.Actor, event.ProcExitData{
			Handle: p.Handle, ExitCode: -1, Reason: "lost",
			DurationMS: now.Sub(p.Started).Milliseconds(), Notify: p.Actor == event.ActorOrchestrator && p.Task == "",
		}).WithTask(p.Task))
		closed = append(closed, fmt.Sprintf("process %s (%s) was lost", p.Handle, firstLine(p.Command, 60)))
	}
	// Orchestrator tool calls without results.
	for _, tc := range r.st.DanglingCalls(event.ActorOrchestrator, "") {
		r.append(event.New(event.ToolResult, event.ActorOrchestrator, event.ToolResultData{
			CallID: tc.ID, Name: tc.Name, Output: "interrupted: the runner restarted before this tool finished", IsError: true,
		}))
	}
	// Tasks: mark interrupted; the orchestrator can re-delegate.
	for _, t := range r.st.RunningTasks() {
		for _, tc := range r.st.DanglingCalls(event.ActorTask, t.ID) {
			r.append(event.New(event.ToolResult, event.ActorTask, event.ToolResultData{
				CallID: tc.ID, Name: tc.Name, Output: "interrupted: the runner restarted", IsError: true,
			}).WithTask(t.ID))
		}
		if t.Kind == "dossier" {
			r.append(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: t.ID, Status: "interrupted", Summary: "the runner restarted while the dossier was being written"}).WithTask(t.ID))
			closed = append(closed, "dossier task "+t.ID+" restarted")
			continue
		}
		r.append(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: t.ID, Status: "interrupted", Summary: "the runner restarted while this task was running; its partial work may be on disk. Check, then re-delegate what is missing."}).WithTask(t.ID))
		closed = append(closed, fmt.Sprintf("task %s (%s) was interrupted", t.ID, t.Title))
	}
	r.append(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: r.opts.Interactive, Closed: closed}))

	// A rollover that never received its dossier needs a fresh dossier task.
	if cur := r.st.Current(); cur != nil && cur.Reason == "rollover" && cur.Dossier == "" {
		r.rolling = true
		r.createDossierTask("the previous runner stopped before the dossier was written")
	}

	// Tell the orchestrator, unless it had finished and nothing new arrived.
	if !(r.st.LastYield != nil && r.st.LastYield.Done) || len(closed) > 0 {
		msg := fmt.Sprintf("[Session resumed at %s after an interruption.]", now.Local().Format("2006-01-02 15:04:05"))
		if len(closed) > 0 {
			msg += " What was lost: "
			for i, c := range closed {
				if i > 0 {
					msg += "; "
				}
				msg += c
			}
			msg += ". Check the filesystem for partial work before redoing anything."
		}
		if r.st.LastYield != nil && r.st.LastYield.Done {
			msg += " You had declared the work done; confirm nothing is left, then yield again."
		} else if r.st.LastYield != nil {
			msg += " You were waiting: " + r.st.LastYield.Reason
		}
		if r.st.Question != nil {
			msg += " A question to the user is still pending: " + firstLine(r.st.Question.Text, 200)
		}
		r.append(event.New(event.HarnessMessage, event.ActorOrchestrator, event.HarnessMessageData{Text: msg}))
	}
	r.lastOrchSeen = 0
	for _, ev := range r.st.Events {
		if ev.Actor != event.ActorNarrator && state.Observe(ev, 100) != "" {
			r.narrWorthy = ev.Seq
		}
		if ev.Type == event.Assistant && ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			var d event.AssistantData
			_ = ev.Decode(&d)
			r.lastOrchSeen = d.SeenSeq
		}
		if ev.Type == event.Assistant && ev.Actor == event.ActorNarrator {
			var d event.AssistantData
			_ = ev.Decode(&d)
			r.narrLastSeen = d.SeenSeq
		}
		if ev.Type == event.NarratorMessage {
			var d event.NarratorMessageData
			_ = ev.Decode(&d)
			r.narrLastSaid = d.Text
			r.narrSaidSeq = ev.Seq
		}
	}
	// Continue process handles after replayed ones.
	r.procs.SetSeq(len(r.st.ProcOrder))
	r.procCursor = map[string]int{}
}
