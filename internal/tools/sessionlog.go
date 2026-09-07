package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// Archive exposes a session's own log to actors, so a dossier can cite
// exactly where in the JSONL files to look.
type Archive struct {
	Path string // session directory
}

// List describes every subsession file.
func (a Archive) List() (string, error) {
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "Session directory: %s\n", a.Path)
	for i, name := range names {
		evs, err := store.ReadFile(filepath.Join(a.Path, name))
		if err != nil {
			fmt.Fprintf(&b, "%s: unreadable (%v)\n", name, err)
			continue
		}
		var first, last string
		var users int
		var tasks int
		if len(evs) > 0 {
			first = evs[0].Time.Format("2006-01-02 15:04:05")
			last = evs[len(evs)-1].Time.Format("15:04:05")
		}
		for _, ev := range evs {
			switch ev.Type {
			case event.UserMessage:
				users++
			case event.TaskCreate:
				tasks++
			}
		}
		fmt.Fprintf(&b, "%d. %s  %d lines  %s -> %s  (%d user messages, %d tasks)\n", i+1, name, len(evs), first, last, users, tasks)
	}
	return b.String(), nil
}

// Read renders lines from..to (1-based, inclusive) of one file. Each line is
// prefixed with file:line so it can be cited.
func (a Archive) Read(file string, from, to int, raw bool, maxChars int) (string, error) {
	if strings.ContainsAny(file, "/\\") || !strings.HasSuffix(file, ".jsonl") {
		return "", fmt.Errorf("file must be a subsession file name like 1788740857293.jsonl (see session_list)")
	}
	evs, err := store.ReadFile(filepath.Join(a.Path, file))
	if err != nil {
		return "", err
	}
	if from < 1 {
		from = 1
	}
	if to < from || to > len(evs) {
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
	case event.TaskCreate:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		body = d.ID + " " + d.Title + ": " + d.Description
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		body = d.ID + " " + d.Status + ": " + d.Summary
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		body = d.Text
	case event.NarratorMessage:
		var d event.NarratorMessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.NarratorQuestion:
		var d event.NarratorQuestionData
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
	case event.ProcStart:
		var d event.ProcStartData
		_ = ev.Decode(&d)
		body = d.Handle + " " + d.Command
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		body = fmt.Sprintf("%s %s code=%d", d.Handle, d.Reason, d.ExitCode)
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
