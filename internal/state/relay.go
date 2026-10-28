package state

import (
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Relay is an external coding agent (Codex, Claude Code) the session runs
// in a terminal of its own and passes the user's words to. Its existence in
// the log, not anything a model remembers, is what makes a session a relay.
type Relay struct {
	Name, Profile, Agent, Label string
	Dir, Host, Target           string
	Command                     []string
	InnerID, Transcript         string
	State                       string // starting | working | idle | blocked | exited | stopped
	Detail                      string
	StateSince                  time.Time
	StartSeq                    int64
	Turns                       int
	LastTurn                    string
	LastTurnAt                  time.Time
	Sends                       int
	// Undelivered holds sends whose arrival the transcript has not shown
	// yet, by send id.
	Undelivered map[string]string
	// Recent is the agent's latest mid-turn remarks, newest last.
	Recent []string
	Limit  *event.RelayLimitData
	Ended  bool
	// LastEventAt is the time of the newest relay event, so a reattaching
	// eagent knows which transcript lines are already on the record.
	LastEventAt time.Time
}

// Who names the agent for people: its label's program name or its name.
func (r *Relay) Who() string {
	switch r.Agent {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	}
	return r.Name
}

// Active reports whether the relay is still meant to be running.
func (r *Relay) Active() bool { return !r.Ended && r.State != "stopped" }

// RelayMode reports whether this session is a relay session: a relay was
// started in it, whether or not it is still running.
func (s *State) RelayMode() bool { return len(s.RelayOrder) > 0 }

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

// relayWakes lists relay events the orchestrator must act on.
func relayWakes(ev event.Event) bool {
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

// RelayWakes reports whether a relay event should wake the orchestrator.
func RelayWakes(ev event.Event) bool { return relayWakes(ev) }

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
		r = &Relay{Name: d.Name, Profile: d.Profile, Agent: d.Agent, Label: d.Label, Dir: d.Dir, Host: d.Host, Target: d.Target, Command: d.Command,
			InnerID: d.InnerID, Transcript: d.Transcript, State: "starting", StateSince: ev.Time, StartSeq: ev.Seq, Undelivered: map[string]string{}}
		if prev != nil {
			// A restart continues the same relay: its history stays.
			r.Turns, r.LastTurn, r.LastTurnAt, r.Sends, r.Recent, r.Limit = prev.Turns, prev.LastTurn, prev.LastTurnAt, prev.Sends, prev.Recent, prev.Limit
			if r.InnerID == "" {
				r.InnerID = prev.InnerID
			}
		} else {
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
	case event.RelayEnd:
		r.Ended = true
		r.State, r.StateSince = "stopped", ev.Time
	}
	if relayWakes(ev) {
		s.wake(ev.Seq)
	}
}

func (s *State) relayWho(name string) string {
	if r := s.Relays[name]; r != nil {
		return r.Who()
	}
	return name
}

// relayNotification renders relay events the orchestrator must hear about.
func (s *State) relayNotification(ev event.Event) string {
	switch ev.Type {
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name)
		head := fmt.Sprintf("[%s finished a turn", who)
		if d.Interrupted {
			head = fmt.Sprintf("[%s's turn was interrupted", who)
		}
		if d.DurationMS > 0 {
			head += " after " + fmtMillis(d.DurationMS)
		}
		if d.Tools > 0 {
			head += fmt.Sprintf(", %d tool calls", d.Tools)
		}
		head += "]"
		if len(d.Images) > 0 {
			head += "\nPictures it made or looked at: " + strings.Join(d.Images, ", ")
		}
		text := strings.TrimSpace(d.Text)
		if text == "" {
			text = "(it ended the turn without a message)"
		}
		return head + "\n" + text
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		who := s.relayWho(d.Name)
		switch d.State {
		case "blocked":
			return fmt.Sprintf("[%s needs attention: %s]\nLook with relay_screen; answer a dialog with relay_control keys, or tell the user what it is asking.", who, d.Detail)
		case "exited":
			return fmt.Sprintf("[%s exited.]\nIts screen ended with:\n%s\nrelay_control restart starts it again and continues its own session.", who, d.Detail)
		}
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		if !d.OK {
			return fmt.Sprintf("[A message to %s (%s) could not be confirmed: %s]", s.relayWho(d.Name), d.ID, d.Detail)
		}
	case event.RelayLimit:
		var d event.RelayLimitData
		_ = ev.Decode(&d)
		return "[" + relayLimitText(s.relayWho(d.Name), d) + "]"
	}
	return ""
}

func relayLimitText(who string, d event.RelayLimitData) string {
	if d.Text != "" {
		return fmt.Sprintf("%s usage limit: %s", who, d.Text)
	}
	s := fmt.Sprintf("%s has used %.0f%% of its", who, d.UsedPercent)
	if d.Window != "" {
		s += " " + d.Window
	}
	s += " usage budget"
	if d.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339, d.ResetsAt); err == nil {
			s += "; it resets " + t.Local().Format("Mon Jan 2 15:04")
		}
	}
	return s
}

// observeRelay renders relay events for the narrator.
func observeRelay(ev event.Event, maxChars int, ts string) string {
	switch ev.Type {
	case event.RelayStart:
		var d event.RelayStartData
		_ = ev.Decode(&d)
		verb := "started"
		if d.Resumed {
			verb = "restarted (continuing its own session)"
		}
		return fmt.Sprintf("%s %s %s in %s", ts, verb, firstNonEmpty(d.Label, d.Agent), d.Dir)
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		how := ""
		switch d.Mode {
		case "interrupt":
			how = ", interrupting it"
		case "after_turn":
			how = ", for when its current turn ends"
		}
		if d.From == "user" {
			return fmt.Sprintf("%s -> forwarded the user's message to %s verbatim%s", ts, d.Name, how)
		}
		return fmt.Sprintf("%s -> sent to %s%s (written by me, mirrored to the user): %s", ts, d.Name, how, clip(d.Text, maxChars))
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		if !d.OK {
			return fmt.Sprintf("%s a message to %s could not be confirmed: %s", ts, d.Name, clip(d.Detail, 300))
		}
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		if d.State == "working" || d.State == "idle" {
			return "" // turns and remarks say this better
		}
		return fmt.Sprintf("%s %s is %s: %s", ts, d.Name, d.State, clip(d.Detail, 600))
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s %s (working): %s", ts, d.Name, clip(d.Text, maxChars))
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		head := fmt.Sprintf("%s %s FINISHED A TURN", ts, strings.ToUpper(d.Name))
		if d.Interrupted {
			head = fmt.Sprintf("%s %s'S TURN WAS INTERRUPTED", ts, strings.ToUpper(d.Name))
		}
		if d.DurationMS > 0 {
			head += " after " + fmtMillis(d.DurationMS)
		}
		if len(d.Images) > 0 {
			head += "; pictures (attach the ones worth seeing): " + strings.Join(d.Images, ", ")
		}
		return head + ". Its own report:\n" + clip(strings.TrimSpace(d.Text), maxChars*4)
	case event.RelayLimit:
		var d event.RelayLimitData
		_ = ev.Decode(&d)
		return ts + " " + relayLimitText(d.Name, d)
	case event.RelayEnd:
		var d event.RelayEndData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s stopped %s (%s)", ts, d.Name, d.Reason)
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
