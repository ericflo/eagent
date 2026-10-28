package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// relaySeat is what the harness needs from a running relay; relay.Seat is
// the real one and tests substitute a fake.
type relaySeat interface {
	Events() <-chan relay.Event
	Send(id, text, mode string) (string, error)
	Interrupt() error
	Keys(keys ...string) error
	Screen(lines int) (string, error)
	Kill() error
	Close()
	Snapshot() relay.Status
}

// launchRelay and attachRelay start or reattach an agent; tests replace them.
var launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
	s, started, err := relay.Launch(spec, innerID, resume)
	if err != nil {
		return nil, started, err
	}
	return s, started, nil
}

var attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
	s, err := relay.Attach(spec, host, target, innerID, transcript, since)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// relaySeats holds the running relays. Tools reach them from actor
// goroutines, so the map has its own lock; the log stays loop-owned.
type relaySeats struct {
	mu    sync.Mutex
	seats map[string]relaySeat
	// activity is what each agent is doing right now (not logged).
	activity map[string]relayActivity
}

type relayActivity struct {
	text string
	at   time.Time
}

func (s *relaySeats) get(name string) relaySeat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seats[name]
}

func (s *relaySeats) put(name string, seat relaySeat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seats == nil {
		s.seats = map[string]relaySeat{}
	}
	s.seats[name] = seat
}

func (s *relaySeats) drop(name string, seat relaySeat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seats[name] == seat {
		delete(s.seats, name)
	}
}

func (s *relaySeats) all() map[string]relaySeat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]relaySeat{}
	for k, v := range s.seats {
		out[k] = v
	}
	return out
}

// relaySpec builds the launch description for a profile. Loop goroutine
// (or before the loop starts).
func (r *Runtime) relaySpec(name, profile, dir string) (relay.Spec, error) {
	p, ok := r.cfg.Relay.RelayProfiles()[profile]
	if !ok {
		return relay.Spec{}, fmt.Errorf("no relay profile %q (have %s)", profile, strings.Join(r.cfg.Relay.RelayProfileNames(), ", "))
	}
	spec := relay.Spec{Session: r.sess.ID, Name: name, Profile: p, Dir: dir}
	if p.Agent == "claude" {
		// Claude Code's Stop hook hands over messages held for the end of a
		// turn; it is this binary, reading the relay's spool.
		spec.Spool = filepath.Join(r.sess.Path, "relays", name, "after_turn")
		if exe, err := os.Executable(); err == nil {
			spec.Hook = relay.ShellQuote(exe) + " relay-hook " + relay.ShellQuote(spec.Spool)
		}
	}
	return spec, nil
}

// startRelay launches an agent and makes the session a relay to it. resume
// continues the named relay's own session (a restart). Loop goroutine (or
// before the loop starts).
func (r *Runtime) startRelay(profile, dir, name string, resume bool) (string, error) {
	if name == "" {
		name = profile
		for i := 2; r.st.Relays[name] != nil && r.st.Relays[name].Active(); i++ {
			name = fmt.Sprintf("%s-%d", profile, i)
		}
	}
	if dir == "" {
		dir = r.workDir("")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	spec, err := r.relaySpec(name, profile, dir)
	if err != nil {
		return "", err
	}
	innerID := ""
	if prev := r.st.Relays[name]; prev != nil && resume {
		innerID = prev.InnerID
	}
	seat, started, err := launchRelay(spec, innerID, resume && innerID != "")
	if err != nil {
		return "", err
	}
	r.append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{
		Name: name, Profile: profile, Agent: spec.Profile.Agent, Label: spec.Profile.Label, Dir: dir,
		Host: started.Host, Target: started.Target, Command: started.Command, InnerID: started.InnerID, Resumed: resume && innerID != "",
	}))
	r.watchSeat(name, seat)
	return name, nil
}

// watchSeat pumps a seat's observations into the log.
func (r *Runtime) watchSeat(name string, seat relaySeat) {
	r.relays.put(name, seat)
	go func() {
		for ev := range seat.Events() {
			ev := ev
			r.post(func() { r.onRelayEvent(name, ev) })
		}
		r.post(func() { r.relays.drop(name, seat) })
	}()
}

// onRelayEvent records what a seat saw. Loop goroutine only.
func (r *Runtime) onRelayEvent(name string, ev relay.Event) {
	if r.ending {
		return
	}
	switch ev.Kind {
	case relay.KindInner:
		r.append(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: name, InnerID: ev.InnerID, Transcript: ev.Transcript}))
	case relay.KindState:
		if cur := r.st.Relays[name]; cur != nil && cur.State == ev.State && ev.Detail == "" {
			return
		}
		r.append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: name, State: ev.State, Detail: clipRunes(ev.Detail, 2000)}))
	case relay.KindDelivered:
		r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: ev.SendID, OK: ev.OK, Detail: clipRunes(ev.Detail, 2000)}))
	case relay.KindProgress:
		r.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: name, Text: clipRunes(ev.Text, 2000)}))
	case relay.KindTurn:
		r.append(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: name, Text: clipRunes(ev.Text, 20000), DurationMS: ev.DurationMS, Tools: ev.Tools, Images: ev.Images, Interrupted: ev.Interrupted}))
		r.relays.mu.Lock()
		delete(r.relays.activity, name)
		r.relays.mu.Unlock()
	case relay.KindLimit:
		r.append(event.New(event.RelayLimit, event.ActorHarness, event.RelayLimitData{Name: name, UsedPercent: ev.UsedPercent, Window: ev.Window, ResetsAt: ev.ResetsAt, Text: ev.Text, Exhausted: ev.Exhausted}))
	case relay.KindActivity:
		r.relays.mu.Lock()
		if r.relays.activity == nil {
			r.relays.activity = map[string]relayActivity{}
		}
		r.relays.activity[name] = relayActivity{text: ev.Text, at: time.Now()}
		r.relays.mu.Unlock()
		if r.phone != nil {
			r.phone.kick()
		}
	}
}

// reattachRelays picks up agents a previous run left running. Called from
// resume, before the loop starts.
func (r *Runtime) reattachRelays() []string {
	var lost []string
	for _, rel := range r.st.ActiveRelays() {
		if rel.State == "exited" {
			continue
		}
		spec, err := r.relaySpec(rel.Name, rel.Profile, rel.Dir)
		if err == nil {
			var seat relaySeat
			if seat, err = attachRelay(spec, rel.Host, rel.Target, rel.InnerID, rel.Transcript, rel.LastEventAt); err == nil {
				r.watchSeat(rel.Name, seat)
				continue
			}
		}
		r.append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: rel.Name, State: "exited", Detail: "it was not running when eagent resumed (" + err.Error() + ")"}))
		lost = append(lost, fmt.Sprintf("relay %s (%s) was no longer running", rel.Name, rel.Who()))
	}
	return lost
}

// closeRelays stops watching every relay; the agents keep running so a
// resumed session can reattach.
func (r *Runtime) closeRelays() {
	for _, seat := range r.relays.all() {
		seat.Close()
	}
}

// ---- routing --------------------------------------------------------------

// addressedToEagent reports a user message meant for eagent itself rather
// than the relayed agent.
func addressedToEagent(text string) bool {
	l := strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"@e ", "@e:", "@e,", "@eagent", "eagent:", "eagent,"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	for _, phrase := range []string{"not for relay", "not for the relay", "not to relay", "don't relay", "do not relay", "not for codex", "not for claude", "this can be you", "between you and me", "you, not codex", "you, not claude", "you (not codex)", "you (not claude)", "can be you and not relayed"} {
		if strings.Contains(l, phrase) {
			return true
		}
	}
	return false
}

// relayMeta reports a short message about the relay itself (its progress,
// its terminal, the update cadence) rather than work for the agent: the
// orchestrator answers or acts on those, so they are not forwarded on
// arrival.
func relayMeta(text string) bool {
	l := strings.ToLower(strings.TrimSpace(text))
	if len(l) > 200 {
		return false // a long message is work, even if it also asks how things are going
	}
	for _, phrase := range []string{
		"how's it going", "hows it going", "how is it going", "how's it looking", "how are things", "how far along", "any update", "what's the update", "whats the update", "status update", "status?",
		"is it stuck", "it's not stuck", "sure it's not stuck", "is it done", "did it finish", "is it working", "what's taking so long", "whats taking so long", "why is it taking so long", "taking so long",
		"what is it doing", "what's it doing", "whats it doing", "what is codex doing", "what's codex doing", "what is claude doing", "what's claude doing",
		"what did you send", "did you send", "send me the prompt", "is this you", "you have claude doing", "you have codex doing", "are both screens", "you're a relay", "you are a relay", "are you there",
		"restart the session", "restart codex", "restart claude", "restart it", "upgrade codex", "upgrade claude", "update codex", "update claude", "upgrade and then restart", "update and then restart",
		"close the screen", "kill the screen", "kill screen", "close out the screen", "stop monitoring", "stop polling", "stop updating", "stop the updates", "updates every", "update me every", "give me updates", "regular updates", "less often", "more often",
	} {
		if strings.Contains(l, phrase) {
			return true
		}
	}
	return false
}

// relayUrgent reports a message that should stop the agent rather than
// wait for its next step: "Stop", "Wait, …", "Don't …", "Never mind".
func relayUrgent(text string) bool {
	t := strings.TrimSpace(text)
	if strings.Contains(t, "STOP") || strings.Contains(t, "WAIT") {
		return true
	}
	l := strings.ToLower(t)
	for _, p := range []string{"stop", "wait", "hold on", "hold up", "pause", "don't", "dont ", "do not", "no!", "no,", "no.", "nope", "never mind", "nevermind", "cancel", "abort", "undo", "revert", "interrupt"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// autoForward sends a user message to the relayed agent the moment it
// arrives, verbatim, so the agent hears it without waiting for an
// orchestrator call (which may be busy with something else). Messages for
// eagent itself, questions about the relay, answers, and messages when more
// than one relay is running are left to the orchestrator. Loop goroutine.
func (r *Runtime) autoForward(seq int64) {
	if r.ending {
		return
	}
	active := r.st.ActiveRelays()
	if len(active) != 1 {
		return
	}
	rel := active[0]
	if rel.State == "exited" || forwardedAs(r.st, rel.Name, seq) != "" {
		return
	}
	var text string
	var atts []event.Attachment
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq != seq {
			continue
		}
		if ev.Type != event.UserMessage {
			return // answers carry the question's context; the orchestrator routes them
		}
		var d event.UserMessageData
		_ = ev.Decode(&d)
		text, atts = d.Text, d.Attachments
		break
	}
	if strings.TrimSpace(text) == "" && len(atts) == 0 || addressedToEagent(text) || relayMeta(text) {
		return
	}
	seat := r.relays.get(rel.Name)
	if seat == nil {
		return
	}
	for _, a := range atts {
		text += fmt.Sprintf("\n\n[The user attached %s: %s]", a.Name, a.Path)
	}
	mode := "queue"
	if relayUrgent(text) {
		mode = "interrupt"
	}
	id := r.st.NextRelaySendID()
	r.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: rel.Name, ID: id, From: "user", Seq: seq, Mode: mode, Text: text, Auto: true}))
	if r.userWaitSeq == seq {
		// Passing it on is the reaction the user waits for: the narrator can
		// say it reached the agent now.
		r.userWaitSeq = 0
		r.wakeNarrator(wakeUser)
	}
	name := rel.Name
	go func() {
		// Typing can wait on the agent's startup screens; never on the loop.
		if _, err := seat.Send(id, text, mode); err != nil {
			r.post(func() {
				r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: id, OK: false, Detail: err.Error()}))
			})
		}
	}()
}

// userEventText returns the text of a user message or answer by seq.
func userEventText(st *state.State, seq int64) (string, []event.Attachment, bool) {
	for i := len(st.Events) - 1; i >= 0; i-- {
		ev := st.Events[i]
		if ev.Seq != seq {
			continue
		}
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			return d.Text, d.Attachments, true
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			return d.Text, d.Attachments, true
		}
		return "", nil, false
	}
	return "", nil, false
}

// relayUnrouted reports that the user's latest message was for the agent
// and has not been sent to it. Loop goroutine only.
func (r *Runtime) relayUnrouted() bool {
	if !r.relayStrict() {
		return false
	}
	for _, rel := range r.st.ActiveRelays() {
		if forwardedAs(r.st, rel.Name, r.st.LastUserSeq) != "" {
			return false
		}
	}
	return true
}

// relayStrict reports that the orchestrator must not do work itself: a
// relay is running and the user's latest message was for the agent. Loop
// goroutine only.
func (r *Runtime) relayStrict() bool {
	if len(r.st.ActiveRelays()) == 0 {
		return false
	}
	text, _, _ := userEventText(r.st, r.st.LastUserSeq)
	return !addressedToEagent(text)
}

// resolveRelay picks the relay a tool call means. Loop goroutine only.
func (r *Runtime) resolveRelay(name string) (*state.Relay, error) {
	if name != "" {
		rel := r.st.Relays[name]
		if rel == nil {
			return nil, fmt.Errorf("no relay named %q", name)
		}
		return rel, nil
	}
	active := r.st.ActiveRelays()
	switch len(active) {
	case 0:
		if n := len(r.st.RelayOrder); n > 0 {
			return r.st.Relays[r.st.RelayOrder[n-1]], nil
		}
		return nil, fmt.Errorf("no relay is running; start one with relay_start")
	case 1:
		return active[0], nil
	}
	var names []string
	for _, a := range active {
		names = append(names, a.Name)
	}
	return nil, fmt.Errorf("several relays are running (%s); say which", strings.Join(names, ", "))
}

// ---- tools ----------------------------------------------------------------

func (r *Runtime) relayTool(name string, str func(string) string, num func(string, int) int, list func(string) []string) (string, bool) {
	switch name {
	case "relay_start":
		profile := strings.TrimSpace(str("profile"))
		dir := strings.TrimSpace(str("dir"))
		var started string
		var err error
		r.sync(func() {
			if dir != "" && !filepath.IsAbs(dir) {
				dir = filepath.Join(r.workDir(""), dir)
			}
			started, err = r.startRelay(profile, dir, "", false)
		})
		if err != nil {
			return "Could not start the relay: " + err.Error(), true
		}
		return r.relayStatusText(started, "Started.") + "\nThis session is now a relay: forward the user's messages with relay_forward.", false
	case "relay_forward", "relay_send":
		mode := str("mode")
		var rel *state.Relay
		var id, text string
		var err error
		r.sync(func() {
			if rel, err = r.resolveRelay(str("relay")); err != nil {
				return
			}
			from, seq := "eagent", int64(0)
			if name == "relay_forward" {
				from = "user"
				seq = int64(num("seq", 0))
				if seq == 0 {
					seq = r.st.LastUserSeq
				}
				var atts []event.Attachment
				var ok bool
				if text, atts, ok = userEventText(r.st, seq); !ok {
					err = fmt.Errorf("no user message with seq %d", seq)
					return
				}
				if prev := forwardedAs(r.st, rel.Name, seq); prev != "" {
					err = fmt.Errorf("that message was already forwarded (%s)", prev)
					return
				}
				for _, a := range atts {
					text += fmt.Sprintf("\n\n[The user attached %s: %s]", a.Name, a.Path)
				}
			} else {
				text = strings.TrimSpace(str("text"))
			}
			if strings.TrimSpace(text) == "" {
				err = fmt.Errorf("nothing to send")
				return
			}
			if mode == "" {
				mode = "queue"
			}
			id = r.st.NextRelaySendID()
			r.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: rel.Name, ID: id, From: from, Seq: seq, Mode: mode, Text: text}))
		})
		if err != nil {
			return err.Error(), true
		}
		seat := r.relays.get(rel.Name)
		if seat == nil {
			r.sync(func() {
				r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: rel.Name, ID: id, OK: false, Detail: "the relay is not running"}))
			})
			return rel.Who() + " is not running; restart it with relay_control restart.", true
		}
		how, err := seat.Send(id, text, mode)
		if err != nil {
			r.sync(func() {
				r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: rel.Name, ID: id, OK: false, Detail: err.Error()}))
			})
			return fmt.Sprintf("Could not send to %s: %v", rel.Who(), err), true
		}
		return fmt.Sprintf("%s (%s). Its transcript will confirm arrival; you are told only if it does not.", how, id), false
	case "relay_status":
		var name string
		var err error
		r.sync(func() {
			var rel *state.Relay
			if rel, err = r.resolveRelay(str("relay")); err == nil {
				name = rel.Name
			}
		})
		if err != nil {
			return err.Error(), true
		}
		return r.relayStatusText(name, ""), false
	case "relay_screen":
		var rel *state.Relay
		var err error
		r.sync(func() { rel, err = r.resolveRelay(str("relay")) })
		if err != nil {
			return err.Error(), true
		}
		seat := r.relays.get(rel.Name)
		if seat == nil {
			return rel.Who() + " is not running.", true
		}
		screen, err := seat.Screen(num("lines", 60))
		if err != nil {
			return err.Error(), true
		}
		return strings.TrimRight(screen, "\n"), false
	case "relay_control":
		action := str("action")
		var rel *state.Relay
		var err error
		r.sync(func() { rel, err = r.resolveRelay(str("relay")) })
		if err != nil {
			return err.Error(), true
		}
		seat := r.relays.get(rel.Name)
		switch action {
		case "interrupt", "keys":
			if seat == nil {
				return rel.Who() + " is not running.", true
			}
			if action == "interrupt" {
				err = seat.Interrupt()
			} else {
				keys := list("keys")
				if len(keys) == 0 {
					return "keys: name at least one key", true
				}
				err = seat.Keys(keys...)
			}
			if err != nil {
				return err.Error(), true
			}
			return "done; look again with relay_status or relay_screen", false
		case "restart":
			if seat != nil {
				_ = seat.Kill()
				r.relays.drop(rel.Name, seat)
			}
			var started string
			r.sync(func() { started, err = r.startRelay(rel.Profile, rel.Dir, rel.Name, true) })
			if err != nil {
				return "Could not restart: " + err.Error(), true
			}
			return r.relayStatusText(started, "Restarted, continuing its own session."), false
		case "stop":
			if seat != nil {
				_ = seat.Kill()
				r.relays.drop(rel.Name, seat)
			}
			r.sync(func() {
				r.append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: rel.Name, Reason: firstNonEmpty(str("reason"), "stopped")}))
			})
			return "Stopped " + rel.Who() + ".", false
		}
		return fmt.Sprintf("unknown action %q", action), true
	}
	return "unknown relay tool", true
}

// forwardedAs names the send that already forwarded a user message.
func forwardedAs(st *state.State, relayName string, seq int64) string {
	for _, ev := range st.Events {
		if ev.Type != event.RelaySend {
			continue
		}
		var d event.RelaySendData
		if ev.Decode(&d) == nil && d.Name == relayName && d.From == "user" && d.Seq == seq {
			return d.ID
		}
	}
	return ""
}

// relayStatusText describes a relay for the orchestrator.
func (r *Runtime) relayStatusText(name, lead string) string {
	seat := r.relays.get(name)
	var b strings.Builder
	if lead != "" {
		b.WriteString(lead + "\n")
	}
	var snap relay.Status
	if seat != nil {
		snap = seat.Snapshot()
	}
	r.sync(func() {
		rel := r.st.Relays[name]
		if rel == nil {
			fmt.Fprintf(&b, "no relay %s", name)
			return
		}
		now := time.Now()
		state, since := rel.State, rel.StateSince
		if seat != nil && snap.State != "" {
			state, since = snap.State, snap.Since
		}
		fmt.Fprintf(&b, "%s (%s profile, %s) in %s: %s for %s.", rel.Who(), rel.Profile, firstNonEmpty(rel.Label, rel.Agent), rel.Dir, state, roundDur(now.Sub(since)))
		if seat == nil && rel.Active() {
			b.WriteString(" Not being watched: it exited or eagent lost it; relay_control restart starts it again.")
		}
		r.relays.mu.Lock()
		act := r.relays.activity[name]
		r.relays.mu.Unlock()
		if state == "working" && act.text != "" {
			fmt.Fprintf(&b, "\nRight now: %s (%s ago).", act.text, roundDur(now.Sub(act.at)))
		}
		if len(rel.Recent) > 0 {
			b.WriteString("\nIt said while working:")
			for _, t := range rel.Recent {
				b.WriteString("\n- " + clipRunes(oneLine(t, 400), 400))
			}
		}
		if rel.LastTurn != "" {
			fmt.Fprintf(&b, "\nTurns finished: %d. The last ended %s ago with:\n%s", rel.Turns, roundDur(now.Sub(rel.LastTurnAt)), clipRunes(rel.LastTurn, 1500))
		}
		if len(rel.Undelivered) > 0 {
			fmt.Fprintf(&b, "\nNot confirmed delivered yet: %d message(s).", len(rel.Undelivered))
		}
		if rel.Limit != nil {
			b.WriteString("\nUsage: " + limitLine(rel.Who(), *rel.Limit))
		}
		if rel.Detail != "" && (state == "blocked" || state == "exited") {
			b.WriteString("\nDetail: " + rel.Detail)
		}
		fmt.Fprintf(&b, "\nTerminal: %s %s (watch: eagent relays attach %s).", rel.Host, rel.Target, rel.Target)
		if rel.InnerID != "" {
			fmt.Fprintf(&b, " Its own session id: %s.", rel.InnerID)
		}
	})
	return b.String()
}

func limitLine(who string, d event.RelayLimitData) string {
	if d.Text != "" {
		return d.Text
	}
	s := fmt.Sprintf("%.0f%% of its %s budget used", d.UsedPercent, firstNonEmpty(d.Window, "usage"))
	if d.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339, d.ResetsAt); err == nil {
			s += ", resets " + t.Local().Format("Mon 15:04")
		}
	}
	return s
}

// relaySteer is the relay line of the orchestrator's steer. Loop goroutine.
func relaySteer(st *state.State, now time.Time, reason string) string {
	if !st.RelayMode() {
		return ""
	}
	active := st.ActiveRelays()
	if len(active) == 0 {
		return " Relay: stopped. If the user wants it back, relay_start or relay_control restart."
	}
	var b strings.Builder
	for _, rel := range active {
		fmt.Fprintf(&b, " Relay: %s in %s is %s for %s", rel.Who(), rel.Dir, rel.State, roundDur(now.Sub(rel.StateSince)))
		if n := len(rel.Undelivered); n > 0 {
			fmt.Fprintf(&b, ", %d message(s) awaiting confirmation", n)
		}
		b.WriteString(".")
	}
	who := active[0].Who()
	if reason == "user message" {
		text, _, _ := userEventText(st, st.LastUserSeq)
		if id := forwardedAs(st, active[0].Name, st.LastUserSeq); id != "" {
			b.WriteString(" The user's latest message already went to " + who + " verbatim (" + id + "), the moment it arrived. Add context with relay_send only if it lacks something it needs; otherwise yield.")
		} else if addressedToEagent(text) {
			b.WriteString(" The user's latest message is addressed to you, not to " + who + ": do it yourself, then go back to relaying.")
		} else {
			b.WriteString(" Forward the user's latest message with relay_forward (verbatim) unless it only asks about the relay; answer that from relay_status in a note.")
		}
	}
	b.WriteString(" You are the relay: " + who + " does the work.")
	return b.String()
}

// relayDossierHeader is the part of a relay session's dossier the harness
// writes from state, so the relay survives a context reset no matter what
// the worker's briefing says. Loop goroutine.
func (r *Runtime) relayDossierHeader() string {
	if !r.st.RelayMode() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## RELAY (from the harness's own records)\nThis session is a relay. The agent below does the work; you forward the user's words to it with relay_forward, verbatim, and do not do the work yourself.\n")
	for _, name := range r.st.RelayOrder {
		rel := r.st.Relays[name]
		fmt.Fprintf(&b, "- %s: %s (profile %s) in %s, terminal %s %s, state %s", rel.Name, firstNonEmpty(rel.Label, rel.Agent), rel.Profile, rel.Dir, rel.Host, rel.Target, rel.State)
		if rel.InnerID != "" {
			fmt.Fprintf(&b, ", its own session %s", rel.InnerID)
		}
		fmt.Fprintf(&b, "; %d turns, %d messages sent", rel.Turns, rel.Sends)
		if n := len(rel.Undelivered); n > 0 {
			fmt.Fprintf(&b, ", %d not confirmed", n)
		}
		if !rel.Active() {
			b.WriteString(" (stopped)")
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// relayActivityLine is the phone's status line while an agent works.
func (r *Runtime) relayActivityLine() (string, bool) {
	for _, rel := range r.st.ActiveRelays() {
		if rel.State != "working" {
			continue
		}
		r.relays.mu.Lock()
		act := r.relays.activity[rel.Name]
		r.relays.mu.Unlock()
		text := rel.Who() + " is working"
		if act.text != "" && time.Since(act.at) < 10*time.Minute {
			text = rel.Who() + ": " + act.text
		}
		return text, true
	}
	return "", false
}

// relayInflight lines for the narrator's steer.
func (r *Runtime) relayInflight(now time.Time) []string {
	var out []string
	for _, rel := range r.st.ActiveRelays() {
		switch rel.State {
		case "working":
			line := fmt.Sprintf("%s has been working for %s", rel.Who(), roundDur(now.Sub(rel.StateSince)))
			r.relays.mu.Lock()
			act := r.relays.activity[rel.Name]
			r.relays.mu.Unlock()
			if act.text != "" {
				line += fmt.Sprintf("; right now it is %s (for %s)", act.text, roundDur(now.Sub(act.at)))
			}
			out = append(out, line)
		case "blocked":
			out = append(out, fmt.Sprintf("%s is stuck on something on its screen", rel.Who()))
		}
	}
	return out
}

func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
