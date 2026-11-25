package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// The tail is the last message of a model call: what is true this second
// and nowhere in the record. Why the model was woken, what each agent is
// doing right now, the steps it has taken since the user last heard, how
// long that has been, and what is expected of this reply.
//
// It is written for one call. If the model holds, the tail is gone and the
// conversation is as it was. If the model acts, the tail is recorded as a
// steer and stays where it stood. Nothing in it is ever placed earlier in
// the prompt, so it can change with every call at no cost to the cache.

// relayStep is one thing an agent did (a tool it ran), as its transcript
// described it. Steps are kept in memory only: they are what a progress
// look reads between the agent's own remarks.
type relayStep struct {
	text string
	at   time.Time
	n    int // how many times in a row
}

// relayStepsKept bounds the steps remembered per relay; relayStepsShown the
// steps one tail lists.
const (
	relayStepsKept  = 80
	relayStepsShown = 30
)

// noteStep records a step an agent took.
func (s *relaySeats) noteStep(name, text string, at time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.steps == nil {
		s.steps = map[string][]relayStep{}
	}
	list := s.steps[name]
	if n := len(list); n > 0 && list[n-1].text == text {
		list[n-1].n++
	} else {
		list = append(list, relayStep{text: text, at: at, n: 1})
	}
	if len(list) > relayStepsKept {
		list = list[len(list)-relayStepsKept:]
	}
	s.steps[name] = list
	s.stepCount++
}

// stepsSince returns the steps one relay took after a moment, oldest first.
func (s *relaySeats) stepsSince(name string, since time.Time) []relayStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []relayStep
	for _, st := range s.steps[name] {
		if st.at.After(since) {
			out = append(out, st)
		}
	}
	return out
}

// stepped reports how many steps the relays have taken in all, and whether
// any came after a moment.
func (s *relaySeats) stepped(since time.Time) (total int, after bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, list := range s.steps {
		if n := len(list); n > 0 && list[n-1].at.After(since) {
			after = true
		}
	}
	return s.stepCount, after
}

// tail writes the end of the prompt for one call. Loop goroutine.
func (r *Runtime) tail(w wake, now time.Time) string {
	t := &r.turns
	var b strings.Builder
	fmt.Fprintf(&b, "[eagent %s] ", now.In(r.st.Location()).Format("15:04:05"))
	active := r.st.ActiveRelays()
	who := "the agent"
	if len(active) > 0 {
		who = active[0].Who()
	}

	// Why this call. Several things may have happened since the last look
	// (the user wrote while the agent was finishing a turn); each is named,
	// most pressing first, because the look answers for all of them.
	switch {
	case w.event:
		var reasons []string
		for k := wakeKinds - 1; k >= 0; k-- {
			if t.wakes[k] <= t.seen {
				continue
			}
			switch k {
			case wakeUser:
				reasons = append(reasons, r.userTail(who))
			case wakeAttention:
				reasons = append(reasons, r.attentionTail(who))
			case wakeTurnEnd:
				reasons = append(reasons, fmt.Sprintf("%s finished a turn; its report is above. Give the user the short version, and ask them what it asks or keep it going, as your instructions say.", r.turnEndWho(who)))
			case wakeNotice:
				reasons = append(reasons, "A notice for you is above. Do what it calls for.")
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "Something happened; see above.")
		}
		b.WriteString(strings.Join(reasons, "\n"))
	case w.look:
		b.WriteString("A look while the work goes on.")
	default:
		b.WriteString("Nothing has happened; this look is only about the thread's title and summary.")
	}

	// What is true right now.
	if len(active) == 0 {
		b.WriteString("\nNo agent is running.")
	}
	for _, rel := range active {
		fmt.Fprintf(&b, "\n%s", r.agentLine(rel, now))
	}
	if w.look || (w.event && w.kind == wakeUser) {
		heard := "so far"
		if !t.saidAt.IsZero() {
			heard = "since the user last heard from you"
		}
		for _, rel := range active {
			if rel.State != "working" {
				continue
			}
			steps := r.relays.stepsSince(rel.Name, t.saidAt)
			if len(steps) == 0 {
				if w.look {
					fmt.Fprintf(&b, "\n%s has started no new step %s.", rel.Who(), heard)
				}
				continue
			}
			shown := steps
			if len(shown) > relayStepsShown {
				shown = shown[len(shown)-relayStepsShown:]
			}
			fmt.Fprintf(&b, "\nWhat %s did %s, oldest first", rel.Who(), heard)
			if skipped := len(steps) - len(shown); skipped > 0 {
				fmt.Fprintf(&b, " (%d earlier steps left out)", skipped)
			}
			b.WriteString(":")
			for _, st := range shown {
				fmt.Fprintf(&b, "\n- %s %s", st.at.In(r.st.Location()).Format("15:04"), st.text)
				if st.n > 1 {
					fmt.Fprintf(&b, " (x%d)", st.n)
				}
			}
		}
	}

	// How long the user has heard nothing, and what that asks of a look.
	quiet, limit := r.quietFor(), r.quietLimit()
	switch {
	case w.look && t.saidAt.IsZero():
		fmt.Fprintf(&b, "\nThe user has heard nothing from you in the %s since this began: tell them now, in two or three sentences, what %s is doing and how.", roundDur(quiet), who)
	case w.look && !w.moved:
		fmt.Fprintf(&b, "\nNothing new has shown since you last spoke, %s ago. Tell the user in one sentence what is still running and for how long, naming the command or step from the lines above, so the quiet does not read as a stop. If you cannot tell what it is, say how long it has been since anything showed.", roundDur(quiet))
	case w.look && limit > 0 && quiet >= limit:
		fmt.Fprintf(&b, "\nThe user has heard nothing for %s while the work kept going: send a progress line now. Two or three sentences: what got done since you last spoke, what is happening now, anything that went wrong. Name the files, commands and numbers.", roundDur(quiet))
	case w.look:
		fmt.Fprintf(&b, "\nThe user last heard from you %s ago. Tell them what has happened since, in a sentence or two, with the files, commands and numbers that matter. Hold only if all of it is still the step you last described, or too slight to be worth a sentence yet.", roundDur(quiet))
	case !t.saidAt.IsZero():
		fmt.Fprintf(&b, "\nThe user last heard from you %s ago.", roundDur(quiet))
	}

	if r.st.Question != nil {
		fmt.Fprintf(&b, "\nYour question to the user is still open: %s", clipRunes(oneLine(r.st.Question.Text, 200), 200))
	}
	if r.st.DismissalStands() {
		b.WriteString("\nThe user dismissed your last question without answering. That is not an answer: nothing was decided, and they will write when they are ready. Do not ask it again, do not ask it in other words, and do not tell them what will be assumed.")
	}
	if w.thread {
		b.WriteString("\n" + r.threadTail())
	}
	return b.String()
}

// agentLine says what one agent is doing right now.
func (r *Runtime) agentLine(rel *state.Relay, now time.Time) string {
	var b strings.Builder
	what := rel.State
	switch rel.State {
	case "working":
		what = "in the middle of a turn"
	case "idle":
		what = "between turns"
	case "blocked":
		what = "stuck on something on its screen"
	case "starting":
		what = "starting"
	case "exited":
		what = "not running: it exited"
	}
	fmt.Fprintf(&b, "%s", rel.Who())
	if rel.Name != rel.Agent {
		fmt.Fprintf(&b, " (%s)", rel.Name)
	}
	fmt.Fprintf(&b, " is %s, for %s.", what, roundDur(now.Sub(rel.StateSince)))
	if rel.State == "working" {
		r.relays.mu.Lock()
		act := r.relays.activity[rel.Name]
		r.relays.mu.Unlock()
		if act.text != "" {
			fmt.Fprintf(&b, " Right now: %s (for %s).", act.text, roundDur(now.Sub(act.at)))
		}
	}
	if n := len(rel.Undelivered); n > 0 {
		fmt.Fprintf(&b, " %d message(s) to it are not confirmed yet.", n)
	}
	if ap := rel.Autopilot; ap != nil {
		fmt.Fprintf(&b, " Autopilot is on %s", autopilotUntil(*ap))
		if !ap.Decide {
			b.WriteString(" (its questions go to the user)")
		}
		b.WriteString(".")
	}
	return b.String()
}

// turnEndWho names the agent whose turn end is still owed a look.
func (r *Runtime) turnEndWho(fallback string) string {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= r.turns.seen {
			break
		}
		if ev.Type == event.RelayTurn {
			var d event.RelayTurnData
			if ev.Decode(&d) == nil {
				if rel := r.st.Relays[d.Name]; rel != nil {
					return rel.Who()
				}
			}
		}
	}
	return fallback
}

// userTail says what became of what the user wrote since the last look,
// and what that leaves for eagent.
func (r *Runtime) userTail(who string) string {
	var passed, held, answered int
	heldWhy := ""
	for _, ev := range r.st.Events {
		if ev.Seq <= r.turns.seen {
			continue
		}
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Held == "" {
				passed++
			} else {
				held++
				heldWhy = d.Held
			}
		case event.UserAnswer:
			answered++
		}
	}
	var parts []string
	if answered > 0 {
		parts = append(parts, fmt.Sprintf("The user answered your question. If %s is waiting on that answer, give it to %s with relay_send in words it can act on; if the question was only yours, act on the answer.", who, who))
	}
	if held > 0 {
		s := "The user wrote, and the message was kept for you (" + heldWhy + "). Answer it or do what it asks"
		switch {
		case len(r.st.ActiveRelays()) == 0:
			s += ". No agent is running: if they want one, start it (relay_start, or relay_control restart for one that exited) and then pass their message on with relay_forward."
		default:
			s += "; if it turns out to be for " + who + ", pass it on with relay_forward."
		}
		parts = append(parts, s)
	}
	if passed > 0 {
		parts = append(parts, fmt.Sprintf("The user wrote, and the message was typed into %s's terminal as written; their own screen shows it as delivered, so say nothing about that. It needs something from you only if it sets a standing order to keep %s going, says how often they want to hear from you, or lacks something %s needs.", who, who, who))
	}
	if len(parts) == 0 {
		return "The user wrote."
	}
	return strings.Join(parts, " ")
}

// attentionTail says what an agent needs, from the newest event that is
// still owed a look.
func (r *Runtime) attentionTail(fallback string) string {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= r.turns.seen {
			break
		}
		if !state.NeedsAttention(ev) || ev.Type == event.RelayTurn {
			continue
		}
		var name struct {
			Name string `json:"name"`
		}
		_ = ev.Decode(&name)
		who := fallback
		if rel := r.st.Relays[name.Name]; rel != nil {
			who = rel.Who()
		}
		switch ev.Type {
		case event.RelayState:
			var d event.RelayStateData
			_ = ev.Decode(&d)
			if d.State == "exited" {
				return who + " exited. Tell the user, and start it again with relay_control restart unless they stopped it themselves."
			}
			return who + " is stuck on something on its screen. Read it with relay_screen and clear it with relay_control keys; ask the user only if the choice is really theirs."
		case event.RelayDelivered:
			return "A message to " + who + " could not be confirmed. Look at relay_screen, then send it again or say what is wrong."
		case event.RelayLimit:
			return who + "'s usage budget changed. Tell the user if it stops the work or soon will; otherwise hold."
		}
	}
	return fallback + " needs attention; see above."
}

// threadTail asks about the thread's title and summary.
func (r *Runtime) threadTail() string {
	project := r.projectName()
	var b strings.Builder
	b.WriteString("The thread's title and summary may be out of date. ")
	switch {
	case r.st.Title == "" || genericTitle(r.st.Title, project):
		b.WriteString("Title: none yet. ")
	case r.st.TitlePinned:
		fmt.Fprintf(&b, "Title: %q (the user chose it: leave it). ", r.st.Title)
	default:
		fmt.Fprintf(&b, "Title: %q. ", r.st.Title)
	}
	if r.st.Description != "" {
		fmt.Fprintf(&b, "Summary: %q. ", clipRunes(r.st.Description, 400))
	} else {
		b.WriteString("Summary: none yet. ")
	}
	b.WriteString("If the summary no longer says where things stand, set it with update_thread in this reply, alongside anything else you do. Set the title only if there is none or it names the wrong subject. If both still fit, leave them.")
	return b.String()
}
