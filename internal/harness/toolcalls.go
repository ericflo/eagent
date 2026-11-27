package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/sched"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/tools"
)

// turnCalls is what one turn's tool calls share.
type turnCalls struct {
	wake wake
	// view is the newest event the call whose reply is being run was
	// shown. An argument that names something on the record by default
	// (relay_forward's message) is resolved against it, not against what
	// came in while the call was out.
	view  int64
	spoke bool // a message or a question went out in this turn
	// said: a message or a question went out in the reply whose calls are
	// being run. One reply sends one message; a later reply of the same
	// turn (it looked at something, or the user wrote meanwhile) may
	// speak again.
	said bool
	// handled: the turn answered the user or did something with the agent,
	// so a message kept for eagent has not been left lying. dealt counts
	// the calls that did so, so that something that arrives in the middle
	// of a turn is dealt with only by a call made after it (handle).
	handled bool
	dealt   int
	// images are the pictures view_image brought back in the call being
	// run, recorded with its result.
	images []event.Attachment
	// threaded: the turn dealt with the thread's title and summary (set
	// them, or said they still fit).
	threaded bool
	// stamped: the reply whose calls are being run recorded how far the
	// title and summary are right. own lists what that reply itself put on
	// the record for the thread list (a message, a question, the agent
	// stopped): the model wrote the title and summary knowing it, so it is
	// covered by them too (coverOwnWords).
	stamped bool
	own     []int64
}

// endsOf lists the ends of the relay called name recorded after seq.
// Loop goroutine only.
func (r *Runtime) endsOf(name string, after int64) []int64 {
	var out []int64
	for i := len(r.st.Events) - 1; i >= 0 && r.st.Events[i].Seq > after; i-- {
		ev := r.st.Events[i]
		var d event.RelayEndData
		if ev.Type == event.RelayEnd && ev.Decode(&d) == nil && d.Name == name {
			out = append(out, ev.Seq)
		}
	}
	return out
}

// userSeqAsOf is the newest message or answer from the user at or before
// view (0: the newest of all). Loop goroutine only.
func userSeqAsOf(st *state.State, view int64) int64 {
	if view <= 0 {
		return st.LastUserSeq
	}
	for i := len(st.Events) - 1; i >= 0; i-- {
		ev := st.Events[i]
		if ev.Seq <= view && (ev.Type == event.UserMessage || ev.Type == event.UserAnswer) {
			return ev.Seq
		}
	}
	return 0
}

// handle notes a call that answered the user or did something with the
// agent.
func (tc *turnCalls) handle() {
	tc.handled = true
	tc.dealt++
}

// loopReadChars bounds one session_read in eagent's own conversation,
// where the result stays in every later prompt until the next restart
// (the number the old tool_output_max_chars had).
const loopReadChars = 24000

// runTool runs one of eagent's tool calls and returns its result. again
// says the model should get another call in this turn: it looked at
// something and has to use what it saw, or the call failed in a way it can
// put right. A call that only acts ends the turn. It runs on the turn's
// goroutine; state changes go through r.sync.
func (r *Runtime) runTool(ctx context.Context, tc *turnCalls, c event.ToolCall) (out string, isErr, again bool) {
	if !tools.Has(c.Name) {
		return fmt.Sprintf("unknown tool %q", c.Name), true, true
	}
	args, err := llm.ArgsObject(c.Args)
	if err != nil {
		return "Tool call rejected: " + err.Error(), true, true
	}
	str, num, list := argStr(args), argNum(args), argList(args)
	switch c.Name {
	case tools.Hold:
		return "ok", false, false

	case tools.SendMessage:
		text := cleanNarration(str("text"))
		if text == "" {
			return "text is required", true, true
		}
		if tc.said {
			return "not sent: one message per reply. Say it all in one next time.", true, false
		}
		atts, err := r.messageAttachments(args["attachments"])
		if err != nil {
			return "not sent: " + err.Error() + ". Fix the attachment or send the message without it.", true, true
		}
		important, _ := args["important"].(bool)
		// The closing word of a session always buzzes.
		important = important || tc.wake.final
		// A closing word the user's own message overtook (they wrote
		// while the call was out) is not sent on top of it: asked in the
		// step that records it. Only that: a closing reply that itself
		// set a timer or started an agent before this call still tells
		// the user so.
		stale := false
		r.sync(func() {
			if stale = tc.wake.final && r.turns.wakes[wakeUser] > r.turns.seen; stale {
				return
			}
			r.recordMessage(event.MessageData{Text: text, Important: important, Attachments: atts})
			if n := len(r.st.Events); n > 0 && r.st.Events[n-1].Type == event.Message {
				tc.own = append(tc.own, r.st.Events[n-1].Seq)
			}
		})
		if stale {
			return "not sent: the user wrote while you wrote this, so no closing word goes out on top of their message. It is looked at next.", true, false
		}
		tc.spoke, tc.said = true, true
		tc.handle()
		if len(atts) > 0 {
			return fmt.Sprintf("delivered with %d attachment(s)", len(atts)), false, false
		}
		return "delivered", false, false

	case tools.AskUser:
		text := strings.TrimSpace(str("text"))
		if text == "" {
			return "text is required", true, true
		}
		var opts []string
		for _, o := range list("options") {
			if strings.TrimSpace(o) != "" {
				opts = append(opts, o)
			}
		}
		var id, refused string
		var onPhone bool
		r.sync(func() {
			switch {
			case r.st.DismissalStands():
				// Asking again would put back the card they just closed.
				refused = "not asked: the user dismissed your last question without answering and has not written since. Nothing was decided. Do not ask again; they will write when they are ready."
			default:
				// A new question takes the place of one still open: the
				// agent has moved on, and the user is shown what it asks now.
				id = r.st.NextQuestionID()
				r.append(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: id, Text: text, Options: opts}))
				asked := r.st.Events[len(r.st.Events)-1]
				r.turns.saidAt, r.turns.saidSeq, r.turns.spokeSeen = asked.Time, asked.Seq, r.turns.seen
				tc.own = append(tc.own, asked.Seq)
				onPhone = r.phone != nil
			}
		})
		if refused != "" {
			return refused, true, false
		}
		r.ui.Ask(id, text, opts)
		tc.spoke, tc.said = true, true
		tc.handle()
		switch {
		case r.opts.Interactive:
			return "asked; the answer will appear as a message from the user", false, false
		case onPhone:
			return "asked on the user's phone; the session waits for the answer, which will appear as a message from them", false, false
		}
		return "shown to the user; nobody is at a terminal and no phone is connected, so this session ends now and they can answer with `eagent resume " + r.sess.ID + " --answer \"...\"`", false, false

	case tools.UpdateThread:
		tc.threaded = true
		out, isErr, again = r.updateThread(str("title"), str("summary"), str("pin") == "true")
		tc.stamped = tc.stamped || !isErr
		return out, isErr, again

	case tools.SessionRead:
		raw, _ := args["raw"].(bool)
		out, err := r.archive.Read(str("file"), num("from", 1), num("to", 0), raw, num("offset", -1), loopReadChars)
		if err != nil {
			return err.Error(), true, true
		}
		if len(out) > loopReadChars+2000 {
			// The reader stops between lines and cuts a line longer than
			// the bound into pieces it says how to reach; this is only a
			// backstop.
			out = clipRunes(out, loopReadChars) + "\n[... cut here; this line is longer than one read may return ...]"
		}
		return out, false, true
	case tools.SessionSearch:
		out, err := r.archive.Search(str("query"), str("file"), num("max_results", 40))
		if err != nil {
			return err.Error(), true, true
		}
		if len(out) > loopReadChars+400 {
			// Like a read, a search stays in every later prompt.
			out = clipRunes(out, loopReadChars) + "\n[... cut here; narrow the query or set a file ...]"
		}
		return out, false, true

	case tools.ViewImage:
		// A picture already in the conversation is not put there again
		// while the file is unchanged.
		path := str("path")
		stamp := r.imageStamp(path)
		var again bool
		r.sync(func() { again = stamp != "" && r.viewed[path] == stamp })
		if again {
			return path + " is unchanged since you looked at it; the picture is in your conversation above.", false, true
		}
		att, err := r.viewImage(path)
		if err != nil {
			return err.Error(), true, true
		}
		r.sync(func() {
			if r.viewed == nil {
				r.viewed = map[string]string{}
			}
			r.viewed[path] = stamp
		})
		tc.images = append(tc.images, att)
		dims := ""
		if att.Width > 0 {
			dims = fmt.Sprintf(", %dx%d", att.Width, att.Height)
		}
		return fmt.Sprintf("%s (%s, %s%s) follows this result as an image.", att.Name, att.ContentType, humanBytes(att.Size), dims), false, true

	case tools.Schedule:
		spec := strings.TrimSpace(str("spec"))
		s, err := sched.Parse(spec)
		if err != nil {
			return err.Error(), true, true
		}
		note := strings.TrimSpace(str("note"))
		if note == "" {
			return "note is required: say what to do when the timer comes due", true, true
		}
		next := s.Next(time.Now())
		if next.IsZero() {
			return "that timer would never come due", true, true
		}
		var id string
		r.sync(func() {
			id = r.st.NextScheduleID()
			r.append(event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: id, Kind: s.Kind, Spec: spec, Note: note, Next: next}))
			r.armSchedule(id, next)
			// As for an agent started: a session with a timer set is not
			// ending, and its closing word is asked again once the timer's
			// work is done.
			r.summary.final, r.turns.finalAsked = false, false
		})
		tc.handle()
		// The timer's id and the time it comes due are in the result, and
		// nothing else would wake eagent to tell the user (as for
		// relay_start below).
		return fmt.Sprintf("timer %s set (%s); it first comes due at %s", id, s.Kind, next.In(r.st.Location()).Format("15:04:05")), false, true

	case tools.CancelSchedule:
		id := str("schedule")
		var msg string
		var bad bool
		r.sync(func() {
			sc := r.st.Schedules[id]
			if sc == nil || sc.Cancelled {
				msg, bad = "no active timer "+id, true
				return
			}
			if t := r.timers[id]; t != nil {
				t.Stop()
				delete(r.timers, id)
			}
			r.append(event.New(event.ScheduleCancel, event.ActorEagent, event.ScheduleCancelData{ID: id}))
			msg = "timer " + id + " cancelled"
		})
		if !bad {
			tc.handle()
		}
		return msg, bad, true
	}
	// The rest drive the agent.
	var before int64
	var controlled string
	if c.Name == tools.RelayControl {
		r.sync(func() {
			before = r.st.LastSeq()
			if rel, err := r.resolveRelay(str("relay")); err == nil {
				controlled = rel.Name
			}
		})
	}
	out, isErr = r.relayToolAt(c.Name, str, num, list, false, tc.view)
	if c.Name == tools.RelayControl && !isErr && controlled != "" {
		// The agent this call stopped (or replaced) ending is the reply's
		// own news; another agent ending meanwhile is not.
		r.sync(func() { tc.own = append(tc.own, r.endsOf(controlled, before)...) })
	}
	if !isErr && !tools.Looks(c.Name) {
		tc.handle()
	}
	switch {
	case tools.Looks(c.Name):
		again = true
	case c.Name == tools.RelayControl && str("action") == "keys":
		again = true // the screen after the keys is there to be read
	case !isErr && (c.Name == tools.RelayStart || c.Name == tools.RelayControl || c.Name == tools.RelayAutopilot):
		// What came of it (the version it restarted on, a model change put
		// off to the end of its turn, the deadline as it was understood)
		// is in the result, and nothing else would wake eagent to tell
		// the user. Passing words on (relay_send, relay_forward) ends the
		// turn: the agent's answer is what comes next.
		again = true
	case isErr && c.Name != tools.RelayAutopilot:
		again = true
	case isErr:
		again = strings.Contains(out, "could not read")
	}
	return out, isErr, again
}

// updateThread sets the title and summary the user sees in their list of
// sessions. With pin the words are the user's own (they asked for the name,
// or dictated the description): they are kept as written, and a pinned
// title stays until the user changes it. Any other title follows the rule
// that keeps a thread findable (acceptSummary): it replaces a missing or
// generic one, may be revised once, and after that moves only when the
// user has written since. A call with neither field says both still fit.
func (r *Runtime) updateThread(rawTitle, rawSummary string, pin bool) (string, bool, bool) {
	title, summary := cleanTitle(rawTitle), cleanSummary(rawSummary)
	if pin {
		title, summary = strings.TrimSpace(rawTitle), clipRunes(strings.TrimSpace(rawSummary), 2000)
	}
	var p *phone
	var out string
	r.sync(func() {
		seen := r.turns.seen
		if title == "" && summary == "" {
			r.threadKept()
			out = "kept as they are"
			return
		}
		if !pin && !r.summary.on {
			out = "not set: titles and summaries are switched off for this session. Set one only when the user asks for it by name, with pin=true."
			return
		}
		d := event.ThreadTitleData{SeenSeq: seen}
		var kept string
		if pin {
			d.Title, d.Pin = title, title != ""
			if summary != r.st.Description {
				d.Description = summary
			}
		} else {
			d.By = "summary" // a running update: later ones may revise it
			d.Title, d.Description = acceptSummary(r.st, r.projectName(), summaryResult{Title: title, Summary: summary})
			if title != "" && d.Title == "" && !strings.EqualFold(title, r.st.Title) {
				kept = fmt.Sprintf(" The title stays %q.", r.st.Title)
			}
		}
		// Recorded even when nothing changes: it says how far the title
		// and summary are known to be right.
		r.append(event.New(event.ThreadTitle, event.ActorEagent, d))
		r.summary.seen = max(r.summary.seen, seen)
		if d.Title == "" && d.Description == "" {
			out = "nothing changed: they already say that." + kept
			return
		}
		p = r.phone
		title, summary = d.Title, d.Description
		out = "updated." + kept
	})
	if p != nil {
		p.RetitleThread(r, title, summary)
	}
	return out, strings.HasPrefix(out, "not set"), false
}

// ---- arguments -------------------------------------------------------------

// argStr reads a string argument; models sometimes pass a number or a
// boolean where a string was asked for.
func argStr(m map[string]any) func(string) string {
	return func(k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case nil:
			return ""
		case float64, int64, int, bool:
			return fmt.Sprint(v)
		}
		return ""
	}
}

func argNum(m map[string]any) func(string, int) int {
	return func(k string, def int) int {
		switch v := m[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
		return def
	}
}

func argList(m map[string]any) func(string) []string {
	return func(k string) []string {
		switch v := m[k].(type) {
		case []string:
			return v
		case []any:
			var out []string
			for _, x := range v {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
			return out
		case string:
			if strings.TrimSpace(v) != "" {
				return []string{v}
			}
		}
		return nil
	}
}
