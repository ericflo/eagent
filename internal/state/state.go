// Package state folds the event log into the runtime picture: the agents the
// session runs, the pending question, subsessions, token accounting, and the
// conversation eagent's model is shown. The same reducer runs live and on
// replay, so a resumed session is byte-for-byte the session that stopped.
package state

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/prompts"
)

// Question is a pending ask_user.
type Question struct {
	ID      string
	Text    string
	Options []string
	Seq     int64
	Answer  string
	Done    bool
}

// Subsession is one JSONL file's span.
type Subsession struct {
	Index    int
	File     string
	Reason   string
	StartSeq int64
	EndSeq   int64 // 0 while open
	// Dossier is the briefing the subsession opened with ("" for the
	// first), and CoversSeq the last event that briefing took in.
	// CoversNone: it took in none, which a CoversSeq of 0 alone does not
	// say (older logs leave it out).
	Dossier    string
	CoversSeq  int64
	CoversNone bool
	// Restart: the one loop opened it from a dossier (reason rollover or
	// upgrade), so its dossier comes right after its start. Briefed: that
	// dossier is in the record. A restart that is not briefed was stopped
	// before it finished (see Conversation).
	Restart, Briefed bool
}

// State is everything derived from the log.
type State struct {
	SessionID string
	// Cwd is the project root. Dir is the folder the session works in when
	// that is somewhere else ("" otherwise): where its agents start.
	Cwd, Dir    string
	Version     string
	Interactive bool
	Models      map[string]string      // actor -> model in use
	Hosts       map[string]string      // actor -> base URL in use
	Efforts     map[string]string      // actor -> reasoning effort in use
	Config      string                 // bundle or preset name the session runs under
	Phone       *event.PhoneThreadData // Finalechat thread, when the session is mirrored
	Title       string                 // latest thread.title title ("" until titled)
	Description string                 // latest thread.title summary ("" until summarized)
	// TitlePinned says someone chose the title on purpose (see
	// event.ThreadTitleData.Pin); TitleSeq is the event that last changed
	// it; TitleRevisions counts the running summary's changes to it;
	// SummarySeq is the newest event the latest summary covered.
	TitlePinned    bool
	TitleSeq       int64
	TitleRevisions int
	SummarySeq     int64
	Started        time.Time

	Events      []event.Event
	Subsessions []Subsession

	Question *Question // pending, or nil
	// LastDismissSeq is the seq of the latest user.dismiss (a question the
	// user closed without answering), 0 if none.
	LastDismissSeq int64
	// Relays are the agents the session runs, by name.
	Relays     map[string]*Relay
	RelayOrder []string
	// LastUserSeq is the newest user message or answer.
	LastUserSeq int64
	// Schedules are the timers eagent set for itself, by id.
	Schedules     map[string]*Schedule
	ScheduleOrder []string

	Ended     bool
	EndReason string
	Resumes   int
	// Turns tracks open turns per actor (turn.start without turn.end), so a
	// reader of the log can tell who is mid-turn right now.
	Turns     map[string]int
	TurnSince map[string]time.Time
	// OpenAtEnd is what Turns held when the session last ended, until a
	// resume or the turn's own end settles it: a run whose shutdown did
	// not wait out a turn ends with it open, and the resume has to close
	// it and take it up (harness/resume.go). An ended session is not
	// mid-turn, so Turns itself is cleared.
	OpenAtEnd map[string]int

	// LastUsage is the newest provider-reported usage per actor; its Input
	// is the size of that actor's last recorded prompt.
	LastUsage map[string]event.Usage
	// Totals accumulate all usage per actor.
	Totals map[string]event.Usage
	// ByRoute accumulates usage under the route that actually served each
	// call, so a mid-session fallback does not reprice tokens already spent.
	ByRoute map[RouteKey]event.Usage
	// RefusedImages are pictures a provider refused (images.refused), by
	// path: the conversation carries a note in their place.
	RefusedImages map[string]bool
	// hostModels is the model last routed to each actor's hosts (Model
	// left empty in the key), to price a reply that came back from the
	// route before the one in use.
	hostModels map[RouteKey]string
	Calls      map[string]int
	// Latencies holds every model call's wall time per actor, for percentiles.
	Latencies map[string][]int64

	Errors []string

	// Counters continue id sequences across restarts.
	questionSeq, relaySendSeq, scheduleSeq int

	// relayHolders are, per name, the relays that held it in turn: a new
	// conversation under a stopped relay's name adds one, a restart
	// replaces the last. See RelayAt.
	relayHolders map[string][]*Relay

	// lastMoveSeq is the newest thing an agent did that the user might be
	// asked about: a finished turn, a dialog it is stuck on, an exit.
	lastMoveSeq int64

	// loc is the time zone the session's clock times are rendered in (see
	// event.SessionStartData.TZ); nil means the reader's own.
	loc *time.Location

	// prompts renders user messages and answers in eagent's conversation.
	// Nil means the built-in defaults; SetPrompts installs a project set
	// (with overrides) loaded by the harness.
	prompts *prompts.Set
}

// SetPrompts installs the prompt set used to render user messages and
// answers. Pass nil to go back to the built-ins.
func (s *State) SetPrompts(p *prompts.Set) { s.prompts = p }

// RouteKey identifies the endpoint that served a set of calls, for pricing.
type RouteKey struct{ Actor, Host, Model string }

// New returns an empty state.
func New() *State {
	return &State{
		Models:     map[string]string{},
		Efforts:    map[string]string{},
		Hosts:      map[string]string{},
		LastUsage:  map[string]event.Usage{},
		Totals:     map[string]event.Usage{},
		ByRoute:    map[RouteKey]event.Usage{},
		hostModels: map[RouteKey]string{},
		Calls:      map[string]int{},
		Latencies:  map[string][]int64{},
		Schedules:  map[string]*Schedule{},
		Turns:      map[string]int{},
		TurnSince:  map[string]time.Time{},
	}
}

// Replay folds a full event list.
func Replay(events []event.Event) *State {
	s := New()
	for _, ev := range events {
		s.Apply(ev)
	}
	return s
}

// Current returns the open subsession.
func (s *State) Current() *Subsession {
	if len(s.Subsessions) == 0 {
		return nil
	}
	return &s.Subsessions[len(s.Subsessions)-1]
}

// LastSeq is the newest applied sequence number.
func (s *State) LastSeq() int64 {
	if len(s.Events) == 0 {
		return 0
	}
	return s.Events[len(s.Events)-1].Seq
}

// Schedule is a loop, cron, or one-shot wake.
type Schedule struct {
	ID        string
	Kind      string
	Spec      string
	Note      string
	Next      time.Time
	Fires     int
	Cancelled bool
	Created   time.Time
}

// NextScheduleID allocates a schedule id that continues after replay.
func (s *State) NextScheduleID() string { s.scheduleSeq++; return "s" + strconv.Itoa(s.scheduleSeq) }

// ActiveSchedules returns schedules that will fire again.
func (s *State) ActiveSchedules() []*Schedule {
	var out []*Schedule
	for _, id := range s.ScheduleOrder {
		if sc := s.Schedules[id]; !sc.Cancelled {
			out = append(out, sc)
		}
	}
	return out
}

// NextQuestionID allocates a question id that continues after replay.
func (s *State) NextQuestionID() string { s.questionSeq++; return "q" + strconv.Itoa(s.questionSeq) }

func bump(counter *int, id string, prefix string) {
	if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && n > *counter {
		*counter = n
	}
}

// WorkDir is the folder the session works in: where its agents start unless
// told otherwise. It is the project root unless the session was started
// somewhere else.
func (s *State) WorkDir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return s.Cwd
}

// Location is the time zone clock times are written in for the model: the
// one the session started in, wherever it is read.
func (s *State) Location() *time.Location {
	if s.loc != nil {
		return s.loc
	}
	return time.Local
}

// clock renders a time of day in the session's zone.
func (s *State) clock(t time.Time) string { return t.In(s.Location()).Format("15:04:05") }

// account adds model usage to an actor's totals, and to the route that
// served it (servedBy; host and asked are "" when the record does not
// say).
func (s *State) account(actor, host, asked string, u event.Usage, calls int, elapsedMS int64) {
	tot := s.Totals[actor]
	tot.Input += u.Input
	tot.Output += u.Output
	tot.Cached += u.Cached
	tot.Reasoning += u.Reasoning
	s.Totals[actor] = tot
	rk := s.servedBy(actor, host, asked)
	ru := s.ByRoute[rk]
	ru.Input += u.Input
	ru.Output += u.Output
	ru.Cached += u.Cached
	ru.Reasoning += u.Reasoning
	s.ByRoute[rk] = ru
	s.Calls[actor] += calls
	if elapsedMS > 0 {
		s.Latencies[actor] = append(s.Latencies[actor], elapsedMS)
	}
}

// servedBy is the route a reply was priced on. A reply that names its
// host and the model that was asked for names its route, whatever is in
// use now (a call can still be in flight when the route changes). An
// older record names only the host: it goes on the route in use, or, when
// it came from another host, on the model last routed to that host. A
// reply that names no host, or a host no route named, is put on the route
// in use.
func (s *State) servedBy(actor, host, asked string) RouteKey {
	rk := RouteKey{Actor: actor, Host: s.Hosts[actor], Model: s.Models[actor]}
	if host != "" && asked != "" {
		return RouteKey{Actor: actor, Host: host, Model: asked}
	}
	if host == "" || host == rk.Host {
		return rk
	}
	if m := s.hostModels[RouteKey{Actor: actor, Host: host}]; m != "" {
		return RouteKey{Actor: actor, Host: host, Model: m}
	}
	return rk
}

// Apply folds one event.
func (s *State) Apply(ev event.Event) {
	s.Events = append(s.Events, ev)
	switch ev.Type {
	case event.SessionStart:
		var d event.SessionStartData
		_ = ev.Decode(&d)
		s.SessionID, s.Cwd, s.Version, s.Interactive = d.Session, d.Cwd, d.Version, d.Interactive
		if d.Models != nil {
			s.Models = d.Models
		}
		if d.Efforts != nil {
			s.Efforts = d.Efforts
		}
		s.Config = d.Config
		for a, u := range d.Endpoints {
			s.Hosts[a] = u
		}
		if d.TZ != "" {
			if loc, err := time.LoadLocation(d.TZ); err == nil {
				s.loc = loc
			}
		}
		s.Started = ev.Time
	case event.PhoneThread:
		var d event.PhoneThreadData
		if ev.Decode(&d) == nil {
			s.Phone = &d
		}
	case event.ThreadTitle:
		var d event.ThreadTitleData
		if ev.Decode(&d) == nil {
			// Each one replaces the previous; empty fields leave that side
			// unchanged.
			if d.Title != "" {
				if d.Title != s.Title {
					s.Title, s.TitleSeq = d.Title, ev.Seq
					if d.By == "summary" {
						s.TitleRevisions++
					}
				}
				if d.By != "summary" {
					s.TitlePinned = d.Pin
				}
			}
			if d.Description != "" {
				s.Description = d.Description
			}
			if d.By == "summary" {
				if d.SeenSeq > s.SummarySeq {
					s.SummarySeq = d.SeenSeq
				}
				if d.Usage != nil {
					s.account(summaryActor(ev), "", "", *d.Usage, 1, d.ElapsedMS)
				}
			}
		}
	case event.CwdChange:
		// Logs from when actors had shells record every cd; only the
		// session's own folder matters now.
		var d event.CwdChangeData
		if ev.Decode(&d) == nil && d.Path != "" && ev.Task == "" {
			s.Dir = d.Path
			if s.Dir == s.Cwd {
				s.Dir = ""
			}
		}
	case event.Route:
		var d event.RouteData
		_ = ev.Decode(&d)
		if d.Actor != "" {
			if d.Model != "" {
				s.Models[d.Actor] = d.Model
			}
			if d.BaseURL != "" {
				s.Hosts[d.Actor] = d.BaseURL
			}
			if h, m := s.Hosts[d.Actor], s.Models[d.Actor]; h != "" && m != "" {
				s.hostModels[RouteKey{Actor: d.Actor, Host: h}] = m
			}
		}
	case event.ImagesRefused:
		var d event.ImagesRefusedData
		if ev.Decode(&d) == nil && len(d.Paths) > 0 {
			if s.RefusedImages == nil {
				s.RefusedImages = map[string]bool{}
			}
			for _, p := range d.Paths {
				s.RefusedImages[p] = true
			}
		}
	case event.SessionResume:
		var d event.SessionResumeData
		_ = ev.Decode(&d)
		s.Interactive = d.Interactive
		s.Ended = false
		s.EndReason = ""
		s.Resumes++
		s.OpenAtEnd = nil
	case event.SessionEnd:
		var d event.SessionEndData
		_ = ev.Decode(&d)
		s.Ended = true
		s.EndReason = d.Reason
		s.OpenAtEnd = nil
		for who, n := range s.Turns {
			if n > 0 {
				if s.OpenAtEnd == nil {
					s.OpenAtEnd = map[string]int{}
				}
				s.OpenAtEnd[who] = n
			}
		}
		s.Turns = map[string]int{}
	case event.TurnStart:
		s.Turns[ev.Actor]++
		s.TurnSince[ev.Actor] = ev.Time
	case event.TurnEnd:
		if s.Turns[ev.Actor] > 0 {
			s.Turns[ev.Actor]--
		} else if s.OpenAtEnd[ev.Actor] > 0 {
			s.OpenAtEnd[ev.Actor]--
		}
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		if cur := s.Current(); cur != nil && cur.EndSeq == 0 {
			cur.EndSeq = ev.Seq - 1
		}
		// The earlier design's rollover opened a file whose dossier a task
		// wrote later, if at all; only the one loop's open with one.
		restart := d.Reason == "upgrade" || d.Reason == "rollover" && (!s.Legacy() || s.Upgraded())
		s.Subsessions = append(s.Subsessions, Subsession{Index: d.Index, File: d.File, Reason: d.Reason, StartSeq: ev.Seq, Restart: restart})
		// A fresh conversation: the prompt size starts over.
		s.LastUsage = map[string]event.Usage{}
	case event.SubsessionEnd:
		if cur := s.Current(); cur != nil {
			cur.EndSeq = ev.Seq
		}
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		if cur := s.Current(); cur != nil {
			cur.Dossier, cur.Briefed = d.Text, true
			cur.CoversSeq, cur.CoversNone = d.CoversSeq, d.CoversNone && d.CoversSeq == 0
		}
		if d.Usage != nil {
			// A dossier is written in several calls (the lookups, then the
			// text): each counts, and the latency kept is one call's share.
			calls := max(d.Calls, 1)
			s.account(event.ActorEagent, "", "", *d.Usage, calls, d.ElapsedMS/int64(calls))
		}
	case event.UserMessage, event.UserAnswer:
		s.LastUserSeq = ev.Seq
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			if s.Question != nil && (d.QuestionID == "" || s.Question.ID == d.QuestionID) {
				s.Question.Answer = d.Text
				s.Question.Done = true
				s.Question = nil
			}
		}
	case event.UserDismiss:
		// The question is over, with nothing decided.
		var d event.UserDismissData
		_ = ev.Decode(&d)
		if s.Question != nil && (d.QuestionID == "" || s.Question.ID == d.QuestionID) {
			s.Question.Done = true
			s.Question = nil
			s.LastDismissSeq = ev.Seq
		}
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		if ev.Task == "" {
			s.LastUsage[ev.Actor] = d.Usage
		}
		s.account(ev.Actor, d.Host, d.Asked, d.Usage, 1, d.ElapsedMS)
	case event.Question:
		var d event.QuestionData
		_ = ev.Decode(&d)
		bump(&s.questionSeq, d.ID, "q")
		s.Question = &Question{ID: d.ID, Text: d.Text, Options: d.Options, Seq: ev.Seq}
	case event.ScheduleCreate:
		var d event.ScheduleCreateData
		_ = ev.Decode(&d)
		bump(&s.scheduleSeq, d.ID, "s")
		s.Schedules[d.ID] = &Schedule{ID: d.ID, Kind: d.Kind, Spec: d.Spec, Note: d.Note, Next: d.Next, Created: ev.Time}
		s.ScheduleOrder = append(s.ScheduleOrder, d.ID)
	case event.ScheduleFire:
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		if sc := s.Schedules[d.ID]; sc != nil {
			sc.Fires++
			sc.Next = d.Next
			if d.Next.IsZero() {
				sc.Cancelled = true
			}
		}
	case event.ScheduleCancel:
		var d event.ScheduleCancelData
		_ = ev.Decode(&d)
		if sc := s.Schedules[d.ID]; sc != nil {
			sc.Cancelled = true
		}
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		s.Errors = append(s.Errors, d.Where+": "+d.Text)
	case event.RelayStart, event.RelayInner, event.RelaySend, event.RelayMoved, event.RelayDelivered, event.RelayState, event.RelayProgress, event.RelayTurn, event.RelayLimit, event.RelayEnd, event.RelayAutopilot:
		s.applyRelay(ev)
	case event.Checks:
		var d event.ChecksData
		if ev.Decode(&d) == nil && d.Count > 0 {
			actor := ev.Actor
			s.account(actor, "", "", d.Usage, d.Count, 0)
			if d.PromptTokens > 0 {
				u := s.LastUsage[actor]
				u.Input = d.PromptTokens
				s.LastUsage[actor] = u
			}
		}
	}
}

// summaryActor is who a running summary's model call is counted under: the
// dedicated summarizer. Logs from when the narrator's route made that call
// wrote the event as the harness.
func summaryActor(ev event.Event) string {
	if ev.Actor == event.ActorSummarizer {
		return event.ActorSummarizer
	}
	return event.LegacyNarrator
}

// DanglingCalls finds an actor's tool calls that never got a result, for
// example because the process died mid-turn.
//
// A result answers a call of the reply it follows, as the conversation is
// rendered (renderActor): a call id alone does not say which call, because
// some providers use the same ids in every response, and an earlier call's
// result would pass for the result of a later call that never got one.
//
// Only the newest reply's calls are listed. A result written now lands
// after that reply and can answer nothing earlier, so a call of an older
// reply that never got one (a log from before results were paired with
// their reply, whose resume took an earlier result with the same id for
// it) stays as it is: listing it would write another result, and wake
// eagent as if cut off mid-turn, on every resume.
func (s *State) DanglingCalls(actor string) []event.ToolCall {
	var open []event.ToolCall // the latest reply's calls still without a result
	for _, ev := range s.Events {
		if ev.Type == event.SubsessionStart {
			// A new conversation file: a result written now lands in it,
			// where the earlier file's calls are not, so it would answer
			// none of them.
			open = nil
			continue
		}
		if ev.Actor != actor || ev.Task != "" {
			continue
		}
		switch ev.Type {
		case event.Assistant:
			var d event.AssistantData
			_ = ev.Decode(&d)
			open = append([]event.ToolCall(nil), d.ToolCalls...)
		case event.ToolResult:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			for i, c := range open {
				if c.ID == d.CallID {
					open = append(open[:i], open[i+1:]...)
					break
				}
			}
		}
	}
	return open
}

// ContextTokens is the size of eagent's last recorded prompt.
func (s *State) ContextTokens() int {
	if u, ok := s.LastUsage[event.ActorEagent]; ok {
		return u.Input
	}
	// A log from before there was one loop: the orchestrator's.
	return s.LastUsage[event.LegacyOrchestrator].Input
}

// Legacy reports whether the log was written by the three-actor design.
func (s *State) Legacy() bool {
	for _, a := range event.LegacyActors {
		if s.Calls[a] > 0 || s.Models[a] != "" {
			return true
		}
	}
	return false
}

// Upgraded reports that a log written by the three-actor design has been
// taken over by the one loop: it continued in a subsession opened from a
// dossier written from the old record. An upgrade stopped before its
// dossier was written has not taken it over.
func (s *State) Upgraded() bool {
	for _, sub := range s.Subsessions {
		if sub.Reason == "upgrade" && sub.Briefed {
			return true
		}
	}
	return false
}

// Unfinished reports that the newest subsession is a restart that was
// stopped before its dossier was written: the record has its start and no
// briefing. Its file is not the conversation yet (Conversation); the
// session finishes the restart before anything else.
func (s *State) Unfinished() bool {
	cur := s.Current()
	return cur != nil && cur.Restart && !cur.Briefed
}

// Conversation is the subsession eagent's conversation is rendered from:
// the newest one, unless restarts were stopped before their dossier was
// written, and then the newest before them. Its events run on through the
// unfinished ones, so nothing recorded since is left out.
func (s *State) Conversation() *Subsession {
	for i := len(s.Subsessions) - 1; i >= 0; i-- {
		if sub := &s.Subsessions[i]; !sub.Restart || sub.Briefed {
			return sub
		}
	}
	return s.Current()
}

// Waiting reports that nothing more is expected until the user writes: no
// agent is starting or in the middle of a turn, and nothing sent to one is
// still on its way. A session with no agent running is waiting too.
func (s *State) Waiting() bool {
	for _, r := range s.ActiveRelays() {
		if r.State == "starting" || r.State == "working" || len(r.Undelivered) > 0 {
			return false
		}
	}
	return true
}

// DismissalStands reports that the user closed the last question without
// answering and nothing has moved since: they have not written, and no agent
// has finished a turn, stopped on a dialog or exited. Asking again would put
// back the card they just closed.
func (s *State) DismissalStands() bool {
	return s.LastDismissSeq > 0 && s.Question == nil && s.LastUserSeq < s.LastDismissSeq && s.lastMoveSeq < s.LastDismissSeq
}

// Summary renders a short status for humans.
func (s *State) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s", s.SessionID)
	if len(s.Subsessions) > 1 {
		fmt.Fprintf(&b, " (%d subsessions)", len(s.Subsessions))
	}
	fmt.Fprintf(&b, "\n")
	for _, r := range s.ActiveRelays() {
		fmt.Fprintf(&b, "%s: %s", r.Who(), r.State)
		if !r.StateSince.IsZero() {
			fmt.Fprintf(&b, " since %s", s.clock(r.StateSince))
		}
		if r.Autopilot != nil {
			fmt.Fprintf(&b, ", autopilot on")
		}
		fmt.Fprintf(&b, " (%s)\n", r.Dir)
	}
	if len(s.ActiveRelays()) == 0 {
		fmt.Fprintf(&b, "no agent is running\n")
	}
	if s.Question != nil {
		fmt.Fprintf(&b, "waiting on your answer to %s: %s\n", s.Question.ID, s.Question.Text)
	}
	if sc := s.ActiveSchedules(); len(sc) > 0 {
		fmt.Fprintf(&b, "timers: %d\n", len(sc))
		for _, x := range sc {
			next := "unknown"
			if !x.Next.IsZero() {
				next = s.clock(x.Next)
			}
			fmt.Fprintf(&b, "  %s %s %q next %s\n", x.ID, x.Kind, x.Spec, next)
		}
	}
	fmt.Fprintf(&b, "context: %s tokens", humanInt(s.ContextTokens()))
	fmt.Fprintf(&b, "\n%s", s.UsageLine())
	if l := s.LatencyLine(); l != "" {
		fmt.Fprintf(&b, "\n%s", l)
	}
	return b.String()
}

// UsageActors lists the actors that made model calls: eagent and the
// summarizer first, then whatever else the log names (the three actors of
// older logs, in their old order).
func (s *State) UsageActors() []string {
	known := append([]string{event.ActorEagent, event.ActorSummarizer}, event.LegacyActors...)
	var out []string
	seen := map[string]bool{}
	for _, a := range known {
		seen[a] = true
		if s.Calls[a] > 0 {
			out = append(out, a)
		}
	}
	var rest []string
	for a, n := range s.Calls {
		if n > 0 && !seen[a] {
			rest = append(rest, a)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// UsageLine renders token totals.
func (s *State) UsageLine() string {
	var parts []string
	for _, a := range s.UsageActors() {
		u := s.Totals[a]
		if u.Input == 0 && u.Output == 0 {
			continue
		}
		pct := 0
		if u.Input > 0 {
			pct = u.Cached * 100 / u.Input
		}
		parts = append(parts, fmt.Sprintf("%s %d calls, %s in (%d%% cached), %s out", a, s.Calls[a], humanInt(u.Input), pct, humanInt(u.Output)))
	}
	if len(parts) == 0 {
		return "usage: none yet"
	}
	return "usage: " + strings.Join(parts, "; ")
}

// Percentile returns the p-th percentile (0-100) of an actor's call latency in ms.
func (s *State) Percentile(actor string, p int) int64 {
	xs := append([]int64{}, s.Latencies[actor]...)
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	idx := (len(xs) - 1) * p / 100
	return xs[idx]
}

// LatencyLine renders per-actor call latency percentiles.
func (s *State) LatencyLine() string {
	var parts []string
	for _, a := range s.UsageActors() {
		if len(s.Latencies[a]) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s p50 %s / p95 %s / max %s", a, fmtMillis(s.Percentile(a, 50)), fmtMillis(s.Percentile(a, 95)), fmtMillis(s.Percentile(a, 100))))
	}
	if len(parts) == 0 {
		return ""
	}
	return "latency: " + strings.Join(parts, "; ")
}

func fmtMillis(ms int64) string {
	switch {
	case ms >= 60_000:
		return fmt.Sprintf("%dm%ds", ms/60_000, (ms%60_000)/1000)
	case ms >= 1000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

func humanInt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.Itoa(n)
}
