// Package tools defines the tools eagent's model may call, and the session
// archive they read. The list is fixed: the same tools, in the same order,
// with the same schemas, for every call of every session. Anything that
// varied would change the start of the prompt and throw away the provider's
// cache of it.
package tools

import (
	"encoding/json"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
)

func def(name, desc string, params string) llm.Tool {
	if !json.Valid([]byte(params)) {
		panic("invalid schema for tool " + name)
	}
	return llm.Tool{Name: name, Description: desc, Parameters: json.RawMessage(params)}
}

// Tool names.
const (
	SendMessage    = "send_message"
	AskUser        = "ask_user"
	Hold           = "hold"
	RelayForward   = "relay_forward"
	RelaySend      = "relay_send"
	RelayScreen    = "relay_screen"
	RelayControl   = "relay_control"
	RelayAutopilot = "relay_autopilot"
	RelayStart     = "relay_start"
	UpdateThread   = "update_thread"
	SessionSearch  = "session_search"
	SessionRead    = "session_read"
	ViewImage      = "view_image"
	Schedule       = "schedule"
	CancelSchedule = "cancel_schedule"
)

// builtinProfiles names the relay profiles every build has ("antigravity,
// claude, codex, pi"). It is the same on every run, so the tool list stays
// byte-for-byte the same.
func builtinProfiles() string {
	return strings.Join((config.Relay{}).RelayProfileNames(), ", ")
}

var all = []llm.Tool{
	def(SendMessage, "Tell the user something (Markdown supported). At most one per reply.",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"important":{"type":"boolean","description":"Buzz the phone (work finished, blocked, or urgent). Leave false for progress."},
			"attachments":{"type":"array","items":{"type":"string"},"description":"Paths of existing files to attach, as they appear in your conversation (up to 8 files, 10 MB each)."}
		},"required":["text"]}`),
	def(AskUser,
		"Ask the user a question and wait for the answer (options optional; free text is always accepted).",
		`{"type":"object","properties":{
			"text":{"type":"string"},
			"options":{"type":"array","items":{"type":"string"}}
		},"required":["text"]}`),
	def(Hold, "Do and say nothing for now. Leaves no trace.",
		`{"type":"object","properties":{}}`),
	def(RelayForward,
		"Pass one of the user's messages to the agent verbatim, with its attachments. Only for a message that was held back for you: the others were passed on when they arrived.",
		`{"type":"object","properties":{
			"relay":{"type":"string","description":"Which agent, when more than one runs."},
			"seq":{"type":"integer","description":"The message's number (default: the user's latest)."},
			"mode":{"type":"string","enum":["queue","interrupt","after_turn"]}
		}}`),
	def(RelaySend,
		"Send the agent words of your own (a nudge, an answer, missing context). The user sees what you sent. Never use it to retell a user message: use relay_forward.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"text":{"type":"string"},
			"mode":{"type":"string","enum":["queue","interrupt","after_turn"]}
		},"required":["text"]}`),
	def(RelayScreen,
		"Read the agent's terminal: what is on screen now, with some scrollback. For dialogs, prompts, and checking what it is running.",
		`{"type":"object","properties":{"relay":{"type":"string"},"lines":{"type":"integer","description":"Scrollback lines (default 60)."}}}`),
	def(RelayControl,
		"Drive the agent's terminal: interrupt; keys (Enter, Escape, Up, Down, Tab, C-c, or text to type; returns the screen afterwards); restart (continues its session); model (change model or effort, 'default' for its own); upgrade (update its CLI, then restart); switch (hand the work to another profile); stop.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"action":{"type":"string","enum":["interrupt","keys","restart","model","upgrade","switch","stop"]},
			"keys":{"type":"array","items":{"type":"string"}},
			"profile":{"type":"string","description":"Target profile for switch."},
			"model":{"type":"string","description":"Model or 'default' (model, switch)."},
			"effort":{"type":"string","description":"Effort or 'default' (model, switch)."},
			"reason":{"type":"string"}
		},"required":["action"]}`),
	def(RelayAutopilot,
		"Set or clear a standing order to keep the agent working without the user. until takes a time or a duration; empty means until the user says stop. off=true ends it.",
		`{"type":"object","properties":{
			"relay":{"type":"string"},
			"instruction":{"type":"string","description":"The standing order, in the user's words."},
			"until":{"type":"string"},
			"decide":{"type":"boolean","description":"Answer the agent's questions yourself instead of asking the user (default true)."},
			"off":{"type":"boolean"}
		}}`),
	def(RelayStart,
		"Start an agent in a terminal of its own. Only when none is running and the user wants one, or asks for a second.",
		`{"type":"object","properties":{
			"profile":{"type":"string","description":"A relay profile: `+builtinProfiles()+`, or one the project defines."},
			"dir":{"type":"string","description":"Folder to start in (default: the session's)."},
			"model":{"type":"string","description":"Only if the user asked for one."},
			"effort":{"type":"string","description":"Only if the user asked for one."}
		},"required":["profile"]}`),
	def(UpdateThread,
		"Set the title and summary the user sees in their list of sessions. Leave a field out to keep it. pin=true only for a title the user asked for by name.",
		`{"type":"object","properties":{
			"title":{"type":"string"},
			"summary":{"type":"string"},
			"pin":{"type":"boolean"}
		}}`),
	def(SessionSearch,
		"Search the session's record on disk, including what your briefing left out. Case-insensitive; returns file:line matches.",
		`{"type":"object","properties":{
			"query":{"type":"string"},
			"file":{"type":"string"},
			"max_results":{"type":"integer"}
		},"required":["query"]}`),
	def(SessionRead,
		"Read lines of one of the session's record files (as cited file:line in your briefing or a search).",
		`{"type":"object","properties":{
			"file":{"type":"string"},
			"from":{"type":"integer","description":"1-based start line (default 1)."},
			"to":{"type":"integer","description":"Inclusive end line (default: 40 lines on)."},
			"raw":{"type":"boolean","description":"Full JSON instead of the readable form."}
		},"required":["file"]}`),
	def(ViewImage,
		"Look at an image file (png, jpeg, gif, webp): a screenshot or render the agent made, or a picture the user sent. It is shown to you right after this result.",
		`{"type":"object","properties":{"path":{"type":"string","description":"As it appears in your conversation."}},"required":["path"]}`),
	def(Schedule,
		"Set a timer that wakes you later with a note to yourself: once ('in 10m', or an RFC3339 time) or repeating ('5m', '90s', cron '*/15 * * * *'). The session stays alive while one is set. Not for watching the agent: you are woken when it does something.",
		`{"type":"object","properties":{
			"spec":{"type":"string"},
			"note":{"type":"string","description":"What to do when woken."}
		},"required":["spec","note"]}`),
	def(CancelSchedule, "Cancel a timer by its id.",
		`{"type":"object","properties":{"schedule":{"type":"string"}},"required":["schedule"]}`),
}

// All returns eagent's tools. The list never changes within a build.
func All() []llm.Tool { return append([]llm.Tool{}, all...) }

// Has reports whether name is one of eagent's tools.
func Has(name string) bool {
	for _, t := range all {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Looks reports whether a tool exists to bring something back for the model
// to read. A reply that only acts (speaks, sends, holds) ends the turn; one
// that looked gets another call to use what it saw.
func Looks(name string) bool {
	switch name {
	case RelayScreen, SessionSearch, SessionRead, ViewImage:
		return true
	}
	return false
}
