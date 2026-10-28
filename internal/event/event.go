// Package event defines the append-only session log schema.
//
// Every fact about a session is an Event written as one JSON line. The log is
// the only source of truth: the in-memory state (see package state) is always
// a pure function of the events read so far, whether they were produced live
// or replayed from disk after a crash.
package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
)

// Actor names. The user and the harness are actors too so that every line has
// an unambiguous origin.
const (
	ActorUser         = "user"
	ActorHarness      = "harness"
	ActorOrchestrator = "orchestrator"
	ActorTask         = "task"
	ActorNarrator     = "narrator"
)

// Event types. Names are dotted and stable; adding a type is backwards
// compatible, renaming one is not.
const (
	SessionStart    = "session.start"
	SessionResume   = "session.resume"
	SessionEnd      = "session.end"
	SubsessionStart = "subsession.start"
	SubsessionEnd   = "subsession.end"

	UserMessage = "user.message"
	UserAnswer  = "user.answer"

	TurnStart  = "turn.start"
	TurnEnd    = "turn.end"
	Assistant  = "assistant"
	ToolResult = "tool.result"

	TaskCreate = "task.create"
	TaskStart  = "task.start"
	TaskEnd    = "task.end"

	Note             = "note"
	NarratorMessage  = "narrator.message"
	NarratorQuestion = "narrator.question"

	ScheduleCreate = "schedule.create"
	ScheduleFire   = "schedule.fire"
	ScheduleCancel = "schedule.cancel"

	ProcStart = "proc.start"
	ProcPID   = "proc.pid"
	ProcExit  = "proc.exit"

	Dossier        = "dossier"
	HarnessMessage = "harness.message"
	Yield          = "yield"
	Route          = "route"
	PhoneThread    = "phone.thread"   // the session is mirrored to the user's phone
	PhoneQuestion  = "phone.question" // a narrator question was posted to the phone as a card
	ThreadTitle    = "thread.title"   // the session's title/description (whole session so far), mirrored to FinaleChat when the phone is on
	Steer          = "steer"          // the harness's per-call instruction to an actor, kept so every prompt extends the last
	CwdChange      = "cwd.change"     // an actor's working directory moved: a cd in one of its commands that persists for its later ones
	Error          = "error"

	// A relay is an external coding agent (Codex, Claude Code) that eagent
	// runs in a terminal it owns and passes the user's words to.
	RelayStart     = "relay.start"     // the agent was launched (or relaunched)
	RelayInner     = "relay.inner"     // the agent's own session id and transcript were found
	RelaySend      = "relay.send"      // text was typed into the agent
	RelayDelivered = "relay.delivered" // the agent's transcript shows the text arrived
	RelayState     = "relay.state"     // working, idle, blocked on a dialog, or exited
	RelayProgress  = "relay.progress"  // something the agent said mid-turn
	RelayTurn      = "relay.turn"      // the agent finished a turn; carries its final message
	RelayLimit     = "relay.limit"     // the agent's usage budget crossed a threshold or ran out
	RelayEnd       = "relay.end"       // the agent was stopped on purpose
	RelayAutopilot = "relay.autopilot" // a standing order to keep the agent going (or its end)
)

// Event is one line of the session log.
type Event struct {
	Seq   int64           `json:"seq"`
	Time  time.Time       `json:"ts"`
	Type  string          `json:"type"`
	Actor string          `json:"actor,omitempty"`
	Task  string          `json:"task,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`

	// Source is filled in by the reader so that a dossier can cite where an
	// event lives on disk. It is never serialised.
	Source Source `json:"-"`
}

// Source locates an event within the session directory.
type Source struct {
	File string // subsession file name, e.g. 1788740857293.jsonl
	Line int    // 1-based line number within that file
}

func (s Source) String() string {
	if s.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", s.File, s.Line)
}

// New builds an event with the given payload. Seq and Time are assigned when
// the event is appended to a session.
func New(typ, actor string, data any) Event {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("event %s: marshal payload: %v", typ, err))
	}
	return Event{Type: typ, Actor: actor, Data: raw}
}

// WithTask tags an event with the task it belongs to.
func (e Event) WithTask(id string) Event {
	e.Task = id
	return e
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// ---- payloads -----------------------------------------------------------

// SessionStartData opens a session.
type SessionStartData struct {
	Session     string            `json:"session"`
	Cwd         string            `json:"cwd"`
	Version     string            `json:"version"`
	Interactive bool              `json:"interactive"`
	Models      map[string]string `json:"models"`              // actor -> model
	Endpoints   map[string]string `json:"endpoints,omitempty"` // actor -> base URL actually in use
	Config      string            `json:"config,omitempty"`    // bundle or preset name
}

// SessionResumeData records a restart and what the replay had to close.
type SessionResumeData struct {
	Interactive bool     `json:"interactive"`
	Closed      []string `json:"closed,omitempty"` // human-readable list of things marked interrupted
}

// SessionEndData records why the runner stopped.
type SessionEndData struct {
	Reason string `json:"reason"` // done | interrupted | quit | awaiting-input | error
}

// SubsessionStartData opens a new subsession file.
type SubsessionStartData struct {
	File   string `json:"file"`
	Index  int    `json:"index"`
	Reason string `json:"reason"` // new | rollover
}

// SubsessionEndData closes a subsession file.
type SubsessionEndData struct {
	Reason      string `json:"reason"`
	NextFile    string `json:"next_file"`
	InputTokens int    `json:"input_tokens"` // orchestrator context size that triggered the rollover
}

// UserMessageData is text from the user.
type UserMessageData struct {
	Text string `json:"text"`
	// Source says where the message came from when it was not the terminal:
	// "web" or "finalechat" (the user's phone).
	Source string `json:"source,omitempty"`
	// Client declares what context the sending client can supply.
	Client *clientcaps.Caps `json:"client,omitempty"`
	// Attachments are files the user sent, saved under the session directory.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Route is who the user addressed, when their client let them say: "agent"
	// (the relayed agent) or "eagent". Delivery is how it should reach the
	// agent: queue, interrupt, or after_turn. Both are empty when unsaid.
	Route    string `json:"route,omitempty"`
	Delivery string `json:"delivery,omitempty"`
}

// Attachment is a file carried by a message: a screenshot the user sent
// from their phone, or one the narrator attached to a report. Bytes live
// at Path, under the session's attachments/ directory.
type Attachment struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Kind        string `json:"kind,omitempty"` // image | file
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	ID          string `json:"id,omitempty"` // the remote id when known
}

// IsImage reports whether the attachment is a picture a model could look at.
func (a Attachment) IsImage() bool {
	switch a.ContentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return a.Kind == "image"
}

// UserAnswerData answers a narrator question.
type UserAnswerData struct {
	QuestionID  string           `json:"question_id"`
	Text        string           `json:"text"`
	Source      string           `json:"source,omitempty"` // "" (terminal), "web", or "finalechat"
	Client      *clientcaps.Caps `json:"client,omitempty"`
	Attachments []Attachment     `json:"attachments,omitempty"`
}

// TurnData brackets one actor turn (one or more model calls).
type TurnData struct {
	Reason string `json:"reason,omitempty"`
}

// ToolCall is one tool invocation requested by a model.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Usage is the token accounting reported by a provider for one call.
type Usage struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Cached    int `json:"cached,omitempty"`
	Reasoning int `json:"reasoning,omitempty"`
}

// AssistantData is one model response.
type AssistantData struct {
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Host      string          `json:"host,omitempty"` // the base URL that served the call; Native replays only there
	Text      string          `json:"text,omitempty"`
	Reasoning string          `json:"reasoning,omitempty"` // summary, for humans
	ToolCalls []ToolCall      `json:"tool_calls,omitempty"`
	Native    json.RawMessage `json:"native,omitempty"` // provider-specific replay payload
	Usage     Usage           `json:"usage"`
	ElapsedMS int64           `json:"elapsed_ms"`
	// SeenSeq is the last event sequence number that was rendered into the
	// prompt for this call. Anything after it arrived while the model was
	// thinking and must be delivered on the next turn.
	SeenSeq int64  `json:"seen_seq"`
	Stop    string `json:"stop,omitempty"`
}

// ToolResultData is the outcome of one tool call.
type ToolResultData struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`
	// Images are pictures the tool produced for the model to look at
	// (view_image); they follow the result as a user message with the image.
	Images []Attachment `json:"images,omitempty"`
}

// TaskCreateData delegates work to the task worker.
type TaskCreateData struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // work | dossier
}

// TaskEndData reports a task's outcome.
type TaskEndData struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // completed | failed | cancelled | interrupted
	Summary string `json:"summary"`
	Turns   int    `json:"turns,omitempty"`
	Usage   Usage  `json:"usage"`
}

// NoteData is an orchestrator hint for the narrator.
type NoteData struct {
	Text string `json:"text"`
	// Answer marks a note that answers a question the user asked. The
	// narrator relays it as the answer rather than deciding whether to.
	Answer bool `json:"answer,omitempty"`
}

// CwdChangeData records that an actor's working directory moved: a cd in one
// of its commands that persists for its later commands. The event's actor and
// task say whose directory it is.
type CwdChangeData struct {
	Path     string `json:"path"`
	Previous string `json:"previous,omitempty"`
}

// NarratorMessageData is user-visible output.
type NarratorMessageData struct {
	Text string `json:"text"`
	// Important marks a message the narrator wanted to buzz the user's
	// phone for (a finished job, a blocker, a finding).
	Important bool `json:"important,omitempty"`
	// Attachments are files sent with the message (screenshots, logs).
	Attachments []Attachment `json:"attachments,omitempty"`
}

// PhoneQuestionData records which phone card carries a narrator question,
// so a later run can withdraw it when the question is answered elsewhere.
type PhoneQuestionData struct {
	QuestionID string `json:"question_id"`
	CardID     string `json:"card_id"`
}

// PhoneThreadData records the Finalechat thread mirroring this session.
type PhoneThreadData struct {
	ThreadID   string `json:"thread_id"`
	ExternalID string `json:"external_id"`
	BaseURL    string `json:"base_url"`
	RemoteMode bool   `json:"remote_mode"` // the user said they are away from the terminal
}

// ThreadTitleData records the session's title and description (always the
// WHOLE session so far, never just the latest segment). Each event replaces
// the previous one; an empty Title or Description leaves that field
// unchanged. The phone mirror PATCHes the same values to FinaleChat.
type ThreadTitleData struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// NarratorQuestionData is a question that blocks on the user.
type NarratorQuestionData struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// ScheduleCreateData registers a loop, cron, or one-shot wake.
type ScheduleCreateData struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"` // loop | cron | once
	Spec string    `json:"spec"` // "45s", "*/5 * * * *", or RFC3339 time
	Note string    `json:"note"`
	Next time.Time `json:"next"`
}

// ScheduleFireData records one firing.
type ScheduleFireData struct {
	ID   string    `json:"id"`
	Note string    `json:"note"`
	Next time.Time `json:"next"`
}

// ScheduleCancelData removes a schedule.
type ScheduleCancelData struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

// ProcStartData records a backgrounded shell command.
type ProcStartData struct {
	Handle   string `json:"handle"`
	Command  string `json:"command"`
	Cwd      string `json:"cwd,omitempty"`
	TimeoutS int    `json:"timeout_s"`
}

// ProcPIDData records a started command's pid and start instant, so a
// resume after a hard kill can find and stop what it left running.
type ProcPIDData struct {
	Handle    string    `json:"handle"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// ProcExitData records how a command ended.
type ProcExitData struct {
	Handle     string `json:"handle"`
	ExitCode   int    `json:"exit_code"`
	Reason     string `json:"reason"` // exited | killed | timeout | lost
	DurationMS int64  `json:"duration_ms"`
	Tail       string `json:"tail,omitempty"`
	// Notify is true when the owning actor had already moved on, so the exit
	// is delivered as a notification on its next turn.
	Notify bool `json:"notify,omitempty"`
}

// DossierData opens a subsession with the task worker's briefing.
type DossierData struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
}

// HarnessMessageData is text the harness injects into an actor's context.
type HarnessMessageData struct {
	Text string `json:"text"`
}

// SteerData is the short instruction the harness puts in front of a model
// call (wake reason, context budget, what to do now). It is recorded in the
// log, addressed by the event's actor and task, so the prompt an actor sees
// is a strict extension of its previous prompt and provider prompt caches
// keep hitting.
type SteerData struct {
	Text string `json:"text"`
}

// YieldData records the orchestrator declaring it has nothing to do. Forced
// yields are issued by the harness (repeated failures, refusal to call tools)
// and always make the session idle, even if unseen events arrived.
type YieldData struct {
	Done   bool   `json:"done"`
	Reason string `json:"reason,omitempty"`
	Forced bool   `json:"forced,omitempty"`
}

// RouteData records a provider routing decision (e.g. Astra fallback).
type RouteData struct {
	Actor    string `json:"actor"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Reason   string `json:"reason"`
}

// ErrorData records a non-fatal failure for humans and the narrator.
type ErrorData struct {
	Where string `json:"where"`
	Text  string `json:"text"`
}

// RelayStartData records a relayed agent being launched. Name is how the
// session refers to it ("codex"); Target is the terminal session that hosts
// it, so a resumed eagent can find it again.
type RelayStartData struct {
	Name    string   `json:"name"`
	Profile string   `json:"profile"`
	Agent   string   `json:"agent"` // codex | claude
	Label   string   `json:"label,omitempty"`
	Dir     string   `json:"dir"`
	Host    string   `json:"host"` // tmux | screen
	Target  string   `json:"target"`
	Command []string `json:"command"`
	// InnerID is the agent's own session id when eagent chose it up front
	// (Claude Code's --session-id) or resumed it; empty until relay.inner
	// otherwise.
	InnerID    string `json:"inner_id,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Resumed    bool   `json:"resumed,omitempty"`
}

// RelayInnerData records where the agent keeps its own transcript.
type RelayInnerData struct {
	Name       string `json:"name"`
	InnerID    string `json:"inner_id"`
	Transcript string `json:"transcript"`
}

// RelaySendData records text typed into the agent. From is "user" when it is
// the user's own message forwarded verbatim (Seq names it) and "eagent" when
// eagent wrote it. Mode is queue, interrupt or after_turn.
type RelaySendData struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	From string `json:"from"`
	Seq  int64  `json:"seq,omitempty"`
	Mode string `json:"mode"`
	Text string `json:"text"`
	// Auto marks a user message the harness forwarded the moment it
	// arrived, without waiting for the orchestrator.
	Auto bool `json:"auto,omitempty"`
}

// RelayDeliveredData confirms (or gives up on confirming) that a send reached
// the agent.
type RelayDeliveredData struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// RelayStateData is the agent's condition: starting, working, idle, blocked
// (a dialog on its screen needs an answer) or exited.
type RelayStateData struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// RelayProgressData is something the agent said before its turn ended.
type RelayProgressData struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// RelayTurnData is the end of one of the agent's turns.
type RelayTurnData struct {
	Name        string   `json:"name"`
	Text        string   `json:"text"` // the agent's final message for the turn
	DurationMS  int64    `json:"duration_ms,omitempty"`
	Tools       int      `json:"tools,omitempty"`  // tool calls it made during the turn
	Images      []string `json:"images,omitempty"` // pictures it made or looked at
	Interrupted bool     `json:"interrupted,omitempty"`
}

// RelayLimitData reports the agent's usage budget.
type RelayLimitData struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"used_percent,omitempty"`
	Window      string  `json:"window,omitempty"` // "weekly", "5h", ...
	ResetsAt    string  `json:"resets_at,omitempty"`
	Text        string  `json:"text,omitempty"`
	Exhausted   bool    `json:"exhausted,omitempty"`
}

// RelayAutopilotData is a standing order: keep the agent working on
// Instruction until Until (RFC3339; empty means until the user says stop),
// deciding its questions instead of waiting for the user when Decide is set.
// Off ends it.
type RelayAutopilotData struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction,omitempty"`
	Until       string `json:"until,omitempty"`
	Decide      bool   `json:"decide,omitempty"`
	Off         bool   `json:"off,omitempty"`
}

// RelayEndData records the agent being stopped on purpose.
type RelayEndData struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}
