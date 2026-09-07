package tools

import (
	"encoding/json"

	"github.com/ericflo/eagent/internal/llm"
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
		"Run a bash command in the project directory. The command starts in the background under a handle; if it finishes within wait_seconds you get its output right away, otherwise you get the handle and whatever it printed so far. Use bash_poll to read more, bash_write to answer a prompt, bash_kill to stop it. Long-running servers should be started with timeout_seconds=0. Output is stdout+stderr interleaved.",
		`{"type":"object","properties":{
			"command":{"type":"string","description":"The bash command (may be multi-line)."},
			"wait_seconds":{"type":"integer","description":"How long to wait inline for completion before returning the handle. Default 20, max 300."},
			"timeout_seconds":{"type":"integer","description":"Kill the command after this many seconds. Default 600. 0 disables the timeout (for servers)."}
		},"required":["command"]}`),
	def("bash_poll",
		"Read new output from a running or finished command. Optionally wait up to wait_seconds for it to finish first.",
		`{"type":"object","properties":{
			"handle":{"type":"string"},
			"wait_seconds":{"type":"integer","description":"Wait up to this long for the command to finish before reading. Default 0, max 300."}
		},"required":["handle"]}`),
	def("bash_write",
		"Send a line to a running command's stdin (for prompts and REPLs). A newline is appended unless the text already ends with one (set raw=true to send bytes exactly). Set close=true to close stdin (send EOF) afterwards.",
		`{"type":"object","properties":{
			"handle":{"type":"string"},
			"input":{"type":"string","description":"Text to send; submitted as a line."},
			"raw":{"type":"boolean","description":"Send input exactly as given, without appending a newline."},
			"close":{"type":"boolean"}
		},"required":["handle"]}`),
	def("bash_kill", "Terminate a running command and its whole process group.",
		`{"type":"object","properties":{"handle":{"type":"string"}},"required":["handle"]}`),
	def("bash_extend", "Give a running command more time before its timeout kills it. Sets the deadline to now + seconds (0 removes the deadline).",
		`{"type":"object","properties":{"handle":{"type":"string"},"seconds":{"type":"integer"}},"required":["handle","seconds"]}`),
	def("bash_list", "List commands you have started and their status.", `{"type":"object","properties":{}}`),
	def("view_image",
		"Look at an image file (png, jpeg, gif, webp): a screenshot the user sent, a picture you rendered, a page you captured. The image is shown to you right after this result. If your model cannot see images the result says so; then delegate the looking to a task worker or describe the file another way.",
		`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	def("read_file",
		"Read a text file. Reads the whole file by default (large files are truncated with a note); pass offset (1-based line) and limit to read a window. A file you already read and that has not changed since comes back as a short note instead of its contents; pass force=true to read it again anyway.",
		`{"type":"object","properties":{
			"path":{"type":"string"},
			"offset":{"type":"integer"},
			"limit":{"type":"integer"},
			"force":{"type":"boolean"}
		},"required":["path"]}`),
	def("write_file",
		"Create or overwrite a file with the full content given. Parent directories are created. Prefer this over shell heredocs.",
		`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	def("edit_file",
		"Replace one exact occurrence of old_text with new_text in a file. old_text must match exactly (whitespace included) and be unique unless replace_all is set.",
		`{"type":"object","properties":{
			"path":{"type":"string"},
			"old_text":{"type":"string"},
			"new_text":{"type":"string"},
			"replace_all":{"type":"boolean"}
		},"required":["path","old_text","new_text"]}`),
	def("list_dir", "List a directory (directories first).",
		`{"type":"object","properties":{"path":{"type":"string","description":"Defaults to the project root."}}}`),
}

// Session archive tools for building and following dossiers.
var sessionTools = []llm.Tool{
	def("session_list", "List this session's subsession log files with line counts and time ranges.", `{"type":"object","properties":{}}`),
	def("session_read",
		"Read lines of a subsession log file, one event per line, each prefixed with file:line for citation. Use raw=true to see full JSON instead of one-line summaries.",
		`{"type":"object","properties":{
			"file":{"type":"string","description":"A file name from session_list, e.g. 1788740857293.jsonl"},
			"from":{"type":"integer","description":"First line (1-based). Default 1."},
			"to":{"type":"integer","description":"Last line (inclusive). Default: end of file."},
			"raw":{"type":"boolean"}
		},"required":["file"]}`),
	def("session_search",
		"Search all subsession logs (or one file) for text, case-insensitive. Returns matching events with file:line citations.",
		`{"type":"object","properties":{
			"query":{"type":"string"},
			"file":{"type":"string"},
			"max_results":{"type":"integer"}
		},"required":["query"]}`),
}

var orchestratorOnly = []llm.Tool{
	def("delegate",
		"Hand a self-contained task to the task worker. It starts immediately in a fresh context and runs in parallel with you; its report arrives later as a message. Include everything it needs: goal, relevant paths, constraints, what 'done' looks like, and what to report back. Delegate real work; keep your own tool use to orientation and verification.",
		`{"type":"object","properties":{
			"title":{"type":"string","description":"Short label, e.g. 'Implement paddle physics'."},
			"description":{"type":"string","description":"Complete standalone instructions. The worker knows nothing about this conversation."}
		},"required":["title","description"]}`),
	def("wait",
		"Pause until one of the given tasks or processes finishes (or the timeout passes), then resume with their results. Use this instead of polling. With no ids, waits for any running task.",
		`{"type":"object","properties":{
			"tasks":{"type":"array","items":{"type":"string"},"description":"Task ids such as t3."},
			"processes":{"type":"array","items":{"type":"string"},"description":"Process handles such as p2."},
			"timeout_seconds":{"type":"integer","description":"Default 600."}
		}}`),
	def("cancel_task", "Cancel a running task.", `{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`),
	def("note",
		"Jot a note for the narrator, who decides what to tell the user. Use it for milestones, decisions, problems, and anything the user would want to know. Also use it to request user input: describe exactly what you need decided, then call yield.",
		`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	def("schedule",
		"Create a recurring loop or a one-shot timer that wakes you later with the note. Interval loops: '5m', '90s'. Cron: '*/15 * * * *'. One-shot: 'in 10m' or an RFC3339 time. While a schedule exists the session stays alive.",
		`{"type":"object","properties":{
			"spec":{"type":"string"},
			"note":{"type":"string","description":"What to do when woken."}
		},"required":["spec","note"]}`),
	def("cancel_schedule", "Cancel a schedule by id.", `{"type":"object","properties":{"schedule":{"type":"string"}},"required":["schedule"]}`),
	def("yield",
		"Stop working until something happens: a task finishes, a schedule fires, or the user replies. Set done=true only when the user's request is completely finished and verified; then the session can end. Give a short reason (what you are waiting for, or what was accomplished).",
		`{"type":"object","properties":{
			"done":{"type":"boolean"},
			"reason":{"type":"string"}
		},"required":["reason"]}`),
}

var taskOnly = []llm.Tool{
	def("complete_task",
		"Finish the task and report back. This ends your context. Be specific: what you changed (paths), how you verified it, and anything unfinished or worth knowing. Use status='failed' if you could not accomplish the goal, and say why.",
		`{"type":"object","properties":{
			"status":{"type":"string","enum":["completed","failed"]},
			"summary":{"type":"string"}
		},"required":["status","summary"]}`),
}

var narratorOnly = []llm.Tool{
	def("send_message", "Send a message to the user. Markdown is fine.",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"important":{"type":"boolean","description":"Buzz the user's phone for this one: the work is finished, something blocks you, or you found something they would want to know right now. Leave it off for progress."},
			"attachments":{"type":"array","items":{"type":"string"},"description":"Paths of files to send with the message: a screenshot of what was built, a diff, a log. Up to 8 files of 10 MB each; the path must exist in the project."}
		},"required":["text"]}`),
	def("ask_user",
		"Ask the user a question and wait for the answer. Offer options when there is a natural short list; free text is always accepted.",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"options":{"type":"array","items":{"type":"string"}}
		},"required":["text"]}`),
	def("hold", "Say nothing for now. Only when the user heard from you recently and nothing has moved since; a long silence is worse than a short progress line.",
		`{"type":"object","properties":{"reason":{"type":"string","description":"One line, for the log."}}}`),
}

// OrchestratorTools returns the orchestrator's stable tool list.
func OrchestratorTools() []llm.Tool {
	out := append([]llm.Tool{}, orchestratorOnly...)
	out = append(out, shellTools...)
	return append(out, sessionTools...)
}

// TaskTools returns the task worker's stable tool list.
func TaskTools() []llm.Tool {
	out := append([]llm.Tool{}, taskOnly...)
	out = append(out, shellTools...)
	return append(out, sessionTools...)
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
