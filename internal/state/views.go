package state

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
)

// ViewOptions tune how much of the log each actor sees.
type ViewOptions struct {
	// ObservationChars bounds tool outputs shown to the narrator.
	ObservationChars int
}

// DefaultViewOptions are used when nil is passed.
var DefaultViewOptions = ViewOptions{ObservationChars: 1500}

// QuestionText returns the text of a narrator question by id, with its
// options, searching every subsession; "" when unknown.
func (s *State) QuestionText(id string) string {
	for i := len(s.Events) - 1; i >= 0; i-- {
		ev := s.Events[i]
		if ev.Type != event.Question {
			continue
		}
		var d event.QuestionData
		_ = ev.Decode(&d)
		if d.ID != id {
			continue
		}
		if len(d.Options) > 0 {
			return d.Text + " (options: " + strings.Join(d.Options, " | ") + ")"
		}
		return d.Text
	}
	return ""
}

// currentEvents returns the events of the open subsession.
func (s *State) currentEvents() []event.Event {
	cur := s.Current()
	if cur == nil {
		return s.Events
	}
	// Events are in seq order; find the subsession start.
	for i := len(s.Events) - 1; i >= 0; i-- {
		if s.Events[i].Seq == cur.StartSeq {
			return s.Events[i:]
		}
	}
	return s.Events
}

// OrchestratorView renders the orchestrator's conversation for the current
// subsession. Tool results follow the call that produced them; anything that
// arrived while a model call was in flight is delivered after that call's
// results, matching what the model actually saw.
func (s *State) OrchestratorView() []llm.Message {
	events := s.currentEvents()
	asked := map[string]bool{}
	for _, ev := range events {
		if ev.Type == event.Question {
			var d event.QuestionData
			_ = ev.Decode(&d)
			asked[d.ID] = true
		}
	}
	msgs := renderActor(events, event.ActorOrchestrator, "", func(ev event.Event) string {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			if !asked[d.QuestionID] {
				// The question was asked in an earlier context; restate it so
				// a terse answer stays readable.
				return s.renderUserAnswer(ev, d, s.QuestionText(d.QuestionID))
			}
		}
		if ev.Type == event.TaskEnd {
			var d event.TaskEndData
			_ = ev.Decode(&d)
			if t := s.Tasks[d.ID]; t != nil && t.Kind == "dossier" {
				return "" // delivered once, as the dossier message that opens the subsession
			}
		}
		return s.orchestratorNotification(ev)
	})
	// A subsession's dossier is always the opening message, even if other
	// notifications landed in the file before the dossier task finished.
	for i, ev := range events {
		if ev.Type != event.Dossier || i == 0 {
			continue
		}
		text := s.orchestratorNotification(ev)
		for j, m := range msgs {
			if m.Role == "user" && m.Text == text && j > 0 {
				copy(msgs[1:j+1], msgs[0:j])
				msgs[0] = m
				break
			}
		}
		break
	}
	return msgs
}

// TaskView renders one task's private conversation.
func (s *State) TaskView(taskID string) []llm.Message {
	t := s.Tasks[taskID]
	if t == nil {
		return nil
	}
	// Tasks may span subsessions; use everything from creation on.
	var events []event.Event
	for _, ev := range s.Events {
		if ev.Seq >= t.CreatedSeq {
			events = append(events, ev)
		}
	}
	msgs := []llm.Message{{Role: "user", Text: t.Description}}
	return append(msgs, renderActor(events, event.ActorTask, taskID, func(ev event.Event) string {
		if ev.Task != taskID {
			return ""
		}
		switch ev.Type {
		case event.ProcExit:
			var d event.ProcExitData
			_ = ev.Decode(&d)
			if !d.Notify {
				return ""
			}
			return procExitText(d)
		case event.HarnessMessage:
			var d event.HarnessMessageData
			_ = ev.Decode(&d)
			return d.Text
		case event.Steer:
			if ev.Actor != event.ActorTask {
				return ""
			}
			var d event.SteerData
			_ = ev.Decode(&d)
			return d.Text
		}
		return ""
	})...)
}

// renderActor builds an actor's message list from its assistant events,
// their tool results, and notifications produced by notify.
func renderActor(events []event.Event, actor, task string, notify func(event.Event) string) []llm.Message {
	// Index tool results by call id.
	results := map[string]event.ToolResultData{}
	for _, ev := range events {
		if ev.Type == event.ToolResult && ev.Actor == actor && ev.Task == task {
			var d event.ToolResultData
			_ = ev.Decode(&d)
			results[d.CallID] = d
		}
	}
	// Assistant events with the window of seqs their call spanned.
	type span struct {
		idx      int
		seenSeq  int64
		seq      int64
		deferred []int // indexes of notifications that arrived mid-call
	}
	var spans []*span
	for i, ev := range events {
		if ev.Type == event.Assistant && ev.Actor == actor && ev.Task == task {
			var d event.AssistantData
			_ = ev.Decode(&d)
			spans = append(spans, &span{idx: i, seenSeq: d.SeenSeq, seq: ev.Seq})
		}
	}
	deferredIdx := map[int]bool{}
	for i, ev := range events {
		if ev.Type == event.Assistant || ev.Type == event.ToolResult {
			continue
		}
		for _, sp := range spans {
			if ev.Seq > sp.seenSeq && ev.Seq < sp.seq {
				sp.deferred = append(sp.deferred, i)
				deferredIdx[i] = true
				break
			}
		}
	}
	spanByIdx := map[int]*span{}
	for _, sp := range spans {
		spanByIdx[sp.idx] = sp
	}

	var msgs []llm.Message
	emitNotify := func(ev event.Event) {
		if text := notify(ev); text != "" {
			msgs = append(msgs, llm.Message{Role: "user", Text: text, Images: imagesOf(ev)})
		}
	}
	for i, ev := range events {
		switch {
		case ev.Type == event.Assistant && ev.Actor == actor && ev.Task == task:
			var d event.AssistantData
			_ = ev.Decode(&d)
			msgs = append(msgs, llm.Message{
				Role: "assistant", Text: d.Text, ToolCalls: d.ToolCalls,
				Native: d.Native, NativeProtocol: d.Provider, NativeHost: d.Host,
			})
			if len(d.ToolCalls) > 0 {
				tm := llm.Message{Role: "tool"}
				for _, tc := range d.ToolCalls {
					r, ok := results[tc.ID]
					if !ok {
						r = event.ToolResultData{CallID: tc.ID, Name: tc.Name, Output: "(no result: the session was interrupted before this tool finished)", IsError: true}
					}
					tm.Results = append(tm.Results, llm.ToolResult{CallID: r.CallID, Name: r.Name, Output: r.Output, IsError: r.IsError})
				}
				msgs = append(msgs, tm)
				var pics []llm.Image
				var names []string
				for _, tc := range d.ToolCalls {
					for _, a := range results[tc.ID].Images {
						if a.IsImage() {
							pics = append(pics, llm.Image{Path: a.Path, MediaType: a.ContentType})
							names = append(names, a.Name)
						}
					}
				}
				if len(pics) > 0 {
					msgs = append(msgs, llm.Message{Role: "user", Text: "[image from view_image: " + strings.Join(names, ", ") + "]", Images: pics})
				}
			}
			if sp := spanByIdx[i]; sp != nil {
				for _, j := range sp.deferred {
					emitNotify(events[j])
				}
			}
		case ev.Type == event.ToolResult:
			// Rendered with its call.
		case deferredIdx[i]:
			// Rendered after the call it interrupted.
		default:
			emitNotify(ev)
		}
	}
	return msgs
}

// defaultPrompts is the built-in set used when no project set was installed
// via SetPrompts, so output is byte-identical to the default templates.
var (
	defaultPromptsOnce sync.Once
	defaultPrompts     *prompts.Set
)

func builtinPrompts() *prompts.Set {
	defaultPromptsOnce.Do(func() {
		s, err := prompts.Load("")
		if err != nil {
			panic("built-in prompts: " + err.Error())
		}
		defaultPrompts = s
	})
	return defaultPrompts
}

func (s *State) promptSet() *prompts.Set {
	if s.prompts != nil {
		return s.prompts
	}
	return builtinPrompts()
}

// userNick derives the IRC nick for a user event from its source:
// "" (terminal) -> user, web -> user@web, finalechat -> user@phone,
// anything else -> user@<source>.
func userNick(source string) string {
	switch source {
	case "":
		return "user"
	case "web":
		return "user@web"
	case "finalechat":
		return "user@phone"
	default:
		return "user@" + source
	}
}

// renderUserMessage renders a user.message event through USER_MESSAGE.md.
// The timestamp comes only from ev.Time (persisted UTC at store.Append),
// never time.Now at render, so replays are byte-identical.
func (s *State) renderUserMessage(ev event.Event, d event.UserMessageData) string {
	return s.promptSet().Render("USER_MESSAGE.md", prompts.UserMessageData{
		Text:        d.Text,
		Time:        ev.Time,
		Timestamp:   ev.Time.Local().Format("2006-01-02 15:04:05"),
		Source:      d.Source,
		Nick:        userNick(d.Source),
		Attachments: attachmentNotes(d.Attachments),
		Client:      clientSource(d.Client),
		ClientLine:  d.Client.Compact(),
	})
}

// renderUserAnswer renders a user.answer event through USER_ANSWER.md.
// question is the restatement ("" when asked in this subsession).
func (s *State) renderUserAnswer(ev event.Event, d event.UserAnswerData, question string) string {
	return s.promptSet().Render("USER_ANSWER.md", prompts.UserAnswerData{
		Text:        d.Text,
		Time:        ev.Time,
		Timestamp:   ev.Time.Local().Format("2006-01-02 15:04:05"),
		Source:      d.Source,
		Nick:        userNick(d.Source),
		Attachments: attachmentNotes(d.Attachments),
		QuestionID:  d.QuestionID,
		Question:    question,
		Client:      clientSource(d.Client),
		ClientLine:  d.Client.Compact(),
	})
}

// DismissNotice is what the orchestrator reads when the user closed a
// question without answering. It arrives with whatever wakes the
// orchestrator next; the dismissal itself wakes nobody.
func DismissNotice(questionID string) string {
	return fmt.Sprintf("[The user dismissed question %s without answering. That is not an answer and not a go-ahead: nothing was decided. Do not pick an option for them, do not carry on with what the question was holding up, and do not ask it again. Go by what they write next.]", questionID)
}

// clientSource is the capsule's source ("" when the client sent none), so
// templates can name the client without touching the event's Source.
func clientSource(c *clientcaps.Caps) string {
	if c == nil {
		return ""
	}
	return c.Source
}

// orchestratorNotification renders events the orchestrator must hear about.
func (s *State) orchestratorNotification(ev event.Event) string {
	switch ev.Type {
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		return "[Dossier: your previous context filled up. The task worker prepared this briefing from the full session log. Read it, then continue the work. Use session_read to see any cited lines in full.]\n\n" + d.Text
	case event.Steer:
		if ev.Actor != event.ActorOrchestrator || ev.Task != "" {
			return ""
		}
		var d event.SteerData
		_ = ev.Decode(&d)
		return d.Text
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		return s.renderUserMessage(ev, d)
	case event.Question:
		// The answer that follows names this question; without it a terse
		// option like "Yes" would be unreadable.
		var d event.QuestionData
		_ = ev.Decode(&d)
		s := fmt.Sprintf("[The narrator asked the user question %s]\n%s", d.ID, d.Text)
		if len(d.Options) > 0 {
			s += "\nOptions: " + strings.Join(d.Options, " | ")
		}
		return s
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		return s.renderUserAnswer(ev, d, "")
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		return DismissNotice(d.QuestionID)
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[Task %s %s]\n%s", d.ID, d.Status, strings.TrimSpace(d.Summary))
	case event.ScheduleFire:
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		next := "no further runs"
		if !d.Next.IsZero() {
			next = "next at " + d.Next.Local().Format("15:04:05")
		}
		return fmt.Sprintf("[Schedule %s fired at %s; %s]\n%s", d.ID, ev.Time.Local().Format("15:04:05"), next, d.Note)
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		if !d.Notify || ev.Actor != event.ActorOrchestrator || ev.Task != "" {
			return ""
		}
		return procExitText(d)
	case event.HarnessMessage:
		if ev.Actor != event.ActorOrchestrator || ev.Task != "" {
			return ""
		}
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		return d.Text
	case event.RelayTurn, event.RelayState, event.RelayDelivered, event.RelayLimit:
		return s.relayNotification(ev)
	}
	return ""
}

func procExitText(d event.ProcExitData) string {
	head := fmt.Sprintf("[Process %s %s with exit code %d after %s]", d.Handle, d.Reason, d.ExitCode, fmtMillis(d.DurationMS))
	if strings.TrimSpace(d.Tail) != "" {
		return head + "\nOutput tail:\n" + d.Tail
	}
	return head
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

// NarratorView renders the narrator's conversation: its own turns plus an
// observation stream of what the orchestrator did, batched between turns.
// Batches are frozen once a narrator turn closes them, so the prompt prefix
// is stable across wakes.
func (s *State) NarratorView(opts *ViewOptions) []llm.Message {
	if opts == nil {
		opts = &DefaultViewOptions
	}
	events := s.currentEvents()
	// A trimmed view starts later: what came before is replaced by a recap
	// of what the narrator told the user. The cut moves rarely, so the
	// prompt prefix stays stable between moves.
	recap := ""
	phoneRemote := false
	if t := s.NarratorTrim; t != nil && len(events) > 0 && t.FromSeq >= events[0].Seq {
		cut := sort.Search(len(events), func(i int) bool { return events[i].Seq > t.FromSeq })
		for _, ev := range events[:cut] {
			if ev.Type == event.PhoneThread {
				var d event.PhoneThreadData
				if ev.Decode(&d) == nil {
					phoneRemote = d.RemoteMode
				}
			}
		}
		events, recap = events[cut:], t.Recap
	}
	results := map[string]event.ToolResultData{}
	for _, ev := range events {
		if ev.Type == event.ToolResult && ev.Actor == event.ActorNarrator {
			var d event.ToolResultData
			_ = ev.Decode(&d)
			results[d.CallID] = d
		}
	}
	// Narrator turns and their seen boundaries.
	type turn struct {
		idx     int
		seenSeq int64
	}
	var turns []turn
	for i, ev := range events {
		if ev.Type == event.Assistant && ev.Actor == event.ActorNarrator {
			var d event.AssistantData
			_ = ev.Decode(&d)
			turns = append(turns, turn{idx: i, seenSeq: d.SeenSeq})
		}
	}
	var msgs []llm.Message
	var batch []string
	if recap != "" {
		batch = append(batch, recap)
	}
	flush := func() {
		if len(batch) == 0 {
			return
		}
		msgs = append(msgs, llm.Message{Role: "user", Text: strings.Join(batch, "\n\n")})
		batch = nil
	}
	ti := 0
	// Observations whose seq is beyond the current turn's seenSeq are held
	// for the batch after that turn.
	var held []string
	for i, ev := range events {
		if ti < len(turns) && i == turns[ti].idx {
			flush()
			var d event.AssistantData
			_ = ev.Decode(&d)
			msgs = append(msgs, llm.Message{Role: "assistant", Text: d.Text, ToolCalls: d.ToolCalls, Native: d.Native, NativeProtocol: d.Provider, NativeHost: d.Host})
			if len(d.ToolCalls) > 0 {
				tm := llm.Message{Role: "tool"}
				for _, tc := range d.ToolCalls {
					r, ok := results[tc.ID]
					if !ok {
						r = event.ToolResultData{CallID: tc.ID, Name: tc.Name, Output: "(interrupted)", IsError: true}
					}
					tm.Results = append(tm.Results, llm.ToolResult{CallID: r.CallID, Name: r.Name, Output: r.Output, IsError: r.IsError})
				}
				msgs = append(msgs, tm)
			}
			batch = append(batch, held...)
			held = nil
			ti++
			continue
		}
		if ev.Type == event.Steer && ev.Actor == event.ActorNarrator {
			var d event.SteerData
			_ = ev.Decode(&d)
			batch = append(batch, d.Text)
			continue
		}
		if ev.Actor == event.ActorNarrator {
			continue // its own tool results, rendered above
		}
		var obs string
		if ev.Type == event.PhoneThread {
			var d event.PhoneThreadData
			if ev.Decode(&d) == nil {
				ts := ev.Time.Local().Format("15:04:05")
				if d.RemoteMode && !phoneRemote {
					phoneRemote = true
					obs = fmt.Sprintf("%s phone remote mode is on (the user is away from the terminal)", ts)
				} else if !d.RemoteMode && phoneRemote {
					phoneRemote = false
					obs = fmt.Sprintf("%s phone remote mode is off (the user is back at the terminal)", ts)
				}
			}
		} else {
			obs = Observe(ev, opts.ObservationChars)
		}
		if obs == "" {
			continue
		}
		if ti < len(turns) && ev.Seq > turns[ti].seenSeq {
			held = append(held, obs)
		} else {
			batch = append(batch, obs)
		}
	}
	batch = append(batch, held...)
	flush()
	return msgs
}

// WithSteer adds a steer to the end of a narrator view exactly as a recorded
// steer event renders there: joined to the open batch of observations, or as
// a batch of its own after a narrator turn. A check that ends in silence can
// then leave the log untouched, and one that speaks records the same steer,
// so the next prompt still extends this one.
func WithSteer(msgs []llm.Message, steer string) []llm.Message {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "user" {
		last := msgs[n-1]
		last.Text += "\n\n" + steer
		return append(msgs[:n-1:n-1], last)
	}
	return append(msgs, llm.Message{Role: "user", Text: steer})
}

// NarratorTrimPoint proposes where a shortened narrator view should start
// so that about keepChars of its newest content stay: the seq to cut at, and
// a recap of what the narrator told the user up to there. ok is false when
// the view is already that short.
func (s *State) NarratorTrimPoint(keepChars int) (from int64, recap string, ok bool) {
	events := s.currentEvents()
	if t := s.NarratorTrim; t != nil && len(events) > 0 && t.FromSeq >= events[0].Seq {
		events = events[sort.Search(len(events), func(i int) bool { return events[i].Seq > t.FromSeq }):]
	}
	size := func(ev event.Event) int {
		if ev.Actor != event.ActorNarrator {
			return len(Observe(ev, DefaultViewOptions.ObservationChars))
		}
		switch ev.Type {
		case event.Steer:
			var d event.SteerData
			_ = ev.Decode(&d)
			return len(d.Text)
		case event.Assistant:
			var d event.AssistantData
			_ = ev.Decode(&d)
			n := len(d.Text)
			for _, tc := range d.ToolCalls {
				n += len(tc.Args) + len(tc.Name)
			}
			return n
		case event.ToolResult:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			return len(d.Output)
		}
		return 0
	}
	total, cut := 0, -1
	for i := len(events) - 1; i >= 0; i-- {
		if total += size(events[i]); total > keepChars {
			cut = i
			break
		}
	}
	if cut < 0 {
		return 0, "", false
	}
	from = events[cut].Seq
	var said []string
	var opening string
	for _, ev := range s.currentEvents() {
		if ev.Seq > from {
			break
		}
		if opening == "" && !s.RelayMode() && ev.Type == event.UserMessage {
			var d event.UserMessageData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				opening = clip(oneLine(d.Text), 400)
			}
		}
		if ev.Type == event.Message {
			var d event.MessageData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				said = append(said, fmt.Sprintf("- %s %s", ev.Time.Local().Format("15:04"), clip(oneLine(d.Text), 400)))
			}
		}
	}
	if len(said) > 5 {
		said = said[len(said)-5:]
	}
	recap = fmt.Sprintf("[harness] To keep your view short, what happened before %s is no longer shown.", events[cut].Time.Local().Format("15:04:05"))
	if opening != "" {
		recap += " Original user request: " + opening + "."
	}
	if len(said) > 0 {
		recap += " Up to then you had told the user, newest last:\n" + strings.Join(said, "\n")
	}
	return from, recap, true
}

// Observe renders one event as the narrator sees it, or "" if it is not
// something the narrator should know about.
func Observe(ev event.Event, maxChars int) string {
	ts := ev.Time.Local().Format("15:04:05")
	switch ev.Type {
	case event.SessionStart:
		return fmt.Sprintf("%s session started", ts)
	case event.SessionResume:
		var d event.SessionResumeData
		_ = ev.Decode(&d)
		if len(d.Closed) > 0 {
			return fmt.Sprintf("%s session resumed after an interruption; %s", ts, strings.Join(d.Closed, "; "))
		}
		return fmt.Sprintf("%s session resumed", ts)
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s [fresh context] The orchestrator restarted with a fresh context; its working notes, carried over from the log:\n%s", ts, clip(d.Text, maxChars*3))
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s USER: %s%s", ts, d.Text, attachmentBrief(d.Attachments))
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s USER answered %s: %s%s", ts, d.QuestionID, d.Text, attachmentBrief(d.Attachments))
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s USER dismissed %s without answering: nothing was decided. Do not ask it again; they will write when they are ready.", ts, d.QuestionID)
	case event.Assistant:
		if ev.Actor != event.ActorOrchestrator {
			return "" // task-worker internals are not the narrator's business
		}
		var d event.AssistantData
		_ = ev.Decode(&d)
		var b strings.Builder
		fmt.Fprintf(&b, "%s orchestrator:", ts)
		if strings.TrimSpace(d.Text) != "" {
			fmt.Fprintf(&b, " %s", clip(strings.TrimSpace(d.Text), maxChars))
		}
		for _, tc := range d.ToolCalls {
			fmt.Fprintf(&b, "\n  -> %s %s", tc.Name, clip(argsPreview(tc), 300))
		}
		return b.String()
	case event.ToolResult:
		if ev.Actor != event.ActorOrchestrator {
			return ""
		}
		var d event.ToolResultData
		_ = ev.Decode(&d)
		switch d.Name {
		case "note", "yield", "delegate", "schedule", "cancel_schedule", "cancel_task":
			return "" // covered by their own events
		}
		label := "result"
		limit := maxChars
		if d.IsError {
			label = "error"
		} else {
			switch d.Name {
			case "bash", "bash_poll", "read_file", "list_dir", "session_read", "session_search":
				limit = min(maxChars, 500)
			}
		}
		return fmt.Sprintf("%s   %s %s: %s", ts, d.Name, label, clip(d.Output, limit))
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		if d.Answer {
			return fmt.Sprintf("%s ANSWER FROM THE ORCHESTRATOR TO THE USER'S QUESTION (relay it in your voice, faithfully, adding nothing it did not say): %s", ts, d.Text)
		}
		return fmt.Sprintf("%s NOTE FROM ORCHESTRATOR (for you to consider relaying): %s", ts, d.Text)
	case event.CwdChange:
		if ev.Actor != event.ActorOrchestrator || ev.Task != "" {
			return ""
		}
		var d event.CwdChangeData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s working directory is now %s (a cd that stuck; later commands start there)", ts, d.Path)
	case event.TaskCreate:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		if d.Kind == "dossier" {
			return fmt.Sprintf("%s the orchestrator's context is being reset; a worker is writing up the working notes it will restart from (%s)", ts, d.ID)
		}
		return fmt.Sprintf("%s task %s delegated: %s\n  %s", ts, d.ID, d.Title, clip(d.Description, maxChars))
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s task %s %s: %s", ts, d.ID, d.Status, clip(strings.TrimSpace(d.Summary), maxChars))
	case event.ScheduleCreate:
		var d event.ScheduleCreateData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s schedule %s created (%s %q): %s", ts, d.ID, d.Kind, d.Spec, d.Note)
	case event.ScheduleFire:
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s schedule %s fired: %s", ts, d.ID, d.Note)
	case event.ScheduleCancel:
		var d event.ScheduleCancelData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s schedule %s cancelled", ts, d.ID)
	case event.ProcStart:
		if ev.Actor != event.ActorOrchestrator {
			return ""
		}
		var d event.ProcStartData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s   started %s", ts, d.Handle)
	case event.ProcExit:
		if ev.Actor != event.ActorOrchestrator {
			return ""
		}
		var d event.ProcExitData
		_ = ev.Decode(&d)
		if !d.Notify {
			return ""
		}
		return fmt.Sprintf("%s   process %s %s (exit %d)", ts, d.Handle, d.Reason, d.ExitCode)
	case event.Yield:
		var d event.YieldData
		_ = ev.Decode(&d)
		if d.Done {
			return fmt.Sprintf("%s ORCHESTRATOR DECLARED THE WORK DONE: %s", ts, d.Reason)
		}
		return fmt.Sprintf("%s orchestrator is idle and waiting: %s", ts, d.Reason)
	case event.HarnessMessage:
		if ev.Actor != event.ActorNarrator {
			return ""
		}
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s HARNESS: %s", ts, d.Text)
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s ERROR (%s): %s", ts, d.Where, clip(d.Text, maxChars))
	case event.Route:
		var d event.RouteData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s %s now using %s (%s)", ts, d.Actor, d.Model, d.Reason)
	case event.PhoneThread:
		var d event.PhoneThreadData
		_ = ev.Decode(&d)
		if d.RemoteMode {
			return fmt.Sprintf("%s phone remote mode is on (the user is away from the terminal)", ts)
		}
		return ""
	case event.RelayStart, event.RelaySend, event.RelayDelivered, event.RelayState, event.RelayProgress, event.RelayTurn, event.RelayLimit, event.RelayEnd, event.RelayAutopilot:
		return observeRelay(ev, maxChars, ts)
	}
	return ""
}

func argsPreview(tc event.ToolCall) string {
	obj, err := llm.ArgsObject(tc.Args)
	if err != nil {
		return "(malformed arguments)"
	}
	for _, key := range []string{"command", "description", "text", "path", "title", "reason"} {
		if v, ok := obj[key].(string); ok {
			return oneLine(v)
		}
	}
	return oneLine(string(tc.Args))
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	for head > 0 && (s[head]&0xC0) == 0x80 {
		head--
	}
	ts := len(s) - tail
	for ts < len(s) && (s[ts]&0xC0) == 0x80 {
		ts++
	}
	return s[:head] + fmt.Sprintf(" [... %d chars ...] ", ts-head) + s[ts:]
}

// attachmentNotes tells the orchestrator where the user's files landed and
// what they are, so it can open them (images are also shown to it directly
// when its model accepts pictures).
func attachmentNotes(atts []event.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[The user attached ")
	if len(atts) == 1 {
		b.WriteString("a file")
	} else {
		fmt.Fprintf(&b, "%d files", len(atts))
	}
	b.WriteString(", saved locally:")
	for _, a := range atts {
		fmt.Fprintf(&b, "\n- %s (%s, %s", a.Name, a.ContentType, humanSize(a.Size))
		if a.Width > 0 && a.Height > 0 {
			fmt.Fprintf(&b, ", %dx%d", a.Width, a.Height)
		}
		fmt.Fprintf(&b, ") at %s", a.Path)
	}
	b.WriteString("\nText files can be read with read_file; other files with a shell command.]")
	return b.String()
}

// attachmentBrief is the narrator's one-line view of what was attached.
func attachmentBrief(atts []event.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var names []string
	for _, a := range atts {
		names = append(names, a.Name)
	}
	return fmt.Sprintf(" [attached: %s]", strings.Join(names, ", "))
}

// imagesOf lists the pictures on a user event for models that can look.
func imagesOf(ev event.Event) []llm.Image {
	var atts []event.Attachment
	switch ev.Type {
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		atts = d.Attachments
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		atts = d.Attachments
	}
	var out []llm.Image
	for _, a := range atts {
		// Only formats every vision model accepts are inlined; a HEIC photo
		// from a phone stays a file the note mentions, so one undecodable
		// picture cannot get the model's pictures switched off.
		switch a.ContentType {
		case "", "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			continue
		}
		if a.IsImage() {
			out = append(out, llm.Image{Path: a.Path, MediaType: a.ContentType})
		}
	}
	return out
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// OrchestratorEdits counts the files the orchestrator wrote or edited
// itself in the current context. Implementation is the task worker's job;
// the steer nudges an orchestrator that keeps doing it.
func (s *State) OrchestratorEdits() int {
	n := 0
	for _, ev := range s.currentEvents() {
		if ev.Type != event.Assistant || ev.Actor != event.ActorOrchestrator || ev.Task != "" {
			continue
		}
		var d event.AssistantData
		_ = ev.Decode(&d)
		for _, tc := range d.ToolCalls {
			switch tc.Name {
			case "write_file", "edit_file":
				n++
			case "bash":
				// Writing a file through the shell counts too: heredocs, cat >, tee.
				var a struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(tc.Args, &a) == nil && writesFile(a.Command) {
					n++
				}
			}
		}
	}
	return n
}

// writesFile reports whether a shell command is really a file write.
func writesFile(cmd string) bool {
	return strings.Contains(cmd, "<<") || strings.Contains(cmd, "cat >") || strings.Contains(cmd, "cat>") || strings.Contains(cmd, " tee ") || strings.HasPrefix(strings.TrimSpace(cmd), "tee ")
}
