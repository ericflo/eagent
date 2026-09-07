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
	return r.st.ContextTokens(event.ActorOrchestrator) >= r.cfg.RolloverTokens
}

// startRollover closes the current subsession, opens the next, and asks the
// task worker for a dossier. The orchestrator stays paused until it lands.
func (r *Runtime) startRollover() {
	r.rolling = true
	tokens := r.st.ContextTokens(event.ActorOrchestrator)
	next, err := r.sess.NewSubsessionName(time.Now())
	if err != nil {
		r.ui.Log("rollover: %v", err)
		r.rolling = false
		return
	}
	r.append(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: next, InputTokens: tokens}))
	if _, err := r.sess.OpenSubsession(next); err != nil {
		r.ui.Log("rollover: %v", err)
		r.rolling = false
		return
	}
	idx := len(r.st.Subsessions)
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: idx, Reason: "rollover"}))
	r.ui.Log("orchestrator context reached %dk tokens; starting subsession %d with a dossier", tokens/1000, idx+1)
	r.createDossierTask(fmt.Sprintf("the orchestrator's prompt reached %d tokens", tokens))
}

func (r *Runtime) createDossierTask(reason string) {
	files, _ := r.sess.Files()
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
			files, _ := r.sess.Files()
			id := r.st.NextTaskID()
			desc := "SECOND ATTEMPT. The previous dossier attempt was rejected: " + rejectReason(status, len(text), cites) + ".\n\n" + r.dossierTask(r.sess.Path, files, r.st, "")
			r.append(event.New(event.TaskCreate, event.ActorHarness, event.TaskCreateData{ID: id, Title: "Dossier for the new subsession (retry)", Description: desc, Kind: "dossier"}))
			return
		}
		text = r.fallbackDossier(text)
	}
	r.append(event.New(event.Dossier, event.ActorHarness, event.DossierData{TaskID: taskID, Text: text}))
	r.rolling = false
	r.wakeNarrator("a new subsession started with a dossier")
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
