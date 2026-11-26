package relay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// The rest is what the agent's prepare sets up (relay.Prepare). Hook is
	// the agent's own hook: Claude Code's Stop hook command, Antigravity's
	// status-line command, pi's extension file. StatusFile is where that
	// hook reports the agent's state; Spool is the directory where messages
	// held for the end of a turn wait for it. Files are written (path to
	// content) before the agent starts; Env is added to its environment.
	StatusFile string
	Hook       string
	Spool      string
	Files      map[string]string
	Env        []string
}

// Started is what a launch did, for the relay.start event.
type Started struct {
	Host, Target, InnerID, Transcript string
	Command                           []string
}

// Seat watches and drives one running agent.
type Seat struct {
	spec   Spec
	agent  Agent
	host   Host
	target string
	events chan Event
	stop   chan struct{}
	done   chan struct{}
	poll   time.Duration // how often the watcher looks; pollEvery when zero (tests shorten it)

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
	readySince time.Time            // when the screen first looked ready while starting
	refollow   bool                 // the agent moved to another transcript: find it again
	followed   bool                 // ...and where its news begins is decided when it opens
	sinceKept  time.Time            // since as it was before the move
	onAt       time.Time            // when the agent was last seen on the session the seat follows
	dialogUp   string               // the dialog the agent itself reports waiting on
	dialogPrev string               // the state to go back to when it ends
	hung       bool                 // blocked because a working agent went quiet, not by a dialog
	attached   bool                 // reattached: the state is a guess until the transcript's history says
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
	if innerID == "" {
		innerID = a.chooseID()
	}
	argv := a.command(spec.Profile, innerID, resume, spec.Hook)
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, Started{}, fmt.Errorf("%s is not installed (not on PATH)", argv[0])
	}
	target := Target(spec.Session, spec.Name)
	if host.Alive(target) {
		return nil, Started{}, fmt.Errorf("a relay named %s is already running (%s)", spec.Name, target)
	}
	_ = host.Kill(target) // a dead pane left over from an earlier run
	if spec.Profile.GitPull {
		// Never a prompt for credentials: nobody is at this terminal.
		cmd, cancel := bounded(time.Minute, "git", "-C", spec.Dir, "pull", "--ff-only")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
		_ = cmd.Run()
		cancel()
	}
	if spec.StatusFile != "" {
		if err := os.Remove(spec.StatusFile); err != nil && !os.IsNotExist(err) {
			return nil, Started{}, err
		}
	}
	if !resume && spec.Spool != "" {
		// The spool belongs to the relay's name, and a name is used again
		// after its agent stopped. What waits there was held for that
		// conversation; a fresh one must not run it. A resume keeps it.
		if err := clearSpool(spec.Spool); err != nil {
			return nil, Started{}, err
		}
	}
	for path, content := range spec.Files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, Started{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, Started{}, err
		}
	}
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
	s.markAttached(since)
	go s.watch()
	return s, nil
}

// markAttached starts a reattached seat. The agent may be in the middle of
// a turn: idle is only what the seat says until the history it re-reads
// shows whether the last turn ended.
func (s *Seat) markAttached(since time.Time) {
	s.launched, s.since = since, since
	s.state, s.attached = "idle", true
}

func newSeat(spec Spec, a Agent, host Host, target, innerID string) *Seat {
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
	if state != "blocked" {
		s.hung = false
	}
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
	// The watcher's own calls are bounded; this bounds the wait as well, so
	// stopping a session can never hang on its relay.
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
	}
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
	// Last look before typing: text typed into a dialog is lost (or worse).
	for i := 0; i < 3; i++ {
		screen, err := s.host.Capture(s.target, 0)
		if err != nil || !s.answerDialog(screen, time.Now()) {
			break
		}
		time.Sleep(time.Second)
	}
	label := s.agent.Label()
	switch mode {
	case "", "queue":
		mode = "queue"
	case "interrupt":
		if state == "working" {
			if err := s.Interrupt(); err != nil {
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
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		// The agent's own command (/goal, /compact): it runs it rather than
		// recording a message, so there is no arrival to confirm.
		return fmt.Sprintf("typed the command into %s", label), nil
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

// ErrClosed is a send to a seat that was closed or killed (the relay was
// stopped, restarted or switched) before its text could be typed. It is
// not the agent's failure: the harness decides where such a send goes.
var ErrClosed = errors.New("the agent's terminal was closed before the message could be typed")

// waitReady blocks while the agent is still on its startup screens, so a
// message is never typed into a trust prompt.
func (s *Seat) waitReady(limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	for {
		s.mu.Lock()
		state, closed, hung := s.state, s.closed, s.hung
		s.mu.Unlock()
		if closed {
			// Nothing watches this seat any more, so its state is stale and
			// it is no longer the relay's terminal.
			return state, ErrClosed
		}
		switch state {
		case "starting":
			if time.Now().After(deadline) {
				return state, errors.New("the agent is still starting; check relay_screen")
			}
			select {
			case <-time.After(300 * time.Millisecond):
			case <-s.stop:
			}
			continue
		case "blocked":
			if hung {
				// Only quiet for a long time: nothing on screen waits for an
				// answer, so a message is typed as into a working agent.
				return "working", nil
			}
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

// Interrupt stops what the agent is doing (Escape, unless the agent says
// otherwise).
func (s *Seat) Interrupt() error {
	return s.host.Keys(s.target, s.agent.interruptKeys()...)
}

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
		if want != "" && (strings.Contains(got, head(want, 160)) || strings.Contains(want, head(got, 160)) && len(got) > 20) {
			ids = append(ids, p.id)
			continue
		}
		keep = append(keep, p)
	}
	s.pending = keep
	return ids
}

// head is the start of s, at most n bytes, cut at a character and without
// the ellipsis clipText adds: it is looked for in other text.
func head(s string, n int) string { return strings.TrimSuffix(clipText(s, n), "…") }

// ---- watching ---------------------------------------------------------------

const pollEvery = 700 * time.Millisecond

// tailBackfill is how much of a transcript is re-read on reattach, to catch
// a turn that ended while no eagent was watching.
const tailBackfill = 4 << 20

func (s *Seat) watch() {
	defer close(s.done)
	defer close(s.events)
	// However watching ends (the pane died, a restart, an upgrade, the
	// session stopping), the transcript is let go, so the next seat for this
	// agent can follow it. Held, it made a restarted Codex deaf until
	// eagent itself restarted.
	defer func() {
		s.mu.Lock()
		path := s.transcript
		s.mu.Unlock()
		if path != "" {
			claimed.CompareAndDelete(path, s.target)
		}
	}()
	var tail *tailer
	var p parser
	every := pollEvery
	if s.poll > 0 {
		every = s.poll
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	lastAlive := time.Now()
	lastDialog := time.Time{}
	missed := 0 // liveness checks in a row that found no pane
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		s.mu.Lock()
		if s.refollow {
			// The agent moved to another transcript of its own (pi's /new):
			// let the old one go and find the new one.
			if s.transcript != "" {
				claimed.CompareAndDelete(s.transcript, s.target)
			}
			s.refollow, s.transcript, tail = false, "", nil
		}
		s.mu.Unlock()
		if tail == nil {
			s.mu.Lock()
			path, inner := s.transcript, s.inner
			s.mu.Unlock()
			if path == "" {
				path, inner = s.agent.locate(s.spec, s.launched, inner)
			}
			if path != "" {
				if owner, loaded := claimed.LoadOrStore(path, s.target); loaded && owner != s.target {
					path = "" // another relay in this process follows it
				}
			}
			if path != "" {
				tail, p = &tailer{path: path}, s.agent.newParser(s.spec)
				s.mu.Lock()
				first := s.transcript == ""
				if _, ok := s.agent.(sessionStarter); ok && first && !s.followed && s.inner != "" && inner != s.inner {
					// The agent left the session it was started on before
					// the seat found any transcript (pi's /resume before its
					// first message). The one it is on holds history the
					// seat never saw live: decide where its news begins as
					// for a move the seat saw.
					s.sinceKept = s.since
					s.since, s.followed, s.onAt = s.moveCut(), true, time.Time{}
				}
				s.transcript, s.inner = path, inner
				exact := false
				if s.followed {
					// The agent moved here itself. If it says how much the
					// transcript held when it opened it, exactly that is
					// history; news written before this look is still news.
					s.followed = false
					if ss, ok := s.agent.(sessionStarter); ok {
						if sk, ok := p.(entrySkipper); ok {
							if n, ok := ss.startEntries(s.spec, path); ok {
								sk.skipEntries(n)
								s.since, exact = s.sinceKept, true
							}
						}
					}
				}
				since := s.since
				s.mu.Unlock()
				if !since.IsZero() && !exact {
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
			// One failed check can be a slow or busy tmux; two in a row,
			// three seconds apart, mean the pane is gone. Marked exited by
			// mistake, a relay could not be restarted while its agent ran.
			if s.host.Alive(s.target) {
				missed = 0
			} else if missed++; missed >= 2 {
				screen, _ := s.host.Capture(s.target, 15)
				s.setState("exited", lastLines(screen, 6))
				return
			}
		}
		_, starter := s.agent.(sessionStarter)
		if pl, ok := s.agent.(poller); ok && (tail != nil || starter) {
			// An agent that moves between sessions of its own (pi) is
			// polled before any transcript is found too: a sighting on the
			// session it was started on dates a move made before its first
			// message.
			pl.poll(s, now)
		}
		if dr, ok := s.agent.(dialogReporter); ok {
			s.reportDialog(dr.reportedDialog(s))
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
		// Already on the record before this eagent attached; it only says
		// whether the agent is still in a turn.
		s.foldHistory(o)
		return
	}
	s.mu.Lock()
	s.attached = false
	s.mu.Unlock()
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
		fresh := s.state != "working" && !s.heldByDialog("working")
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
		s.mu.Lock()
		held := s.heldByDialog("idle")
		s.mu.Unlock()
		if !held {
			s.setState("idle", "")
		}
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

// foldHistory follows the turns in history a reattached seat re-reads, so
// it says working when the agent is still in the turn it was in when eagent
// stopped watching. Nothing is reported but the state.
func (s *Seat) foldHistory(o obs) {
	s.mu.Lock()
	if !s.attached {
		s.mu.Unlock()
		return
	}
	state := ""
	switch o.kind {
	case "user", "working", "progress", "activity", "image":
		if s.state == "idle" {
			state, s.turnStart, s.turnTools, s.turnImages = "working", o.at, 0, nil
		}
	case "turn", "aborted":
		if s.state == "working" {
			state = "idle"
		}
	}
	s.mu.Unlock()
	if state != "" {
		s.setState(state, "")
	}
}

func (s *Seat) ensureWorking(at time.Time) {
	s.mu.Lock()
	working := s.state == "working" || s.heldByDialog("working")
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

func (s *Seat) checkScreen(now time.Time) {
	screen, err := s.host.Capture(s.target, 0)
	if err != nil {
		return
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if s.answerDialog(screen, now) {
		s.mu.Lock()
		s.readySince = time.Time{} // a dialog was up: the screen was not ready
		s.mu.Unlock()
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
		start := head(strings.TrimSpace(strings.SplitN(stuck.text, "\n", 2)[0]), 40)
		if stuck.nudges < 2 && start != "" && strings.Contains(screen, start) && state != "working" {
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
	ready := s.ready(screen)
	if state == "starting" && ready {
		// Codex draws a ready-looking screen for a moment before its trust
		// prompt appears: ready only counts when it holds across two looks.
		s.mu.Lock()
		first := s.readySince.IsZero()
		if first {
			s.readySince = now
		}
		settled := !first && now.Sub(s.readySince) >= time.Second
		s.mu.Unlock()
		if settled {
			s.setState("idle", "")
		}
	} else if state == "starting" && since > 25*time.Second {
		s.setState("blocked", "still not ready after 25s; the screen ends:\n"+lastLines(screen, 10))
	} else if state == "working" && since > 10*time.Minute {
		s.setState("blocked", "no transcript activity for 10m; the screen ends:\n"+lastLines(screen, 10))
		s.mu.Lock()
		s.hung = s.state == "blocked"
		s.mu.Unlock()
	} else if state == "blocked" && ready && !s.busy(screen) && !s.dialogShowing() {
		s.setState("idle", "")
	}
}

// heldByDialog reports whether a dialog the agent reported holds the seat
// blocked; if so, state is what the seat goes back to when it ends. The
// caller holds s.mu.
func (s *Seat) heldByDialog(state string) bool {
	if s.dialogUp == "" || s.state != "blocked" {
		return false
	}
	s.dialogPrev = state
	return true
}

func (s *Seat) dialogShowing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dialogUp != ""
}

// reportDialog follows the dialog the agent says it waits on (title, ""
// when none): the seat is blocked while one is up, so eagent is told and
// nothing is typed into it, and goes back to what it was doing after.
func (s *Seat) reportDialog(title string) {
	s.mu.Lock()
	prev, state := s.dialogUp, s.state
	if state == "exited" || title == prev {
		s.mu.Unlock()
		return
	}
	s.dialogUp = title
	if title != "" {
		s.hung = false // a dialog holds it now: nothing is typed into it
		if prev == "" {
			s.dialogPrev = state
		}
		s.mu.Unlock()
		s.setState("blocked", s.agent.Label()+" is waiting on a dialog: "+oneLine(title, 120)+"; see relay_screen and answer it with relay_control keys")
		return
	}
	back := s.dialogPrev
	s.dialogPrev = ""
	s.mu.Unlock()
	if state != "blocked" {
		return
	}
	if back == "" || back == "blocked" {
		back = "idle"
	}
	s.setState(back, "")
}

// answerDialog presses the answer to one of the agent's own dialogs on
// screen, at most once per dialog every few seconds, and reports whether
// one was showing. Another agent's dialog text on this screen is output
// (a file, a log), never a dialog this agent shows.
func (s *Seat) answerDialog(screen string, now time.Time) bool {
	for _, d := range s.agent.dialogs() {
		if !containsAll(screen, d.match) {
			continue
		}
		s.mu.Lock()
		last := s.handled[d.name]
		if now.Sub(last) < 3*time.Second {
			s.mu.Unlock()
			return true
		}
		s.handled[d.name] = now
		s.mu.Unlock()
		_ = s.host.Keys(s.target, d.keys...)
		s.emit(Event{Kind: KindProgress, Text: "(" + d.what + ")"})
		return true
	}
	return false
}

// readyReporter is an agent whose own hook says whether it waits at its
// prompt (pi's status file); known is false when it says nothing usable.
type readyReporter interface {
	reportedReady(s *Seat) (ready, known bool)
}

// ready says whether the agent waits at its prompt: what its hook reports,
// when it reports anything, and otherwise whether the screen shows this
// agent's own prompt. A screen can look ready while the agent works (pi
// always draws its footer).
func (s *Seat) ready(screen string) bool {
	if rr, ok := s.agent.(readyReporter); ok {
		if ready, known := rr.reportedReady(s); known {
			return ready
		}
	}
	return s.agent.ready(screen)
}

// busyScreen is an agent whose prompt or status line stays on screen while
// it works (Claude's footer, Antigravity's relay line), and that shows
// something else only while it works.
type busyScreen interface {
	busy(screen string) bool
}

// busy says whether the screen shows the agent working, whatever prompt or
// status line it also shows.
func (s *Seat) busy(screen string) bool {
	b, ok := s.agent.(busyScreen)
	return ok && b.busy(screen)
}

// sessionStarter is an agent that can say how many entries a transcript
// held when the agent opened it (pi's extension records it): those are
// history, the rest is news.
type sessionStarter interface {
	startEntries(spec Spec, path string) (int, bool)
}

// entrySkipper is a parser told to pass over a transcript's history.
type entrySkipper interface {
	skipEntries(n int)
}

// onSession notes that the agent was still on the session the seat follows
// at now (the agent's poll read it so).
func (s *Seat) onSession(now time.Time) {
	s.mu.Lock()
	s.onAt = now
	s.mu.Unlock()
}

// follow makes the seat find the agent's transcript again on its next
// tick: the agent switched to another session of its own (id, "" when the
// agent's locate knows it). Where the new transcript's news begins is
// decided when it opens: the agent's sessionStarter says exactly, if it
// can. Otherwise news is what was written after the agent was last seen on
// the old session (it switched after that), less a millisecond for
// transcripts that stamp to the millisecond; never earlier than what the
// seat already ignored. Without that sighting, news is from now on.
func (s *Seat) follow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.followed {
		s.sinceKept = s.since
	}
	s.refollow, s.followed, s.inner, s.since, s.onAt = true, true, id, s.moveCut(), time.Time{}
}

// moveCut is where news begins in a transcript the agent moved to, when the
// agent cannot say: just before it was last seen on the session it left,
// never earlier than sinceKept, "now" without a sighting. The caller holds
// s.mu and has set sinceKept.
func (s *Seat) moveCut() time.Time {
	cut := time.Now()
	if !s.onAt.IsZero() && s.onAt.Before(cut) {
		cut = s.onAt.Add(-time.Millisecond)
	}
	if cut.Before(s.sinceKept) {
		cut = s.sinceKept
	}
	return cut
}

// clearSpool removes the files waiting in a spool: messages, ones a hook
// claimed (.taken) and half-written ones.
func clearSpool(spool string) error {
	list, err := os.ReadDir(spool)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range list {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(spool, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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

// CheckDir reports whether an agent can start in dir: it exists, is a
// directory, and can be entered and listed. A directory that exists but
// cannot be entered (another user's home, mode 000) fails every command run
// in it.
func CheckDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s does not exist", dir)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".")); err != nil {
		return fmt.Errorf("%s cannot be entered (permission denied)", dir)
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("%s cannot be read (permission denied)", dir)
	}
	f.Close()
	return nil
}
