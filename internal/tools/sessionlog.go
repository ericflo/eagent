package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// Archive exposes a session's own log to eagent, so it can follow the
// file:line places its briefing cites and look up what the briefing left out.
type Archive struct {
	Path string // session directory
}

// Read renders lines from..to (1-based, inclusive) of one file. Each line is
// prefixed with file:line so it can be cited.
func (a Archive) Read(file string, from, to int, raw bool, maxChars int) (string, error) {
	if strings.ContainsAny(file, "/\\") || !strings.HasSuffix(file, ".jsonl") {
		return "", fmt.Errorf("file must be one of the session's record files, named like 1788740857293.jsonl")
	}
	evs, err := store.ReadFile(filepath.Join(a.Path, file))
	if err != nil {
		return "", err
	}
	if from < 1 {
		from = 1
	}
	if to < from {
		to = from + 39
	}
	if to > len(evs) {
		to = len(evs)
	}
	if from > len(evs) {
		return fmt.Sprintf("%s has only %d lines", file, len(evs)), nil
	}
	var b strings.Builder
	for _, ev := range evs[from-1 : to] {
		if raw {
			line, _ := json.Marshal(ev)
			fmt.Fprintf(&b, "%s:%d %s\n", file, ev.Source.Line, line)
		} else {
			fmt.Fprintf(&b, "%s:%d %s\n", file, ev.Source.Line, Summarize(ev, 400))
		}
		if maxChars > 0 && b.Len() > maxChars {
			fmt.Fprintf(&b, "[... output limit reached at line %d of %d; call again with from=%d ...]\n", ev.Source.Line, len(evs), ev.Source.Line+1)
			break
		}
	}
	return b.String(), nil
}

// Search finds events whose rendered form contains query (case-insensitive).
func (a Archive) Search(query, file string, maxResults int) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", err
	}
	if maxResults <= 0 {
		maxResults = 40
	}
	q := strings.ToLower(query)
	var b strings.Builder
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") || (file != "" && name != file) {
			continue
		}
		evs, err := store.ReadFile(filepath.Join(a.Path, name))
		if err != nil {
			continue
		}
		for _, ev := range evs {
			hay := strings.ToLower(string(ev.Data) + " " + ev.Type + " " + ev.Actor)
			if !strings.Contains(hay, q) {
				continue
			}
			fmt.Fprintf(&b, "%s:%d %s\n", name, ev.Source.Line, Summarize(ev, 300))
			n++
			if n >= maxResults {
				fmt.Fprintf(&b, "[... %d results shown; narrow the query or set a file ...]\n", n)
				return b.String(), nil
			}
		}
	}
	if n == 0 {
		return "no matches", nil
	}
	return b.String(), nil
}

// Summarize renders one event as a compact human-readable line.
func Summarize(ev event.Event, max int) string {
	ts := ev.Time.Format("15:04:05")
	who := ev.Actor
	if ev.Task != "" {
		who += "(" + ev.Task + ")"
	}
	body := ""
	switch ev.Type {
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		body = d.Text
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		body = "dismissed " + d.QuestionID + " without answering"
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		var parts []string
		if d.Text != "" {
			parts = append(parts, d.Text)
		}
		for _, tc := range d.ToolCalls {
			parts = append(parts, fmt.Sprintf("%s(%s)", tc.Name, oneLine(string(tc.Args))))
		}
		body = strings.Join(parts, " | ")
	case event.ToolResult:
		var d event.ToolResultData
		_ = ev.Decode(&d)
		body = d.Name + " -> " + d.Output
		if d.IsError {
			body = d.Name + " ERROR -> " + d.Output
		}
	case event.Message:
		var d event.MessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Question:
		var d event.QuestionData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		body = d.Text
	case event.HarnessMessage:
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Steer:
		var d event.SteerData
		_ = ev.Decode(&d)
		body = d.Text
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		body = d.Name + " finished a turn: " + d.Text
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		body = d.Name + " said: " + d.Text
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		body = fmt.Sprintf("to %s from %s (%s, %s): %s", d.Name, d.From, d.ID, d.Mode, d.Text)
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		body = d.Name + " " + d.State
		if d.Detail != "" {
			body += ": " + d.Detail
		}
	default:
		if b, ok := summarizeLegacy(ev); ok {
			body = b
		} else {
			body = string(ev.Data)
		}
	}
	body = oneLine(body)
	if max > 0 && len(body) > max {
		body = Truncate(body, max)
		body = oneLine(body)
	}
	return fmt.Sprintf("%s %s %s: %s", ts, ev.Type, who, body)
}

// The data of event types only logs from before eagent was one loop hold,
// as they were written then.
type (
	legacyTaskCreate struct {
		ID, Title, Description, Kind string
	}
	legacyTaskStart struct {
		ID string
	}
	legacyTaskEnd struct {
		ID, Status, Summary string
	}
	legacyNote struct {
		Text   string
		Answer bool
	}
	legacyProcStart struct {
		Handle, Command, Cwd string
	}
	legacyProcPID struct {
		Handle string
		PID    int
	}
	legacyProcExit struct {
		Handle   string
		ExitCode int `json:"exit_code"`
		Reason   string
		Tail     string
	}
	legacyNarratorTrim struct {
		FromSeq int64 `json:"from_seq"`
		Recap   string
	}
)

// summarizeLegacy renders the events of the three-actor design readably;
// ok is false for any other type.
func summarizeLegacy(ev event.Event) (string, bool) {
	switch ev.Type {
	case event.LegacyTaskCreate:
		var d legacyTaskCreate
		_ = ev.Decode(&d)
		body := d.ID + " " + d.Title + ": " + d.Description
		if d.Kind == "dossier" {
			body = d.ID + " (notes for a fresh context) " + d.Title + ": " + d.Description
		}
		return body, true
	case event.LegacyTaskStart:
		var d legacyTaskStart
		_ = ev.Decode(&d)
		return d.ID + " started", true
	case event.LegacyTaskEnd:
		var d legacyTaskEnd
		_ = ev.Decode(&d)
		return d.ID + " " + d.Status + ": " + d.Summary, true
	case event.LegacyNote:
		var d legacyNote
		_ = ev.Decode(&d)
		if d.Answer {
			return "(answer) " + d.Text, true
		}
		return d.Text, true
	case event.LegacyYield:
		var d event.LegacyYieldData
		_ = ev.Decode(&d)
		if d.Done {
			return "done: " + d.Reason, true
		}
		return "waiting: " + d.Reason, true
	case event.LegacyProcStart:
		var d legacyProcStart
		_ = ev.Decode(&d)
		body := d.Handle + " " + d.Command
		if d.Cwd != "" {
			body += " (in " + d.Cwd + ")"
		}
		return body, true
	case event.LegacyProcPID:
		var d legacyProcPID
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s pid %d", d.Handle, d.PID), true
	case event.LegacyProcExit:
		var d legacyProcExit
		_ = ev.Decode(&d)
		body := fmt.Sprintf("%s %s code=%d", d.Handle, d.Reason, d.ExitCode)
		if d.Tail != "" {
			body += ": " + d.Tail
		}
		return body, true
	case event.LegacyNarratorTrim:
		var d legacyNarratorTrim
		_ = ev.Decode(&d)
		body := fmt.Sprintf("narrator conversation trimmed from #%d", d.FromSeq)
		if d.Recap != "" {
			body += ": " + d.Recap
		}
		return body, true
	}
	return "", false
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "⏎ ")
	return strings.TrimSpace(s)
}

// Truncate bounds text to about max characters, keeping the head and the
// tail, without splitting a UTF-8 sequence.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - tail
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return s[:head] + fmt.Sprintf("\n\n[... %d characters omitted; %d total ...]\n\n", tailStart-head, len(s)) + s[tailStart:]
}
