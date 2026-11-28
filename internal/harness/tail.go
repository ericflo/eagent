package harness

import (
	"fmt"
	"sort"
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
	// times are when the latest repeats came, oldest first, so a run
	// that goes on past a moment counts as movement after it and is
	// counted from it. Only kept for a run of more than one.
	times []time.Time
}

// relayStepsKept bounds the steps remembered per relay; relayStepsShown the
// steps one tail lists; relayRepeatsKept the times remembered of one run.
const (
	relayStepsKept   = 80
	relayStepsShown  = 30
	relayRepeatsKept = 256
)

// last is when the step last came.
func (st relayStep) last() time.Time {
	if len(st.times) > 0 {
		return st.times[len(st.times)-1]
	}
	return st.at
}

// since is the part of a run that came after a moment: whole when it began
// after it, else the repeats after it, from the first of them. ok is false
// when none came after it. Of a run longer than the times kept, the
// repeats whose times were let go are counted as before the moment when it
// falls among them, so the count never claims more than happened since.
func (st relayStep) since(t time.Time) (relayStep, bool) {
	if st.at.After(t) {
		st.times = nil // the caller's copy; noteStep reuses the array
		return st, true
	}
	if !st.last().After(t) {
		return relayStep{}, false
	}
	i := len(st.times)
	for i > 0 && st.times[i-1].After(t) {
		i--
	}
	return relayStep{text: st.text, at: st.times[i], n: len(st.times) - i}, true
}

// stepsFrom is the moment from which a relay's remembered steps are its
// own and new: after since, and after the agent that held its name before
// it stopped.
func stepsFrom(rel *state.Relay, since time.Time) time.Time {
	if rel != nil && rel.PrevEnded.After(since) {
		return rel.PrevEnded
	}
	return since
}

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
		st := &list[n-1]
		if st.times == nil {
			st.times = []time.Time{st.at}
		}
		st.n++
		st.times = append(st.times, at)
		if len(st.times) > relayRepeatsKept {
			st.times = append(st.times[:0], st.times[len(st.times)-relayRepeatsKept:]...)
		}
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
		if part, ok := st.since(since); ok {
			out = append(out, part)
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
		if n := len(list); n > 0 && list[n-1].last().After(since) {
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
				if s := r.attentionTail(who); s != "" {
					reasons = append(reasons, s)
				}
			case wakeTurnEnd:
				s := fmt.Sprintf("%s finished a turn; its report is above. Give the user the short version, and ask them what it asks or keep it going, as your instructions say.", r.turnEndWho(who))
				if r.phone != nil {
					s += " If this finishes what the user asked for, or stops on something only they can settle, mark your message important so their phone buzzes."
				}
				reasons = append(reasons, s)
			case wakeNotice:
				reasons = append(reasons, r.noticeTail())
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "Something happened; see above.")
		}
		b.WriteString(strings.Join(reasons, "\n"))
	case w.look:
		b.WriteString("A look while the work goes on.")
	case w.final:
		switch {
		case t.saidAt.IsZero():
			b.WriteString("The session is ending now: no agent is running and nobody is at a terminal. The user has heard nothing from you yet. Send a complete final report, marked important: what was done, where it is, how to run it, what was verified, and what is missing or uncertain. Do not hold.")
		case closingWordOwed(r.st.LastUserSeq, t.saidSeq, t.spokeSeen):
			b.WriteString("The session is ending now: no agent is running and nobody is at a terminal. The user has written since you last spoke and has heard nothing back. Send a complete final report, marked important: what was done, where it is, how to run it, what was verified, and what is missing or uncertain. Do not hold.")
		default:
			b.WriteString("The session is ending now: no agent is running and nobody is at a terminal. If your last message already covers the final state (what was done, where it is, how to run it, what was verified, what is missing), hold. Otherwise send one final report with what is missing from it. Never repeat yourself.")
		}
	default:
		b.WriteString("Nothing has happened; this look is only about the thread's title and summary.")
	}

	// What is true right now.
	if len(active) == 0 {
		b.WriteString("\nNo agent is running.")
	}
	for _, rel := range active {
		fmt.Fprintf(&b, "\n%s", r.agentLine(rel, now))
		if ap := rel.Autopilot; ap != nil && !w.look && w.event {
			// The standing order in full, every time eagent is woken to
			// act: it is what the decision is made from.
			fmt.Fprintf(&b, " The standing order: %s. When %s ends a turn having stopped, with nothing of its own still running, send it on with relay_send toward what is still missing", clipRunes(oneLine(ap.Instruction, 300), 300), rel.Who())
			if ap.Decide {
				b.WriteString("; when it asks something, decide as the user would, answer it with relay_send, and tell the user what you chose. Do not wait for the user.")
			} else {
				b.WriteString("; pass its questions to the user.")
			}
			fmt.Fprintf(&b, " When it ends a turn waiting on work of its own (a command, a render, a timer it set, a helper), send nothing: it wakes itself when that work reports, and every message starts another turn. You are woken if it stays idle for %s.", roundDur(autopilotCheckAfter))
		}
	}
	if n := len(r.st.ActiveSchedules()); n > 0 && !w.look {
		fmt.Fprintf(&b, "\n%d timer(s) of yours are set.", n)
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
			steps := r.relays.stepsSince(rel.Name, stepsFrom(rel, t.saidAt))
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
	if rel.Limit != nil {
		// Where its budget stood when it last said: the user asks, and the
		// line in the record is gone once the conversation is restarted.
		fmt.Fprintf(&b, " Usage: %s.", limitLine(rel.Who(), *rel.Limit))
	}
	if ap := rel.Autopilot; ap != nil {
		fmt.Fprintf(&b, " Autopilot is on %s", r.autopilotUntil(*ap))
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
				if rel := r.st.RelayAt(d.Name, ev.Seq); rel != nil {
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
	var held, answered int
	heldWhy := ""
	for _, ev := range r.st.Events {
		if ev.Seq <= r.turns.seen {
			continue
		}
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Held != "" {
				held++
				heldWhy = d.Held
			}
		case event.UserAnswer:
			answered++
		}
	}
	// What a message typed into the agent's terminal is said to have done
	// follows its send's outcome on the record.
	var passed, waiting, failed, unsure, late int
	// When the messages ended differently, each sentence names the ones
	// it is about, quoted, in the order they were written.
	sends := r.passedSends()
	seqs := make([]int64, 0, len(sends))
	for seq := range sends {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	named := map[*int][]string{}
	for _, seq := range seqs {
		n := &failed // not_sent, or no send recorded
		switch r.st.SendOutcome(sends[seq]) {
		case state.SendArrived:
			n = &passed
			if r.st.ArrivedLate(sends[seq]) {
				// Not "it reached the agent": the agent it was typed into
				// may be stopped or replaced by now.
				n = &late
			}
		case state.SendWaiting, state.SendHeld:
			n = &waiting
		case state.SendUnconfirmed:
			n = &unsure
		}
		*n++
		text, _, _ := userEventText(r.st, seq)
		named[n] = append(named[n], fmt.Sprintf("%q", clipRunes(oneLine(text, 80), 80)))
	}
	which := func(n *int) string {
		if len(named) < 2 {
			return ""
		}
		return " (" + strings.Join(named[n], ", ") + ")"
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
		parts = append(parts, fmt.Sprintf("The user wrote, and the message%s was typed into %s's terminal as written the moment it arrived. Say in one short line that it reached %s (and, if %s was in the middle of a turn, that it reads it at its next step); do not repeat the message back or answer it. Hold instead if %s has already answered it. Beyond that it needs something from you only if it sets a standing order to keep %s going, says how often they want to hear from you, or lacks something %s needs (add that with relay_send).", which(&passed), who, who, who, who, who, who))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("The user wrote, and the message%s was passed to %s as written; it waits to be taken up. Say in one short line that it was passed on and waits to be taken up; do not repeat the message back or answer it. Hold instead if %s has already answered it.", which(&waiting), who, who))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("The user wrote, and the message%s to %s did not go; nothing of it reached it. Send it again (relay_forward sends a message of the user's again as written) or say what is wrong.", which(&failed), who))
	}
	if unsure > 0 {
		parts = append(parts, fmt.Sprintf("The user wrote, and whether the message%s reached %s could not be confirmed. Look at relay_screen, then send it again or say what is wrong.", which(&unsure), who))
	}
	if late > 0 {
		parts = append(parts, fmt.Sprintf("The user wrote, and the message%s, which could not be confirmed at first, %s", which(&late), arrivedLate))
	}
	if len(parts) == 0 {
		return "The user wrote."
	}
	return strings.Join(parts, " ")
}

// passedSends maps each message of the user's since the last look that was
// not held to the id of its automatic send, "" when none is recorded.
func (r *Runtime) passedSends() map[int64]string {
	out := map[int64]string{}
	for _, ev := range r.st.Events {
		if ev.Seq <= r.turns.seen {
			continue
		}
		var m event.UserMessageData
		var s event.RelaySendData
		switch {
		case ev.Type == event.UserMessage && ev.Decode(&m) == nil && m.Held == "":
			out[ev.Seq] = ""
		case ev.Type == event.RelaySend && ev.Decode(&s) == nil && s.Auto && s.From == "user":
			if id, ok := out[s.Seq]; ok && id == "" {
				out[s.Seq] = s.ID
			}
		}
	}
	return out
}

// arrivedLate ends the sentence about a send whose arrival showed only
// after the record had said it was in doubt. It says where the send
// arrived and no more: whether an agent running now has it is for the
// model to see.
const arrivedLate = "did arrive in the agent it was typed into. If that agent was stopped, started again or replaced since, look at relay_screen to see whether an agent running now is acting on it before sending it again."

// attentionTail says what an agent needs, from the newest event that is
// still owed a look.
func (r *Runtime) attentionTail(fallback string) string {
	late := "" // an agent whose send in doubt has arrived since
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= r.turns.seen {
			break
		}
		if !r.st.NeedsAttention(ev) || ev.Type == event.RelayTurn {
			continue
		}
		var name struct {
			Name string `json:"name"`
		}
		_ = ev.Decode(&name)
		who := fallback
		if rel := r.st.RelayAt(name.Name, ev.Seq); rel != nil {
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
			var d event.RelayDeliveredData
			ok := ev.Decode(&d) == nil
			for _, id := range r.passedSends() {
				if ok && id == d.ID {
					return "" // the user's message it carried: userTail says how it went
				}
			}
			if ok && (d.OK || r.st.SendOutcome(d.ID) == state.SendArrived) {
				// The late arrival itself, or the word in doubt before it:
				// the send arrived in the agent it was typed into (the
				// conversation says so too).
				if late == "" {
					late = who // the newest such
				}
				continue
			}
			if ok && state.DeliveryOutcome(d) == state.SendNotSent {
				return "A message to " + who + " did not go; nothing of it reached it. Send it again (relay_forward sends a message of the user's again as written) or say what is wrong."
			}
			return "A message to " + who + " could not be confirmed. Look at relay_screen, then send it again or say what is wrong."
		case event.RelayLimit:
			return who + "'s usage budget changed. Tell the user if it stops the work or soon will; otherwise hold."
		}
	}
	if late != "" {
		return "The message to " + late + " that could not be confirmed " + arrivedLate
	}
	return fallback + " needs attention; see above."
}

// noticeTail says what the newest notice still owed a look is: a timer of
// eagent's own that came due, or a notice from the harness.
func (r *Runtime) noticeTail() string {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= r.turns.seen {
			break
		}
		if ev.Type == event.TurnEnd && ev.Actor == event.ActorEagent {
			// The wake of a turn that ended owing its next call ("error",
			// "not closing"; wakeFor): no notice is above, the turn's own
			// results are.
			var d event.TurnData
			if ev.Decode(&d) == nil && (d.Reason == "error" || d.Reason == "not closing") {
				return "Your last turn ended before it was done; carry on from what its calls returned above, and if a notice is above as well, do what it calls for."
			}
		}
		if ev.Type == event.ScheduleFire && ev.Actor == event.ActorHarness {
			return "A timer you set came due; the note you left yourself is above. Do what it says, and cancel the timer with cancel_schedule if it has done its job."
		}
	}
	return "A notice for you is above. Do what it calls for."
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
	b.WriteString("If the summary no longer says where things stand, set it with update_thread in this reply, alongside anything else you do. Set the title only if it is missing, generic, wrong, carries a status, or the session has become about something else. If both still fit, call update_thread with neither, so that you are not asked again.")
	return b.String()
}
