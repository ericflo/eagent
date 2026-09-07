package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
)

// System prompts are fixed for the life of a subsession so that the cached
// prefix survives every turn. Anything that changes goes into the steering
// text appended as the final user message (see steer* below).

func (r *Runtime) orchestratorSystem() string {
	return r.prompts.Render("ORCHESTRATOR.md", prompts.OrchestratorData{Project: r.projectPath(), Instructions: r.cfg.Instructions, Interactive: r.opts.Interactive})
}

func (r *Runtime) taskSystem() string {
	return r.prompts.Render("TASK-WORKER.md", prompts.TaskData{Project: r.projectPath(), Instructions: r.cfg.Instructions})
}

func (r *Runtime) narratorSystem() string {
	persona := strings.TrimSpace(r.cfg.Persona)
	if persona == "" {
		persona = strings.TrimSpace(r.prompts.Render("PERSONA.md", nil))
	}
	return r.prompts.Render("NARRATOR.md", prompts.NarratorData{Persona: persona})
}

// ---- steering ------------------------------------------------------------

// steerOrchestrator is appended as the final user message and never
// persisted, so the cached prefix stays intact.
func steerOrchestrator(st *state.State, now time.Time, ctxTokens, rolloverTokens int, reason string, calls int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[harness %s]", now.Local().Format("15:04:05"))
	if reason != "" {
		fmt.Fprintf(&b, " wake: %s.", reason)
	}
	running := st.RunningTasks()
	if len(running) > 0 {
		var ids []string
		for _, t := range running {
			ids = append(ids, fmt.Sprintf("%s (%s, %s)", t.ID, t.Status, since(t.Created, now)))
		}
		fmt.Fprintf(&b, " Tasks in flight: %s.", strings.Join(ids, ", "))
	}
	if procs := st.RunningProcs(); len(procs) > 0 {
		var hs []string
		for _, p := range procs {
			if p.Actor == "orchestrator" && p.Task == "" {
				hs = append(hs, p.Handle)
			}
		}
		if len(hs) > 0 {
			fmt.Fprintf(&b, " Your processes still running: %s.", strings.Join(hs, ", "))
		}
	}
	if sc := st.ActiveSchedules(); len(sc) > 0 {
		var ss []string
		for _, s := range sc {
			ss = append(ss, fmt.Sprintf("%s %q", s.ID, s.Spec))
		}
		fmt.Fprintf(&b, " Schedules: %s.", strings.Join(ss, ", "))
	}
	if ctxTokens > 0 {
		fmt.Fprintf(&b, " Context: %dk of %dk tokens before a dossier reset.", ctxTokens/1000, rolloverTokens/1000)
	}
	if calls >= 30 {
		fmt.Fprintf(&b, " You have made %d consecutive calls this turn; if you are looping or polling, delegate or use wait/yield instead.", calls)
	}
	b.WriteString(" Continue. Respond with tool calls; your text is not shown to the user.")
	return b.String()
}

func steerTask(t *state.Task, turn, maxTurns int, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[harness %s] Task %s, call %d of %d.", now.Local().Format("15:04:05"), t.ID, turn, maxTurns)
	switch remaining := maxTurns - turn; {
	case remaining <= 0:
		b.WriteString(" THIS IS YOUR LAST CALL. Call complete_task now with an honest report: what is done, what is verified, what is not. Any other tool call will be discarded and the task will be reported as failed.")
		return b.String()
	case remaining <= 10:
		fmt.Fprintf(&b, " Only %d calls remain: stop expanding scope, finish the essential piece, and call complete_task with an honest report of what is done and what is not.", remaining)
	}
	b.WriteString(" Respond with tool calls; call complete_task when finished.")
	return b.String()
}

// narratorWake reasons.
const (
	wakeNote     = "the orchestrator left a note"
	wakeUser     = "the user wrote"
	wakeTask     = "a task finished"
	wakeYield    = "the orchestrator is idle"
	wakeDone     = "the orchestrator declared the work done"
	wakePeriodic = "periodic check while work continues"
	wakeError    = "an error occurred"
	wakeFinal    = "final"
)

func steerNarrator(st *state.State, now time.Time, reason string, interactive bool, lastMessage string, mustSpeak bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[harness %s] You were woken because: %s.", now.Local().Format("15:04:05"), reason)
	switch reason {
	case wakeFinal:
		if mustSpeak {
			b.WriteString(" The session is ending now and the user has heard nothing yet. Send a complete final report (what was built, where, how to run it, what was verified, what is missing). Do not hold.")
		} else {
			b.WriteString(" The session is ending now. If your last message already covers the final state (what was built, where, how to run it, what was verified, what is missing), hold; otherwise send one final report with what is missing from it. Never repeat yourself.")
		}
	case wakeDone:
		b.WriteString(" Send the final report now unless your last message already covers everything; in that case hold.")
	case wakeError:
		if interactive {
			b.WriteString(" The orchestrator has stopped because its model calls keep failing. Tell the user plainly what happened, what state the work is in, and that typing a message will make it try again.")
		} else {
			b.WriteString(" The orchestrator has stopped because its model calls keep failing, and this non-interactive session ends now. Tell the user plainly what happened, what state the work is in, and that `eagent -c` resumes it. Do not say it will retry on its own.")
		}
	case wakeYield:
		if interactive {
			b.WriteString(" The orchestrator is waiting. If it asked for a decision, ask the user (ask_user). Otherwise tell the user where things stand, briefly, if that has changed since your last message.")
		} else {
			b.WriteString(" The orchestrator is waiting and the user is not present to answer; the session will end so they can resume later. Tell the user exactly what is needed from them and what state the work is in.")
		}
	}
	if !interactive {
		b.WriteString(" This is a non-interactive session: ask_user cannot receive an answer, so prefer send_message.")
	}
	if running := st.RunningTasks(); len(running) > 0 {
		fmt.Fprintf(&b, " %d task(s) are still running.", len(running))
	}
	if lastMessage != "" {
		fmt.Fprintf(&b, " Your last message to the user (do not repeat it): %q", clipTail(lastMessage, 400))
	}
	b.WriteString(" Respond with exactly one tool call.")
	return b.String()
}

func (r *Runtime) dossierTask(sessionDir string, files []string, st *state.State, reason string) string {
	var running []string
	for _, t := range st.RunningTasks() {
		if t.Kind != "dossier" {
			running = append(running, t.ID+": "+t.Title)
		}
	}
	return r.prompts.Render("COMPACTION-DOSSIER.md", prompts.DossierData{SessionDir: sessionDir, Files: strings.Join(files, ", "), Reason: reason, RunningTasks: running})
}

func since(t, now time.Time) string {
	d := now.Sub(t).Round(time.Second)
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func clipTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
