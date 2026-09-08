package harness

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// needsRollover reports whether the orchestrator's context has grown past
// the configured threshold. Loop goroutine only.
func (r *Runtime) needsRollover() bool {
	if r.rolling {
		return false
	}
	if r.st.Idle() {
		// Nothing to carry into a fresh context; roll when work arrives.
		return false
	}
	return r.st.ContextTokens(event.ActorOrchestrator) >= r.cfg.RolloverTokens
}

// rolloverFutile reports that the current subsession itself came from a
// rollover and its prompt was already at the threshold on the first
// orchestrator call (or the orchestrator never got a call in at all, as
// when the provider rejects the prompt as too long). Rolling again would
// rebuild the same oversized prompt, forever.
func (r *Runtime) rolloverFutile() bool {
	cur := r.st.Current()
	if cur == nil || cur.Reason != "rollover" {
		return false
	}
	for _, ev := range r.st.Events {
		if ev.Seq < cur.StartSeq || ev.Type != event.Assistant || ev.Actor != event.ActorOrchestrator {
			continue
		}
		var d event.AssistantData
		_ = ev.Decode(&d)
		return d.Usage.Input >= r.cfg.RolloverTokens
	}
	return true
}

// futileYieldReason marks the forced yield a futile rollover records.
const futileYieldReason = "a fresh context's prompt is already at the rollover threshold; the context cannot be reclaimed by another rollover. Raise rollover_tokens or shorten the standing instructions, then resume."

// futileAlreadyPaused reports that the most recent orchestrator event is the
// forced yield of a futile rollover: the pause is on the record and nothing
// the orchestrator did since has changed the picture. A later call of its
// own (which may succeed: the provider's window is larger than the
// threshold) re-arms the pause.
func (r *Runtime) futileAlreadyPaused() bool {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Actor != event.ActorOrchestrator {
			continue
		}
		switch ev.Type {
		case event.Yield:
			var d event.YieldData
			_ = ev.Decode(&d)
			return d.Forced && d.Reason == futileYieldReason
		case event.Assistant, event.TurnStart:
			return false
		}
	}
	return false
}

// startRollover closes the current subsession, opens the next, and asks the
// task worker for a dossier. The orchestrator stays paused until it lands.
// It returns false when nothing was started because a futile rollover is
// already paused on the record: the orchestrator may then take its turn
// with the prompt as it is.
func (r *Runtime) startRollover() bool {
	if r.rolloverFutile() {
		if r.futileAlreadyPaused() {
			return false
		}
		// A second rollover cannot reclaim the context: the fresh prompt is
		// already over the line. Pause once instead of spending forever.
		r.ui.Log("rollover: a fresh context is already at %dk tokens; the prompt cannot be shrunk by another rollover, pausing", r.cfg.RolloverTokens/1000)
		r.append(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Forced: true, Reason: futileYieldReason}))
		r.wakeNarrator(wakeError)
		return true
	}
	r.rolling = true
	tokens := r.st.ContextTokens(event.ActorOrchestrator)
	next, err := r.sess.NewSubsessionName(time.Now())
	if err != nil {
		r.ui.Log("rollover: %v", err)
		r.rolling = false
		return true
	}
	r.append(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: next, InputTokens: tokens}))
	if _, err := r.sess.OpenSubsession(next); err != nil {
		r.ui.Log("rollover: %v", err)
		r.rolling = false
		return true
	}
	idx := len(r.st.Subsessions)
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: idx, Reason: "rollover"}))
	// What the closed subsession still owed the orchestrator: an open
	// question, and answers it never got to read. The fresh view starts at
	// the dossier, so they are restated here.
	if msg := r.carriedOver(); msg != "" {
		r.append(event.New(event.HarnessMessage, event.ActorOrchestrator, event.HarnessMessageData{Text: msg}))
	}
	r.ui.Log("orchestrator context reached %dk tokens; starting subsession %d with a dossier", tokens/1000, idx+1)
	r.createDossierTask(fmt.Sprintf("the orchestrator's prompt reached %d tokens", tokens))
	return true
}

// carriedOver names the user's answers the orchestrator has not seen yet
// and any question still open, for the first message of a new context.
func (r *Runtime) carriedOver() string {
	var parts []string
	for _, ev := range r.st.Events {
		if ev.Type != event.UserAnswer || ev.Seq <= r.lastOrchSeen {
			continue
		}
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		parts = append(parts, fmt.Sprintf("The user answered question %s (%q): %s", d.QuestionID, firstLine(r.st.QuestionText(d.QuestionID), 200), d.Text))
	}
	if q := r.st.Question; q != nil {
		parts = append(parts, "A question to the user is still pending: "+firstLine(q.Text, 200))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Carried over from before the context reset. " + strings.Join(parts, " ")
}

func (r *Runtime) createDossierTask(reason string) {
	files := r.dossierFiles()
	id := r.st.NextTaskID()
	desc := r.dossierTask(r.sess.Path, files, r.st, reason)
	r.append(event.New(event.TaskCreate, event.ActorHarness, event.TaskCreateData{ID: id, Title: "Dossier for the new subsession", Description: desc, Kind: "dossier"}))
}

var citation = regexp.MustCompile(`\d{10,}\.jsonl:\d+`)

// finishRollover delivers the dossier and resumes the orchestrator.
func (r *Runtime) finishRollover(taskID, status, summary string) {
	cur := r.st.Current()
	if cur == nil || cur.Dossier != "" {
		return
	}
	text := strings.TrimSpace(summary)
	cites := len(citation.FindAllString(text, -1))
	if status != "completed" || len(text) < 200 || cites == 0 {
		// Retry once with feedback, then fall back to a harness-built stub
		// rather than leaving the orchestrator amnesiac.
		if !strings.Contains(r.st.Tasks[taskID].Description, "SECOND ATTEMPT") {
			r.ui.Log("dossier %s was unusable (%s, %d chars, %d citations); retrying", taskID, status, len(text), cites)
			files := r.dossierFiles()
			id := r.st.NextTaskID()
			desc := "SECOND ATTEMPT. The previous dossier attempt was rejected: " + rejectReason(status, len(text), cites) + ".\n\n" + r.dossierTask(r.sess.Path, files, r.st, "")
			r.append(event.New(event.TaskCreate, event.ActorHarness, event.TaskCreateData{ID: id, Title: "Dossier for the new subsession (retry)", Description: desc, Kind: "dossier"}))
			return
		}
		text = r.fallbackDossier(text)
	}
	r.append(event.New(event.Dossier, event.ActorHarness, event.DossierData{TaskID: taskID, Text: text}))
	r.rolling = false
	r.wakeNarrator("the orchestrator restarted with a fresh context and its working notes")
}

func rejectReason(status string, chars, cites int) string {
	switch {
	case status != "completed":
		return "it did not complete (" + status + ")"
	case cites == 0:
		return "it contained no file:line citations into the session logs"
	default:
		return fmt.Sprintf("it was too short (%d characters)", chars)
	}
}

// fallbackDossier builds a minimal briefing from state when the worker
// could not.
func (r *Runtime) fallbackDossier(partial string) string {
	var b strings.Builder
	b.WriteString("(The task worker could not produce a full dossier; this briefing was assembled by the harness. Use session_list/session_read/session_search to reconstruct details.)\n\n")
	b.WriteString("USER MESSAGES:\n")
	for _, ev := range r.st.Events {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			fmt.Fprintf(&b, "- [%s] %s\n", ev.Source, firstLine(d.Text, 500))
		}
	}
	b.WriteString("\nTASKS:\n")
	for _, id := range r.st.TaskOrder {
		t := r.st.Tasks[id]
		if t.Kind == "dossier" {
			continue
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n  %s\n", t.ID, t.Status, t.Title, firstLine(t.Summary, 400))
	}
	if len(r.st.Notes) > 0 {
		b.WriteString("\nRECENT NOTES:\n")
		start := max(0, len(r.st.Notes)-10)
		for _, n := range r.st.Notes[start:] {
			fmt.Fprintf(&b, "- %s\n", firstLine(n.Text, 300))
		}
	}
	if strings.TrimSpace(partial) != "" {
		b.WriteString("\nPARTIAL DOSSIER FROM THE WORKER:\n" + partial)
	}
	return b.String()
}

// dossierFiles lists the completed subsession files: everything but the one
// the harness is appending to, which holds nothing yet except this task.
func (r *Runtime) dossierFiles() []string {
	files, _ := r.sess.Files()
	cur := r.sess.Current()
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f != cur {
			out = append(out, f)
		}
	}
	return out
}
