package state

import (
	"fmt"
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
	StateSince          time.Time
	StartSeq            int64
	Turns               int
	LastTurn            string
	LastTurnAt          time.Time
	Sends               int
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
		case prev != nil:
			// A new agent under the name of one that was stopped (a switch
			// back, a new start) is a new conversation. Only the usage
			// budget, which is the agent account's, carries over.
			r.Limit = prev.Limit
		default:
			s.RelayOrder = append(s.RelayOrder, d.Name)
		}
		s.Relays[d.Name] = r
	}
	if r == nil {
		return
	}
	r.LastEventAt = ev.Time
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
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		delete(r.Undelivered, d.ID)
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		r.State, r.Detail, r.StateSince = d.State, d.Detail, ev.Time
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		r.Recent = append(r.Recent, d.Text)
		if len(r.Recent) > 8 {
			r.Recent = r.Recent[len(r.Recent)-8:]
		}
		if r.State != "working" {
			r.State, r.StateSince = "working", ev.Time
		}
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		r.Turns++
		r.LastTurn, r.LastTurnAt = d.Text, ev.Time
		r.Recent = nil
		r.State, r.StateSince = "idle", ev.Time
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

func (s *State) relayWho(name string) string {
	if r := s.Relays[name]; r != nil {
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
		who := s.relayWho(d.Name)
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
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		if !d.OK {
			return fmt.Sprintf("[%s A message to %s (%s) could not be confirmed: %s]", ts, s.relayWho(d.Name), d.ID, clip(d.Detail, detailChars))
		}
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name)
		switch d.State {
		case "blocked":
			return fmt.Sprintf("[%s %s needs attention: %s]", ts, who, clip(d.Detail, detailChars))
		case "exited":
			return fmt.Sprintf("[%s %s exited. Its screen ended with:]\n%s", ts, who, clip(d.Detail, detailChars))
		}
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		return fmt.Sprintf("[%s] <%s> %s", ts, s.relayWho(d.Name), clip(strings.TrimSpace(d.Text), opts.RemarkChars))
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name)
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
		return fmt.Sprintf("[%s %s]", ts, s.relayLimitText(s.relayWho(d.Name), d))
	case event.RelayEnd:
		var d event.RelayEndData
		_ = ev.Decode(&d)
		if d.Reason != "" {
			return fmt.Sprintf("[%s Stopped %s (%s).]", ts, s.relayWho(d.Name), d.Reason)
		}
		return fmt.Sprintf("[%s Stopped %s.]", ts, s.relayWho(d.Name))
	case event.RelayAutopilot:
		var d event.RelayAutopilotData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name)
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
