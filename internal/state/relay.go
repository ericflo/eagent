package state

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Relay is a coding agent (Claude Code, Codex, Antigravity, pi) the session
// runs in a terminal of its own and passes the user's words to.
type Relay struct {
	Name, Profile, Agent, Label string
	// AgentLabel is the agent's short name ("Claude"), from relay.start.
	AgentLabel string
	// Model and Effort are the session's picks over the profile's ("" for
	// the profile's own).
	Model, Effort       string
	Dir, Host, Target   string
	Command             []string
	InnerID, Transcript string
	State               string // starting | working | idle | blocked | exited | stopped
	Detail              string
	// Dialog is the title of the dialog the agent reports holding it
	// blocked, from its last relay.state ("" for any other state, and in
	// records written before relay.state said it).
	Dialog     string
	StateSince time.Time
	StartSeq   int64
	// PrevEnded is when the agent that held this name before stopped, zero
	// for the name's first agent: what the harness saw before then was
	// that agent's, not this one's.
	PrevEnded  time.Time
	Turns      int
	LastTurn   string
	LastTurnAt time.Time
	Sends      int
	// LastSendFrom ("user" or "eagent") and LastSendAt describe the newest
	// message sent to the agent.
	LastSendFrom string
	LastSendAt   time.Time
	// Undelivered holds sends whose arrival the transcript has not shown
	// yet, by send id.
	Undelivered map[string]string
	// Recent is the agent's latest mid-turn remarks, newest last.
	Recent []string
	Limit  *event.RelayLimitData
	// Autopilot is the standing order to keep the agent going, nil when off.
	Autopilot *event.RelayAutopilotData
	Ended     bool
	// LastEventAt is the time of the newest relay event, so a reattaching
	// eagent knows which transcript lines are already on the record.
	LastEventAt time.Time
	// Read is how far the transcript ReadIn was read, from the newest relay
	// event that says (`read`); a reattach to the same transcript reads on
	// from there in place of LastEventAt. 0 in records written before.
	Read   int64
	ReadIn string
}

// Who names the agent for people: the short name relay.start recorded,
// else (older logs) the agent's name capitalised ("Codex"), else the
// relay's name.
func (r *Relay) Who() string {
	if r.AgentLabel != "" {
		return r.AgentLabel
	}
	if r.Agent != "" {
		return strings.ToUpper(r.Agent[:1]) + r.Agent[1:]
	}
	return r.Name
}

// Active reports whether the relay is still meant to be running.
func (r *Relay) Active() bool { return !r.Ended && r.State != "stopped" }

// ActiveRelays returns the relays not stopped on purpose, in start order.
func (s *State) ActiveRelays() []*Relay {
	var out []*Relay
	for _, name := range s.RelayOrder {
		if r := s.Relays[name]; r != nil && r.Active() {
			out = append(out, r)
		}
	}
	return out
}

// RelayWorking reports whether any relayed agent is in the middle of a turn.
func (s *State) RelayWorking() bool {
	for _, r := range s.ActiveRelays() {
		if r.State == "working" {
			return true
		}
	}
	return false
}

// SendOutcome is where a send got to, as the record tells it.
type SendOutcome string

const (
	// SendWaiting: queued or typed, and no word yet of its arrival.
	SendWaiting SendOutcome = "waiting"
	// SendHeld: sent after_turn, for the agent to read when its current
	// turn ends, and no word yet of its arrival.
	SendHeld SendOutcome = "held"
	// SendArrived: the agent's transcript showed it (or it was the agent's
	// own command, which is delivered once typed).
	SendArrived SendOutcome = "arrived"
	// SendNotSent: certainly nothing reached the agent.
	SendNotSent SendOutcome = "not_sent"
	// SendUnconfirmed: typed, and its arrival never shown. It may have
	// reached the agent.
	SendUnconfirmed SendOutcome = "unconfirmed"
)

// DeliveryOutcome reads a relay.delivered event. A failure that does not
// say how sure it is (a record from before failures said so, or a value
// this build does not know) may have reached the agent.
func DeliveryOutcome(d event.RelayDeliveredData) SendOutcome {
	switch {
	case d.OK:
		return SendArrived
	case d.Outcome == event.DeliveryNotSent:
		return SendNotSent
	}
	return SendUnconfirmed
}

// SendOutcome says where the send with this id got to; "" for an id the
// record does not have. A send that a switch passed on to another agent
// (relay.moved) is that agent's from then on, and this is its outcome
// there.
func (s *State) SendOutcome(id string) SendOutcome {
	return s.sendOutcomes[id]
}

func (s *State) noteSend(d event.RelaySendData) {
	if s.sendOutcomes == nil {
		s.sendOutcomes = map[string]SendOutcome{}
	}
	s.sendOutcomes[d.ID] = SendWaiting
	if d.Mode == "after_turn" {
		s.sendOutcomes[d.ID] = SendHeld
	}
}

// HeldNeverTyped is how a send in NeverTyped went (relay.delivered's detail).
const HeldNeverTyped = "it was held for a start of the agent, and eagent stopped before the agent came up: it was never typed"

// NeverTyped are the sends without an outcome that the record itself shows
// were never typed, in the order they were sent: each was held for a start
// of its agent (relay.send's held), and the record has neither a start
// under its name after it nor its passing on to the agent taking over
// (relay.moved), so whoever wrote the record stopped before that agent
// came up. It is the one thing the record says of when a send is typed.
func NeverTyped(evs []event.Event) []event.RelaySendData {
	var held []event.RelaySendData
	drop := func(match func(event.RelaySendData) bool) {
		held = slices.DeleteFunc(held, match)
	}
	for _, ev := range evs {
		switch ev.Type {
		case event.RelaySend:
			var d event.RelaySendData
			if ev.Decode(&d) == nil && d.Held {
				held = append(held, d)
			}
		case event.RelayMoved, event.RelayDelivered:
			var d struct {
				ID string `json:"id"`
			}
			if ev.Decode(&d) == nil {
				drop(func(h event.RelaySendData) bool { return h.ID == d.ID })
			}
		case event.RelayStart:
			var d event.RelayStartData
			if ev.Decode(&d) == nil {
				drop(func(h event.RelaySendData) bool { return h.Name == d.Name })
			}
		}
	}
	return held
}

// NotTyped is the outcome of a send NeverTyped lists.
func NotTyped(d event.RelaySendData) event.RelayDeliveredData {
	return event.RelayDeliveredData{Name: d.Name, ID: d.ID, OK: false, Outcome: event.DeliveryNotSent, Detail: HeldNeverTyped}
}

// NextRelaySendID allocates a send id that continues after replay.
func (s *State) NextRelaySendID() string {
	s.relaySendSeq++
	return fmt.Sprintf("r%d", s.relaySendSeq)
}

// NeedsAttention reports whether a relay event is one eagent must look at
// now rather than at its next progress look: a finished turn, a usage limit,
// a dialog the agent is stuck on, an exit, a message that did not arrive.
func NeedsAttention(ev event.Event) bool {
	switch ev.Type {
	case event.RelayTurn, event.RelayLimit:
		return true
	case event.RelayState:
		var d event.RelayStateData
		return ev.Decode(&d) == nil && (d.State == "blocked" || d.State == "exited")
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		return ev.Decode(&d) == nil && !d.OK
	}
	return false
}

func (s *State) applyRelay(ev event.Event) {
	if s.Relays == nil {
		s.Relays = map[string]*Relay{}
	}
	var name struct {
		Name string `json:"name"`
	}
	_ = ev.Decode(&name)
	r := s.Relays[name.Name]
	if ev.Type == event.RelayStart {
		var d event.RelayStartData
		_ = ev.Decode(&d)
		prev := r
		if s.relayHolders == nil {
			s.relayHolders = map[string][]*Relay{}
		}
		r = &Relay{Name: d.Name, Profile: d.Profile, Agent: d.Agent, AgentLabel: d.AgentLabel, Label: d.Label, Model: d.Model, Effort: d.Effort, Dir: d.Dir, Host: d.Host, Target: d.Target, Command: d.Command,
			InnerID: d.InnerID, Transcript: d.Transcript, State: "starting", StateSince: ev.Time, StartSeq: ev.Seq, Undelivered: map[string]string{}}
		switch {
		case prev != nil && (d.Resumed || !prev.Ended):
			// A restart continues the same relay: its history stays. That
			// is a start of a relay that was never stopped (a restart or a
			// model change), or one that continues its own conversation.
			r.Turns, r.LastTurn, r.LastTurnAt, r.Sends, r.LastSendFrom, r.LastSendAt, r.Recent, r.Limit, r.Autopilot = prev.Turns, prev.LastTurn, prev.LastTurnAt, prev.Sends, prev.LastSendFrom, prev.LastSendAt, prev.Recent, prev.Limit, prev.Autopilot
			if r.InnerID == "" {
				r.InnerID = prev.InnerID
			}
			r.PrevEnded = prev.PrevEnded
			// Its earlier events are still its own: the name is held from
			// its first start, not from this restart.
			r.StartSeq = prev.StartSeq
			if h := s.relayHolders[d.Name]; len(h) > 0 {
				h[len(h)-1] = r
			}
			s.keepHeld(r, prev)
		case prev != nil:
			// A new agent under the name of one that was stopped (a switch
			// back, a new start) is a new conversation. Only the usage
			// budget, which is the agent account's, carries over, and the
			// sends held for this start.
			r.Limit = prev.Limit
			r.PrevEnded = prev.StateSince
			s.relayHolders[d.Name] = append(s.relayHolders[d.Name], r)
			s.keepHeld(r, prev)
		default:
			s.RelayOrder = append(s.RelayOrder, d.Name)
			s.relayHolders[d.Name] = append(s.relayHolders[d.Name], r)
		}
		s.Relays[d.Name] = r
	}
	if r == nil {
		return
	}
	r.LastEventAt = ev.Time
	var pos struct {
		Read int64 `json:"read"`
	}
	if ev.Decode(&pos) == nil && pos.Read > 0 {
		r.Read, r.ReadIn = pos.Read, r.Transcript
	}
	switch ev.Type {
	case event.RelayInner:
		var d event.RelayInnerData
		_ = ev.Decode(&d)
		r.InnerID, r.Transcript = d.InnerID, d.Transcript
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		bump(&s.relaySendSeq, d.ID, "r")
		r.Sends++
		r.LastSendFrom, r.LastSendAt = d.From, ev.Time
		r.Undelivered[d.ID] = d.Text
		s.noteSend(d)
	case event.RelayMoved:
		// The send is the new relay's from here on; the relay it was first
		// sent to no longer waits for it.
		var d event.RelayMovedData
		_ = ev.Decode(&d)
		bump(&s.relaySendSeq, d.ID, "r")
		if prev := s.Relays[d.Prev]; prev != nil && prev != r {
			delete(prev.Undelivered, d.ID)
		}
		r.Sends++
		r.LastSendFrom, r.LastSendAt = d.From, ev.Time
		r.Undelivered[d.ID] = d.Text
		s.noteSend(d.RelaySendData)
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		// A send has one outcome, whichever agent under the name it was
		// sent to: none that held the name waits for it any more.
		delete(r.Undelivered, d.ID)
		for _, h := range s.relayHolders[r.Name] {
			delete(h.Undelivered, d.ID)
		}
		if was, known := s.sendOutcomes[d.ID]; known {
			// The newest word on a send is how it ended: a late
			// confirmation after one that gave up means it arrived.
			if d.OK && (was == SendUnconfirmed || was == SendNotSent) {
				if s.lateArrivals == nil {
					s.lateArrivals = map[int64]bool{}
				}
				s.lateArrivals[ev.Seq] = true
			}
			s.sendOutcomes[d.ID] = DeliveryOutcome(d)
		}
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		r.State, r.Detail, r.Dialog, r.StateSince = d.State, d.Detail, d.Dialog, ev.Time
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		r.Recent = append(r.Recent, d.Text)
		if len(r.Recent) > 8 {
			r.Recent = r.Recent[len(r.Recent)-8:]
		}
		// A blocked agent stays blocked until its seat says otherwise
		// (relay.state): pi still writes under a dialog it reports. One
		// blocked for going quiet or a slow start is said working by its
		// seat before it writes again.
		if r.State != "working" && r.State != "blocked" {
			r.State, r.StateSince = "working", ev.Time
		}
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		r.Turns++
		r.LastTurn, r.LastTurnAt = d.Text, ev.Time
		r.Recent = nil
		// A turn that ends under a dialog the agent reports leaves it
		// blocked: the dialog still holds it, and its seat says when it no
		// longer does (relay.state). Any other turn makes it idle, a quiet
		// agent's among them (its idle, which follows, is not recorded).
		if r.State != "blocked" || r.Dialog == "" {
			r.State, r.StateSince = "idle", ev.Time
		}
	case event.RelayLimit:
		var d event.RelayLimitData
		_ = ev.Decode(&d)
		r.Limit = &d
	case event.RelayAutopilot:
		var d event.RelayAutopilotData
		_ = ev.Decode(&d)
		if d.Off {
			r.Autopilot = nil
		} else {
			r.Autopilot = &d
		}
	case event.RelayEnd:
		r.Ended = true
		r.State, r.StateSince = "stopped", ev.Time
		r.Autopilot = nil
	}
	switch ev.Type {
	case event.RelayStart, event.RelayTurn:
		s.lastMoveSeq = ev.Seq
	case event.RelayState:
		if r.State == "blocked" || r.State == "exited" {
			s.lastMoveSeq = ev.Seq
		}
	}
}

// keepHeld keeps r, a new start of prev's name, waiting on the sends held
// for it. A restart of a relay not stopped keeps all prev waits on (the
// harness settles the rest before the start); after a stop, only what was
// sent to the name since, in this run.
func (s *State) keepHeld(r, prev *Relay) {
	if !prev.Ended {
		maps.Copy(r.Undelivered, prev.Undelivered)
		return
	}
	for i := len(s.Events) - 1; i >= 0 && len(prev.Undelivered) > 0; i-- {
		ev := s.Events[i]
		if ev.Type == event.SessionResume {
			return
		}
		var d event.RelaySendData
		if ev.Type != event.RelaySend && ev.Type != event.RelayEnd || ev.Decode(&d) != nil || d.Name != r.Name {
			continue
		}
		if ev.Type == event.RelayEnd {
			return
		}
		if text, ok := prev.Undelivered[d.ID]; ok {
			// The new agent's from here on: the stopped one no longer
			// waits for it.
			r.Undelivered[d.ID] = text
			delete(prev.Undelivered, d.ID)
			if ev.Seq < r.StartSeq {
				// Held for a new agent under the name (a restart on its own
				// conversation holds it from its first start already): the
				// send is that agent's own, and it holds the name from it.
				r.StartSeq, r.Sends = ev.Seq, r.Sends+1
				if r.LastSendAt.IsZero() {
					r.LastSendFrom, r.LastSendAt = d.From, ev.Time
				}
			}
		}
	}
}

// Awaited is how many sends to r are still expected to be confirmed. An
// agent that has exited or been stopped confirms nothing more, so nothing
// is awaited from it, whatever the record still lists as unconfirmed: a
// decision that waits for sends to arrive must not wait on an agent that
// is gone.
func (r *Relay) Awaited() int {
	if r.Ended || r.State == "exited" {
		return 0
	}
	return len(r.Undelivered)
}

// RelayAt returns the relay that held a name when the event at seq was
// written: the name can be taken again by a new agent (a custom profile now
// backed by another program, a switch back), and what is said about an
// older event must not change when that happens. A restart continues the
// same relay, so it is the relay as it is now. Nil for an unknown name.
func (s *State) RelayAt(name string, seq int64) *Relay {
	h := s.relayHolders[name]
	for i := len(h) - 1; i > 0; i-- {
		if h[i].StartSeq <= seq {
			return h[i]
		}
	}
	if len(h) > 0 {
		return h[0]
	}
	return s.Relays[name]
}

func (s *State) relayWho(name string, seq int64) string {
	if r := s.RelayAt(name, seq); r != nil {
		return r.Who()
	}
	return name
}

// relayNotice renders a relay event as a line of eagent's conversation.
// What an agent said is written under its name, the way the user's messages
// are written under theirs; everything else is a bracketed notice.
func (s *State) relayNotice(ev event.Event, opts ViewOptions) string {
	ts := s.clock(ev.Time)
	switch ev.Type {
	case event.RelayStart:
		var d event.RelayStartData
		_ = ev.Decode(&d)
		verb := "Started"
		if d.Resumed {
			verb = "Restarted"
		}
		line := fmt.Sprintf("[%s %s %s in %s", ts, verb, firstNonEmpty(d.Label, d.Agent), d.Dir)
		if d.Resumed {
			line += ", continuing its own session"
		}
		if d.Name != d.Agent {
			line += fmt.Sprintf(`; it is called %q here`, d.Name)
		}
		return line + ".]"
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		if !d.Auto {
			return "" // sent by a tool call of eagent's own, whose result says so
		}
		who := s.relayWho(d.Name, ev.Seq)
		how := "."
		switch d.Mode {
		case "interrupt":
			how = ", interrupting what it was doing."
		case "after_turn":
			how = ", to be read when its current turn ends."
		}
		if d.From == "user" {
			return fmt.Sprintf("[%s Passed to %s as written%s]", ts, who, how)
		}
		return fmt.Sprintf("[%s eagent sent %s this itself%s]\n%s", ts, who, how, clip(d.Text, opts.RemarkChars))
	case event.RelayMoved:
		var d event.RelayMovedData
		_ = ev.Decode(&d)
		whose := "Your message"
		if d.From == "user" {
			whose = "The user's message"
		}
		return fmt.Sprintf("[%s %s (%s) to %s waited for the handover; it goes to %s instead.]", ts, whose, d.ID, s.relayWho(d.Prev, ev.Seq), s.relayWho(d.Name, ev.Seq))
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		switch DeliveryOutcome(d) {
		case SendArrived:
			// Said only where the conversation holds an earlier word that
			// it did not (a stop settled it before its terminal's last
			// look was in). Where it arrived is all that is known: the
			// agent it was typed into may be gone, or started again with
			// the message's turn cut off.
			if s.lateArrivals[ev.Seq] {
				return fmt.Sprintf("[%s The message to %s (%s) did arrive in the agent it was typed into, though an earlier word here said otherwise. If that agent was stopped, started again or replaced since, check whether the one running now is acting on it before sending it again.]", ts, s.relayWho(d.Name, ev.Seq), d.ID)
			}
		case SendNotSent:
			// Only records that say so: what older ones rendered stays.
			return fmt.Sprintf("[%s A message to %s (%s) did not go; nothing of it reached the agent: %s]", ts, s.relayWho(d.Name, ev.Seq), d.ID, clip(d.Detail, detailChars))
		case SendUnconfirmed:
			return fmt.Sprintf("[%s A message to %s (%s) could not be confirmed: %s]", ts, s.relayWho(d.Name, ev.Seq), d.ID, clip(d.Detail, detailChars))
		}
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name, ev.Seq)
		switch d.State {
		case "blocked":
			return fmt.Sprintf("[%s %s needs attention: %s]", ts, who, clip(d.Detail, detailChars))
		case "exited":
			return fmt.Sprintf("[%s %s exited. Its screen ended with:]\n%s", ts, who, clip(d.Detail, detailChars))
		}
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[%s] <%s> %s", ts, s.relayWho(d.Name, ev.Seq), clip(strings.TrimSpace(d.Text), opts.RemarkChars))
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name, ev.Seq)
		head := fmt.Sprintf("[%s %s finished a turn", ts, who)
		if d.Interrupted {
			head = fmt.Sprintf("[%s %s's turn was interrupted", ts, who)
		}
		if d.DurationMS > 0 {
			head += " after " + fmtMillis(d.DurationMS)
		}
		if d.Tools > 0 {
			head += fmt.Sprintf(", %d tool calls", d.Tools)
		}
		head += ". Its report:]"
		text := strings.TrimSpace(d.Text)
		if text == "" {
			text = "(it ended the turn without a message)"
		}
		out := head + "\n" + fmt.Sprintf("<%s> %s", who, clip(text, opts.ReportChars))
		if len(d.Images) > 0 {
			out += "\n[Pictures it made or looked at: " + strings.Join(d.Images, ", ") + "]"
		}
		if d.Idle {
			out += "\n[It ran nothing in reply to what you sent. If it is waiting on work of its own (a command, a render, a timer it set, a helper), do not send again: it reports when that work does, and each message only starts another turn like this one.]"
		}
		return out
	case event.RelayLimit:
		var d event.RelayLimitData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[%s %s]", ts, s.relayLimitText(s.relayWho(d.Name, ev.Seq), d))
	case event.RelayEnd:
		var d event.RelayEndData
		_ = ev.Decode(&d)
		if d.Reason != "" {
			return fmt.Sprintf("[%s Stopped %s (%s).]", ts, s.relayWho(d.Name, ev.Seq), d.Reason)
		}
		return fmt.Sprintf("[%s Stopped %s.]", ts, s.relayWho(d.Name, ev.Seq))
	case event.RelayAutopilot:
		var d event.RelayAutopilotData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name, ev.Seq)
		if d.Off {
			return fmt.Sprintf("[%s Autopilot for %s is off.]", ts, who)
		}
		until := "until the user says stop"
		if t, err := time.Parse(time.RFC3339, d.Until); err == nil {
			until = "until " + t.In(s.Location()).Format("Mon 15:04")
		}
		decide := ""
		if !d.Decide {
			decide = " Its questions still go to the user."
		}
		return fmt.Sprintf("[%s Autopilot is on for %s, %s.%s The standing order:]\n%s", ts, who, until, decide, clip(d.Instruction, opts.RemarkChars))
	}
	return ""
}

// detailChars bounds what eagent is shown of an agent's screen when it is
// stuck, has exited, or a message to it could not be confirmed: all of what
// the harness kept (the last lines of the screen), as the model that
// decides what to do about it always had.
const detailChars = 2000

// RelayLimitText says where an agent's usage budget stands, for people.
func (s *State) RelayLimitText(who string, d event.RelayLimitData) string {
	return s.relayLimitText(who, d)
}

func (s *State) relayLimitText(who string, d event.RelayLimitData) string {
	if d.Text != "" {
		return fmt.Sprintf("%s usage limit: %s", who, d.Text)
	}
	text := fmt.Sprintf("%s has used %.0f%% of its", who, d.UsedPercent)
	if d.Window != "" {
		text += " " + d.Window
	}
	text += " usage budget"
	if d.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339, d.ResetsAt); err == nil {
			text += "; it resets " + t.In(s.Location()).Format("Mon Jan 2 15:04")
		}
	}
	return text
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
