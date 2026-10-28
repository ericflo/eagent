package relay

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// obs is one thing a transcript line says, before the seat folds it into
// turns and events.
type obs struct {
	kind       string // meta | user | working | progress | turn | aborted | activity | image | limit | compact
	text       string
	at         time.Time
	durationMS int64
	id         string // meta: the agent's own session id
	used       float64
	window     string
	resets     string
	exhausted  bool
	tool       bool // activity/image: counts as a tool call
}

// parser turns transcript lines into observations. It keeps whatever state
// the format needs across lines (Claude Code's last message of a turn).
type parser interface {
	line(raw []byte) []obs
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func oneLine(s string, n int) string {
	return clipText(strings.Join(strings.Fields(s), " "), n)
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

func isImagePath(p string) bool { return imageExt[strings.ToLower(filepath.Ext(p))] }

// ---- Codex rollouts ---------------------------------------------------------

type codexParser struct{}

func (codexParser) line(raw []byte) []obs {
	var ev struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return nil
	}
	at := parseTime(ev.Timestamp)
	switch ev.Type {
	case "session_meta":
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		return []obs{{kind: "meta", id: p.ID, at: at}}
	case "event_msg":
	default:
		return nil
	}
	var p struct {
		Type             string          `json:"type"`
		LastAgentMessage string          `json:"last_agent_message"`
		DurationMS       int64           `json:"duration_ms"`
		Item             json.RawMessage `json:"item"`
		RateLimits       *struct {
			Primary *struct {
				UsedPercent   float64 `json:"used_percent"`
				WindowMinutes int     `json:"window_minutes"`
				ResetsAt      int64   `json:"resets_at"`
			} `json:"primary"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(ev.Payload, &p) != nil {
		return nil
	}
	switch p.Type {
	case "task_started":
		return []obs{{kind: "working", at: at}}
	case "task_complete":
		return []obs{{kind: "turn", text: p.LastAgentMessage, at: at, durationMS: p.DurationMS}}
	case "turn_aborted":
		return []obs{{kind: "aborted", at: at, durationMS: p.DurationMS}}
	case "token_count":
		if p.RateLimits == nil || p.RateLimits.Primary == nil {
			return nil
		}
		pr := p.RateLimits.Primary
		o := obs{kind: "limit", used: pr.UsedPercent, window: windowName(pr.WindowMinutes), at: at}
		if pr.ResetsAt > 0 {
			o.resets = time.Unix(pr.ResetsAt, 0).Format(time.RFC3339)
		}
		return []obs{o}
	case "item_completed":
		return codexItem(p.Item, at)
	}
	return nil
}

func windowName(minutes int) string {
	switch {
	case minutes == 10080:
		return "weekly"
	case minutes >= 1440 && minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes >= 60 && minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	}
	return ""
}

func codexItem(raw json.RawMessage, at time.Time) []obs {
	var it struct {
		Type    string `json:"type"`
		Phase   string `json:"phase"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Command   []string `json:"command"`
		ParsedCmd []struct {
			Cmd string `json:"cmd"`
		} `json:"parsed_cmd"`
		Changes map[string]json.RawMessage `json:"changes"`
		Path    string                     `json:"path"`
		Kind    string                     `json:"kind"`
		Query   string                     `json:"query"`
		Tool    string                     `json:"tool"`
		Server  string                     `json:"server"`
	}
	if json.Unmarshal(raw, &it) != nil {
		return nil
	}
	text := func() string {
		var parts []string
		for _, c := range it.Content {
			parts = append(parts, c.Text)
		}
		return strings.Join(parts, "\n")
	}
	switch it.Type {
	case "UserMessage":
		return []obs{{kind: "user", text: text(), at: at}}
	case "AgentMessage":
		if it.Phase == "final_answer" {
			return nil // the turn's report arrives with task_complete
		}
		if t := strings.TrimSpace(text()); t != "" {
			return []obs{{kind: "progress", text: t, at: at}}
		}
	case "CommandExecution":
		cmd := ""
		if len(it.ParsedCmd) > 0 {
			cmd = it.ParsedCmd[0].Cmd
		} else if n := len(it.Command); n > 0 {
			cmd = it.Command[n-1]
		}
		return []obs{{kind: "activity", text: "running " + oneLine(cmd, 120), at: at, tool: true}}
	case "FileChange":
		var names []string
		for p := range it.Changes {
			names = append(names, filepath.Base(p))
		}
		return []obs{{kind: "activity", text: "editing " + oneLine(strings.Join(names, ", "), 120), at: at, tool: true}}
	case "ImageView":
		p := strings.TrimPrefix(it.Path, "file://")
		return []obs{{kind: "image", text: p, at: at, tool: true}}
	case "ContextCompaction":
		return []obs{{kind: "compact", at: at}}
	case "Extension":
		if it.Query != "" {
			return []obs{{kind: "activity", text: "looking up " + oneLine(it.Query, 120), at: at, tool: true}}
		}
		return []obs{{kind: "activity", text: it.Kind, at: at, tool: true}}
	case "McpToolCall":
		return []obs{{kind: "activity", text: "calling " + it.Server + "." + it.Tool, at: at, tool: true}}
	}
	return nil
}

// ---- Claude Code transcripts -------------------------------------------------

const (
	stopHookPrefix = "Stop hook feedback:\n"
	interrupted    = "[Request interrupted by user"
)

type claudeParser struct {
	held string // the latest assistant text; it is either progress or the turn's report
}

func (p *claudeParser) line(raw []byte) []obs {
	var ev struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		Timestamp   string          `json:"timestamp"`
		IsSidechain bool            `json:"isSidechain"`
		IsMeta      bool            `json:"isMeta"`
		SessionID   string          `json:"sessionId"`
		Operation   string          `json:"operation"`
		Content     json.RawMessage `json:"content"`
		DurationMS  int64           `json:"durationMs"`
		APIError    bool            `json:"isApiErrorMessage"`
		Message     *struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.IsSidechain {
		return nil
	}
	at := parseTime(ev.Timestamp)
	switch ev.Type {
	case "queue-operation":
		if ev.Operation == "enqueue" {
			var s string
			if json.Unmarshal(ev.Content, &s) == nil && s != "" {
				return []obs{{kind: "user", text: s, at: at}}
			}
		}
		return nil
	case "system":
		switch ev.Subtype {
		case "turn_duration":
			text := p.held
			p.held = ""
			return []obs{{kind: "turn", text: text, at: at, durationMS: ev.DurationMS}}
		case "compact_boundary":
			return []obs{{kind: "compact", at: at}}
		}
		return nil
	case "user":
		if ev.IsMeta || ev.Message == nil {
			return nil
		}
		var s string
		if json.Unmarshal(ev.Message.Content, &s) == nil {
			return p.userText(s, at)
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Message.Content, &parts) != nil {
			return nil
		}
		var texts []string
		for _, part := range parts {
			if part.Type == "text" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) == 0 {
			return nil // tool results
		}
		return p.userText(strings.Join(texts, "\n"), at)
	case "assistant":
		if ev.Message == nil {
			return nil
		}
		var parts []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal(ev.Message.Content, &parts) != nil {
			return nil
		}
		var out []obs
		for _, part := range parts {
			switch part.Type {
			case "text":
				t := strings.TrimSpace(part.Text)
				if t == "" {
					continue
				}
				if ev.APIError || ev.Message.Model == "<synthetic>" {
					if l := limitText(t); l != "" {
						out = append(out, obs{kind: "limit", text: l, exhausted: true, at: at})
					}
				}
				if p.held != "" {
					out = append(out, obs{kind: "progress", text: p.held, at: at})
				}
				p.held = t
			case "tool_use":
				if p.held != "" {
					out = append(out, obs{kind: "progress", text: p.held, at: at})
					p.held = ""
				}
				out = append(out, claudeTool(part.Name, part.Input, at)...)
			}
		}
		return out
	}
	return nil
}

func (p *claudeParser) userText(s string, at time.Time) []obs {
	switch {
	case strings.HasPrefix(s, interrupted):
		text := p.held
		p.held = ""
		return []obs{{kind: "aborted", text: text, at: at}}
	case strings.HasPrefix(s, stopHookPrefix):
		return []obs{{kind: "user", text: strings.TrimPrefix(s, stopHookPrefix), at: at}, {kind: "working", at: at}}
	case strings.HasPrefix(s, "<local-command") || strings.HasPrefix(s, "<command-name>") || strings.HasPrefix(s, "<bash-"):
		return nil // the TUI's own bookkeeping
	}
	p.held = ""
	return []obs{{kind: "user", text: s, at: at}, {kind: "working", at: at}}
}

func claudeTool(name string, in map[string]any, at time.Time) []obs {
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "Bash":
		return []obs{{kind: "activity", text: "running " + oneLine(str("command"), 120), at: at, tool: true}}
	case "Read":
		if p := str("file_path"); isImagePath(p) {
			return []obs{{kind: "image", text: p, at: at, tool: true}}
		}
		return []obs{{kind: "activity", text: "reading " + filepath.Base(str("file_path")), at: at, tool: true}}
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		p := str("file_path")
		if p == "" {
			p = str("notebook_path")
		}
		out := []obs{{kind: "activity", text: "editing " + filepath.Base(p), at: at, tool: true}}
		if isImagePath(p) {
			out = append(out, obs{kind: "image", text: p, at: at})
		}
		return out
	case "Grep", "Glob":
		return []obs{{kind: "activity", text: "searching for " + oneLine(str("pattern"), 80), at: at, tool: true}}
	case "Agent", "Task":
		return []obs{{kind: "activity", text: "running a subagent: " + oneLine(str("description"), 80), at: at, tool: true}}
	case "WebFetch", "WebSearch":
		q := str("query")
		if q == "" {
			q = str("url")
		}
		return []obs{{kind: "activity", text: "looking up " + oneLine(q, 100), at: at, tool: true}}
	}
	return []obs{{kind: "activity", text: "using " + name, at: at, tool: true}}
}

// limitText recognises a usage-limit notice in a synthetic assistant message.
func limitText(s string) string {
	l := strings.ToLower(s)
	if strings.Contains(l, "limit") && (strings.Contains(l, "usage") || strings.Contains(l, "reached") || strings.Contains(l, "resets")) {
		return oneLine(s, 200)
	}
	return ""
}
