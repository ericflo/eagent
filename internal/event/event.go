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
// an unambiguous origin. eagent is the one model-driven actor: it reads what
// the relayed agent does, talks to the user, and drives the agent's terminal.
// (Logs written before there was one loop name three others; see legacy.go.)
const (
	ActorUser    = "user"
	ActorHarness = "harness"
	ActorEagent  = "eagent"
	// ActorSummarizer is the dedicated thread summarizer, when one is
	// configured. It appears only in usage accounting and route events.
	ActorSummarizer = "summarizer"
)

// Event types. Names are dotted and stable; adding a type is backwards
// compatible, renaming one is not. That is why what eagent says to the user
// is still written as narrator.message: the name dates from when a separate
// narrator did the talking, and old logs and their readers know it.
const (
	SessionStart    = "session.start"
	SessionResume   = "session.resume"
	SessionEnd      = "session.end"
	SubsessionStart = "subsession.start"
	SubsessionEnd   = "subsession.end"

	UserMessage = "user.message"
	UserAnswer  = "user.answer"
	UserDismiss = "user.dismiss" // the user closed a question without answering it

	TurnStart  = "turn.start"
	TurnEnd    = "turn.end"
	Assistant  = "assistant"
	ToolResult = "tool.result"

	Message  = "narrator.message"  // something eagent told the user
	Question = "narrator.question" // something eagent asked the user
	Checks   = "narrator.checks"   // looks that ended in silence, counted for their tokens only
	Owed     = "answer.owed"       // what the user wrote to eagent that is still owed an answer, and the reminders it has had

	Dossier        = "dossier"         // the briefing that opens a subsession after a rollover
	HarnessMessage = "harness.message" // a notice from the harness in eagent's conversation
	Route          = "route"
	PhoneThread    = "phone.thread"   // the session is mirrored to the user's phone
	PhoneQuestion  = "phone.question" // a question was posted to the phone: as a card, or (an empty card_id) as a message
	PhoneMirror    = "phone.mirror"   // a user message from the terminal or the web was copied to the phone thread
	ImagesRefused  = "images.refused" // a provider refused pictures of the conversation; they are left out from here on
	ThreadTitle    = "thread.title"   // the session's title and summary (whole session so far), mirrored to FinaleChat when the phone is on
	Steer          = "steer"          // what eagent was shown at the end of its prompt for a look it acted on, kept so every prompt extends the last
	CwdChange      = "cwd.change"     // the folder the session works in, when it is not the project root
	Error          = "error"

	// Timers eagent sets for itself: wake me then, or every so often, with
	// this note.
	ScheduleCreate = "schedule.create"
	ScheduleFire   = "schedule.fire"
	ScheduleCancel = "schedule.cancel"

	// A relay is a coding agent (Claude Code, Codex, Antigravity, pi) that
	// eagent runs in a terminal it owns and passes the user's words to.
	RelayStart     = "relay.start"     // the agent was launched (or relaunched)
	RelayInner     = "relay.inner"     // the agent's own session id and transcript were found
	RelaySend      = "relay.send"      // text was typed into the agent
	RelayMoved     = "relay.moved"     // a send not yet typed went to the agent that took over
	RelayDelivered = "relay.delivered" // the agent's transcript shows the text arrived
	RelayState     = "relay.state"     // working, idle, blocked on a dialog, or exited
	RelayProgress  = "relay.progress"  // something the agent said mid-turn
	RelayTurn      = "relay.turn"      // the agent finished a turn; carries its final message
	RelayLimit     = "relay.limit"     // the agent's usage budget crossed a threshold or ran out
	RelayEnd       = "relay.end"       // the agent was stopped on purpose
	RelayAutopilot = "relay.autopilot" // a standing order to keep the agent going (or its end)
	RelayRead      = "relay.read"      // how far the agent's transcript was read, after a read the record has nothing else of
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
	Efforts     map[string]string `json:"efforts,omitempty"`   // actor -> reasoning effort in use
	Config      string            `json:"config,omitempty"`    // bundle or preset name
	// TZ is the IANA time zone the session's clock times are written in for
	// the model ("America/Los_Angeles"). It is fixed at the start so the
	// same event always renders the same text, wherever the session is
	// resumed. Empty in older logs, which render in the reader's zone.
	TZ string `json:"tz,omitempty"`
	// PhoneLedger says this run records every message from the app it
	// takes in (phone.read and the phone ids on user.message and
	// user.answer). A run by an eagent without that leaves it out, and its
	// record is read by time.
	PhoneLedger bool `json:"phone_ledger,omitempty"`
}

// SessionResumeData records a restart and what the replay had to close.
type SessionResumeData struct {
	Interactive bool     `json:"interactive"`
	Closed      []string `json:"closed,omitempty"` // human-readable list of things marked interrupted
	// PhoneLedger: as on SessionStartData, for this run.
	PhoneLedger bool `json:"phone_ledger,omitempty"`
	// After is the seq of the newest event the record held when this run
	// opened it. A resume repairs a cut turn before it writes this event,
	// so the event just before it can be the run's own; After says where
	// the run before really stopped.
	After int64 `json:"after,omitempty"`
}

// SessionEndData records why the runner stopped.
type SessionEndData struct {
	Reason string `json:"reason"` // done | interrupted | quit | awaiting-input | error
}

// SubsessionStartData opens a new subsession file.
type SubsessionStartData struct {
	File   string `json:"file"`
	Index  int    `json:"index"`
	Reason string `json:"reason"` // new | rollover | upgrade (an older session continued from a dossier)
}

// SubsessionEndData closes a subsession file.
type SubsessionEndData struct {
	Reason      string `json:"reason"`
	NextFile    string `json:"next_file"`
	InputTokens int    `json:"input_tokens"` // prompt size that triggered the rollover
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
	// Held is why the message was kept for eagent instead of being typed
	// into the agent's terminal as it arrived ("it is addressed to you").
	// Empty for a message that was passed straight on.
	Held string `json:"held,omitempty"`
	// PhoneID is the Finalechat message id when the message came from the
	// phone, so the thread can show what became of it (relay receipts).
	PhoneID string `json:"phone_id,omitempty"`
	// CardID is the phone card whose answer these words are, when a card's
	// answer was kept as a message (its question was settled another way).
	CardID string `json:"card_id,omitempty"`
	// Inbox is the id of the inbox file the words came in, when the file
	// gave one (its own name), which lets a later run tell that a file
	// still lying there was already taken.
	Inbox string `json:"inbox,omitempty"`
}

// Attachment is a file carried by a message: a screenshot the user sent
// from their phone, or one eagent attached to a message. Bytes live
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

// UserAnswerData answers a question.
type UserAnswerData struct {
	QuestionID  string           `json:"question_id"`
	Text        string           `json:"text"`
	Source      string           `json:"source,omitempty"` // "" (terminal), "web", or "finalechat"
	Client      *clientcaps.Caps `json:"client,omitempty"`
	Attachments []Attachment     `json:"attachments,omitempty"`
	// PhoneID is the app message the answer came from, and CardID the card
	// it was written on, when it came from the phone: in the one write that
	// records the answer, the session's ledger of app messages taken in
	// (phone.read) learns both.
	PhoneID string `json:"phone_id,omitempty"`
	CardID  string `json:"card_id,omitempty"`
	// Inbox is the id of the inbox file the answer came in, when the file
	// gave one (its own name), which lets a later run tell that a file
	// still lying there was already taken.
	Inbox string `json:"inbox,omitempty"`
}

// ImagesRefusedData lists pictures (by the path of the session's own copy)
// that a provider refused during a call, for a reason the file alone does
// not show. The conversation is rendered with a note in their place from
// then on, so the request does not depend on which process or client
// remembers the refusal.
type ImagesRefusedData struct {
	Paths []string `json:"paths"`
}

// UserDismissData closes a question with no answer. It is not a reply:
// nothing is decided and nobody is woken by it.
type UserDismissData struct {
	QuestionID string `json:"question_id"`
	Source     string `json:"source,omitempty"` // "" (terminal), "web", or "finalechat"
}

// TurnData brackets one turn of eagent's that went on the record (one or
// more model calls, at least one of which did something).
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
	Provider string `json:"provider"`
	Model    string `json:"model"` // as the response named it
	// Asked is the model the request asked for. The response's name for
	// it can differ (a dated variant, a router's alias), and it is the
	// asked name that says which route served the call and which model
	// the native items can go back to. Absent in older records.
	Asked     string          `json:"asked,omitempty"`
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
	// Images are pictures a tool brought back for the model to look at
	// (view_image), copied into the session so the conversation renders the
	// same bytes every time.
	Images []Attachment `json:"images,omitempty"`
}

// CwdChangeData records the folder the session works in when it is not the
// project root: where its agents start unless told otherwise.
type CwdChangeData struct {
	Path     string `json:"path"`
	Previous string `json:"previous,omitempty"`
}

// MessageData is something eagent told the user.
type MessageData struct {
	Text string `json:"text"`
	// Important marks a message worth buzzing the user's phone for (a
	// finished job, a blocker, a finding).
	Important bool `json:"important,omitempty"`
	// Attachments are files sent with the message (screenshots, logs).
	Attachments []Attachment `json:"attachments,omitempty"`
	// Auto marks words the harness said itself, without a model call: the
	// outcome of a command the user typed, or that the model cannot be
	// reached. They are shown to the model as a notice, since no reply of
	// its own carries them.
	Auto bool `json:"auto,omitempty"`
	// Room, on the notice that no restart can make room, is the room for
	// input (window less reply ceiling, in tokens) of the model it was found
	// on: the decision stands only while the room in force is no larger.
	// Absent on every other message, and on such a notice in older records.
	Room int `json:"room,omitempty"`
}

// PhoneQuestionData records how a question was posted to the phone: the
// card that carries it, so a later run can withdraw it when the question
// is answered elsewhere; or, with an empty CardID, a message in ThreadID
// (the service refused the card and gives no client keys), which a later
// ask of the question does not post again.
type PhoneQuestionData struct {
	QuestionID string `json:"question_id"`
	CardID     string `json:"card_id"`
	// ThreadID is the thread the card was made in and Key the number of
	// its client key (1 for the plain key), so a later ask of the same
	// question starts its walk at the newest card of that thread.
	ThreadID string `json:"thread_id,omitempty"`
	Key      int    `json:"key,omitempty"`
}

// PhoneAnswerTaken records that a card's answer reached the session (as
// the answer, or as a message), so no later poll or re-ask takes it a
// second time.
const PhoneAnswerTaken = "phone.answer_taken"

// PhoneAnswerTakenData names the card whose answer was taken.
type PhoneAnswerTakenData struct {
	CardID string `json:"card_id"`
}

// PhoneRead records that the session took in a message the user sent from
// the app whose effect carries no phone id of its own (an answer, a
// command, a reply whose files were all refused), and, when the mirror
// starts keeping this ledger, the message it reads on from. With the
// phone_id on user.message it says, after any stop, which app messages
// the session has taken in: a resume goes on after the newest of them.
const PhoneRead = "phone.read"

// PhoneReadData names the app message taken in.
type PhoneReadData struct {
	MessageID string `json:"message_id"`
}

// PhoneMirrorData records which thread message shows a user message the
// user wrote at the terminal or in the web UI, so a later run can keep the
// receipt under it (a message from the phone carries its own id).
type PhoneMirrorData struct {
	Seq       int64  `json:"seq"`
	MessageID string `json:"message_id"`
}

// PhoneThreadData records the Finalechat thread mirroring this session.
type PhoneThreadData struct {
	ThreadID   string `json:"thread_id"`
	ExternalID string `json:"external_id"`
	BaseURL    string `json:"base_url"`
	RemoteMode bool   `json:"remote_mode"` // the user said they are away from the terminal
}

// ThreadTitleData records the session's title and summary (always the WHOLE
// session so far, never just the latest segment). Each event replaces the
// previous one; an empty Title or Description leaves that field unchanged.
// The phone mirror PATCHes the same values to FinaleChat.
//
// By "summary" is the running summary: eagent's own update_thread calls, or
// the dedicated summarizer's. By "" is a name the user asked for, and By
// "finalechat" is what the app already shows when the mirror attaches.
type ThreadTitleData struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// By is who wrote it: "summary" the running summary, "" a name the
	// user asked for, "finalechat" the thread as the app has it.
	By string `json:"by,omitempty"`
	// Pin marks a title someone chose on purpose (the user in the app, or
	// eagent at the user's request): later summaries keep it.
	Pin bool `json:"pin,omitempty"`
	// SeenSeq is the newest event a summary took into account.
	SeenSeq int64 `json:"seen_seq,omitempty"`
	// Usage and ElapsedMS account for a dedicated summarizer's model call.
	// An inline update has none: it is part of one of eagent's own calls.
	Usage     *Usage `json:"usage,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	// Host and Asked are the route that served that call, as on an
	// assistant reply. Neither is written when its calls were served by
	// two routes, nor in a record from before they were: the call then
	// goes on the route in use where it stands.
	Host  string `json:"host,omitempty"`
	Asked string `json:"asked,omitempty"`
}

// QuestionData is a question that waits on the user.
type QuestionData struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// DossierData opens a subsession with a dossier: where the session stands
// and a map (file:line) of where in the record each topic can be read, as
// eagent wrote them, followed by the facts the harness adds from the log.
type DossierData struct {
	Text string `json:"text"`
	// CoversSeq is the last event the briefing took into account. The new
	// subsession's conversation is the briefing followed by every event
	// after it, so nothing that arrived while it was being written is lost.
	CoversSeq int64 `json:"covers_seq,omitempty"`
	// CoversNone says the briefing took in no event at all: no call had
	// seen anything when it was written (the session's first request was
	// refused as too long). Everything on record follows it. Without it a
	// CoversSeq of 0 is an older log that does not say.
	CoversNone bool `json:"covers_none,omitempty"`
	// By is who wrote the body: "eagent" (the model, on its cached prompt)
	// or "harness" (built from the log alone, when the model could not).
	By string `json:"by,omitempty"`
	// NoMap marks a body by the model that was kept although it cites too
	// few places in the record to be a map (it was sent back once and
	// came back the same). The body says so in its first lines.
	NoMap bool `json:"no_map,omitempty"`
	// Usage and ElapsedMS account for the model calls that wrote it, Calls
	// says how many there were, and Consulted lists what the writer looked
	// up in the record first ("read 1788740857293.jsonl:40-90", "search
	// deploy"). The lookups themselves are not recorded.
	Usage     *Usage   `json:"usage,omitempty"`
	ElapsedMS int64    `json:"elapsed_ms,omitempty"`
	Calls     int      `json:"calls,omitempty"`
	Consulted []string `json:"consulted,omitempty"`
	// Host and Asked are the route that served the calls in Usage, as on
	// an assistant reply. Earlier holds the calls of the same pass that
	// another route served before it took over (the model changed while
	// the pass ran), each priced on its own; Usage is then the last
	// route's alone, and Calls still counts them all. A record from before
	// they were written names no route, and the pass goes on the route in
	// use where it stands.
	Host    string       `json:"host,omitempty"`
	Asked   string       `json:"asked,omitempty"`
	Earlier []RouteUsage `json:"earlier,omitempty"`
	// Restart is how the restart this dossier opens stands in a run of
	// restarts. A dossier from before it was written leaves it out.
	Restart *DossierRestart `json:"restart,omitempty"`
	// TaskID names the task worker that wrote it, in logs from before
	// eagent wrote its own.
	TaskID string `json:"task_id,omitempty"`
}

// DossierRestart is a dossier's `restart`: whether the restart carried
// whole what no look had taken in, whether it was the one more try after a
// restart that carried nothing and was refused, and how many restarts in
// a row, this one included, were made for conversations too long from
// their start.
type DossierRestart struct {
	Carried bool `json:"carried,omitempty"`
	Retried bool `json:"retried,omitempty"`
	Rescues int  `json:"rescues,omitempty"`
}

// RouteUsage is what some calls served by one route used.
type RouteUsage struct {
	Host  string `json:"host"`
	Asked string `json:"asked"`
	Usage Usage  `json:"usage"`
	Calls int    `json:"calls"`
}

// HarnessMessageData is a notice the harness puts in eagent's conversation.
type HarnessMessageData struct {
	Text string `json:"text"`
}

// SteerData is the end of the prompt for one look: why eagent was woken,
// what the agent is doing right now, how long the user has heard nothing.
// It is recorded only when eagent acted on that look, so its conversation
// reads as what it saw and then did, and each prompt is a strict extension
// of the one before (provider prompt caches keep hitting). A look that ended
// in silence leaves nothing.
type SteerData struct {
	Text string `json:"text"`
}

// RouteData records a provider routing decision (e.g. Astra fallback).
type RouteData struct {
	Actor    string `json:"actor"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Reason   string `json:"reason"`
}

// ErrorData records a non-fatal failure.
type ErrorData struct {
	Where string `json:"where"`
	Text  string `json:"text"`
}

// RelayStartData records a relayed agent being launched. Name is how the
// session refers to it ("codex"); Target is the terminal session that hosts
// it, so a resumed eagent can find it again.
type RelayStartData struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
	Agent   string `json:"agent"` // a relay agent's name: claude, codex, antigravity, pi
	// AgentLabel is the agent's short name for people ("Claude"); logs
	// written before it existed leave it out.
	AgentLabel string   `json:"agent_label,omitempty"`
	Label      string   `json:"label,omitempty"`
	Dir        string   `json:"dir"`
	Host       string   `json:"host"` // tmux | screen
	Target     string   `json:"target"`
	Command    []string `json:"command"`
	// InnerID is the agent's own session id when eagent chose it up front
	// (Claude Code's --session-id) or resumed it; empty until relay.inner
	// otherwise.
	InnerID    string `json:"inner_id,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Resumed    bool   `json:"resumed,omitempty"`
	// Model and Effort are the session's picks over the profile's ("" keeps
	// the profile's, and a profile without one leaves it to the agent's own
	// configuration). A restart starts it with the same picks.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
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
	// arrived, with no model call.
	Auto bool `json:"auto,omitempty"`
}

// RelayMovedData is a send that waited for a switch (or was waiting to be
// typed when the switch stopped its agent) and goes to the agent that took
// over instead. Its fields are the send's own, under the new relay's Name
// and with the same ID; Prev is the relay it was first sent to. From now on
// it is the new relay's send: arrival is confirmed under the new name.
type RelayMovedData struct {
	RelaySendData
	Prev string `json:"prev"`
}

// RelayDeliveredData confirms (or gives up on confirming) that a send reached
// the agent.
type RelayDeliveredData struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	// Outcome says, when OK is false, how sure the failure is:
	// DeliveryNotSent or DeliveryUnconfirmed. Empty in a record written
	// before it existed, which reads as DeliveryUnconfirmed.
	Outcome string `json:"outcome,omitempty"`
	// Read, on this and the other relay events a line of the agent's
	// transcript gave, is how far that transcript was read then (the end
	// of the line); 0 otherwise, and in older records.
	Read int64 `json:"read,omitempty"`
}

// The outcomes of a send that did not arrive.
const (
	// DeliveryNotSent: certainly nothing reached the agent. Nothing was
	// typed, or the copy held for the end of its turn was taken back
	// unread.
	DeliveryNotSent = "not_sent"
	// DeliveryUnconfirmed: it was typed, and its arrival was never shown.
	DeliveryUnconfirmed = "unconfirmed"
)

// RelayStateData is the agent's condition: starting, working, idle, blocked
// (a dialog on its screen needs an answer) or exited.
type RelayStateData struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Dialog, on blocked, is the title of the dialog the agent reports
	// holding it (pi's extension); "" otherwise, and in older records.
	Dialog string `json:"dialog,omitempty"`
	Read   int64  `json:"read,omitempty"` // as RelayDeliveredData.Read
}

// RelayProgressData is something the agent said before its turn ended.
type RelayProgressData struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Read int64  `json:"read,omitempty"` // as RelayDeliveredData.Read
}

// RelayReadData is how far the agent's transcript was read, after a read
// whose only news was a user line that confirmed nothing (relay.read).
type RelayReadData struct {
	Name string `json:"name"`
	Read int64  `json:"read"`
}

// RelayTurnData is the end of one of the agent's turns.
type RelayTurnData struct {
	Name        string   `json:"name"`
	Text        string   `json:"text"` // the agent's final message for the turn
	DurationMS  int64    `json:"duration_ms,omitempty"`
	Tools       int      `json:"tools,omitempty"`  // tool calls it made during the turn
	Images      []string `json:"images,omitempty"` // pictures it made or looked at
	Interrupted bool     `json:"interrupted,omitempty"`
	// Idle marks a turn that answered a message eagent itself sent (not
	// one of the user's) without running any tools: an agent waiting on its
	// own work.
	Idle bool  `json:"idle,omitempty"`
	Read int64 `json:"read,omitempty"` // as RelayDeliveredData.Read
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

// RelayLimitData reports the agent's usage budget.
type RelayLimitData struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"used_percent,omitempty"`
	Window      string  `json:"window,omitempty"` // "weekly", "5h", ...
	ResetsAt    string  `json:"resets_at,omitempty"`
	Text        string  `json:"text,omitempty"`
	Exhausted   bool    `json:"exhausted,omitempty"`
	Read        int64   `json:"read,omitempty"` // as RelayDeliveredData.Read
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

// ChecksData totals the looks that ended in silence since the last such
// record. They leave nothing in eagent's conversation; this keeps their
// tokens in the session's totals.
type ChecksData struct {
	Count     int   `json:"count"`
	Usage     Usage `json:"usage"`
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`
	// PromptTokens is the size of the last of those prompts, so a reader
	// of the log knows how full the conversation is.
	PromptTokens int `json:"prompt_tokens,omitempty"`
	// FirstPrompt is the size of the first of those prompts. A resumed
	// session reads it to know how long the conversation was at its first
	// call, when that call left no record of its own.
	FirstPrompt int `json:"first_prompt,omitempty"`
	// SeenSeq is the newest event the last of those looks had in view, so
	// a resumed session does not look again at what was already weighed.
	SeenSeq int64 `json:"seen_seq,omitempty"`
	// Host and Asked are the route that served those looks, its address
	// and the model asked for, as on an assistant reply: a look still out
	// when the model changed is priced on the model that made it. A record
	// from before they were written names neither, and its looks go on the
	// route in use where it stands.
	Host  string `json:"host,omitempty"`
	Asked string `json:"asked,omitempty"`
}

// OwedData is what eagent still owes the user, as it stands after the
// change it records: the oldest message kept for eagent that was looked at
// and left with no answer and nothing done about it (or, with Answer, the
// user's answer to eagent's question that it held on), and how many
// reminder calls have been arranged for it. Seq 0 says nothing is owed any
// more. A resumed session reads the newest one to keep asking where the
// last run stopped. It renders nothing in eagent's conversation.
type OwedData struct {
	Seq    int64 `json:"seq"`
	Answer bool  `json:"answer,omitempty"`
	// Tries counts the reminder calls arranged so far, the one waiting on
	// its delay included.
	Tries int `json:"tries,omitempty"`
	// Started says reminder number Tries has gone out: a resumed session
	// counts it as made, whether or not it came back.
	Started bool `json:"started,omitempty"`
}

// RelayEndData records the agent being stopped on purpose.
type RelayEndData struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}
