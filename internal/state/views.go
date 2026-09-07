package state

import (
	"fmt"
	"strings"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// ViewOptions tune how much of the log each actor sees.
type ViewOptions struct {
	// ObservationChars bounds tool outputs shown to the narrator.
	ObservationChars int
}

// DefaultViewOptions are used when nil is passed.
var DefaultViewOptions = ViewOptions{ObservationChars: 1500}

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
	msgs := renderActor(events, event.ActorOrchestrator, "", func(ev event.Event) string {
		return orchestratorNotification(ev)
	})
	// A subsession's dossier is always the opening message, even if other
	// notifications landed in the file before the dossier task finished.
	for i, ev := range events {
		if ev.Type != event.Dossier || i == 0 {
			continue
		}
		text := orchestratorNotification(ev)
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
				Native: d.Native, NativeProtocol: d.Provider,
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

// orchestratorNotification renders events the orchestrator must hear about.
func orchestratorNotification(ev event.Event) string {
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
		return d.Text + attachmentNotes(d.Attachments)
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[The user answered question %s]\n%s", d.QuestionID, d.Text) + attachmentNotes(d.Attachments)
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		if ev.Task == "" {
			// Work tasks report as notifications; dossier tasks are delivered via Dossier.
		}
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
			msgs = append(msgs, llm.Message{Role: "assistant", Text: d.Text, ToolCalls: d.ToolCalls, Native: d.Native, NativeProtocol: d.Provider})
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
		obs := Observe(ev, opts.ObservationChars)
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
		if d.IsError {
			label = "error"
		}
		return fmt.Sprintf("%s   %s %s: %s", ts, d.Name, label, clip(d.Output, maxChars))
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s NOTE FROM ORCHESTRATOR (for you to consider relaying): %s", ts, d.Text)
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
		return fmt.Sprintf("%s   started %s: %s", ts, d.Handle, clip(d.Command, 300))
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
