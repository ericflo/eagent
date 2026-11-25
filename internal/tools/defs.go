package tools

import (
	"encoding/json"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/relay"
)

func def(name, desc string, params string) llm.Tool {
	if !json.Valid([]byte(params)) {
		panic("invalid schema for tool " + name)
	}
	return llm.Tool{Name: name, Description: desc, Parameters: json.RawMessage(params)}
}

// Shell and file tools shared by the orchestrator and the task worker.
var shellTools = []llm.Tool{
	def("bash",
		"Run a bash command in your working directory (`cd` persists across calls). Starts under a handle; returns output inline if it finishes within wait_seconds, otherwise the handle and output so far (use bash_poll/bash_write/bash_kill). Use timeout_seconds=0 for long-running servers. Output is stdout+stderr.",
		`{"type":"object","properties":{
			"command":{"type":"string","description":"Bash command (may be multi-line)."},
			"wait_seconds":{"type":"integer","description":"Inline wait before returning handle (default 20, max 300)."},
			"timeout_seconds":{"type":"integer","description":"Kill after N seconds (default 600; 0 disables timeout)."}
		},"required":["command"]}`),
	def("bash_poll",
		"Read new output from a running or finished command, optionally waiting up to wait_seconds for completion.",
		`{"type":"object","properties":{
			"handle":{"type":"string"},
			"wait_seconds":{"type":"integer","description":"Wait up to N seconds for completion (default 0, max 300)."}
		},"required":["handle"]}`),
	def("bash_write",
		"Write to a running command's stdin. Appends a newline unless present or raw=true; close=true sends EOF.",
		`{"type":"object","properties":{
			"handle":{"type":"string"},
			"input":{"type":"string"},
			"raw":{"type":"boolean"},
			"close":{"type":"boolean"}
		},"required":["handle"]}`),
	def("bash_kill", "Terminate a running command and its process group.",
		`{"type":"object","properties":{"handle":{"type":"string"}},"required":["handle"]}`),
	def("bash_extend", "Extend a running command's deadline to now + seconds (0 removes deadline).",
		`{"type":"object","properties":{"handle":{"type":"string"},"seconds":{"type":"integer"}},"required":["handle","seconds"]}`),
	def("bash_list", "List started commands and their status.", `{"type":"object","properties":{}}`),
	def("view_image",
		"Inspect an image file (png, jpeg, gif, webp). Shown right after this result (if your model is text-only, delegate or inspect another way).",
		`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	def("read_file",
		"Read a text file (whole file by default; offset is 1-based line, limit is line count). Unchanged re-reads return a short note unless force=true.",
		`{"type":"object","properties":{
			"path":{"type":"string"},
			"offset":{"type":"integer"},
			"limit":{"type":"integer"},
			"force":{"type":"boolean"}
		},"required":["path"]}`),
	def("write_file",
		"Create or overwrite a file with full content (creates parent dirs). Prefer over shell heredocs.",
		`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	def("edit_file",
		"Replace exact old_text with new_text in a file (must be unique unless replace_all=true).",
		`{"type":"object","properties":{
			"path":{"type":"string"},
			"old_text":{"type":"string"},
			"new_text":{"type":"string"},
			"replace_all":{"type":"boolean"}
		},"required":["path","old_text","new_text"]}`),
	def("list_dir", "List a directory (directories first; defaults to working directory).",
		`{"type":"object","properties":{"path":{"type":"string"}}}`),
}

// Session archive tools for building and following dossiers.
var sessionTools = []llm.Tool{
	def("session_list", "List this session's subsession log files with line counts and time ranges.", `{"type":"object","properties":{}}`),
	def("session_read",
		"Read lines of a subsession log file (prefixed with file:line). raw=true returns full JSON.",
		`{"type":"object","properties":{
			"file":{"type":"string","description":"Log filename from session_list."},
			"from":{"type":"integer","description":"1-based start line (default 1)."},
			"to":{"type":"integer","description":"Inclusive end line (default EOF)."},
			"raw":{"type":"boolean"}
		},"required":["file"]}`),
	def("session_search",
		"Case-insensitive search across subsession logs (or one file); returns file:line matches.",
		`{"type":"object","properties":{
			"query":{"type":"string"},
			"file":{"type":"string"},
			"max_results":{"type":"integer"}
		},"required":["query"]}`),
}

var orchestratorOnly = []llm.Tool{
	def("delegate",
		"Delegate a self-contained task to a parallel task worker in a fresh context. Include goal, paths, constraints, done criteria, and what to report. Delegate implementation; keep your own tools to orientation and verification.",
		`{"type":"object","properties":{
			"title":{"type":"string","description":"Short label."},
			"description":{"type":"string","description":"Complete standalone instructions."}
		},"required":["title","description"]}`),
	def("wait",
		"Pause until any listed task/process finishes or timeout expires (default 600s). With no ids, waits for any running task.",
		`{"type":"object","properties":{
			"tasks":{"type":"array","items":{"type":"string"}},
			"processes":{"type":"array","items":{"type":"string"}},
			"timeout_seconds":{"type":"integer"}
		}}`),
	def("cancel_task", "Cancel a running task.", `{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`),
	def("note",
		"Send a note to the narrator (milestones, problems, or a decision needed before yield). Set answer=true to answer a user question directly from what this context's log shows.",
		`{"type":"object","properties":{"text":{"type":"string"},"answer":{"type":"boolean","description":"True when answering a user question."}},"required":["text"]}`),
	def("schedule",
		"Create a recurring or one-shot timer ('5m', '90s', cron '*/15 * * * *', 'in 10m', or RFC3339). Keeps the session alive while active.",
		`{"type":"object","properties":{
			"spec":{"type":"string"},
			"note":{"type":"string","description":"What to do when woken."}
		},"required":["spec","note"]}`),
	def("cancel_schedule", "Cancel a schedule by id.", `{"type":"object","properties":{"schedule":{"type":"string"}},"required":["schedule"]}`),
	def("retitle_thread",
		"Manually set the thread title and/or summary when the user asks to rename or fix them (the harness updates both automatically otherwise). Manual titles are preserved.",
		`{"type":"object","properties":{
			"title":{"type":"string","description":"Short subject (not just the project dir name)."},
			"description":{"type":"string","description":"1-2 sentence session summary (max 2000 chars)."}
		}}`),
	def("yield",
		"Pause until a task finishes, schedule fires, or the user replies. Set done=true only when the request is complete and verified.",
		`{"type":"object","properties":{
			"done":{"type":"boolean"},
			"reason":{"type":"string"}
		},"required":["reason"]}`),
}

// relayAgentsHint names the agents a relay can run and their built-in
// profiles: "Antigravity, Claude, Codex, or Pi (profiles: 'antigravity',
// 'claude', 'codex', 'pi')". It is the same on every run, so the tool
// list stays cacheable.
func relayAgentsHint() string {
	var labels, profiles []string
	for _, a := range relay.Agents() {
		labels = append(labels, a.Label())
	}
	for _, name := range (config.Relay{}).RelayProfileNames() {
		profiles = append(profiles, "'"+name+"'")
	}
	if n := len(labels); n > 1 {
		labels[n-1] = "or " + labels[n-1]
	}
	return strings.Join(labels, ", ") + " (profiles: " + strings.Join(profiles, ", ") + ")"
}

// relayTools drive an external coding agent (a relay). Only relay_start is
// exposed before relay mode is active; the rest join when relay mode begins.
var relayTools = []llm.Tool{
	def("relay_start",
		"Start an external coding agent in a managed terminal and switch this session into relay mode. Only use when the user asks for "+relayAgentsHint()+".",
		`{"type":"object","properties":{
			"profile":{"type":"string"},
			"dir":{"type":"string","description":"Working dir (default: current working dir)."},
			"model":{"type":"string","description":"Optional model override if requested by user."},
			"effort":{"type":"string","description":"Optional reasoning effort if requested by user."}
		},"required":["profile"]}`),
	def("relay_forward",
		"Forward a user message verbatim (with attachments) to the relayed agent. Modes: queue (default), interrupt (stop current step), after_turn.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"seq":{"type":"integer","description":"User message seq (default: latest)."},
			"mode":{"type":"string","enum":["queue","interrupt","after_turn"]}
		}}`),
	def("relay_send",
		"Send your own text to the relayed agent (nudge, answer, missing context; mirrored to user). Never paraphrase user messages (use relay_forward).",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"text":{"type":"string"},
			"mode":{"type":"string","enum":["queue","interrupt","after_turn"]}
		},"required":["text"]}`),
	def("relay_status",
		"Show the relayed agent's state, current activity, recent remarks, last report, unconfirmed messages, usage budget, and attach info.",
		`{"type":"object","properties":{"relay":{"type":"string"}}}`),
	def("relay_screen",
		"Read the relayed agent's terminal screen/scrollback (only for interactive dialogs/prompts).",
		`{"type":"object","properties":{"relay":{"type":"string"},"lines":{"type":"integer","description":"Scrollback lines (default 60)."}}}`),
	def("relay_control",
		"Control the relayed agent: interrupt, keys (Enter/Escape/Up/Down/Tab/C-c or text), restart (resume session), model (change model/effort or 'default'), upgrade (update CLI + restart), switch (hand off to another profile), or stop.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"action":{"type":"string","enum":["interrupt","keys","restart","model","upgrade","switch","stop"]},
			"keys":{"type":"array","items":{"type":"string"}},
			"profile":{"type":"string","description":"Target profile for switch."},
			"model":{"type":"string","description":"Model or 'default' (model/switch)."},
			"effort":{"type":"string","description":"Effort or 'default' (model/switch)."},
			"reason":{"type":"string"}
		},"required":["action"]}`),
	def("relay_autopilot",
		"Keep the relayed agent driving without the user: nudge with relay_send when it stops with nothing running; yield when it is waiting on its own background work; answer its questions when decide=true (default). until accepts a time/duration or empty for until stopped; off=true ends it.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"instruction":{"type":"string","description":"Standing order."},
			"until":{"type":"string"},
			"decide":{"type":"boolean","description":"Answer agent questions instead of asking user (default true)."},
			"off":{"type":"boolean"}
		}}`),
}

var taskOnly = []llm.Tool{
	def("complete_task",
		"Finish the task and report back (ends your context). Report changed paths, verification, and anything unfinished. Use status='failed' if unsuccessful.",
		`{"type":"object","properties":{
			"status":{"type":"string","enum":["completed","failed"]},
			"summary":{"type":"string"}
		},"required":["status","summary"]}`),
}

var narratorOnly = []llm.Tool{
	def("send_message", "Send a message to the user (Markdown supported).",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"important":{"type":"boolean","description":"Buzz phone (done, blocked, or urgent); leave false for progress."},
			"attachments":{"type":"array","items":{"type":"string"},"description":"Existing project file paths to attach (up to 8 files, 10 MB each)."}
		},"required":["text"]}`),
	def("ask_user",
		"Ask the user a question and wait for their answer (options optional; free text always accepted).",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"options":{"type":"array","items":{"type":"string"}}
		},"required":["text"]}`),
	def("hold", "Stay silent when the user heard from you recently and nothing has moved.",
		`{"type":"object","properties":{"reason":{"type":"string","description":"One-line log reason."}}}`),
}

// OrchestratorTools returns the orchestrator's full tool list.
func OrchestratorTools() []llm.Tool {
	return OrchestratorToolsFor(true, true)
}

// OrchestratorToolsFor returns the orchestrator's tool list for the current
// mode. Relay tools beyond relay_start are omitted until relay mode is active
// (matching the ORCHESTRATOR.md -> RELAY.md switch), and session archive tools
// are omitted until a rollover has created prior subsession logs.
func OrchestratorToolsFor(relayMode, hasRollover bool) []llm.Tool {
	out := append([]llm.Tool{}, orchestratorOnly...)
	out = append(out, shellTools...)
	if hasRollover {
		out = append(out, sessionTools...)
	}
	if relayMode {
		out = append(out, relayTools...)
	} else {
		out = append(out, relayTools[0]) // relay_start only
	}
	return out
}

// TaskTools returns the task worker's full tool list.
func TaskTools() []llm.Tool {
	return TaskToolsFor("dossier")
}

// TaskToolsFor returns the task worker's tool list for a task kind. Session
// archive tools are included only for dossier tasks.
func TaskToolsFor(kind string) []llm.Tool {
	out := append([]llm.Tool{}, taskOnly...)
	out = append(out, shellTools...)
	if kind == "dossier" {
		out = append(out, sessionTools...)
	}
	return out
}

// NarratorTools returns the narrator's tools.
func NarratorTools() []llm.Tool { return append([]llm.Tool{}, narratorOnly...) }

// Has reports whether a tool list contains name.
func Has(tools []llm.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
