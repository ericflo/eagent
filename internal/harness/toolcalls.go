package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/sched"
	"github.com/ericflo/eagent/internal/tools"
)

// turnCalls is what one turn's tool calls share.
type turnCalls struct {
	wake  wake
	spoke bool // a message or a question went out in this turn
	// handled: the turn answered the user or did something with the agent,
	// so a message kept for eagent has not been left lying.
	handled bool
	// images are the pictures view_image brought back in the call being
	// run, recorded with its result.
	images []event.Attachment
}

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
		text := strings.TrimSpace(str("text"))
		if text == "" {
			return "text is required", true, true
		}
		if tc.spoke {
			return "not sent: one message per reply. Say it all in one next time.", true, false
		}
		atts, err := r.messageAttachments(args["attachments"])
		if err != nil {
			return "not sent: " + err.Error() + ". Fix the attachment or send the message without it.", true, true
		}
		important, _ := args["important"].(bool)
		// The closing word of a session always buzzes.
		important = important || tc.wake.final
		r.sync(func() { r.recordMessage(event.MessageData{Text: text, Important: important, Attachments: atts}) })
		tc.spoke, tc.handled = true, true
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
				r.turns.saidAt = r.st.Events[len(r.st.Events)-1].Time
				onPhone = r.phone != nil
			}
		})
		if refused != "" {
			return refused, true, false
		}
		r.ui.Ask(id, text, opts)
		tc.spoke, tc.handled = true, true
		if onPhone && !r.opts.Interactive {
			return "asked on the user's phone; the answer will appear as a message from them", false, false
		}
		return "asked; the answer will appear as a message from the user", false, false

	case tools.UpdateThread:
		return r.updateThread(str("title"), str("summary"), str("pin") == "true")

	case tools.SessionRead:
		raw, _ := args["raw"].(bool)
		out, err := r.archive.Read(str("file"), num("from", 1), num("to", 0), raw, 0)
		if err != nil {
			return err.Error(), true, true
		}
		return out, false, true
	case tools.SessionSearch:
		out, err := r.archive.Search(str("query"), str("file"), num("max_results", 40))
		if err != nil {
			return err.Error(), true, true
		}
		return out, false, true

	case tools.ViewImage:
		att, err := r.viewImage(str("path"))
		if err != nil {
			return err.Error(), true, true
		}
		tc.images = append(tc.images, att)
		return fmt.Sprintf("showing %s (%s, %dx%d)", att.Name, att.ContentType, att.Width, att.Height), false, true

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
		})
		tc.handled = true
		return fmt.Sprintf("timer %s set (%s); it first comes due at %s", id, s.Kind, next.In(r.st.Location()).Format("15:04:05")), false, false

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
		tc.handled = tc.handled || !bad
		return msg, bad, bad
	}
	// The rest drive the agent.
	out, isErr = r.relayTool(c.Name, str, num, list, false)
	if !isErr && !tools.Looks(c.Name) {
		tc.handled = true
	}
	switch {
	case tools.Looks(c.Name):
		again = true
	case c.Name == tools.RelayControl && str("action") == "keys":
		again = true // the screen after the keys is there to be read
	case isErr && c.Name != tools.RelayAutopilot:
		again = true
	case isErr:
		again = strings.Contains(out, "could not read")
	}
	return out, isErr, again
}

// updateThread sets the title and summary the user sees in their list of
// sessions. A title set with pin is one the user asked for by name: it
// stays until they change it. Any other title follows the rule that keeps
// a thread findable (acceptSummary): it replaces a missing or generic one,
// may be revised once, and after that moves only when the user has written
// since.
func (r *Runtime) updateThread(title, summary string, pin bool) (string, bool, bool) {
	title, summary = cleanTitle(title), cleanSummary(summary)
	if title == "" && summary == "" {
		return "nothing to set: give a title, a summary, or both", true, false
	}
	var p *phone
	var kept string
	r.sync(func() {
		d := event.ThreadTitleData{SeenSeq: r.turns.seen}
		if pin && title != "" {
			d.Title, d.Pin = title, true
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
		r.append(event.New(event.ThreadTitle, event.ActorEagent, d))
		r.summary.seen = max(r.summary.seen, d.SeenSeq)
		if d.Title != "" || d.Description != "" {
			p = r.phone
		}
		title, summary = d.Title, d.Description
	})
	if p != nil {
		p.RetitleThread(r, title, summary)
	}
	return "updated." + kept, false, false
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
