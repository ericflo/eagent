package harness

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
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

// relaySeats holds the running relays. Tools reach them from the turn's
// goroutine, so the map has its own lock; the log stays loop-owned.
type relaySeats struct {
	mu    sync.Mutex
	seats map[string]relaySeat
	// activity is what each agent is doing right now (not logged).
	activity map[string]relayActivity
	// steps are each agent's latest steps, oldest first, and stepCount how
	// many it has taken in all (not logged; see tail.go).
	steps     map[string][]relayStep
	stepCount int
	// launching are the starts under way off the loop goroutine, by the
	// name they start and the name of the relay they replace.
	launching map[string]*relayLaunch
	// queues are the sends waiting to be typed into each seat, in the
	// order they were accepted (enqueueSend).
	queues map[relaySeat]*sendQueue
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

// launch is the start under way for a relay name (the one it starts or
// the one it replaces), or nil.
func (s *relaySeats) launch(name string) *relayLaunch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.launching[name]
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

// relayPick is a session's choice of model and reasoning effort for a
// relay, over its profile's ("" keeps the profile's).
type relayPick struct{ Model, Effort string }

// relaySpec builds the launch description for a profile with the session's
// picks. Loop goroutine (or before the loop starts).
func (r *Runtime) relaySpec(name, profile, dir string, pick relayPick) (relay.Spec, error) {
	p, ok := r.cfg.Relay.RelayProfiles()[profile]
	if !ok {
		return relay.Spec{}, fmt.Errorf("no relay profile %q (have %s)", profile, strings.Join(r.cfg.Relay.RelayProfileNames(), ", "))
	}
	// A profile that names its own model keeps its own label; otherwise the
	// label says what it runs ("Codex · GPT-6.2-Astra xhigh").
	custom := p.Label != "" && p.Model != "" && pick == (relayPick{})
	p = p.With(pick.Model, pick.Effort)
	if !custom {
		p.Label = relay.Label(p.Agent, r.opts.Project, p.Model, p.Effort)
	}
	spec := relay.Spec{Session: r.sess.ID, Name: name, Profile: p, Dir: dir}
	// The agent's own hooks (a Stop hook, a status line, an extension) and
	// the files they use live in the relay's directory of the session.
	if err := relay.Prepare(&spec, filepath.Join(r.sess.Path, "relays", name)); err != nil {
		return relay.Spec{}, err
	}
	return spec, nil
}

// pickArg reads a model or effort a tool was given: "default" (or nothing)
// leaves it to the profile and the agent.
func pickArg(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "default") {
		return ""
	}
	return s
}

// checkRelayPick refuses an effort the profile's agent does not take, before
// anything starts.
func (r *Runtime) checkRelayPick(profile string, pick relayPick) error {
	p, ok := r.cfg.Relay.RelayProfiles()[profile]
	if !ok {
		return fmt.Errorf("no relay profile %q (have %s)", profile, strings.Join(r.cfg.Relay.RelayProfileNames(), ", "))
	}
	return relay.CheckPick(p, pick.Model, pick.Effort)
}

// relayLaunch is one start of an agent. Its name, spec and the seat it
// replaces are settled on the loop goroutine (prepareLaunch, beginLaunch);
// killing the old seat and launching (which can wait on a git pull and the
// terminal) happen off it (run); the result is recorded on it again
// (finishLaunch). Sends that arrive meanwhile wait in held.
type relayLaunch struct {
	name, profile, dir string
	resume             bool // continues the named relay's own session
	pick               relayPick
	spec               relay.Spec
	innerID            string
	// replaces is the relay whose agent this start stops first (a restart
	// replaces itself; a switch the agent it hands over from). endOld, for
	// a switch, is why that relay ends.
	replaces, endOld string
	old              relaySeat
	// oldIdle, when the old seat's queue was typing a send as this began,
	// is closed once that send is settled: a send cut off by the old
	// agent's stop then joins held before the new agent is launched.
	oldIdle <-chan struct{}
	held    []heldSend
	// brief, for a switch, is the handoff brief, typed into the new agent
	// before anything that waited for it; briefAuto marks its send as the
	// harness's own. briefDone hears how typing it went, and drained when
	// what waited during the launch has been typed too.
	brief              string
	briefAuto          bool
	briefDone, drained chan sendResult
	// wasActive: the relay it replaces was running when this began (a
	// restart can also bring back one that was stopped). stopped is why
	// the relay was stopped while this ran (relay_control stop, Stop
	// session); the new agent is then not kept. Both under relays.mu.
	wasActive bool
	stopped   string
	launch    func(relay.Spec, string, bool) (relaySeat, relay.Started, error)

	seat    relaySeat
	started relay.Started
	err     error
}

// heldSend is a send that waits its turn to be typed: for a relay to
// finish starting, or in a seat's queue. done, when set, hears how typing
// it went, and whoever waits on it records a failure; otherwise a failure is
// recorded here as undelivered. A barrier types nothing: it tells done that
// everything queued before it has been typed.
type heldSend struct {
	id, text, mode string
	done           chan sendResult
	barrier        bool
	// on is the relay the send is on the record for, when it waits in a
	// start that may hand it to another (a switch).
	on string
}

// sendResult is how typing one send went.
type sendResult struct {
	how string
	err error
}

// sendQueue types sends into one seat one at a time, in the order they were
// accepted: seat.Send can wait many seconds for the agent to be ready, and
// two sends waiting at once could be typed the other way round (a
// correction reaching the agent before what it corrects). Under relays.mu.
type sendQueue struct {
	name    string
	items   []heldSend
	running bool
	// movedTo is the start that took this seat out of use: what it was
	// typing when the seat closed goes there, as the rest of the queue did.
	movedTo *relayLaunch
	// idle is closed when the goroutine typing this queue ends.
	idle chan struct{}
}

// enqueueSend queues a send for a seat and makes sure something types it.
// Called on the loop goroutine, where sends are accepted and recorded, so
// the queue's order is the record's order.
func (r *Runtime) enqueueSend(name string, seat relaySeat, items ...heldSend) {
	r.relays.mu.Lock()
	defer r.relays.mu.Unlock()
	if r.relays.queues == nil {
		r.relays.queues = map[relaySeat]*sendQueue{}
	}
	q := r.relays.queues[seat]
	if q == nil {
		q = &sendQueue{name: name, idle: make(chan struct{})}
		r.relays.queues[seat] = q
	}
	q.items = append(q.items, items...)
	if !q.running && len(q.items) > 0 {
		q.running = true
		go r.drainSends(seat, q)
	}
}

// drainSends types a seat's queued sends in order and ends when the queue
// is empty. Sends for a seat that is no longer the relay's (stopped, or
// replaced) are not typed: they are recorded undelivered, as a send to a
// stopped relay is. Once the session has ended they are dropped. Never on
// the loop goroutine.
func (r *Runtime) drainSends(seat relaySeat, q *sendQueue) {
	for {
		r.relays.mu.Lock()
		if len(q.items) == 0 {
			q.running = false
			if r.relays.queues[seat] == q {
				delete(r.relays.queues, seat)
			}
			close(q.idle)
			r.relays.mu.Unlock()
			return
		}
		h := q.items[0]
		q.items = q.items[1:]
		current := r.relays.seats[q.name] == seat
		r.relays.mu.Unlock()
		var res sendResult
		select {
		case <-r.closed:
			res.err = fmt.Errorf("the session is ending")
		default:
			switch {
			case h.barrier:
			case !current:
				res.err = fmt.Errorf("the agent is not running")
			default:
				res.how, res.err = seat.Send(h.id, h.text, h.mode)
			}
		}
		if res.err != nil && r.passOn(q, h) {
			continue
		}
		if errors.Is(res.err, relay.ErrClosed) {
			// Its terminal was closed under it and no agent took over: the
			// relay was stopped.
			res.err = fmt.Errorf("the agent is not running")
		}
		r.settleSend(q.name, h, res)
	}
}

// passOn hands a send that could not be typed because a restart or a
// switch took its seat out of use to that start, ahead of the sends the
// start took from the queue (this one was accepted before them). False when
// no start took the seat, or that start is already over. Not on the loop
// goroutine.
func (r *Runtime) passOn(q *sendQueue, h heldSend) bool {
	r.relays.mu.Lock()
	defer r.relays.mu.Unlock()
	job := q.movedTo
	if job == nil || r.relays.launching[job.name] != job {
		return false
	}
	if h.on == "" {
		h.on = q.name
	}
	job.held = append([]heldSend{h}, job.held...)
	return true
}

// awaitSend waits for a queued send to be typed, or for the session to
// end: a send that was moved into a start the session's end cut short may
// never be settled, and its tool call must not wait for it for ever.
func (r *Runtime) awaitSend(done <-chan sendResult) sendResult {
	select {
	case res := <-done:
		return res
	case <-r.closed:
		select {
		case res := <-done:
			return res
		default:
		}
		return sendResult{err: fmt.Errorf("the session is ending")}
	}
}

// settleSend tells a send's waiter how it went, or records a failure as
// undelivered when nobody waits. Any goroutine.
func (r *Runtime) settleSend(name string, h heldSend, res sendResult) {
	switch {
	case h.done != nil:
		h.done <- res
	case res.err != nil && !h.barrier:
		r.post(func() {
			r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: h.id, OK: false, Detail: clipRunes(res.err.Error(), 2000)}))
		})
	}
}

// prepareLaunch settles what a start launches. Loop goroutine (or before
// the loop starts).
func (r *Runtime) prepareLaunch(profile, dir, name string, resume bool, pick relayPick) (*relayLaunch, error) {
	if name == "" {
		name = profile
		for i := 2; r.st.Relays[name] != nil && r.st.Relays[name].Active() || r.relays.launch(name) != nil; i++ {
			name = fmt.Sprintf("%s-%d", profile, i)
		}
	} else if r.relays.launch(name) != nil {
		return nil, fmt.Errorf("%s is already starting; wait for it", name)
	}
	if dir == "" {
		dir = r.workDir()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if !resume {
		if err := r.checkRelayPick(profile, pick); err != nil {
			return nil, err
		}
	}
	spec, err := r.relaySpec(name, profile, dir, pick)
	if err != nil {
		return nil, err
	}
	job := &relayLaunch{name: name, profile: profile, dir: dir, resume: resume, pick: pick, spec: spec, launch: launchRelay}
	if prev := r.st.Relays[name]; prev != nil && resume {
		job.innerID = prev.InnerID
	}
	return job, nil
}

// beginLaunch claims the job's names and takes the seat it replaces out of
// use, so nothing is typed into an agent about to be stopped and its late
// news is not taken for the new one's. Loop goroutine.
func (r *Runtime) beginLaunch(job *relayLaunch, replaces string) {
	job.replaces = replaces
	if rel := r.st.Relays[replaces]; replaces != "" && rel != nil && rel.Active() {
		job.wasActive = true
	}
	r.relays.mu.Lock()
	if r.relays.launching == nil {
		r.relays.launching = map[string]*relayLaunch{}
	}
	r.relays.launching[job.name] = job
	if replaces != "" {
		r.relays.launching[replaces] = job
		job.old = r.relays.seats[replaces]
		delete(r.relays.seats, replaces)
		// What was still waiting to be typed into the agent being stopped
		// goes to the new one, ahead of what arrives during the launch.
		if q := r.relays.queues[job.old]; job.old != nil && q != nil {
			for _, h := range q.items {
				if h.on == "" {
					h.on = replaces
				}
				job.held = append(job.held, h)
			}
			q.items = nil
			q.movedTo = job
			if q.running {
				job.oldIdle = q.idle
			}
		}
	}
	r.relays.mu.Unlock()
}

// run stops the replaced agent and launches the new one. Never on the loop
// goroutine: both can take many seconds.
func (job *relayLaunch) run() {
	if job.old != nil {
		_ = job.old.Kill()
	}
	if job.oldIdle != nil {
		// The send the old agent was waiting to type stops waiting once its
		// terminal is closed, and joins held; bounded, as a stuck terminal
		// call must not hold the start.
		select {
		case <-job.oldIdle:
		case <-time.After(oldSendSettle):
		}
	}
	job.seat, job.started, job.err = job.launch(job.spec, job.innerID, job.resume && job.innerID != "")
}

// oldSendSettle bounds how long a start waits for the send the agent it
// replaces was typing to give up. Once the old terminal is closed that send
// fails at its next step, which takes well under a second.
const oldSendSettle = 15 * time.Second

// finishLaunch records how a start went and returns the relay's name. The
// sends that waited for it are queued for the new seat, after a switch's
// handoff brief and before anything sent from now on (or are recorded as
// undelivered when the start failed). Loop goroutine.
func (r *Runtime) finishLaunch(job *relayLaunch) (_ string, err error) {
	var held []heldSend
	r.relays.mu.Lock()
	for name, j := range r.relays.launching {
		if j == job {
			delete(r.relays.launching, name)
		}
	}
	held, job.held = job.held, nil
	stopped := job.stopped
	r.relays.mu.Unlock()
	err = job.err
	if err == nil {
		rel := r.st.Relays[job.replaces]
		gone := job.replaces != "" && job.endOld == "" && job.wasActive && (rel == nil || !rel.Active())
		if stopped != "" || gone {
			// Stopped while it started (again): it stays stopped.
			err = fmt.Errorf("%s was stopped while it started", firstNonEmpty(job.replaces, job.name))
			seat := job.seat
			if r.ending {
				// The session is being stopped and the process may exit
				// before another goroutine would run: an agent that came
				// up now is stopped before anything else, so "stop
				// everything" leaves none behind.
				_ = seat.Kill()
			} else {
				go func() { _ = seat.Kill() }()
			}
		}
	}
	r.startFailed = err != nil
	if pick, ok := r.relayRepick[job.name]; ok && (err != nil || pick == job.pick) {
		delete(r.relayRepick, job.name)
	}
	if err != nil {
		switch rel := r.st.Relays[job.replaces]; {
		case job.endOld != "" && rel != nil && rel.Active():
			r.append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: job.replaces, Reason: job.endOld + ", which could not start: " + err.Error()}))
		case job.endOld == "" && rel != nil && rel.Active() && rel.State != "exited":
			// Its agent was stopped and no other runs in its place: it is
			// on the record as exited, so eagent is told and can start it
			// again or stop it.
			r.append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: job.replaces, State: "exited", Detail: clipRunes("it was stopped to start again and could not start: "+err.Error(), 2000)}))
		}
		for _, h := range held {
			if h.done != nil {
				h.done <- sendResult{err: fmt.Errorf("the agent could not start again: %w", err)}
				continue
			}
			r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: firstNonEmpty(h.on, job.replaces), ID: h.id, OK: false, Detail: clipRunes("the agent could not start again: "+err.Error(), 2000)}))
		}
		r.syncRelaySettings()
		return "", err
	}
	if rel := r.st.Relays[job.replaces]; job.endOld != "" && rel != nil && rel.Active() {
		r.append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: job.replaces, Reason: job.endOld}))
	}
	r.summary.final = false // a session with an agent started is not ending
	spec, started := job.spec, job.started
	r.append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{
		Name: job.name, Profile: job.profile, Agent: spec.Profile.Agent, AgentLabel: relay.AgentLabel(spec.Profile.Agent), Label: spec.Profile.Label, Dir: job.dir,
		Host: started.Host, Target: started.Target, Command: started.Command, InnerID: started.InnerID, Resumed: job.resume && job.innerID != "",
		Model: job.pick.Model, Effort: job.pick.Effort,
	}))
	r.syncRelaySettings()
	r.watchSeat(job.name, job.seat)
	// One queue for the new agent, in order: the handoff brief, what
	// waited during the launch, then whatever is sent from now on.
	var queue []heldSend
	if job.brief != "" {
		id := r.st.NextRelaySendID()
		r.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: job.name, ID: id, From: "eagent", Mode: "queue", Text: job.brief, Auto: job.briefAuto}))
		queue = append(queue, heldSend{id: id, text: job.brief, mode: "queue", done: job.briefDone})
	}
	queue = append(queue, held...)
	if job.drained != nil {
		queue = append(queue, heldSend{barrier: true, done: job.drained})
	}
	r.enqueueSend(job.name, job.seat, queue...)
	return job.name, nil
}

// abandonLaunch is a start whose result could not be recorded because the
// session ended: the new agent is stopped, as nothing would watch it, and
// whoever waits on a send held for it is told. Not on the loop goroutine.
func (r *Runtime) abandonLaunch(job *relayLaunch) {
	if job.err == nil {
		_ = job.seat.Kill()
	}
	r.relays.mu.Lock()
	held := job.held
	job.held = nil
	r.relays.mu.Unlock()
	for _, h := range held {
		if h.done != nil {
			h.done <- sendResult{err: fmt.Errorf("the session is ending")}
		}
	}
}

// stopLaunches marks the starts under way for a relay name (every one,
// for "") as stopped: what they launch is killed when it is up rather
// than kept. Loop goroutine.
func (r *Runtime) stopLaunches(name, reason string) {
	r.relays.mu.Lock()
	defer r.relays.mu.Unlock()
	for n, job := range r.relays.launching {
		if name == "" || n == name {
			job.stopped = reason
		}
	}
}

// holdSend keeps a send for a relay that is starting (again), to type once
// it is up; false when none is. Loop goroutine.
func (r *Runtime) holdSend(name string, h heldSend) bool {
	r.relays.mu.Lock()
	defer r.relays.mu.Unlock()
	job := r.relays.launching[name]
	if job == nil {
		return false
	}
	h.on = name
	job.held = append(job.held, h)
	return true
}

// launchOffLoop runs a start from a tool's goroutine: prepare is run on
// the loop goroutine and returns the job (or an error), the launch runs
// here, and the result is recorded on the loop again. Sends that waited
// for it are queued for the new agent there.
func (r *Runtime) launchOffLoop(prepare func() (*relayLaunch, error)) (string, error) {
	var job *relayLaunch
	var err error
	r.sync(func() { job, err = prepare() })
	if err != nil {
		return "", err
	}
	if job == nil {
		return "", fmt.Errorf("the session is ending")
	}
	job.run()
	var name string
	ran := false
	r.sync(func() {
		ran = true
		name, err = r.finishLaunch(job)
	})
	if !ran {
		// The session ended meanwhile: nothing will watch the new agent
		// or know it runs.
		r.abandonLaunch(job)
		return "", fmt.Errorf("the session is ending")
	}
	if err != nil {
		return "", err
	}
	return name, nil
}

// relaunch starts a relay again on its own session with its picks, from a
// tool's goroutine; what waited for it is typed in after.
func (r *Runtime) relaunch(name string) (string, error) {
	return r.launchOffLoop(func() (*relayLaunch, error) {
		rel := r.st.Relays[name]
		if rel == nil {
			return nil, fmt.Errorf("no agent named %q here", name)
		}
		job, err := r.prepareLaunch(rel.Profile, rel.Dir, rel.Name, true, relayPick{rel.Model, rel.Effort})
		if err != nil {
			return nil, err
		}
		r.beginLaunch(job, rel.Name)
		return job, nil
	})
}

// startRelay launches an agent in a terminal of its own, with the session's
// model and effort picks, and waits for it. resume continues the named
// relay's own session. Before the loop starts: on the loop goroutine, use
// launchOffLoop or restartRelayWith, which do not hold the loop while the
// agent launches.
func (r *Runtime) startRelay(profile, dir, name string, resume bool, pick relayPick) (_ string, err error) {
	// A session with no terminal that ends without ever having had an
	// agent, after a start that failed, did not do what it was started
	// for (maybeEnd).
	defer func() { r.startFailed = err != nil }()
	job, err := r.prepareLaunch(profile, dir, name, resume, pick)
	if err != nil {
		return "", err
	}
	r.beginLaunch(job, "")
	job.run()
	return r.finishLaunch(job)
}

// watchSeat pumps a seat's observations into the log.
func (r *Runtime) watchSeat(name string, seat relaySeat) {
	r.relays.put(name, seat)
	go func() {
		for ev := range seat.Events() {
			ev := ev
			r.post(func() {
				// News from a seat that is no longer the relay's (stopped
				// for a restart, a switch or a stop) is not the new one's.
				if r.relays.get(name) == seat {
					r.onRelayEvent(name, ev)
				}
			})
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
		if ev.State == "idle" {
			r.applyWaitingRepick(name)
		}
	case relay.KindDelivered:
		r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: ev.SendID, OK: ev.OK, Detail: clipRunes(ev.Detail, 2000)}))
	case relay.KindProgress:
		r.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: name, Text: clipRunes(ev.Text, 2000)}))
	case relay.KindTurn:
		// A turn that answered eagent's own message and ran nothing is an
		// agent waiting on work of its own; nudging it again only starts
		// another such turn.
		idle := false
		if cur := r.st.Relays[name]; cur != nil {
			idle = ev.Tools == 0 && !ev.Interrupted && cur.LastSendFrom == "eagent" && cur.LastSendAt.After(cur.LastTurnAt)
		}
		r.append(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: name, Text: clipRunes(ev.Text, maxRecordedReport), DurationMS: ev.DurationMS, Tools: ev.Tools, Images: ev.Images, Interrupted: ev.Interrupted, Idle: idle}))
		r.relays.mu.Lock()
		delete(r.relays.activity, name)
		r.relays.mu.Unlock()
		// A model or effort change that waited for this turn to end.
		r.applyWaitingRepick(name)
		r.armAutopilotCheck(name)
	case relay.KindLimit:
		r.append(event.New(event.RelayLimit, event.ActorHarness, event.RelayLimitData{Name: name, UsedPercent: ev.UsedPercent, Window: ev.Window, ResetsAt: ev.ResetsAt, Text: ev.Text, Exhausted: ev.Exhausted}))
	case relay.KindActivity:
		r.relays.mu.Lock()
		if r.relays.activity == nil {
			r.relays.activity = map[string]relayActivity{}
		}
		r.relays.activity[name] = relayActivity{text: ev.Text, at: time.Now()}
		r.relays.mu.Unlock()
		r.relays.noteStep(name, ev.Text, time.Now())
		if r.phone != nil {
			// The status line names the step: work it out again (which
			// wakes the keeper when it changed).
			r.phone.noteState(r)
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
		spec, err := r.relaySpec(rel.Name, rel.Profile, rel.Dir, relayPick{rel.Model, rel.Effort})
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
// stopSession is Stop session (from the app or the web UI): the relayed
// agents stop too, as /stop-relay stops one, and then the session quits.
// Other ways a session ends leave them running for a resume to pick up.
// Loop goroutine.
func (r *Runtime) stopSession(reason string) {
	r.stopLaunches("", reason)
	for _, rel := range r.st.ActiveRelays() {
		if seat := r.relays.get(rel.Name); seat != nil {
			if err := seat.Kill(); err != nil {
				r.ui.Log("relay %s: %v", rel.Name, err)
			}
			r.relays.drop(rel.Name, seat)
		}
		r.append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: rel.Name, Reason: reason}))
	}
	r.beginShutdown("quit", 0)
}

func (r *Runtime) closeRelays() {
	for _, seat := range r.relays.all() {
		seat.Close()
	}
}

// ---- routing --------------------------------------------------------------

// agentWords are the names people use for the agents a relay can run
// ("claude", "claude code", "agy"), lower case, from the registry: a new
// agent is recognised in messages without a word list to keep up.
var agentWords = sync.OnceValue(func() []string {
	var out []string
	for _, a := range relay.Agents() {
		for _, w := range []string{a.Name(), a.Label(), a.DisplayName(), a.Binary()} {
			if w = strings.ToLower(strings.TrimSpace(w)); w != "" && !slices.Contains(out, w) {
				out = append(out, w)
			}
		}
	}
	return out
})

// agentPhrases fills each agent's names into phrases that mention one
// ("what is {} doing").
func agentPhrases(patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		for _, w := range agentWords() {
			out = append(out, strings.ReplaceAll(p, "{}", w))
		}
	}
	return out
}

var (
	// eagentPhrases mark a message as meant for eagent, wherever they stand.
	eagentPhrases = sync.OnceValue(func() []string {
		return append([]string{"not for relay", "not for the relay", "not to relay", "don't relay", "do not relay", "not for the agent", "this can be you", "between you and me", "can be you and not relayed"},
			agentPhrases("you (not {})")...)
	})
	// eagentNamePhrases and metaNamePhrases are the same two kinds of
	// phrase ending in an agent's name. They count only where the name is
	// a whole word (namesAgent): agents have short names ("pi"), and
	// "update pip" or "not for pictures" is work for the agent.
	eagentNamePhrases = sync.OnceValue(func() []string { return agentPhrases("not for {}", "you, not {}") })
	metaNamePhrases   = sync.OnceValue(func() []string { return agentPhrases("restart {}", "upgrade {}", "update {}") })
	// metaPhrases mark a short message as a question or an order about the
	// session rather than work for the agent.
	metaPhrases = sync.OnceValue(func() []string {
		return append([]string{
			"how's it going", "hows it going", "how is it going", "how's it looking", "how are things", "how far along", "any update", "what's the update", "whats the update", "status update", "status?",
			"is it stuck", "it's not stuck", "sure it's not stuck", "is it done", "did it finish", "is it working", "what's taking so long", "whats taking so long", "why is it taking so long", "taking so long",
			"what is it doing", "what's it doing", "whats it doing", "what is the agent doing", "what's the agent doing",
			"what did you send", "did you send", "send me the prompt", "is this you", "are both screens", "you're a relay", "you are a relay", "are you there",
			"restart the session", "restart the agent", "restart it", "upgrade and then restart", "update and then restart",
			"close the screen", "kill the screen", "kill screen", "close out the screen", "stop monitoring", "stop polling", "stop updating", "stop the updates", "updates every", "update me every", "give me updates", "regular updates", "less often", "more often",
		}, agentPhrases("what is {} doing", "what's {} doing", "whats {} doing", "you have {} doing")...)
	})
)

// namesAgent reports that l holds phrase, which ends in an agent's name,
// with the name as a whole word: "update pi" and "restart codex, please",
// but not "update pip", "restart pipeline" or the start of a file's name
// ("update claude.md").
func namesAgent(l, phrase string) bool {
	word := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' }
	for from := 0; from < len(l); {
		i := strings.Index(l[from:], phrase)
		if i < 0 {
			return false
		}
		end := from + i + len(phrase)
		switch {
		case end == len(l):
			return true
		case word(l[end]):
		case (l[end] == '.' || l[end] == '-' || l[end] == '/') && end+1 < len(l) && word(l[end+1]):
		default:
			return true
		}
		from += i + 1
	}
	return false
}

// addressedToEagent reports a user message meant for eagent itself rather
// than the agent.
func addressedToEagent(text string) bool {
	l := strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"@e ", "@e:", "@e,", "@eagent", "eagent:", "eagent,"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	for _, phrase := range eagentPhrases() {
		if strings.Contains(l, phrase) {
			return true
		}
	}
	for _, phrase := range eagentNamePhrases() {
		if namesAgent(l, phrase) {
			return true
		}
	}
	return false
}

// relayMeta reports a short message about the session itself (the agent's
// progress, its terminal, how often to hear about it) rather than work for
// the agent: eagent answers or acts on those, so they are not typed into
// the agent's terminal.
func relayMeta(text string) bool {
	l := strings.ToLower(strings.TrimSpace(text))
	if len(l) > 200 {
		return false // a long message is work, even if it also asks how things are going
	}
	for _, phrase := range metaPhrases() {
		if strings.Contains(l, phrase) {
			return true
		}
	}
	for _, phrase := range metaNamePhrases() {
		if namesAgent(l, phrase) {
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

// routeUserMessage decides what becomes of something the user wrote: it is
// typed into the agent's terminal as written (rel says which agent), or it
// is kept for eagent's model (held says why, in words the model reads).
// What the client chose wins over what the text suggests. Loop goroutine
// (or before the loop starts).
func (r *Runtime) routeUserMessage(d event.UserMessageData) (rel *state.Relay, held string) {
	active := r.st.ActiveRelays()
	switch {
	case d.Route == "eagent":
		return nil, "the user addressed it to you"
	case len(active) == 0:
		return nil, "no agent is running"
	case len(active) > 1:
		return nil, "more than one agent is running"
	}
	rel = active[0]
	// One starting again takes the message once it is up.
	if r.relays.launch(rel.Name) == nil && (rel.State == "exited" || r.relays.get(rel.Name) == nil) {
		return nil, "the agent is not running"
	}
	if d.Route != "agent" {
		switch {
		case addressedToEagent(d.Text):
			return nil, "it is addressed to you"
		case relayMeta(d.Text):
			return nil, "it asks about the session"
		}
	}
	return rel, ""
}

// forwardUserMessage types a user message into the agent's terminal the
// moment it arrives, word for word, so the agent hears it without waiting
// for a model call. Loop goroutine (or before the loop starts).
func (r *Runtime) forwardUserMessage(rel *state.Relay, seq int64, d event.UserMessageData) {
	text := d.Text
	for _, a := range d.Attachments {
		text += fmt.Sprintf("\n\n[The user attached %s: %s]", a.Name, a.Path)
	}
	mode := "queue"
	if relayUrgent(text) {
		mode = "interrupt"
	}
	if d.Delivery != "" {
		mode = d.Delivery
	}
	name, id := rel.Name, r.st.NextRelaySendID()
	r.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: name, ID: id, From: "user", Seq: seq, Mode: mode, Text: text, Auto: true}))
	seat := r.relays.get(name)
	if seat == nil {
		if !r.holdSend(name, heldSend{id: id, text: text, mode: mode}) {
			r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: id, OK: false, Detail: "the agent is not running"}))
		}
		return
	}
	// Typing can wait on the agent's startup screens; never on the loop.
	// The seat's queue types it after what was accepted before it.
	r.enqueueSend(name, seat, heldSend{id: id, text: text, mode: mode})
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

// resolveRelay picks the relay a tool call means. Loop goroutine only.
func (r *Runtime) resolveRelay(name string) (*state.Relay, error) {
	if name != "" {
		rel := r.st.Relays[name]
		if rel == nil {
			return nil, fmt.Errorf("no agent named %q here", name)
		}
		return rel, nil
	}
	active := r.st.ActiveRelays()
	switch len(active) {
	case 0:
		if n := len(r.st.RelayOrder); n > 0 {
			return r.st.Relays[r.st.RelayOrder[n-1]], nil
		}
		return nil, fmt.Errorf("no agent is running; start one with relay_start")
	case 1:
		return active[0], nil
	}
	var names []string
	for _, a := range active {
		names = append(names, a.Name)
	}
	return nil, fmt.Errorf("several agents are running (%s); say which with relay", strings.Join(names, ", "))
}

// ---- tools ----------------------------------------------------------------

// relayTool runs one of the tools that drive an agent. auto says the call
// is the harness's own (a command the user typed), not the model's: what it
// sends the agent is then marked so, and shows in the model's conversation
// as a notice. Not on the loop goroutine.
func (r *Runtime) relayTool(name string, str func(string) string, num func(string, int) int, list func(string) []string, auto bool) (string, bool) {
	switch name {
	case "relay_start":
		profile := strings.TrimSpace(str("profile"))
		dir := relay.ExpandHome(strings.TrimSpace(str("dir")))
		started, err := r.launchOffLoop(func() (*relayLaunch, error) {
			if dir != "" && !filepath.IsAbs(dir) {
				dir = filepath.Join(r.workDir(), dir)
			}
			job, err := r.prepareLaunch(profile, dir, "", false, relayPick{pickArg(str("model")), pickArg(str("effort"))})
			if err != nil {
				r.startFailed = true
				return nil, err
			}
			r.beginLaunch(job, "")
			return job, nil
		})
		if err != nil {
			return "Could not start the agent: " + err.Error(), true
		}
		return r.relayStatusText(started, "Started."), false
	case "relay_forward", "relay_send":
		mode := str("mode")
		var rel *state.Relay
		var id, text string
		var err error
		var seat relaySeat
		held := false
		done := make(chan sendResult, 1)
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
					err = fmt.Errorf("that message already went to %s as written (%s); to add to it, use relay_send", rel.Who(), prev)
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
			r.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: rel.Name, ID: id, From: from, Seq: seq, Mode: mode, Text: text, Auto: auto}))
			// Queued (or held) as it is recorded, so it is typed in the
			// order sends were accepted.
			if seat = r.relays.get(rel.Name); seat != nil {
				r.enqueueSend(rel.Name, seat, heldSend{id: id, text: text, mode: mode, done: done})
				return
			}
			if held = r.holdSend(rel.Name, heldSend{id: id, text: text, mode: mode}); !held {
				r.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: rel.Name, ID: id, OK: false, Detail: "the agent is not running"}))
			}
		})
		if err != nil {
			return err.Error(), true
		}
		if held {
			return fmt.Sprintf("%s is starting; the message (%s) is typed in once it is up. Its transcript will confirm arrival; you are told only if it does not.", rel.Who(), id), false
		}
		if seat == nil {
			return rel.Who() + " is not running; restart it with relay_control restart.", true
		}
		res := r.awaitSend(done)
		how, err := res.how, res.err
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
			if action == "interrupt" {
				return "Interrupted " + rel.Who() + ".", false
			}
			// What the keys did is on the screen; show it, so the next
			// call can tell whether the dialog is gone.
			time.Sleep(keysSettle)
			screen, serr := seat.Screen(40)
			if serr != nil {
				return "Keys sent, but the screen could not be read: " + serr.Error(), false
			}
			return "Keys sent. The screen now:\n" + strings.TrimRight(screen, "\n"), false
		case "restart":
			started, err := r.relaunch(rel.Name)
			if err != nil {
				return "Could not restart: " + err.Error(), true
			}
			return r.relayStatusText(started, "Restarted, continuing its own session."), false
		case "upgrade":
			out, uerr := upgradeAgent(rel.Agent)
			if uerr != nil {
				return "The upgrade failed, so nothing was restarted: " + uerr.Error() + "\n" + out, true
			}
			started, err := r.relaunch(rel.Name)
			if err != nil {
				return "Upgraded, but the restart failed: " + err.Error() + "\n" + out, true
			}
			return r.relayStatusText(started, "Upgraded ("+lastLine(out)+") and restarted, continuing its own session."), false
		case "switch":
			profile := strings.TrimSpace(str("profile"))
			if profile == "" {
				return "switch: say which profile to hand over to (" + strings.Join(r.cfg.Relay.RelayProfileNames(), ", ") + ")", true
			}
			briefDone, drained := make(chan sendResult, 1), make(chan sendResult, 1)
			// The old agent is stopped and the new one launched off the loop
			// goroutine; the old one ends on the record just before the new
			// one starts.
			started, err := r.launchOffLoop(func() (*relayLaunch, error) {
				// Checked before the running agent is stopped: a model or
				// effort the new one cannot take must leave this one working.
				pick := relayPick{pickArg(str("model")), pickArg(str("effort"))}
				if err := r.checkRelayPick(profile, pick); err != nil {
					return nil, err
				}
				job, err := r.prepareLaunch(profile, rel.Dir, "", false, pick)
				if err != nil {
					return nil, err
				}
				if r.relays.launch(rel.Name) != nil {
					return nil, fmt.Errorf("%s is starting again; wait for it", rel.Who())
				}
				job.endOld = "handed over to " + profile
				job.brief, job.briefAuto, job.briefDone, job.drained = r.handoffBrief(rel), auto, briefDone, drained
				r.beginLaunch(job, rel.Name)
				return job, nil
			})
			if err != nil {
				return "Could not switch: " + err.Error(), true
			}
			// The brief is typed first, then what the user sent during the
			// handover (finishLaunch queued both, in that order).
			berr := r.awaitSend(briefDone).err
			r.awaitSend(drained)
			if berr != nil {
				return "Switched to " + started + ", but the handoff brief could not be typed: " + berr.Error(), true
			}
			return r.relayStatusText(started, "Handed over from "+rel.Who()+"; the new agent has the handoff brief (mirrored to the user)."), false
		case "model":
			pick := relayPick{strings.TrimSpace(str("model")), strings.TrimSpace(str("effort"))}
			if pick == (relayPick{}) {
				return "model: say the model, the effort, or both (\"default\" goes back to the agent's own)", true
			}
			var msg string
			r.sync(func() {
				// "default" clears that pick; the other one is kept.
				next := relayPick{rel.Model, rel.Effort}
				if p, ok := r.relayRepick[rel.Name]; ok {
					next = p
				}
				for _, f := range []struct {
					in  string
					out *string
				}{{pick.Model, &next.Model}, {pick.Effort, &next.Effort}} {
					switch {
					case strings.EqualFold(f.in, "default"):
						*f.out = ""
					case f.in != "":
						*f.out = f.in
					}
				}
				msg, err = r.repickRelay(rel.Name, next)
			})
			if err != nil {
				return err.Error(), true
			}
			return msg, false
		case "stop":
			// The terminal that is the relay's now, not the one read
			// above: a restart may have finished in between, and its new
			// agent is the one to stop.
			var current relaySeat
			r.sync(func() {
				if current = r.relays.get(rel.Name); current != nil {
					r.relays.drop(rel.Name, current)
				}
				r.stopLaunches(rel.Name, "stopped")
			})
			if current != nil {
				_ = current.Kill()
			}
			r.sync(func() {
				r.append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: rel.Name, Reason: firstNonEmpty(str("reason"), "stopped")}))
				r.armAutopilots()
			})
			return "Stopped " + rel.Who() + ".", false
		}
		return fmt.Sprintf("unknown action %q", action), true
	case "relay_autopilot":
		var out string
		var err error
		r.sync(func() {
			var rel *state.Relay
			if rel, err = r.resolveRelay(str("relay")); err != nil {
				return
			}
			d := event.RelayAutopilotData{Name: rel.Name}
			args := str("off")
			if args == "true" {
				d.Off = true
				out = "Autopilot off for " + rel.Who() + "."
			} else {
				d.Instruction = strings.TrimSpace(str("instruction"))
				if d.Instruction == "" {
					d.Instruction = "Keep going: pick the next most valuable thing toward what the user asked, do it, commit, and continue."
				}
				d.Decide = str("decide") != "false"
				if u := strings.TrimSpace(str("until")); u != "" {
					t, perr := parseUntil(u, time.Now())
					if perr != nil {
						err = perr
						return
					}
					d.Until = t.Format(time.RFC3339)
				}
				out = "Autopilot on for " + rel.Who() + " " + autopilotUntil(d) + ". At every turn end: nudge it on if it stopped, leave it alone while it waits on work of its own, decide its questions yourself"
				if !d.Decide {
					out = "Autopilot on for " + rel.Who() + " " + autopilotUntil(d) + ". At every turn end: nudge it on if it stopped, leave it alone while it waits on work of its own, pass its questions to the user"
				}
				out += "."
			}
			r.append(event.New(event.RelayAutopilot, event.ActorHarness, d))
			r.armAutopilots()
		})
		if err != nil {
			return err.Error(), true
		}
		return out, false
	}
	return "unknown relay tool", true
}

// maxRecordedReport bounds the agent's turn report kept in the record. The
// whole report is meant to be there (the user gets it, and eagent's own
// prompt carries report_max_chars of it); this only keeps one event a sane
// line of the log. A megabyte is far beyond any report an agent writes,
// and stays well under the 16 MiB a line of the log may be when the
// session is archived, even with JSON's escaping.
const maxRecordedReport = 1 << 20

// keysSettle is how long a screen is given to redraw after keys were sent
// to it. A variable so tests can run without it.
var keysSettle = 700 * time.Millisecond

// upgradeAgent runs the agent CLI's own updater.
func upgradeAgent(agent string) (string, error) {
	argv, err := relay.UpgradeCommand(agent)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 5 * time.Second // an updater child that keeps the output open
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return clipRunes(s, 160)
}

// handoffBrief is what a new agent is told when it takes over: the user's
// instructions, verbatim, and the old agent's last word, from the record.
// Loop goroutine.
func (r *Runtime) handoffBrief(rel *state.Relay) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are taking over this work from %s, which was working in %s.", rel.Who(), rel.Dir)
	if rel.Transcript != "" {
		fmt.Fprintf(&b, " Its full session log is %s (JSONL) if you need detail.", rel.Transcript)
	}
	b.WriteString("\n\nThe user's instructions so far, verbatim, oldest first:\n")
	var texts []string
	for _, ev := range r.st.Events {
		if ev.Type != event.RelaySend {
			continue
		}
		var d event.RelaySendData
		if ev.Decode(&d) == nil && d.Name == rel.Name && d.From == "user" {
			texts = append(texts, d.Text)
		}
	}
	if len(texts) > 12 {
		texts = texts[len(texts)-12:]
		b.WriteString("(the latest 12)\n")
	}
	for _, t := range texts {
		b.WriteString("- " + clipRunes(strings.ReplaceAll(strings.TrimSpace(t), "\n", "\n  "), 3000) + "\n")
	}
	if rel.LastTurn != "" {
		fmt.Fprintf(&b, "\n%s's last report:\n%s\n", rel.Who(), clipRunes(rel.LastTurn, 6000))
	}
	b.WriteString("\nCheck the working tree and recent commits, then continue where it left off.")
	return b.String()
}

// parseUntil reads an autopilot deadline: "10:00" (the next such local
// time), "8h" or "90m" from now, or a date and time.
func parseUntil(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(d), nil
	}
	for _, layout := range []string{"15:04", "3:04pm", "3pm", "15"} {
		if t, err := time.ParseInLocation(layout, strings.ToLower(strings.ReplaceAll(s, " ", "")), now.Location()); err == nil {
			at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
			if !at.After(now) {
				at = at.Add(24 * time.Hour)
			}
			return at, nil
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not read %q as a time: use 10:00, 8h, or 2026-10-28T10:00", s)
}

func autopilotUntil(d event.RelayAutopilotData) string {
	if t, err := time.Parse(time.RFC3339, d.Until); err == nil {
		return "until " + t.Local().Format("Mon 15:04")
	}
	return "until the user says stop"
}

// autopilotCheckAfter is how long an agent on autopilot may sit idle, with
// nothing sent to it, before eagent's model is woken to look. It leaves an
// agent that is waiting on its own work alone; this catches one that will
// not wake by itself.
var autopilotCheckAfter = 10 * time.Minute

// armAutopilotCheck (re)starts the idle check for one relay: if the agent is
// still idle after its last turn, with autopilot on and nothing sent to it,
// the model is woken, and again every autopilotCheckAfter while that lasts.
// Loop goroutine.
func (r *Runtime) armAutopilotCheck(name string) {
	if t := r.autopilotChecks[name]; t != nil {
		t.Stop()
		delete(r.autopilotChecks, name)
	}
	rel := r.st.Relays[name]
	if rel == nil || !rel.Active() || rel.Autopilot == nil || rel.State != "idle" {
		return
	}
	if r.autopilotChecks == nil {
		r.autopilotChecks = map[string]*time.Timer{}
	}
	turnAt := rel.LastTurnAt
	r.autopilotChecks[name] = time.AfterFunc(autopilotCheckAfter, func() {
		r.post(func() {
			cur := r.st.Relays[name]
			if r.ending || cur == nil || !cur.Active() || cur.Autopilot == nil || cur.State != "idle" || !cur.LastTurnAt.Equal(turnAt) || cur.LastSendAt.After(turnAt) {
				return
			}
			since := "its last turn"
			if !turnAt.IsZero() {
				since = fmt.Sprintf("its last turn %s ago", roundDur(time.Since(turnAt)))
			}
			r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: fmt.Sprintf("[Autopilot check: %s has been idle since %s, and nothing has been sent to it since.] If what it was waiting on should have reported by now, look with relay_screen and send it on with relay_send. If its own work is still running, hold; you are asked again in %s if it stays idle.", cur.Who(), since, roundDur(autopilotCheckAfter))}))
			r.armAutopilotCheck(name)
		})
	})
}

// armAutopilots sets a timer for each autopilot deadline; when one passes,
// the autopilot ends and eagent's model is told to wrap up. It also
// restarts each idle check. Loop goroutine.
func (r *Runtime) armAutopilots() {
	for name, t := range r.autopilotTimers {
		t.Stop()
		delete(r.autopilotTimers, name)
	}
	if r.autopilotTimers == nil {
		r.autopilotTimers = map[string]*time.Timer{}
	}
	names := map[string]bool{}
	for name := range r.autopilotChecks {
		names[name] = true
	}
	for _, rel := range r.st.ActiveRelays() {
		names[rel.Name] = true
	}
	for name := range names {
		r.armAutopilotCheck(name)
	}
	for _, rel := range r.st.ActiveRelays() {
		ap := rel.Autopilot
		if ap == nil || ap.Until == "" {
			continue
		}
		until, err := time.Parse(time.RFC3339, ap.Until)
		if err != nil {
			continue
		}
		name := rel.Name
		r.autopilotTimers[name] = time.AfterFunc(max(time.Until(until), time.Second), func() {
			r.post(func() {
				cur := r.st.Relays[name]
				if cur == nil || cur.Autopilot == nil || cur.Autopilot.Until != ap.Until || r.ending {
					return
				}
				r.append(event.New(event.RelayAutopilot, event.ActorHarness, event.RelayAutopilotData{Name: name, Off: true}))
				r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: fmt.Sprintf("[Autopilot for %s ended: it was set to run until %s.] Stop sending it on. Let it finish what it is doing, then tell the user what was done while they were away and where things stand.", cur.Who(), until.Local().Format("Mon 15:04"))}))
			})
		})
	}
}

// userSends lists the latest user messages sent to a relay, each with when
// it went and whether its arrival was confirmed. Loop goroutine.
func userSends(st *state.State, name string, n int) []string {
	rel := st.Relays[name]
	var out []string
	for _, ev := range st.Events {
		if ev.Type != event.RelaySend {
			continue
		}
		var d event.RelaySendData
		if ev.Decode(&d) != nil || d.Name != name || d.From != "user" {
			continue
		}
		status := "arrived"
		if rel != nil {
			if _, waiting := rel.Undelivered[d.ID]; waiting {
				status = "not confirmed yet"
				if d.Mode == "after_turn" {
					status = "held for the end of its turn"
				}
			}
		}
		out = append(out, fmt.Sprintf("%s %s ago (%s): %s", d.ID, roundDur(time.Since(ev.Time)), status, clipRunes(oneLine(d.Text, 200), 200)))
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
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

// relayStatusText describes an agent in full: for the user who asks with
// /relay, and as the result of starting or restarting one.
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
		if ap := rel.Autopilot; ap != nil {
			fmt.Fprintf(&b, "\nAutopilot: on %s; standing order: %s", autopilotUntil(*ap), clipRunes(ap.Instruction, 400))
		}
		if sent := userSends(r.st, rel.Name, 8); len(sent) > 0 {
			b.WriteString("\nWhat the user sent it, newest last:")
			for _, line := range sent {
				b.WriteString("\n- " + line)
			}
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

// relayPassthrough reports a slash command meant for the agent (Codex's
// /goal, Claude Code's /compact, ...): anything eagent does not own goes to
// the agent as typed. From the phone or the web, even eagent's
// terminal-only commands (/status, /help) go to the agent, since their
// output only shows in a terminal. Loop goroutine.
func (r *Runtime) relayPassthrough(line, source string) bool {
	if len(r.st.ActiveRelays()) == 0 {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch cmd := strings.ToLower(fields[0]); {
	case relayCommands[cmd]:
		return false
	case cmd == "/dismiss" && r.st.Question != nil:
		return false // closes eagent's own open question
	case cmd == "/quit" || cmd == "/exit" || cmd == "/q" || cmd == "/kill":
		// /kill stopped a process of eagent's own. That is gone, and the
		// word must never be typed into an agent out of habit.
		return false
	case cmd == "/status" || cmd == "/s" || cmd == "/tasks" || cmd == "/procs" || cmd == "/schedules" || cmd == "/help" || cmd == "/?":
		return source != ""
	}
	return true
}

// relayCommands are the slash commands a user can type (terminal, web UI,
// phone, Messenger) to manage the agent directly, with no model call.
var relayCommands = map[string]bool{"/interrupt": true, "/restart": true, "/model": true, "/upgrade": true, "/switch": true, "/autopilot": true, "/updates": true, "/stop-relay": true, "/relay": true, "/screen": true, "/notify-done": true}

// relayCommand runs one. Called on the loop; the work runs on its own
// goroutine (an upgrade takes minutes) and the outcome is told to the user
// directly, in plain words, without waiting for a model.
func (r *Runtime) relayCommand(fields []string, client *clientcaps.Caps) {
	if len(r.st.RelayOrder) == 0 {
		r.ui.Log("no agent has been started in this session")
		go r.tell("No agent has been started in this session yet. Ask me to start one ("+strings.Join(r.cfg.Relay.RelayProfileNames(), ", ")+").", false)
		return
	}
	cmd, args := fields[0], fields[1:]
	switch cmd {
	case "/updates":
		// No longer a setting (older clients still send it): eagent says
		// what the agent is doing as it goes.
		go r.tell("Updates are not a setting any more. While the agent works I tell you what it is doing as it goes, and you never wait more than a few minutes to hear.", false)
		return
	case "/notify-done":
		r.armNotifyDone()
		return
	case "/screen":
		active := r.st.ActiveRelays()
		if len(active) == 0 {
			go r.tell("No agent is running.", false)
			return
		}
		name, seat, phone := active[0].Name, r.relays.get(active[0].Name), r.phone
		go func() {
			if seat == nil {
				r.tell("The agent is not running, so there is no screen to show.", false)
				return
			}
			screen, err := seat.Screen(40)
			if err != nil {
				r.tell("Could not read its screen: "+err.Error(), false)
				return
			}
			r.ui.Log("%s", screen)
			if phone != nil {
				phone.postRelayScreen(name, screen)
			}
		}()
		return
	}
	go func() {
		params := map[string]any{}
		tool, ack := "relay_control", ""
		switch cmd {
		case "/interrupt":
			params["action"] = "interrupt"
			ack = "Interrupted {who}."
		case "/restart":
			params["action"] = "restart"
			ack = "Restarted {who}; it continues its own session."
		case "/upgrade":
			params["action"] = "upgrade"
			ack = "Upgraded and restarted {who}; it continues its own session."
		case "/stop-relay":
			params["action"], params["reason"] = "stop", "the user stopped it"
			ack = "Stopped {who}."
		case "/switch":
			// /switch codex, /switch codex gpt-6.2-astra high, /switch claude default max.
			if len(args) == 0 {
				var names []string
				r.sync(func() { names = r.cfg.Relay.RelayProfileNames() })
				r.tell("Say which agent takes over: /switch <"+strings.Join(names, "|")+">, with a model and effort after it if you want other than its own.", false)
				return
			}
			params["action"], params["profile"] = "switch", args[0]
			agent := ""
			r.sync(func() { agent = r.cfg.Relay.RelayProfiles()[args[0]].Agent })
			modelAndEffort(params, args[1:], relay.AgentEfforts(agent))
		case "/model":
			// /model sonnet, /model high, /model opus max, /model default.
			var agent, now string
			r.sync(func() {
				if active := r.st.ActiveRelays(); len(active) > 0 {
					rel := active[0]
					agent = rel.Agent
					pick := relayPick{rel.Model, rel.Effort}
					if p, ok := r.relayRepick[rel.Name]; ok {
						pick = p
					}
					now = rel.Who() + " runs " + r.pickText(rel, pick) + "."
				}
			})
			if len(args) == 0 {
				r.tell(now+" Change it with /model <model> [effort], e.g. /model sonnet high, or /model default for its own. It restarts on the same conversation, after the turn it's on.", false)
				return
			}
			params["action"] = "model"
			modelAndEffort(params, args, relay.AgentEfforts(agent))
		case "/autopilot":
			tool = "relay_autopilot"
			switch {
			case len(args) > 0 && (args[0] == "off" || args[0] == "stop"):
				params["off"] = true
			case len(args) > 1 && args[0] == "until":
				// Read the time where the user is: a phone in Manila means
				// Manila's 10:00, whatever the machine's zone.
				now := time.Now()
				if client != nil && client.Timezone != "" {
					if loc, err := time.LoadLocation(client.Timezone); err == nil {
						now = now.In(loc)
					}
				}
				if t, err := parseUntil(args[1], now); err == nil {
					params["until"] = t.Format(time.RFC3339)
				} else {
					params["until"] = args[1] // the tool reports the error
				}
				if len(args) > 2 {
					params["instruction"] = strings.Join(args[2:], " ")
				}
			case len(args) > 0:
				params["instruction"] = strings.Join(args, " ")
			}
		case "/relay":
			tool = "relay_status"
		}
		who := "the agent"
		r.sync(func() {
			if active := r.st.ActiveRelays(); len(active) > 0 {
				who = active[0].Who()
			} else if n := len(r.st.RelayOrder); n > 0 {
				who = r.st.Relays[r.st.RelayOrder[n-1]].Who()
			}
		})
		out, isErr := r.relayTool(tool, argStr(params), argNum(params), argList(params), true)
		switch {
		case isErr:
			r.tell(cmd+" did not work: "+out, true)
		case ack != "":
			r.tell(strings.ReplaceAll(ack, "{who}", who), false)
		default:
			r.tell(out, false)
		}
	}()
}

// armNotifyDone handles /notify-done: the agent's next turn report buzzes
// the phone. Loop goroutine.
func (r *Runtime) armNotifyDone() {
	active := r.st.ActiveRelays()
	if len(active) == 0 {
		go r.tell("No agent is running.", false)
		return
	}
	rel := active[0]
	if r.relayNotifyDone == nil {
		r.relayNotifyDone = map[string]bool{}
	}
	r.relayNotifyDone[rel.Name] = true
	r.publishRelayMeta()
	msg := "You'll get a buzz when " + rel.Who() + " finishes this turn."
	if rel.State != "working" {
		msg = rel.Who() + " is not working right now; you'll get a buzz when its next turn ends."
	}
	r.ui.Log("%s", msg)
	go r.tell(msg, false)
}

// modelAndEffort reads a command's "[model] [effort]" into tool
// parameters: a word that is one of the agent's efforts is the effort, the
// other is the model, and "default" stands for the agent's own.
func modelAndEffort(params map[string]any, args, efforts []string) {
	for _, a := range args[:min(len(args), 2)] {
		switch {
		case slices.Contains(efforts, strings.ToLower(a)):
			params["effort"] = strings.ToLower(a)
		case params["model"] == nil:
			params["model"] = a
		default:
			params["effort"] = a // "default", or an effort the tool refuses
		}
	}
}

// splitArgs splits a command line on spaces, keeping a double-quoted
// stretch (a model named "Gemini 3.1 Pro") as one argument.
func splitArgs(line string) []string {
	var out []string
	var cur strings.Builder
	quoted, any := false, false
	for _, c := range line {
		switch {
		case c == '"':
			quoted, any = !quoted, true
		case !quoted && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			if any {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(c)
			any = true
		}
	}
	if any {
		out = append(out, cur.String())
	}
	return out
}

// relayThreadMeta is what the phone's thread header shows about the agent:
// the first active one, or nil when none is left. Loop goroutine.
func (r *Runtime) relayThreadMeta() any {
	active := r.st.ActiveRelays()
	if len(active) == 0 {
		return nil
	}
	rel := active[0]
	m := map[string]any{"name": rel.Name, "agent": rel.Agent, "profile": rel.Profile, "label": rel.Label, "state": rel.State, "autopilot": rel.Autopilot != nil, "autopilot_until": "",
		"target": rel.Target, "attach": "eagent relays attach " + rel.Target, "notify_done": r.relayNotifyDone[rel.Name]}
	// What it runs (its picks, else its own defaults), and a change that
	// waits for its turn to end, in words.
	profile := r.cfg.Relay.RelayProfiles()[rel.Profile]
	m["model"], m["effort"] = relay.ProfileDefaults(rel.Agent, firstNonEmpty(rel.Model, profile.Model), firstNonEmpty(rel.Effort, profile.Effort))
	if pick, waiting := r.relayRepick[rel.Name]; waiting {
		m["pending"] = r.pickText(rel, pick)
	}
	var alternatives []string
	for _, name := range r.cfg.Relay.RelayProfileNames() {
		if name != rel.Profile {
			alternatives = append(alternatives, name)
		}
	}
	m["switch_profiles"] = alternatives

	if rel.Autopilot != nil {
		m["autopilot_until"] = rel.Autopilot.Until
	}
	return m
}

// publishRelayMeta updates the phone thread's relay header. Loop goroutine.
func (r *Runtime) publishRelayMeta() {
	if r.phone == nil {
		return
	}
	p, meta := r.phone, map[string]any{"relay": r.relayThreadMeta()}
	p.enqueue(func(ctx context.Context) {
		_, _ = p.client.Patch(ctx, p.ref, finalechat.PatchRequest{Meta: meta})
	})
}
