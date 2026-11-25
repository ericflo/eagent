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
	default:
		body = string(ev.Data)
	}
	body = oneLine(body)
	if max > 0 && len(body) > max {
		body = Truncate(body, max)
		body = oneLine(body)
	}
	return fmt.Sprintf("%s %s %s: %s", ts, ev.Type, who, body)
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
