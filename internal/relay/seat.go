package relay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Event kinds a seat reports.
const (
	KindInner     = "inner"     // the transcript was found
	KindState     = "state"     // starting | working | idle | blocked | exited
	KindDelivered = "delivered" // a send arrived (OK) or could not be confirmed
	KindProgress  = "progress"  // the agent said something mid-turn
	KindTurn      = "turn"      // the agent finished a turn
	KindLimit     = "limit"     // usage budget news
	KindActivity  = "activity"  // what the agent is doing right now (not logged)
)

// Event is one thing a seat observed.
type Event struct {
	Kind        string
	State       string
	Detail      string
	Text        string
	SendID      string
	OK          bool
	InnerID     string
	Transcript  string
	DurationMS  int64
	Tools       int
	Images      []string
	Interrupted bool
	UsedPercent float64
	Window      string
	ResetsAt    string
	Exhausted   bool
}

// Spec describes one relay.
type Spec struct {
	Session string // the eagent session that owns it
	Name    string // how the session refers to it
	Profile config.RelayProfile
	Dir     string
	// Hook is the shell command Claude Code runs as its Stop hook; Spool is
	// the directory where messages wait for it.
	Hook  string
	Spool string
	Env   []string
}

// Started is what a launch did, for the relay.start event.
type Started struct {
	Host, Target, InnerID, Transcript string
	Command                           []string
}

// Seat watches and drives one running agent.
type Seat struct {
	spec   Spec
	agent  agent
	host   Host
	target string
	events chan Event
	stop   chan struct{}
	done   chan struct{}

	mu         sync.Mutex
	inner      string
	transcript string
	launched   time.Time
	since      time.Time // ignore transcript lines at or before this (reattach)
	state      string
	stateAt    time.Time
	activity   string
	activityAt time.Time
	lastLine   time.Time
	pending    []*pending
	turnStart  time.Time
	turnTools  int
	turnImages []string
	limitMark  float64
	handled    map[string]time.Time // dialog -> when it was last answered
	closed     bool
}

type pending struct {
	id, text, mode string
	at             time.Time
	nudges         int
}

// Launch starts the agent in a new terminal session and begins watching it.
// With resume set, innerID names the agent's own session to continue.
func Launch(spec Spec, innerID string, resume bool) (*Seat, Started, error) {
	a, err := agentFor(spec.Profile.Agent)
	if err != nil {
		return nil, Started{}, err
	}
	host, err := Detect(spec.Profile.Host)
	if err != nil {
		return nil, Started{}, err
	}
	if st, err := os.Stat(spec.Dir); err != nil || !st.IsDir() {
		return nil, Started{}, fmt.Errorf("%s is not a directory", spec.Dir)
	}
	if _, err := exec.LookPath(spec.Profile.Agent); err != nil {
		return nil, Started{}, fmt.Errorf("%s is not installed (not on PATH)", spec.Profile.Agent)
	}
	target := Target(spec.Session, spec.Name)
	if host.Alive(target) {
		return nil, Started{}, fmt.Errorf("a relay named %s is already running (%s)", spec.Name, target)
	}
	_ = host.Kill(target) // a dead pane left over from an earlier run
	if innerID == "" {
		innerID = a.chooseID()
	}
	if spec.Profile.GitPull {
		_ = exec.Command("git", "-C", spec.Dir, "pull", "--ff-only").Run()
	}
	argv := a.command(spec.Profile, innerID, resume, spec.Hook)
	env := append([]string{"PATH=" + os.Getenv("PATH"), "FINALECHAT_MIRROR=off", "EAGENT_RELAY=" + target}, spec.Env...)
	if err := host.Start(target, spec.Dir, argv, env); err != nil {
		return nil, Started{}, err
	}
	s := newSeat(spec, a, host, target, innerID)
	s.launched = time.Now()
	if resume {
		// A resumed agent's transcript already holds its history; only what
		// happens from now on is news.
		s.since = time.Now()
	}
	go s.watch()
	return s, Started{Host: host.Name(), Target: target, InnerID: innerID, Command: argv}, nil
}

// Attach watches an agent a previous eagent run started. since is the time
// of the last thing already on the record; earlier transcript lines are
// skipped.
func Attach(spec Spec, hostName, target, innerID, transcript string, since time.Time) (*Seat, error) {
	a, err := agentFor(spec.Profile.Agent)
	if err != nil {
		return nil, err
	}
	host := HostByName(hostName)
	if !host.Alive(target) {
		return nil, errors.New("not running")
	}
	s := newSeat(spec, a, host, target, innerID)
	s.transcript = transcript
	s.launched = since
	s.since = since
	s.state = "idle"
	go s.watch()
	return s, nil
}

func newSeat(spec Spec, a agent, host Host, target, innerID string) *Seat {
	return &Seat{spec: spec, agent: a, host: host, target: target, inner: innerID,
		events: make(chan Event, 256), stop: make(chan struct{}), done: make(chan struct{}),
		state: "starting", stateAt: time.Now(), handled: map[string]time.Time{}}
}

// Events delivers what the seat observes. It is closed when watching stops.
func (s *Seat) Events() <-chan Event { return s.events }

// Target is the terminal session name.
func (s *Seat) Target() string { return s.target }

// Host is the multiplexer the agent runs in.
func (s *Seat) Host() Host { return s.host }

func (s *Seat) innerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner
}

func (s *Seat) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.stop:
	}
}

func (s *Seat) setState(state, detail string) {
	s.mu.Lock()
	if s.state == state && detail == "" {
		s.mu.Unlock()
		return
	}
	s.state, s.stateAt = state, time.Now()
	s.mu.Unlock()
	s.emit(Event{Kind: KindState, State: state, Detail: detail})
}

// Close stops watching. The agent keeps running.
func (s *Seat) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	<-s.done
}

// Kill stops watching and ends the agent's terminal session.
func (s *Seat) Kill() error {
	s.Close()
	return s.host.Kill(s.target)
}

// Status is a snapshot for the orchestrator.
type Status struct {
	State      string
	Since      time.Time
	Activity   string
	ActivityAt time.Time
	InnerID    string
	Transcript string
	Target     string
	Pending    []string
	Attach     []string
}

// Snapshot describes the seat now.
func (s *Seat) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{State: s.state, Since: s.stateAt, Activity: s.activity, ActivityAt: s.activityAt, InnerID: s.inner, Transcript: s.transcript, Target: s.target, Attach: s.host.Attach(s.target)}
	for _, p := range s.pending {
		st.Pending = append(st.Pending, fmt.Sprintf("%s (%s, %s ago): %s", p.id, p.mode, time.Since(p.at).Round(time.Second), oneLine(p.text, 80)))
	}
	return st
}

// ---- sending ----------------------------------------------------------------

// Send types text into the agent. mode is queue (the agent reads it at its
// next step, or now when idle), interrupt (stop what it is doing first), or
// after_turn (hold it until the current turn ends). Delivery is confirmed
// later, from the transcript, as a KindDelivered event.
func (s *Seat) Send(id, text, mode string) (string, error) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", errors.New("nothing to send")
	}
	if !s.host.Alive(s.target) {
		return "", errors.New("the agent is not running")
	}
	state, err := s.waitReady(40 * time.Second)
	if err != nil {
		return "", err
	}
	label := s.agent.label()
	switch mode {
	case "", "queue":
		mode = "queue"
	case "interrupt":
		if state == "working" {
			if err := s.host.Keys(s.target, "Escape"); err != nil {
				return "", err
			}
			time.Sleep(1500 * time.Millisecond)
		}
	case "after_turn":
		if state == "working" {
			if err := s.agent.afterTurn(s, id, text); err == nil {
				s.track(id, text, mode)
				return fmt.Sprintf("held for %s; it reads it when its current turn ends", label), nil
			}
			// No native hand-off available: type it now; the agent queues it.
		}
	default:
		return "", fmt.Errorf("unknown mode %q (queue, interrupt, after_turn)", mode)
	}
	if err := s.typeText(text); err != nil {
		return "", err
	}
	s.track(id, text, mode)
	switch {
	case mode == "interrupt" && state == "working":
		return fmt.Sprintf("interrupted %s and typed the message", label), nil
	case state == "working":
		return fmt.Sprintf("typed into %s while it works; it reads it at its next step", label), nil
	}
	return fmt.Sprintf("typed into %s", label), nil
}

// waitReady blocks while the agent is still on its startup screens, so a
// message is never typed into a trust prompt.
func (s *Seat) waitReady(limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	for {
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		switch state {
		case "starting":
			if time.Now().After(deadline) {
				return state, errors.New("the agent is still starting; check relay_screen")
			}
			time.Sleep(300 * time.Millisecond)
			continue
		case "blocked":
			return state, errors.New("the agent is showing something that needs an answer first; check relay_screen and answer it with relay_control keys")
		case "exited":
			return state, errors.New("the agent has exited")
		}
		return state, nil
	}
}

func (s *Seat) typeText(text string) error {
	if err := s.host.Paste(s.target, text); err != nil {
		return err
	}
	time.Sleep(350 * time.Millisecond)
	return s.host.Keys(s.target, "Enter")
}

func (s *Seat) track(id, text, mode string) {
	s.mu.Lock()
	s.pending = append(s.pending, &pending{id: id, text: text, mode: mode, at: time.Now()})
	s.mu.Unlock()
}

// Interrupt stops what the agent is doing (Escape).
func (s *Seat) Interrupt() error { return s.host.Keys(s.target, "Escape") }

// Keys presses keys in the agent's terminal (answering a dialog).
func (s *Seat) Keys(keys ...string) error { return s.host.Keys(s.target, keys...) }

// Screen returns what the agent's terminal shows.
func (s *Seat) Screen(lines int) (string, error) { return s.host.Capture(s.target, lines) }

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// matchPending confirms the sends whose text the transcript shows arriving.
func (s *Seat) matchPending(text string) []string {
	got := normalize(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	keep := s.pending[:0]
	for _, p := range s.pending {
		want := normalize(p.text)
		if want != "" && (strings.Contains(got, clipText(want, 160)) || strings.Contains(want, clipText(got, 160)) && len(got) > 20) {
			ids = append(ids, p.id)
			continue
		}
		keep = append(keep, p)
	}
	s.pending = keep
	return ids
}

// ---- watching ---------------------------------------------------------------

const pollEvery = 700 * time.Millisecond

// tailBackfill is how much of a transcript is re-read on reattach, to catch
// a turn that ended while no eagent was watching.
const tailBackfill = 4 << 20

func (s *Seat) watch() {
	defer close(s.done)
	defer close(s.events)
	var tail *tailer
	var p parser
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	lastAlive := time.Now()
	lastDialog := time.Time{}
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		if tail == nil {
			s.mu.Lock()
			path, inner := s.transcript, s.inner
			s.mu.Unlock()
			if path == "" {
				path, inner = s.agent.locate(s.spec.Dir, s.launched, inner)
			}
			if path != "" {
				if owner, loaded := claimed.LoadOrStore(path, s.target); loaded && owner != s.target {
					path = "" // another relay in this process follows it
				}
			}
			if path != "" {
				s.mu.Lock()
				first := s.transcript == ""
				s.transcript, s.inner = path, inner
				s.mu.Unlock()
				tail, p = &tailer{path: path}, s.agent.newParser()
				if !s.since.IsZero() {
					// Reattached or resumed: the history is already on the
					// record; read only the end of a long transcript.
					if st, err := os.Stat(path); err == nil && st.Size() > tailBackfill {
						tail.offset, tail.skipPartial = st.Size()-tailBackfill, true
					}
				}
				if first {
					s.emit(Event{Kind: KindInner, InnerID: inner, Transcript: path})
				}
			}
		}
		if tail != nil {
			lines, err := tail.next()
			if err == nil && len(lines) > 0 {
				s.mu.Lock()
				s.lastLine = now
				s.mu.Unlock()
				for _, line := range lines {
					for _, o := range p.line(line) {
						s.fold(o)
					}
				}
			}
		}
		if now.Sub(lastAlive) >= 3*time.Second {
			lastAlive = now
			if !s.host.Alive(s.target) {
				screen, _ := s.host.Capture(s.target, 15)
				s.setState("exited", lastLines(screen, 6))
				claimed.Delete(s.transcript)
				return
			}
		}
		if now.Sub(lastDialog) >= 1500*time.Millisecond && s.wantsDialogCheck(now) {
			lastDialog = now
			s.checkScreen(now)
		}
	}
}

// fold applies one observation.
func (s *Seat) fold(o obs) {
	s.mu.Lock()
	since := s.since
	s.mu.Unlock()
	if !since.IsZero() && !o.at.IsZero() && !o.at.After(since) && o.kind != "meta" {
		return // already on the record before this eagent attached
	}
	switch o.kind {
	case "meta":
		s.mu.Lock()
		if s.inner == "" {
			s.inner = o.id
		}
		s.mu.Unlock()
	case "user":
		for _, id := range s.matchPending(o.text) {
			s.emit(Event{Kind: KindDelivered, SendID: id, OK: true})
		}
	case "working":
		s.mu.Lock()
		fresh := s.state != "working"
		if fresh {
			s.turnStart, s.turnTools, s.turnImages = o.at, 0, nil
		}
		s.mu.Unlock()
		if fresh {
			s.setState("working", "")
		}
	case "progress":
		s.ensureWorking(o.at)
		s.emit(Event{Kind: KindProgress, Text: o.text})
	case "activity", "image":
		s.ensureWorking(o.at)
		s.mu.Lock()
		if o.tool {
			s.turnTools++
		}
		if o.kind == "image" && o.text != "" {
			s.turnImages = append(s.turnImages, o.text)
			s.activity = "looking at " + o.text
		} else {
			s.activity = o.text
		}
		s.activityAt = time.Now()
		act := s.activity
		s.mu.Unlock()
		s.emit(Event{Kind: KindActivity, Text: act})
	case "compact":
		s.emit(Event{Kind: KindProgress, Text: "(compacted its context)"})
	case "turn", "aborted":
		s.mu.Lock()
		ev := Event{Kind: KindTurn, Text: o.text, DurationMS: o.durationMS, Tools: s.turnTools, Images: s.turnImages, Interrupted: o.kind == "aborted"}
		if ev.DurationMS == 0 && !s.turnStart.IsZero() && !o.at.IsZero() {
			ev.DurationMS = o.at.Sub(s.turnStart).Milliseconds()
		}
		s.turnTools, s.turnImages, s.activity = 0, nil, ""
		s.mu.Unlock()
		s.emit(ev)
		s.setState("idle", "")
	case "limit":
		if o.exhausted {
			s.emit(Event{Kind: KindLimit, Text: o.text, Exhausted: true})
			return
		}
		s.mu.Lock()
		mark := s.limitMark
		next := 0.0
		for _, t := range []float64{50, 80, 95, 100} {
			if o.used >= t {
				next = t
			}
		}
		if next > mark {
			s.limitMark = next
		}
		s.mu.Unlock()
		if next > mark {
			s.emit(Event{Kind: KindLimit, UsedPercent: o.used, Window: o.window, ResetsAt: o.resets, Exhausted: o.used >= 100})
		}
	}
}

func (s *Seat) ensureWorking(at time.Time) {
	s.mu.Lock()
	working := s.state == "working"
	if !working {
		s.turnStart, s.turnTools, s.turnImages = at, 0, nil
	}
	s.mu.Unlock()
	if !working {
		s.setState("working", "")
	}
}

// wantsDialogCheck says when the screen is worth reading: while starting
// (trust and update prompts), while a send is unconfirmed, and when a working
// agent has gone quiet for a long time (a permission or other dialog).
func (s *Seat) wantsDialogCheck(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "starting" || s.state == "blocked" {
		return true
	}
	if s.state != "working" {
		for _, p := range s.pending {
			if p.mode != "after_turn" && now.Sub(p.at) > 6*time.Second {
				return true
			}
		}
	}
	return s.state == "working" && !s.lastLine.IsZero() && now.Sub(s.lastLine) > 10*time.Minute && now.Sub(s.stateAt) > 10*time.Minute
}

// dialog is a screen the agent shows before (or instead of) working, and the
// keys that answer it the way the relay's user always wants.
type dialog struct {
	name  string
	match []string // all must appear
	keys  []string
	what  string
}

var dialogs = []dialog{
	// Claude Code: the default choice is "No, exit".
	{name: "claude-trust", match: []string{"Yes, I trust this folder"}, keys: []string{"Down", "Enter"}, what: "accepted Claude Code's folder-trust prompt"},
	{name: "claude-bypass", match: []string{"Bypass Permissions mode", "Yes, I accept"}, keys: []string{"Down", "Enter"}, what: "accepted Claude Code's bypass-permissions warning"},
	// Codex: the default choice is "Trust and continue".
	{name: "codex-trust", match: []string{"Trust this folder?", "Trust and continue"}, keys: []string{"Enter"}, what: "accepted Codex's folder-trust prompt"},
	// Codex's update prompt: choose "2. Skip" explicitly (never Escape,
	// which quits on the trust prompt). Upgrading is the user's call; the
	// notice says one is available. Without the menu, the "Update
	// available" box is only a notice above a working prompt.
	{name: "codex-update", match: []string{"Update now", "Skip until next version"}, keys: []string{"Down", "Enter"}, what: "skipped Codex's update prompt for now (a newer Codex is available; upgrade with `codex update` and a restart)"},
}

func (s *Seat) checkScreen(now time.Time) {
	screen, err := s.host.Capture(s.target, 0)
	if err != nil {
		return
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	for _, d := range dialogs {
		if !containsAll(screen, d.match) {
			continue
		}
		s.mu.Lock()
		last := s.handled[d.name]
		if now.Sub(last) < 5*time.Second {
			s.mu.Unlock()
			return
		}
		s.handled[d.name] = now
		s.mu.Unlock()
		_ = s.host.Keys(s.target, d.keys...)
		s.emit(Event{Kind: KindProgress, Text: "(" + d.what + ")"})
		return
	}
	// A send that never arrived: if its text still sits in the input box,
	// the Enter was lost; press it again, twice at most.
	s.mu.Lock()
	var stuck *pending
	for _, p := range s.pending {
		if p.mode != "after_turn" && now.Sub(p.at) > 6*time.Second {
			stuck = p
			break
		}
	}
	if stuck != nil {
		head := clipText(strings.TrimSpace(strings.SplitN(stuck.text, "\n", 2)[0]), 40)
		head = strings.TrimSuffix(head, "…")
		if stuck.nudges < 2 && head != "" && strings.Contains(screen, head) && state != "working" {
			stuck.nudges++
			stuck.at = now
			s.mu.Unlock()
			_ = s.host.Keys(s.target, "Enter")
			return
		}
		if state != "working" && now.Sub(stuck.at) > 45*time.Second && now.Sub(s.stateAt) > 15*time.Second {
			s.pending = removePending(s.pending, stuck)
			s.mu.Unlock()
			s.emit(Event{Kind: KindDelivered, SendID: stuck.id, OK: false, Detail: "never showed up in the agent's transcript; the screen ends:\n" + lastLines(screen, 8)})
			return
		}
	}
	since := now.Sub(s.stateAt)
	s.mu.Unlock()
	if state == "starting" && looksReady(screen) {
		s.setState("idle", "")
	} else if state == "starting" && since > 25*time.Second {
		s.setState("blocked", "still not ready after 25s; the screen ends:\n"+lastLines(screen, 10))
	} else if state == "working" && since > 10*time.Minute {
		s.setState("blocked", "no transcript activity for 10m; the screen ends:\n"+lastLines(screen, 10))
	} else if state == "blocked" && looksReady(screen) {
		s.setState("idle", "")
	}
}

// looksReady recognises an agent at its input prompt.
func looksReady(screen string) bool {
	return strings.Contains(screen, "bypass permissions on") || strings.Contains(screen, "Ask Codex to do anything") || strings.Contains(screen, "for shortcuts")
}

func removePending(list []*pending, x *pending) []*pending {
	out := list[:0]
	for _, p := range list {
		if p != x {
			out = append(out, p)
		}
	}
	return out
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " "))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
