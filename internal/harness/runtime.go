// Package harness runs a session: it owns the event log and derived state,
// wakes the three actors, executes their tools, rolls subsessions over with
// dossiers, and decides when the session is finished.
//
// Concurrency model: one loop goroutine owns the store and the state. Actor
// turns run in their own goroutines and mutate state only through sync(),
// which executes a closure on the loop goroutine. Tool execution (which may
// block for seconds) happens on the actor goroutine.
package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/procs"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/sched"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	"github.com/ericflo/eagent/internal/tools"
)

// Version is stamped into session.start.
var Version = "dev"

// Options control a run.
type Options struct {
	Project     string
	Interactive bool
	Verbose     bool
	// Prompt is delivered as the first user message (new session) or an
	// additional one (resume).
	Prompt string
	// Answer answers the pending question on resume.
	Answer string
}

// Runtime is one live session.
type Runtime struct {
	cfg  config.Config
	opts Options
	ui   UI

	sess    *store.Session
	st      *state.State
	procs   *procs.Manager
	files   tools.Files
	archive tools.Archive

	orchRoutes []llm.Endpoint
	orchClient *llm.Client
	taskClient *llm.Client
	narrClient *llm.Client

	orchTools []llm.Tool
	taskTools []llm.Tool
	narrTools []llm.Tool
	prompts   *prompts.Set

	loop chan func()
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	// Loop-owned scheduling state.
	orchBusy     bool
	orchCallAt   time.Time
	narrBusy     bool
	narrPending  string // reason for a wake requested during a turn
	narrLastSeen int64  // seq the narrator saw on its latest call
	narrWorthy   int64  // seq of the newest event the narrator can observe
	narrLastSaid string
	narrSaidSeq  int64 // seq of the last narrator.message
	narrFinal    bool  // final report delivered
	narrTicker   *time.Timer
	phone        *phone                        // Finalechat mirror; nil when off
	running      map[string]context.CancelFunc // task id -> cancel
	waiters      []*waiter
	timers       map[string]*time.Timer // schedule id -> timer
	rolling      bool
	ending       bool
	endReason    string
	shutdownOnce sync.Once
	exitCode     int
	awaitingUser bool
	lastOrchSeen int64
	lastWake     int64 // seq of the newest event that should wake the orchestrator
	lastOrchText string

	procCursor   map[string]int // handle -> bytes already shown
	procOwner    map[string]owner
	procSeenAt   map[string]bool // handle -> tool result already delivered final output
	procAttended map[string]bool // handle -> owner is blocked waiting on it right now
	closed       chan struct{}   // closed when the loop has fully stopped
}

type owner struct{ actor, task string }

// waiter is a blocked `wait` tool call.
type waiter struct {
	tasks map[string]bool
	procs map[string]bool
	any   bool
	ch    chan string
}

// New creates a new session in the project directory.
func New(cfg config.Config, opts Options, ui UI) (*Runtime, error) {
	root := store.Root(opts.Project)
	sess, err := store.Create(root, time.Now())
	if err != nil {
		return nil, err
	}
	r, err := build(cfg, opts, ui, sess, state.New())
	if err != nil {
		sess.Close()
		return nil, err
	}
	// Record the routes actually in use, which differ from the configured
	// primaries when a fallback took over at startup.
	models := map[string]string{
		event.ActorOrchestrator: r.orchClient.Endpoint.Model,
		event.ActorTask:         r.taskClient.Endpoint.Model,
		event.ActorNarrator:     r.narrClient.Endpoint.Model,
	}
	endpoints := map[string]string{
		event.ActorOrchestrator: r.orchClient.Endpoint.BaseURL,
		event.ActorTask:         r.taskClient.Endpoint.BaseURL,
		event.ActorNarrator:     r.narrClient.Endpoint.BaseURL,
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Preset
	}
	r.append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{
		Session: sess.ID, Cwd: opts.Project, Version: Version, Interactive: opts.Interactive, Models: models, Endpoints: endpoints, Config: name,
	}))
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: sess.Current(), Index: 0, Reason: "new"}))
	if strings.TrimSpace(opts.Prompt) != "" {
		r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: opts.Prompt}))
	}
	r.startPhone()
	return r, nil
}

// Resume reopens an existing session directory.
func Resume(cfg config.Config, opts Options, ui UI, sessionPath string) (*Runtime, error) {
	events, err := store.Read(sessionPath)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("%s has no events", sessionPath)
	}
	st := state.Replay(events)
	sess, err := store.Open(sessionPath, st.LastSeq(), time.Now())
	if err != nil {
		return nil, err
	}
	if opts.Project == "" {
		opts.Project = st.Cwd
	}
	r, err := build(cfg, opts, ui, sess, st)
	if err != nil {
		sess.Close()
		return nil, err
	}
	r.closeInterrupted()
	if strings.TrimSpace(opts.Answer) != "" && st.Question != nil {
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: st.Question.ID, Text: opts.Answer}))
	}
	if strings.TrimSpace(opts.Prompt) != "" {
		r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: opts.Prompt}))
	}
	r.startPhone()
	return r, nil
}

func build(cfg config.Config, opts Options, ui UI, sess *store.Session, st *state.State) (*Runtime, error) {
	if opts.Project == "" {
		return nil, errors.New("project directory is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		cfg: cfg, opts: opts, ui: ui, sess: sess, st: st,
		procs:     procs.NewManager(),
		files:     tools.Files{Root: opts.Project, AllowOutside: cfg.AllowOutsideProject},
		archive:   tools.Archive{Path: sess.Path},
		orchTools: tools.OrchestratorTools(),
		taskTools: tools.TaskTools(),
		narrTools: tools.NarratorTools(),
		loop:      make(chan func(), 1024),
		ctx:       ctx, stop: cancel,
		running:      map[string]context.CancelFunc{},
		timers:       map[string]*time.Timer{},
		procCursor:   map[string]int{},
		procOwner:    map[string]owner{},
		procSeenAt:   map[string]bool{},
		procAttended: map[string]bool{},
		closed:       make(chan struct{}),
	}
	var err error
	if r.prompts, err = prompts.Load(opts.Project); err != nil {
		cancel()
		return nil, fmt.Errorf("prompts: %w", err)
	}
	if r.orchRoutes, err = cfg.Orchestrator.Routes(); err != nil {
		cancel()
		return nil, fmt.Errorf("orchestrator: %w", err)
	}
	r.orchClient = r.newClient(r.orchRoutes[0], event.ActorOrchestrator)
	taskEP, err := cfg.Task.Endpoint()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("task worker: %w", err)
	}
	r.taskClient = r.newClient(taskEP, event.ActorTask)
	narrEP, err := cfg.Narrator.Endpoint()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("narrator: %w", err)
	}
	r.narrClient = r.newClient(narrEP, event.ActorNarrator)
	return r, nil
}

func (r *Runtime) newClient(ep llm.Endpoint, actor string) *llm.Client {
	c := llm.NewClient(ep)
	c.OnRetry = func(attempt int, err error, wait time.Duration) {
		r.ui.Log("%s: %v; retrying in %s (attempt %d)", actor, shortErr(err), wait.Round(time.Second), attempt+1)
	}
	return c
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// State returns the live state for read-only inspection by the UI. Callers
// must not mutate it and should treat it as a snapshot.
func (r *Runtime) State() *state.State { return r.st }

// SessionID returns the session identifier.
func (r *Runtime) SessionID() string { return r.sess.ID }

// KillProcesses terminates every child process; used on a hard exit.
func (r *Runtime) KillProcesses() { r.procs.KillAll() }

// ---- loop plumbing --------------------------------------------------------

// sync runs fn on the loop goroutine and waits for it. During shutdown the
// loop keeps serving closures until the actors have stopped, so late events
// (a final narrator message, a tool result) are still recorded.
func (r *Runtime) sync(fn func()) {
	done := make(chan struct{})
	select {
	case r.loop <- func() { fn(); close(done) }:
	case <-r.closed:
		return
	}
	select {
	case <-done:
	case <-r.closed:
	}
}

// post runs fn on the loop goroutine without waiting.
func (r *Runtime) post(fn func()) {
	select {
	case r.loop <- fn:
	case <-r.closed:
	}
}

// append writes an event and folds it into state. Loop goroutine only
// (or before the loop starts).
func (r *Runtime) append(ev event.Event) event.Event {
	ev, err := r.sess.Append(ev)
	if err != nil {
		r.ui.Log("FATAL: cannot append to session log: %v", err)
		r.stop()
		return ev
	}
	r.st.Apply(ev)
	r.noteWake(ev)
	if r.phone != nil {
		r.phone.observe(r, ev)
	}
	r.ui.Trace(ev)
	return ev
}

// noteWake tracks which events should wake whom.
func (r *Runtime) noteWake(ev event.Event) {
	if ev.Actor != event.ActorNarrator && state.Observe(ev, 100) != "" {
		r.narrWorthy = ev.Seq
	}
	switch ev.Type {
	case event.UserMessage, event.UserAnswer, event.ScheduleFire, event.Dossier:
		r.lastWake = ev.Seq
		r.narrFinal = false
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		if t := r.st.Tasks[d.ID]; t != nil && t.Kind != "dossier" {
			r.lastWake = ev.Seq
		}
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		if d.Notify && ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			r.lastWake = ev.Seq
		}
	case event.HarnessMessage:
		if ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			r.lastWake = ev.Seq
		}
	case event.Assistant:
		if ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			var d event.AssistantData
			_ = ev.Decode(&d)
			r.lastOrchSeen = d.SeenSeq
		}
	}
}

// orchestratorHasWork reports whether the orchestrator should be running.
// The reducer clears LastYield whenever something arrives that the
// orchestrator has not seen (including during the call that yielded), so
// "not idle" is the whole test once the session has a user message.
func (r *Runtime) orchestratorHasWork() bool {
	if r.st.LastYield != nil {
		return false
	}
	return r.st.LastUserSeq > 0 || r.lastWake > 0
}

// Run drives the session until it ends or ctx is cancelled. It returns the
// process exit code.
func (r *Runtime) Run(ctx context.Context) int {
	go func() {
		<-ctx.Done()
		r.post(func() { r.beginShutdown("interrupted", 130) })
	}()
	r.rearmSchedules()
	r.narrTicker = time.AfterFunc(time.Duration(r.cfg.NarratorTickSeconds)*time.Second, r.narratorTick)
	if r.st.Question != nil && r.opts.Interactive {
		q := r.st.Question
		r.ui.Ask(q.ID, q.Text, q.Options)
	}
	inbox := time.NewTicker(500 * time.Millisecond)
	defer inbox.Stop()
	r.pollInbox()
	r.tick()
	for {
		select {
		case <-inbox.C:
			r.pollInbox()
			r.tick()
		case fn := <-r.loop:
			fn()
			r.tick()
		case p := <-r.procs.Exited():
			r.onProcExit(p)
			r.tick()
		case line, ok := <-r.inputChan():
			if !ok {
				r.beginShutdown("quit", 0)
				continue
			}
			r.onInput(line)
			r.tick()
		case <-r.ctx.Done():
			r.finish()
			return r.exitCode
		}
	}
}

func (r *Runtime) inputChan() <-chan string {
	if r.ui == nil {
		return nil
	}
	return r.ui.Input()
}

// tick is the scheduler: it looks at state and starts whatever should run.
func (r *Runtime) tick() {
	if r.ending {
		return
	}
	r.startQueuedTasks()
	if r.rolling {
		// Waiting for the dossier; nothing else for the orchestrator to do.
	} else if !r.orchBusy {
		if r.needsRollover() {
			r.startRollover()
		} else if r.orchestratorHasWork() {
			r.startOrchestratorTurn()
		}
	}
	r.maybeWakeNarrator()
	r.maybeEnd()
	r.publishStatus()
}

func (r *Runtime) publishStatus() {
	s := Status{
		OrchestratorBusy: r.orchBusy,
		NarratorBusy:     r.narrBusy,
		Procs:            len(r.st.RunningProcs()),
		ContextTokens:    r.st.ContextTokens(event.ActorOrchestrator),
		Rollover:         r.rolling,
		Waiting:          r.st.Idle() && !r.orchBusy && r.st.Question == nil,
	}
	if r.orchBusy && !r.orchCallAt.IsZero() {
		s.OrchestratorFor = since(r.orchCallAt, time.Now())
	}
	for _, t := range r.st.RunningTasks() {
		if t.Status == "running" {
			s.TasksRunning++
		} else {
			s.TasksQueued++
		}
	}
	s.Phone = r.phone != nil
	r.ui.Status(s)
}

// maybeEnd decides whether a non-interactive session is finished.
func (r *Runtime) maybeEnd() {
	if r.ending || r.orchBusy || r.rolling || r.narrBusy {
		return
	}
	if !r.st.Idle() || r.orchestratorHasWork() {
		return
	}
	if len(r.st.RunningTasks()) > 0 || len(r.st.ActiveSchedules()) > 0 {
		return
	}
	if r.opts.Interactive {
		// Interactive sessions wait for the user. Make sure the narrator had
		// its say about the idle state, then show the prompt.
		if !r.narrFinal && r.narrLastSeen < r.st.LastYield.Seq {
			r.wakeNarrator(narratorReasonForYield(r.st))
			return
		}
		r.ui.Idle(true)
		return
	}
	if r.waitingOnPhone() {
		// A question is open on the user's phone: a batch session waits for
		// the answer (or the question's expiry) instead of ending.
		return
	}
	// Batch: give the narrator a final word, then end.
	if !r.narrFinal {
		r.narrFinal = true
		if r.narrSaidSeq > r.st.LastYield.Seq {
			// It already reported after the orchestrator finished.
			r.beginShutdown(r.endReasonForIdle(), r.endCodeForIdle())
			return
		}
		r.wakeNarrator(wakeFinal)
		return
	}
	r.beginShutdown(r.endReasonForIdle(), r.endCodeForIdle())
}

func (r *Runtime) endReasonForIdle() string {
	if !r.st.LastYield.Done || r.st.Question != nil {
		return "awaiting-input"
	}
	return "done"
}

func (r *Runtime) endCodeForIdle() int {
	if r.endReasonForIdle() == "awaiting-input" {
		return 2
	}
	return 0
}

func narratorReasonForYield(st *state.State) string {
	if st.LastYield != nil && st.LastYield.Done {
		return wakeDone
	}
	return wakeYield
}

// beginShutdown records the end and stops actors; finish() completes it.
func (r *Runtime) beginShutdown(reason string, code int) {
	r.shutdownOnce.Do(func() {
		r.ending = true
		r.endReason = reason
		r.exitCode = code
		for _, cancel := range r.running {
			cancel()
		}
		r.stop()
	})
}

// finish runs after the loop exits: waits for actors, kills processes,
// records session.end, closes the store.
func (r *Runtime) finish() {
	waitDone := make(chan struct{})
	go func() { r.wg.Wait(); close(waitDone) }()
	grace := 5 * time.Second
	if r.narrBusy {
		grace = 75 * time.Second // the final report gets a chance to land
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
drain:
	for {
		select {
		case fn := <-r.loop:
			fn() // actors finishing up still record their events
		case p := <-r.procs.Exited():
			r.onProcExit(p)
		case <-waitDone:
			break drain
		case <-deadline.C:
			r.ui.Log("some actor goroutines did not stop in time")
			break drain
		}
	}
	close(r.closed)
	for _, p := range r.procs.Running() {
		p.Kill()
	}
	for _, t := range r.timers {
		t.Stop()
	}
	if r.narrTicker != nil {
		r.narrTicker.Stop()
	}
	// Direct writes: the loop is no longer running.
	for _, p := range r.procs.All() {
		if sp := r.st.Procs[p.Handle]; sp != nil && sp.Status == "running" {
			ev := event.New(event.ProcExit, sp.Actor, event.ProcExitData{Handle: p.Handle, ExitCode: -1, Reason: "killed", DurationMS: p.Duration().Milliseconds()}).WithTask(sp.Task)
			r.appendDirect(ev)
		}
	}
	// Tasks that were queued or whose worker did not stop in time are closed
	// here so the log is consistent at every session.end; resume re-checks
	// anyway for hard crashes that never reach this point.
	for _, t := range r.st.RunningTasks() {
		summary := "the runner stopped before this task started; delegate it again if it is still needed"
		if t.Status == "running" {
			summary = "the runner stopped while this task was running; its partial work may be on disk. Check, then re-delegate what is missing."
		}
		r.appendDirect(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: t.ID, Status: "interrupted", Summary: summary, Turns: t.Turns, Usage: t.Usage}).WithTask(t.ID))
	}
	r.appendDirect(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: r.endReason}))
	if r.phone != nil {
		r.phone.close(r.endReason)
	}
	if err := r.sess.Close(); err != nil {
		r.ui.Log("closing session: %v", err)
	}
	r.ui.Status(Status{})
}

func (r *Runtime) appendDirect(ev event.Event) {
	ev, err := r.sess.Append(ev)
	if err != nil {
		r.ui.Log("append: %v", err)
		return
	}
	r.st.Apply(ev)
	r.ui.Trace(ev)
}

// ---- user input -----------------------------------------------------------

func (r *Runtime) onInput(line string) { r.onInputFrom(line, "") }

// onInputFrom records a line from the user; source is "" for the terminal,
// otherwise "web" or "finalechat".
func (r *Runtime) onInputFrom(line, source string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "/") {
		r.slashCommand(line)
		return
	}
	r.ui.Idle(false)
	if q := r.st.Question; q != nil {
		text := line
		if n := optionIndex(line, q.Options); n >= 0 {
			text = q.Options[n]
		}
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: source}))
		return
	}
	r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: line, Source: source}))
}

func optionIndex(line string, options []string) int {
	if len(options) == 0 {
		return -1
	}
	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(options) && fmt.Sprint(n) == line {
		return n - 1
	}
	return -1
}

func (r *Runtime) slashCommand(line string) {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/quit", "/exit", "/q":
		r.beginShutdown("quit", 0)
	case "/status", "/s":
		r.ui.Log("%s", r.st.Summary())
	case "/tasks":
		for _, id := range r.st.TaskOrder {
			t := r.st.Tasks[id]
			r.ui.Log("%s [%s] %s", t.ID, t.Status, t.Title)
		}
		if len(r.st.TaskOrder) == 0 {
			r.ui.Log("no tasks yet")
		}
	case "/procs":
		for _, p := range r.procs.All() {
			r.ui.Log("%s", p.Describe())
		}
		if len(r.procs.All()) == 0 {
			r.ui.Log("no processes")
		}
	case "/kill":
		if len(fields) < 2 {
			r.ui.Log("usage: /kill <handle>")
			return
		}
		if p, ok := r.procs.Get(fields[1]); ok {
			p.Kill()
			r.ui.Log("killed %s", fields[1])
		} else {
			r.ui.Log("no such process %s", fields[1])
		}
	case "/schedules":
		for _, s := range r.st.ActiveSchedules() {
			r.ui.Log("%s %s %q next %s: %s", s.ID, s.Kind, s.Spec, s.Next.Local().Format("15:04:05"), s.Note)
		}
		if len(r.st.ActiveSchedules()) == 0 {
			r.ui.Log("no schedules")
		}
	case "/help", "/?":
		r.ui.Log("commands: /status /tasks /procs /kill <handle> /schedules /quit  (anything else is sent to the agent)")
	default:
		r.ui.Log("unknown command %s (try /help)", fields[0])
	}
}

// ---- processes ------------------------------------------------------------

func (r *Runtime) onProcExit(p *procs.Proc) {
	sp := r.st.Procs[p.Handle]
	if sp == nil || sp.Status != "running" {
		return
	}
	reason := string(p.Status())
	// Notify the owner only if it already moved on: its tool result did not
	// include the final output and it is not blocked waiting on the process.
	notify := !r.procSeenAt[p.Handle] && !r.procAttended[p.Handle]
	tail := ""
	if notify {
		tail = tools.Truncate(p.Tail(4000), 4000)
	}
	ev := event.New(event.ProcExit, sp.Actor, event.ProcExitData{
		Handle: p.Handle, ExitCode: p.ExitCode(), Reason: reason,
		DurationMS: p.Duration().Milliseconds(), Tail: tail, Notify: notify,
	}).WithTask(sp.Task)
	r.append(ev)
	r.signalWaiters("", p.Handle)
}

// ---- schedules ------------------------------------------------------------

func (r *Runtime) rearmSchedules() {
	now := time.Now()
	for _, sc := range r.st.ActiveSchedules() {
		next := sc.Next
		if next.IsZero() || next.Before(now) {
			// Missed while we were away: fire soon, once.
			next = now.Add(2 * time.Second)
		}
		r.armSchedule(sc.ID, next)
	}
}

func (r *Runtime) armSchedule(id string, at time.Time) {
	if t := r.timers[id]; t != nil {
		t.Stop()
	}
	d := time.Until(at)
	if d < 0 {
		d = 0
	}
	r.timers[id] = time.AfterFunc(d, func() {
		r.post(func() { r.fireSchedule(id) })
	})
}

func (r *Runtime) fireSchedule(id string) {
	sc := r.st.Schedules[id]
	if sc == nil || sc.Cancelled || r.ending {
		return
	}
	now := time.Now()
	var next time.Time
	if sc.Kind != sched.Once {
		// Relative specs ("in 10m") must not be re-parsed: a one-shot fires once.
		s, err := sched.Parse(sc.Spec)
		if err != nil {
			return
		}
		next = s.Next(now)
	}
	r.append(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: id, Note: sc.Note, Next: next}))
	if !next.IsZero() {
		r.armSchedule(id, next)
	} else {
		delete(r.timers, id)
	}
}

// ---- waiters --------------------------------------------------------------

func (r *Runtime) signalWaiters(taskID, handle string) {
	var keep []*waiter
	for _, w := range r.waiters {
		hit := (taskID != "" && (w.tasks[taskID] || (w.any && len(w.procs) == 0))) || (handle != "" && w.procs[handle])
		if hit {
			what := taskID
			if what == "" {
				what = handle
			}
			select {
			case w.ch <- what:
			default:
			}
			continue
		}
		keep = append(keep, w)
	}
	r.waiters = keep
}

// projectPath is the absolute project directory.
func (r *Runtime) projectPath() string {
	abs, err := filepath.Abs(r.opts.Project)
	if err != nil {
		return r.opts.Project
	}
	return abs
}
