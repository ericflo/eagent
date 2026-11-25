package state

import (
	"cmp"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
)

// ViewOptions bound how much of the long texts eagent's conversation
// carries. They must not change while a subsession is open: the conversation
// is rendered again from the log for every model call, and a different bound
// would change text the provider has already cached.
type ViewOptions struct {
	// RemarkChars bounds something an agent said in the middle of a turn.
	RemarkChars int
	// ReportChars bounds an agent's report at the end of a turn. The user
	// is always sent the whole report.
	ReportChars int
}

// DefaultViewOptions are used when nil is passed.
var DefaultViewOptions = ViewOptions{RemarkChars: 1500, ReportChars: 20000}

// QuestionText returns the text of a question by id, with its options,
// searching every subsession; "" when unknown.
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

// viewEvents returns the events eagent's conversation is rendered from, and
// the briefing that opens it (nil in a session's first subsession).
//
// A subsession that opened with a briefing shows everything after the last
// event the briefing took in, including events that were written to the
// previous file while the briefing was being composed. Any other subsession
// shows its own file. A restart stopped before its briefing was written is
// not the conversation yet: the one before it is, with everything since.
func (s *State) viewEvents() ([]event.Event, *event.Event) {
	cur := s.Conversation()
	if cur == nil {
		return s.Events, nil
	}
	start := 0
	for i := len(s.Events) - 1; i >= 0; i-- {
		if s.Events[i].Seq == cur.StartSeq {
			start = i
			break
		}
	}
	var dossier *event.Event
	for i := start; i < len(s.Events); i++ {
		if s.Events[i].Type == event.Dossier {
			dossier = &s.Events[i]
			break
		}
	}
	if dossier == nil || (cur.CoversSeq <= 0 && !cur.CoversNone) {
		// No briefing, or one that does not say what it took in (an older
		// log): the subsession shows its own file. A briefing that took in
		// nothing at all (CoversNone) is followed by the whole record.
		return s.Events[start:], dossier
	}
	from := start
	for from > 0 && s.Events[from-1].Seq > cur.CoversSeq {
		from--
	}
	if from == start {
		return s.Events[start:], dossier
	}
	// What the user and the agents put in the previous file after the last
	// event the briefing took in is carried over whole. eagent's own calls
	// there are not: they belong to the conversation the briefing replaced
	// and was written in, so it accounts for them.
	carried := make([]event.Event, 0, len(s.Events)-from)
	for _, ev := range s.Events[from:start] {
		if ev.Actor == event.ActorEagent && ev.Task == "" {
			switch ev.Type {
			case event.Steer, event.Assistant, event.ToolResult:
				continue
			}
		}
		carried = append(carried, ev)
	}
	return append(carried, s.Events[start:]...), dossier
}

// View renders eagent's conversation: the session's record since its last
// briefing, in order. What the user wrote, what the agents said and did, and
// notices from the harness are user messages; eagent's own replies and their
// tool results sit where they happened. Anything that arrived while one of
// its model calls was in flight comes after that call's results, which is
// when the model first saw it.
//
// The rendering depends only on the log, so the conversation for one call is
// a strict extension of the conversation for the call before it.
func (s *State) View(opts *ViewOptions) []llm.Message {
	if opts == nil {
		opts = &DefaultViewOptions
	}
	events, dossier := s.viewEvents()
	asked := map[string]bool{}
	remote := s.remoteChanges(events)
	for _, ev := range events {
		if ev.Type == event.Question {
			var d event.QuestionData
			_ = ev.Decode(&d)
			asked[d.ID] = true
		}
	}
	var msgs []llm.Message
	if dossier != nil {
		msgs = append(msgs, llm.Message{Role: "user", Text: s.notice(*dossier, *opts)})
	}
	msgs = append(msgs, renderActor(events, event.ActorEagent, func(ev event.Event) string {
		switch ev.Type {
		case event.Dossier:
			return "" // the opening message, above
		case event.PhoneThread:
			return remote[ev.Seq]
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			if !asked[d.QuestionID] {
				// The question was asked before the briefing; restate it so a
				// terse answer stays readable.
				return s.renderUserAnswer(ev, d, s.QuestionText(d.QuestionID))
			}
		}
		return s.notice(ev, *opts)
	})...)
	// A picture a provider refused is marked, so every request from here
	// on carries the same note in its place.
	for i := range msgs {
		for j := range msgs[i].Images {
			if s.RefusedImages[msgs[i].Images[j].Path] {
				msgs[i].Images[j].Refused = true
			}
		}
	}
	return msgs
}

// remoteChanges finds the moments the user said they went away from, or
// came back to, their terminal, by event seq. The mirror re-records its
// thread for other reasons too; only a change is worth a line.
func (s *State) remoteChanges(events []event.Event) map[int64]string {
	out := map[int64]string{}
	away := false
	if len(events) > 0 {
		for _, ev := range s.Events {
			if ev.Seq >= events[0].Seq {
				break
			}
			if ev.Type == event.PhoneThread {
				var d event.PhoneThreadData
				if ev.Decode(&d) == nil {
					away = d.RemoteMode
				}
			}
		}
	}
	for _, ev := range events {
		if ev.Type != event.PhoneThread {
			continue
		}
		var d event.PhoneThreadData
		if ev.Decode(&d) != nil || d.RemoteMode == away {
			continue
		}
		away = d.RemoteMode
		if away {
			out[ev.Seq] = fmt.Sprintf("[%s The user is away from the terminal and reading on their phone.]", s.clock(ev.Time))
		} else {
			out[ev.Seq] = fmt.Sprintf("[%s The user is back at the terminal.]", s.clock(ev.Time))
		}
	}
	return out
}

// renderActor builds an actor's message list from its assistant events,
// their tool results, and notifications produced by notify.
func renderActor(events []event.Event, actor string, notify func(event.Event) string) []llm.Message {
	mine := func(ev event.Event) bool { return ev.Actor == actor && ev.Task == "" }
	// A tool result answers a call of the reply it follows in the log: the
	// results between one reply and the next are that reply's, in order. A
	// call id alone does not say which call a result answers, because some
	// providers number their calls from 0 in every response, and matching
	// by id would put the newest result under every earlier call with that
	// id and rewrite a conversation that was already sent.
	results := map[int]map[string][]event.ToolResultData{}
	reply := -1
	for i, ev := range events {
		switch {
		case ev.Type == event.Assistant && mine(ev):
			reply = i
		case ev.Type == event.ToolResult && mine(ev) && reply >= 0:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			if results[reply] == nil {
				results[reply] = map[string][]event.ToolResultData{}
			}
			results[reply][d.CallID] = append(results[reply][d.CallID], d)
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
		if ev.Type == event.Assistant && mine(ev) {
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
		// The steer is the end of the prompt the call was given. It is
		// written when the call comes back, but the model saw it first.
		if ev.Type == event.Steer && mine(ev) {
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
		case ev.Type == event.Assistant && mine(ev):
			var d event.AssistantData
			_ = ev.Decode(&d)
			msgs = append(msgs, llm.Message{
				Role: "assistant", Text: d.Text, ToolCalls: d.ToolCalls,
				Native: d.Native, NativeProtocol: d.Provider, NativeHost: d.Host, NativeModel: cmp.Or(d.Asked, d.Model),
			})
			if len(d.ToolCalls) > 0 {
				tm := llm.Message{Role: "tool"}
				// Pictures a call brought back follow its result. They are
				// read from the copy kept in the session, so the message is
				// the same bytes every time it is rendered.
				var pics []llm.Image
				var names []string
				// Two calls of one reply with the same id take its results
				// in the order they were recorded.
				took := map[string]int{}
				for _, tc := range d.ToolCalls {
					r := event.ToolResultData{CallID: tc.ID, Name: tc.Name, Output: "(no result: the session was interrupted before this finished)", IsError: true}
					if got := results[i][tc.ID]; took[tc.ID] < len(got) {
						r = got[took[tc.ID]]
						took[tc.ID]++
					}
					tm.Results = append(tm.Results, llm.ToolResult{CallID: r.CallID, Name: r.Name, Output: r.Output, IsError: r.IsError})
					for _, a := range r.Images {
						if a.IsImage() {
							pics = append(pics, llm.Image{Path: a.Path, MediaType: a.ContentType})
							names = append(names, a.Name)
						}
					}
				}
				msgs = append(msgs, tm)
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

// WithTail adds the end of a prompt to a conversation exactly as a recorded
// steer renders there: as one more user message. A look that ends in silence
// can then leave the log untouched, and one that acts records the same text
// as a steer, so the next prompt still extends this one.
func WithTail(msgs []llm.Message, tail string) []llm.Message {
	return append(msgs[:len(msgs):len(msgs)], llm.Message{Role: "user", Text: tail})
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

// stamp is a full date and time in the session's zone. It comes only from
// the event's own time, never from a clock at render, so the same event
// always renders the same text.
func (s *State) stamp(t time.Time) string {
	return t.In(s.Location()).Format("2006-01-02 15:04:05")
}

// renderUserMessage renders a user.message event through USER_MESSAGE.md,
// with a line saying what became of it when it was not passed on.
func (s *State) renderUserMessage(ev event.Event, d event.UserMessageData) string {
	text := strings.TrimRight(s.promptSet().Render("USER_MESSAGE.md", prompts.UserMessageData{
		Text:        d.Text,
		Time:        ev.Time,
		Timestamp:   s.stamp(ev.Time),
		Source:      d.Source,
		Nick:        userNick(d.Source),
		Attachments: attachmentNotes(d.Attachments),
		Client:      clientSource(d.Client),
		ClientLine:  d.Client.Compact(),
	}), "\n")
	if d.Held != "" {
		text += "\n[Not passed to the agent: " + d.Held + ".]"
	}
	return text
}

// renderUserAnswer renders a user.answer event through USER_ANSWER.md.
// question is the restatement ("" when asked in this conversation).
func (s *State) renderUserAnswer(ev event.Event, d event.UserAnswerData, question string) string {
	return strings.TrimRight(s.promptSet().Render("USER_ANSWER.md", prompts.UserAnswerData{
		Text:        d.Text,
		Time:        ev.Time,
		Timestamp:   s.stamp(ev.Time),
		Source:      d.Source,
		Nick:        userNick(d.Source),
		Attachments: attachmentNotes(d.Attachments),
		QuestionID:  d.QuestionID,
		Question:    question,
		Client:      clientSource(d.Client),
		ClientLine:  d.Client.Compact(),
	}), "\n")
}

// DismissNotice is what eagent reads when the user closed a question without
// answering. It arrives with whatever wakes eagent next; the dismissal itself
// wakes nobody.
func DismissNotice(questionID string) string {
	return fmt.Sprintf("[The user dismissed question %s without answering. That is not an answer and not a go-ahead: nothing was decided. Do not pick an option for them, do not send the agent anything on the strength of it, and do not ask it again. Go by what they write next.]", questionID)
}

// clientSource is the capsule's source ("" when the client sent none), so
// templates can name the client without touching the event's Source.
func clientSource(c *clientcaps.Caps) string {
	if c == nil {
		return ""
	}
	return c.Source
}

// notice renders one event as a line of eagent's conversation, or "" when
// the event is not something eagent reads (its own replies and tool results
// are placed by renderActor).
func (s *State) notice(ev event.Event, opts ViewOptions) string {
	switch ev.Type {
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		if d.NoMap {
			// Not passed off as a map: see dossier.go.
			return "[Briefing. Your conversation grew long and was restarted here. What you wrote before the restart says where the session stands, but it has no usable MAP of the record: its first lines say why. After it come the facts eagent keeps itself, which give the place in the record of every message from the user and every turn the agent finished. Carry on as if nothing had happened. The briefing is a summary and the record is the source: when the detail matters, read those places with session_read (file:line) and find the rest with session_search.]\n\n" + d.Text
		}
		return "[Briefing. Your conversation grew long and was restarted here. This is what you wrote before the restart: where the session stands, and a MAP of where in the record each topic can be read, followed by the facts eagent keeps itself. Carry on as if nothing had happened. The briefing is a summary and the record is the source: when the detail matters, or the user comes back to something it only touches, read the places it cites with session_read (file:line) and find the rest with session_search.]\n\n" + d.Text
	case event.Steer:
		if ev.Actor != event.ActorEagent {
			return ""
		}
		var d event.SteerData
		_ = ev.Decode(&d)
		return d.Text
	case event.HarnessMessage:
		if ev.Actor != event.ActorEagent {
			return ""
		}
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		return d.Text
	case event.ScheduleFire:
		if ev.Actor != event.ActorHarness {
			return "" // a timer of the three-actor design's orchestrator
		}
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		next := "it does not come again"
		if !d.Next.IsZero() {
			next = "next at " + s.clock(d.Next)
		}
		return fmt.Sprintf("[%s Timer %s came due; %s. The note you left yourself:]\n%s", s.clock(ev.Time), d.ID, next, d.Note)
	case event.Message:
		// A reply of eagent's own carries what it said. Only words the
		// harness said for it need a line.
		var d event.MessageData
		_ = ev.Decode(&d)
		if !d.Auto {
			return ""
		}
		return fmt.Sprintf("[%s eagent told the user this itself, without asking you:]\n%s", s.clock(ev.Time), clip(d.Text, opts.RemarkChars))
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		return s.renderUserMessage(ev, d)
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		return s.renderUserAnswer(ev, d, "")
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		return DismissNotice(d.QuestionID)
	case event.SessionResume:
		var d event.SessionResumeData
		_ = ev.Decode(&d)
		if len(d.Closed) > 0 {
			return fmt.Sprintf("[%s eagent was restarted and picked the session up again; %s]", s.clock(ev.Time), strings.Join(d.Closed, "; "))
		}
		return fmt.Sprintf("[%s eagent was restarted and picked the session up again.]", s.clock(ev.Time))
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[%s Error (%s): %s]", s.clock(ev.Time), d.Where, clip(d.Text, opts.RemarkChars))
	case event.Route:
		var d event.RouteData
		_ = ev.Decode(&d)
		if d.Actor != event.ActorEagent {
			return ""
		}
		return fmt.Sprintf("[%s You now run on %s (%s).]", s.clock(ev.Time), d.Model, d.Reason)
	case event.RelayStart, event.RelaySend, event.RelayMoved, event.RelayDelivered, event.RelayState, event.RelayProgress, event.RelayTurn, event.RelayLimit, event.RelayEnd, event.RelayAutopilot:
		return s.relayNotice(ev, opts)
	}
	return ""
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

// attachmentNotes says where the user's files landed and what they are.
// Pictures are also shown to the model directly when it accepts them. A
// message passed to the agent takes its attachments along.
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
	b.WriteString("]")
	return b.String()
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
