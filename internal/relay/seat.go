package relay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	Outcome     string // with OK false: OutcomeNotSent or OutcomeUnconfirmed
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
	// Restore, on a reattach, is the sends the record still waits on, oldest
	// first: the new seat confirms them from the transcript as the old one
	// would have, and never types them again.
	Restore []Restored
}

// Restored is a send recorded before eagent stopped whose arrival the
// record has not confirmed.
type Restored struct {
	ID, Text, Mode string
	At             time.Time // when it was recorded
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
	turnEndAt  time.Time            // when the transcript last showed a turn end; zero once the agent works again
	hung       bool                 // blocked because a working agent went quiet, not by a dialog
	attached   bool                 // reattached: the state is a guess until the transcript's history says
	catchingUp bool                 // restored sends wait for the first read to the transcript's end
	closed     bool

	typing sync.Mutex // one message is pasted and entered at a time
}

type pending struct {
	id, text, mode string
	at             time.Time
	nudges         int
	// held: the message waits in the spool for a hook to hand it over.
	held bool
	// restored: recorded before eagent stopped (see Seat.restore); sentAt
	// is when. Such a send can be confirmed by transcript lines from before
	// the reattach, and is never pressed again. dormant: the seat has not
	// caught up on the transcript yet. lost: an after-turn send nothing
	// holds (typed straight in, or its spool file gone at the catch-up, or
	// gone once the turn it waited for ended), watched on a timer as a
	// typed send is, but never pressed.
	restored, dormant, lost bool
	sentAt                  time.Time
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
	argv, _ := launchCommand(a, spec, innerID, resume)
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
	if spec.Spool != "" {
		// The spool belongs to the relay's name, and a name is used again
		// after its agent stopped. What waits there was held for that
		// conversation; a fresh one must not run it. Nor does a resume: a
		// restart reports what the old agent still waited on unconfirmed,
		// and a copy left here would be handed over as well.
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

// resumer is an agent that can resume only a session it has saved.
type resumer interface {
	canResume(spec Spec, innerID string) bool
}

// launchCommand is the agent's command line, and whether it resumes: a
// session the agent never saved is started afresh on the same id.
func launchCommand(a Agent, spec Spec, innerID string, resume bool) ([]string, bool) {
	if r, ok := a.(resumer); ok && resume && !r.canResume(spec, innerID) {
		resume = false
	}
	return a.command(spec.Profile, innerID, resume, spec.Hook), resume
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
	s := attachSeat(spec, a, host, target, innerID, transcript, since)
	go s.watch()
	return s, nil
}

// attachSeat is the seat Attach watches, before it starts watching.
func attachSeat(spec Spec, a Agent, host Host, target, innerID, transcript string, since time.Time) *Seat {
	s := newSeat(spec, a, host, target, innerID)
	s.transcript = transcript
	s.markAttached(since)
	s.restore(spec.Restore)
	return s
}

// restore takes over the sends the record still waits on. Until the seat
// has caught up on the transcript they are only matched: no timer runs for
// them, since the transcript may show them arriving while eagent was down.
// A message held for the end of a turn is held as it was, if its agent
// hands such messages over through the spool.
func (s *Seat) restore(list []Restored) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range list {
		mode := r.Mode
		if mode == "" {
			mode = "queue"
		}
		held := mode == "after_turn" && s.spec.Spool != "" && s.spec.Hook != ""
		s.pending = append(s.pending, &pending{id: r.ID, text: r.Text, mode: mode, at: r.At, held: held, restored: true, dormant: true, sentAt: r.At})
		s.catchingUp = true
	}
}

// caughtUp ends the catch-up: the restored sends the transcript did not
// show are watched from now on as a live send is, from now. One held in
// the spool (claimed or not) stays held: the hook hands it over at the end
// of a turn, or the seat reports it left over. Any other, a held one whose
// file is gone among them, is reported unconfirmed after the wait a live
// send gets, and is never pressed or typed again.
func (s *Seat) caughtUp(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.catchingUp {
		return
	}
	s.catchingUp = false
	var names []string
	for _, p := range s.pending {
		if !p.dormant {
			continue
		}
		p.dormant, p.at = false, now
		if p.held {
			if names == nil {
				names = spoolNames(s.spec.Spool)
			}
			if spoolHolds(names, p.id) {
				continue
			}
		}
		p.lost = p.mode == "after_turn"
	}
}

// spoolHolds says whether the spool still has the held message id, waiting
// or claimed by a hook that has not handed it over yet.
func spoolHolds(names []string, id string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, "-"+id+".txt") || strings.HasSuffix(n, "-"+id+".txt.taken") {
			return true
		}
	}
	return false
}

// timed says whether a pending send is watched on a timer: pressed again
// when its text sits in the input box, and reported unconfirmed when it
// never arrives. A message held for the end of a turn is not until it is
// lost: restored with nothing holding it any more, or its turn over and its
// spool file gone (reportLeftOvers). One handed to an agent's own queue
// (Codex, Antigravity) is never timed: that queue runs one message a turn,
// so the end of a turn does not say a queued message is due. Nor is a
// restored send before the catch-up has ended.
func (p *pending) timed() bool {
	return !p.dormant && (p.mode != "after_turn" || p.lost)
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
	if state != "idle" {
		s.turnEndAt = time.Time{}
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
}

// Snapshot describes the seat now.
func (s *Seat) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{State: s.state, Since: s.stateAt, Activity: s.activity, ActivityAt: s.activityAt, InnerID: s.inner, Transcript: s.transcript, Target: s.target}
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
	if errors.Is(err, errUnknownScreen) && mode == "interrupt" {
		// Quiet for ten minutes on a screen the seat does not know: the
		// interrupt itself goes (it is how a stuck agent is got back), its
		// text only if the agent's prompt shows after it.
		if err := s.Interrupt(); err != nil {
			return "", err
		}
		time.Sleep(interruptPause)
		if !s.atOwnScreen() {
			return "", fmt.Errorf("interrupted %s, but its screen still shows something that is not its prompt; the message was not typed. Check relay_screen", s.agent.Label())
		}
		state, err, mode = "idle", nil, "queue"
	}
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
			time.Sleep(interruptPause)
		}
	case "after_turn":
		if state == "working" {
			if err := s.agent.afterTurn(s, id, text); err == nil {
				// An agent with a spool holds it there; one without has handed
				// it to its own queue. Either way it is tracked for its arrival,
				// and, as every after-turn send, never pressed again, nor
				// reported lost on a timer before its turn is over (see
				// pending.lost). Only Claude's and pi's hand-overs go through
				// the spool.
				s.trackHeld(id, text, mode, s.spec.Spool != "")
				return fmt.Sprintf("held for %s; it reads it when its current turn ends", label), nil
			} else if errors.As(err, new(*TypingError)) {
				// It was being typed when it failed: typing it again
				// could put it into the agent twice, and the caller
				// records it as a send that may have gone.
				return "", err
			}
			// No native hand-off available: type it now; the agent queues it.
		}
	default:
		return "", fmt.Errorf("unknown mode %q (queue, interrupt, after_turn)", mode)
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		// Stopped during the last look or the interrupt's pause: nothing typed.
		return "", ErrClosed
	}
	if err := s.typeText(text); err != nil {
		return "", &TypingError{err}
	}
	if isAgentCommand(text) {
		// The agent's own command (/goal, /compact): it runs it rather than
		// recording a message, so there is no arrival to confirm; typed is
		// delivered.
		s.emit(Event{Kind: KindDelivered, SendID: id, OK: true})
		return fmt.Sprintf("typed the command into %s", label), nil
	}
	// An after-turn send typed here (the agent idle, or the hand-over
	// failed) is held by nothing: it is timed from the start, never pressed.
	s.mu.Lock()
	s.pending = append(s.pending, &pending{id: id, text: text, mode: mode, at: time.Now(), lost: mode == "after_turn"})
	s.mu.Unlock()
	switch {
	case mode == "interrupt" && state == "working":
		return fmt.Sprintf("interrupted %s and typed the message", label), nil
	case state == "working":
		return fmt.Sprintf("typed into %s while it works; it reads it at its next step", label), nil
	}
	return fmt.Sprintf("typed into %s", label), nil
}

// interruptPause is how long a send waits after interrupting the agent
// before it types. Tests shorten it.
var interruptPause = 1500 * time.Millisecond

// errUnknownScreen is a seat quiet for ten minutes whose screen shows
// neither the agent's prompt nor its busy line: maybe a dialog the seat
// does not know. Only an interrupt goes to it.
var errUnknownScreen = errors.New("the agent is showing something that needs an answer first; check relay_screen and answer it with relay_control keys")

// atOwnScreen says whether the agent is at its own prompt or visibly busy:
// its screen shows either, or its own status says it works (pi reports its
// dialogs itself, so working is no dialog).
func (s *Seat) atOwnScreen() bool {
	if wr, ok := s.agent.(workReporter); ok && wr.reportedWorking(s) {
		return true
	}
	screen, err := s.host.Capture(s.target, 0)
	return err == nil && (s.ready(screen) || s.busy(screen))
}

// workReporter is an agent whose hook says whether it is in a turn (pi's
// status file).
type workReporter interface {
	reportedWorking(s *Seat) bool
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
				// Quiet for a long time: a message is typed as into a working
				// agent only while the screen shows its own prompt or busy
				// line. Anything else may be a dialog the seat does not know,
				// and the Enter would answer it.
				if s.atOwnScreen() {
					return "working", nil
				}
				return state, errUnknownScreen
			}
			return state, errors.New("the agent is showing something that needs an answer first; check relay_screen and answer it with relay_control keys")
		case "exited":
			return state, errors.New("the agent has exited")
		}
		return state, nil
	}
}

// typeSettle is the pause between pasting a message and pressing Enter.
// Tests shorten it.
var typeSettle = 350 * time.Millisecond

func (s *Seat) typeText(text string) error {
	s.typing.Lock()
	defer s.typing.Unlock()
	if err := s.host.Paste(s.target, text); err != nil {
		return err
	}
	time.Sleep(typeSettle)
	return s.host.Keys(s.target, "Enter")
}

func (s *Seat) track(id, text, mode string) { s.trackHeld(id, text, mode, false) }

func (s *Seat) trackHeld(id, text, mode string, held bool) {
	s.mu.Lock()
	s.pending = append(s.pending, &pending{id: id, text: text, mode: mode, at: time.Now(), held: held})
	s.mu.Unlock()
}

// heldGrace is how long after the transcript shows a turn over a message
// still in the spool counts as left over (the Stop hook had its chance).
// Tests shorten it.
var heldGrace = 5 * time.Second

// reportLeftOvers reports undelivered every message still in the spool
// heldGrace after the transcript showed the agent's turn over: the Stop
// hook runs only at a turn's end, so nothing will hand them over now (an
// interrupt, a message held just as the turn ended, a restart). Each is
// claimed with the rename the Stop hook uses, so nothing goes twice, and
// reported in order. Nothing here reads the screen: an idle seen only on
// the screen (after going quiet) is no turn end, and leaves the spool to
// the hook.
//
// A held message tracked before that turn ended whose file is gone (the
// hook claimed it, and may have died before handing it over) is lost from
// now: confirmed if the transcript shows it, reported unconfirmed after the
// usual wait otherwise.
func (s *Seat) reportLeftOvers(now time.Time) {
	if s.spec.Spool == "" {
		return
	}
	s.mu.Lock()
	end := s.turnEndAt
	over := s.state == "idle" && !end.IsZero() && now.Sub(end) >= heldGrace
	s.mu.Unlock()
	if !over {
		return
	}
	taken, _ := takeSpool(s.spec.Spool)
	var order []string
	ids := map[string]bool{}
	for _, path := range taken {
		_ = os.Remove(path)
		if _, id, ok := strings.Cut(strings.TrimSuffix(filepath.Base(path), ".txt.taken"), "-"); ok && !ids[id] {
			ids[id] = true
			order = append(order, id)
		}
	}
	names := spoolNames(s.spec.Spool)
	s.mu.Lock()
	same := s.state == "idle" && s.turnEndAt.Equal(end)
	keep := s.pending[:0]
	for _, p := range s.pending {
		if ids[p.id] {
			continue
		}
		if same && p.held && !p.dormant && !p.lost && p.at.Before(end) && !spoolHolds(names, p.id) && !isAgentCommand(p.text) {
			p.lost, p.at = true, now
		}
		keep = append(keep, p)
	}
	s.pending = keep
	s.mu.Unlock()
	if len(order) == 0 {
		return
	}
	detail := s.agent.Label() + "'s turn ended before it took this message (for example after an interrupt), so it was not delivered; send it again if it is still wanted"
	for _, id := range order {
		s.emit(Event{Kind: KindDelivered, SendID: id, OK: false, Outcome: OutcomeNotSent, Detail: detail})
	}
}

// isAgentCommand says whether text is the agent's own command: "/" and a
// name (letters, digits, "_", "-", ":" for a plugin's namespace), then a
// space or the end. "/home/eric/plan.md has it" is a message.
func isAgentCommand(text string) bool { return agentCommand.MatchString(strings.TrimSpace(text)) }

var agentCommand = regexp.MustCompile(`^/[A-Za-z][\w:-]*(\s|$)`)

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

// matchPending confirms the sends one user entry of the transcript shows
// arriving (see arrivals) and returns their ids, oldest first.
func (s *Seat) matchPending(text string) []string { return s.matchSends(text, time.Time{}) }

// matchSends is matchPending for an entry the transcript stamped at. An
// entry from before the reattach (history, at set) is the arrival only of
// a restored send recorded no later than the entry: it may have arrived
// just before the record's last event, but a send of the same words
// confirmed earlier, or sent after the reattach, is not it.
func (s *Seat) matchSends(text string, at time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cands []int
	for i, p := range s.pending {
		// Antigravity stamps to the whole second, so its entry for a send
		// can carry a time up to a second before the send's own; an entry
		// stamped finer than that gets no such grace.
		sent := p.sentAt
		if at.Nanosecond() == 0 {
			sent = sent.Truncate(time.Second)
		}
		if at.IsZero() || p.restored && !at.Before(sent) {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	views := make([]sendView, len(cands))
	var spool []string
	for k, i := range cands {
		p := s.pending[i]
		views[k].text = p.text
		if p.held {
			if spool == nil {
				spool = spoolNames(s.spec.Spool)
			}
			views[k].waiting = spoolWaits(spool, p.id)
		}
	}
	got := arrivals(views, text)
	if len(got) == 0 {
		return nil
	}
	drop := map[int]bool{}
	for _, k := range got {
		drop[cands[k]] = true
	}
	var ids []string
	keep := s.pending[:0]
	for i, p := range s.pending {
		if drop[i] {
			ids = append(ids, p.id)
			continue
		}
		keep = append(keep, p)
	}
	s.pending = keep
	return ids
}

// spoolNames lists a spool's file names; [] (not nil) when it has none.
func spoolNames(spool string) []string {
	names := []string{}
	if spool == "" {
		return names
	}
	entries, _ := os.ReadDir(spool)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// spoolWaits says whether the held message id still waits in the spool,
// unclaimed (a hook renames what it takes to .taken), or is still being
// written (a hidden temporary name).
func spoolWaits(names []string, id string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, "-"+id+".txt") {
			return true
		}
	}
	return false
}

// sendView is what arrivals needs to know of one pending send.
type sendView struct {
	text string
	// waiting: a message held in the spool whose file is still there.
	// Nothing has handed it over, so no entry is its arrival.
	waiting bool
}

// arrivals decides which pending sends one user entry of the agent's
// transcript shows arriving, and returns their positions, in order.
//
// Whether the entry shows a send is the rule eagent has always used (shows,
// below), because transcripts wrap and join messages in more ways than
// anyone has listed: Claude wraps a multi-line paste in a tag, a send typed
// after text left in the input box arrives glued to it, a hook hands over
// several held messages at once. What one entry may not do is confirm two
// sends with one place in it. So each confirmed send claims the stretch of
// the entry it matched, stretches may not overlap, and the longest stretch
// is given first (a longer send claims its words before a shorter send that
// is part of them can, and a long send shown cut short keeps its stretch
// from a short send inside it). Between stretches of one length, a send the
// entry shows whole comes before one it shows only cut short or in part,
// then the longer send, then the older, so a send that arrived word for
// word is never left for a longer one that merely contains it:
//
//	pending                         entry                      confirms
//	"continue", "continue"          "continue"                 the older one
//	"continue", "continue"          "continue\n\ncontinue"     both
//	"deploy", "do not deploy"       "do not deploy"            "do not deploy" only
//	"deploy", "do not deploy"       "deploy"                   "deploy"
//	a, a + " and more" (both > 20)  a                          a only
//	five held messages              the hook's join of them    all five
//	a, b (a's Enter was lost)       a then b, glued            both
//	a long send                     its first 200 bytes + "…"  it
//	"yes", a long "…yesterday…"     the long one cut short     the long one
//	two long sends, same opening    one of them                that one
//	anything                        words that are no send     nothing
//
// A held message whose file still waits in the spool is no candidate:
// nothing has handed it over yet.
func arrivals(pending []sendView, entry string) []int {
	got := normalize(entry)
	if got == "" {
		return nil
	}
	order := make([]int, 0, len(pending))
	norms := make([]string, len(pending))
	for i, p := range pending {
		norms[i] = normalize(p.text)
		if !p.waiting && norms[i] != "" {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return len(norms[b]) - len(norms[a]) })
	type match struct {
		i  int
		sp [2]int
	}
	var matches []match
	for _, whole := range []bool{true, false} {
		for _, i := range order {
			for _, sp := range shows(got, norms[i], whole) {
				matches = append(matches, match{i, sp})
			}
		}
	}
	slices.SortStableFunc(matches, func(a, b match) int { return (b.sp[1] - b.sp[0]) - (a.sp[1] - a.sp[0]) })
	var claimed [][2]int
	var out []int
	for _, m := range matches {
		if !slices.Contains(out, m.i) && !overlaps(claimed, m.sp) {
			claimed = append(claimed, m.sp)
			out = append(out, m.i)
		}
	}
	slices.Sort(out)
	return out
}

// shows lists the stretches [start, end) of the transcript text got where
// it shows the send want (both normalized). Where got holds want's first
// 160 bytes, what follows them must agree with want too, as far as either
// goes (a transcript may cut a long message short), so two long messages
// with a common start are told apart; every place that start appears
// counts. A transcript text that is a shortened piece of the message, over
// 20 bytes, counts as well, as all of got. whole asks for the places that
// show all of want; otherwise only the cut-short and piece ones.
func shows(got, want string, whole bool) [][2]int {
	var out [][2]int
	h := head(want, 160)
	if h == "" {
		return nil
	}
	for from := 0; from < len(got); {
		i := strings.Index(got[from:], h)
		if i < 0 {
			break
		}
		at := from + i
		rest := got[at:]
		switch {
		case strings.HasPrefix(rest, want):
			if whole {
				out = append(out, [2]int{at, at + len(want)})
			}
		case strings.HasPrefix(want, strings.TrimSuffix(rest, "…")):
			if !whole {
				out = append(out, [2]int{at, len(got)})
			}
		}
		from = at + 1
	}
	if !whole && len(got) > 20 && strings.Contains(want, strings.TrimSuffix(got, "…")) {
		out = append(out, [2]int{0, len(got)})
	}
	return out
}

func overlaps(claimed [][2]int, sp [2]int) bool {
	for _, c := range claimed {
		if sp[0] < c[1] && c[0] < sp[1] {
			return true
		}
	}
	return false
}

// head is the start of s, at most n bytes, cut at a character and without
// the ellipsis clipText adds: it is looked for in other text.
func head(s string, n int) string { return strings.TrimSuffix(clipText(s, n), "…") }

// ---- watching ---------------------------------------------------------------

const pollEvery = 700 * time.Millisecond

// tailBackfill is how much of a transcript is re-read on reattach, to catch
// a turn that ended while no eagent was watching. Tests lower it.
var tailBackfill int64 = 4 << 20

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
			if err != nil || tail.atEnd {
				// Read to the end as it stood (or there is nothing to read):
				// what the transcript did not show of the restored sends
				// did not arrive while eagent was down.
				s.caughtUp(time.Now())
			}
		} else {
			s.caughtUp(time.Now()) // no transcript to catch up on
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
		// whether the agent is still in a turn, and whether a send the
		// record still waits on arrived just before the record ends.
		if o.kind == "user" {
			for _, id := range s.matchSends(o.text, o.at) {
				s.emit(Event{Kind: KindDelivered, SendID: id, OK: true})
			}
		}
		s.foldHistory(o)
		return
	}
	if o.kind != "meta" {
		// A session header (Codex's session_meta, pi's session line) is
		// the first line of every transcript, also one re-read from the
		// top: only what follows it says where the turn is.
		s.mu.Lock()
		s.attached = false
		s.mu.Unlock()
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
			s.mu.Lock()
			s.turnEndAt = time.Now() // the transcript says the turn is over
			s.mu.Unlock()
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
	case "working", "progress", "activity", "image":
		// A "user" line alone is no turn: Claude records a message queued
		// while it worked ("enqueue") that way, and the live watcher does
		// not take it for working either. A real message comes with a
		// working observation of its own.
		s.turnEndAt = time.Time{}
		if s.state == "idle" {
			state, s.turnStart, s.turnTools, s.turnImages = "working", o.at, 0, nil
		}
	case "turn", "aborted":
		if s.state == "working" {
			state = "idle"
		}
		s.turnEndAt = time.Now() // the history says the agent's turn is over
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
			if p.timed() && now.Sub(p.at) > 6*time.Second {
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
	// The send pressed is the oldest one typed since eagent (re)started: a
	// send from before it restarted is never pressed (its words on screen
	// may be from long ago, and whatever the box holds now may be someone
	// else's), and does not stand in front of one that may be. The one
	// reported is the oldest whose wait is over.
	var press, report *pending
	for _, p := range s.pending {
		if !p.timed() || now.Sub(p.at) <= 6*time.Second {
			continue
		}
		if press == nil && !p.restored && !p.lost {
			press = p
		}
		if report == nil && now.Sub(p.at) > 45*time.Second {
			report = p
		}
	}
	if press != nil {
		start := head(strings.TrimSpace(strings.SplitN(press.text, "\n", 2)[0]), 40)
		// A message meant for after the turn is pressed only once the agent
		// has stayed idle a while: after an interrupt, whatever it was for
		// goes first (an agent may put the queued message back in its box).
		// No Enter goes while a dialog is up: known ones were answered
		// above, and one the agent reports holds the seat.
		if press.nudges < 2 && start != "" && strings.Contains(screen, start) && state != "working" && s.dialogUp == "" {
			press.nudges++
			press.at = now
			s.mu.Unlock()
			_ = s.host.Keys(s.target, "Enter")
			return
		}
	}
	if stuck := report; stuck != nil {
		if state != "working" && now.Sub(s.stateAt) > 15*time.Second {
			s.pending = removePending(s.pending, stuck)
			s.mu.Unlock()
			detail := "never showed up in the agent's transcript; the screen ends:\n" + lastLines(screen, 8)
			if stuck.restored {
				detail = "was recorded before eagent restarted, and the agent's transcript does not show it arriving; it was not typed again, so send it again if it is still wanted. The screen ends:\n" + lastLines(screen, 8)
			}
			s.emit(Event{Kind: KindDelivered, SendID: stuck.id, OK: false, Outcome: OutcomeUnconfirmed, Detail: detail})
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
	} else if state == "blocked" && ready && !(s.hung && s.busy(screen)) && !s.dialogShowing() {
		// A quiet agent's prompt or status line shows while it works, so
		// it is idle only if its screen does not also say it is busy. A
		// slow start is over as soon as the prompt shows.
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

// spinnerLine matches the line Claude and Codex draw while they work: one of
// the agent's own spinner glyphs, a few words, and at the end of the line a
// parenthesis that says how to interrupt ("✻ Thinking… (12s · esc to
// interrupt)", "• Working (3s • esc to interrupt)"). A markdown bullet ("-",
// "1.") or a sentence that quotes the words is not it.
func spinnerLine(glyphs string) *regexp.Regexp {
	return regexp.MustCompile(`(?im)^\s*[` + regexp.QuoteMeta(glyphs) + `]\s+[^()\n]*\([^()\n]*esc to interrupt[^()\n]*\)\s*$`)
}

// The spinner glyphs each agent draws: Claude cycles through these frames,
// Codex draws a bullet.
var (
	claudeSpinner = spinnerLine("·✢✳✶✻✽*")
	codexSpinner  = spinnerLine("•")
)

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
