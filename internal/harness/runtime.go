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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/procs"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/runtimecontrol"
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
	// PromptSource says where Prompt or Answer came from when it was not the
	// terminal: "web", or "finalechat" for a reply the user sent from the
	// phone to a session that had finished (the mirror then does not echo
	// it back to the phone).
	PromptSource string
	// PromptAttachments are files already saved under the session directory
	// that arrived with Prompt.
	PromptAttachments []event.Attachment
	// StartDir, when set, is where a new session's commands start instead
	// of the project root; it is recorded as the first working-directory
	// move so the log, the phone and the UI show it from the start.
	StartDir string
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

	// Each actor has an ordered list of routes (primary, then fallbacks)
	// and a current client; completeActor advances the client when a route
	// turns out to be unroutable. Clients are read and swapped under sync.
	orchRoutes []llm.Endpoint
	taskRoutes []llm.Endpoint
	narrRoutes []llm.Endpoint
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
	orchBusy            bool
	orchCallAt          time.Time
	narrBusy            bool
	narrPending         string // reason for a wake requested during a turn
	narrLastSeen        int64  // seq the narrator saw on its latest call
	narrWorthy          int64  // seq of the newest event the narrator can observe
	narrLastSaid        string
	narrSaidSeq         int64     // seq of the last narrator.message
	narrSaidAt          time.Time // when the user last heard from the narrator (message or question)
	narrFinal           bool      // final report delivered
	narrTicker          *time.Timer
	narrTimerGeneration uint64
	activeSettings      runtimecontrol.Values // loop-owned; cfg remains immutable
	runtimeControl      *runtimecontrol.Controller
	phone               *phone // Finalechat mirror; nil when off
	toolImagesMu        sync.Mutex
	memoMu              sync.Mutex
	memo                map[string]fileMark           // what each caller last read or viewed, to skip repeats
	toolImages          map[string][]event.Attachment // call id -> pictures for the model (view_image)
	running             map[string]context.CancelFunc // task id -> cancel
	waiters             []*waiter
	timers              map[string]*time.Timer // schedule id -> timer
	rolling             bool
	ending              bool
	endReason           string
	shutdownOnce        sync.Once
	exitCode            int
	awaitingUser        bool
	lastOrchSeen        int64
	lastWake            int64 // seq of the newest event that should wake the orchestrator
	// lastArrival is the newest wake that came from outside the harness (a
	// user message or answer, a schedule firing, a task or notified process
	// ending). Harness-made wakes (recovery messages, the dossier) advance
	// lastWake but not this, so the futile-rollover pause cannot be stood
	// aside by the harness's own nudges.
	lastArrival int64
	// futileSkipped is the newest unseen arrival for which the futile-rollover
	// pause already stood aside once (see futileStandAside).
	futileSkipped int64
	// yieldSeenSeq is the seq at which the latest yield first became visible
	// to the narrator (the assistant call that made it, or the yield event
	// itself for a forced one), so a narrator that already reported it is
	// not woken again by the yield event's own, later, seq.
	yieldSeenSeq int64
	lastOrchText string

	// userWaitSeq is the user message whose narrator acknowledgement is held
	// until the orchestrator reacts to it (or narratorAckGrace passes), so
	// the user hears one reply grounded in what actually happened. When the
	// message asks a question, only an answer or the end of the
	// orchestrator's turn counts as the reaction.
	userWaitSeq      int64
	userWaitQuestion bool
	// orchSawSeq is the newest event the orchestrator's latest response had
	// seen, so a reaction is only credited to a message it had in view.
	orchSawSeq int64
	// bashEnv is the BASH_ENV file every command sources: an EXIT trap that
	// reports the shell's final directory, so a cd persists across commands.
	bashEnv string

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
		// Nothing was committed; leave no stub behind.
		_ = sess.Discard()
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
	if dir := strings.TrimSpace(opts.StartDir); dir != "" && !sameDir(dir, r.projectPath()) {
		r.append(event.New(event.CwdChange, event.ActorOrchestrator, event.CwdChangeData{Path: dir, Previous: r.projectPath()}))
	}
	if strings.TrimSpace(opts.Prompt) != "" {
		r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: opts.Prompt, Source: opts.PromptSource, Attachments: opts.PromptAttachments}))
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
	if a := strings.TrimSpace(opts.Answer); a != "" {
		if st.Question != nil {
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: st.Question.ID, Text: a, Source: opts.PromptSource, Attachments: opts.PromptAttachments}))
		} else {
			// Answered elsewhere first (the phone, another resume): the words
			// still count, as a message, rather than vanishing with exit 0.
			r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: a, Source: opts.PromptSource, Attachments: opts.PromptAttachments}))
		}
	}
	if text := strings.TrimSpace(opts.Prompt); text != "" || len(opts.PromptAttachments) > 0 {
		if q := st.Question; q != nil {
			// The natural reply to a session that stopped on a question is
			// the answer, exactly as it would be from the terminal or the web.
			if n := optionIndex(text, q.Options); n >= 0 {
				text = q.Options[n]
			}
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: opts.PromptSource, Attachments: opts.PromptAttachments}))
		} else {
			r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: opts.Prompt, Source: opts.PromptSource, Attachments: opts.PromptAttachments}))
		}
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
		activeSettings: runtimecontrol.FromConfig(cfg),
		procs:          procs.NewManager(),
		files:          tools.Files{Root: opts.Project, AllowOutside: cfg.AllowOutsideProject},
		bashEnv:        writeBashEnv(opts.Project),
		archive:        tools.Archive{Path: sess.Path},
		orchTools:      tools.OrchestratorTools(),
		taskTools:      tools.TaskTools(),
		narrTools:      tools.NarratorTools(),
		loop:           make(chan func(), 1024),
		ctx:            ctx, stop: cancel,
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
	if r.taskRoutes, err = cfg.Task.Routes(); err != nil {
		cancel()
		return nil, fmt.Errorf("task worker: %w", err)
	}
	r.taskClient = r.newClient(r.taskRoutes[0], event.ActorTask)
	if r.narrRoutes, err = cfg.Narrator.Routes(); err != nil {
		cancel()
		return nil, fmt.Errorf("narrator: %w", err)
	}
	r.narrClient = r.newClient(r.narrRoutes[0], event.ActorNarrator)
	return r, nil
}

// clientFor returns an actor's current client. Loop goroutine only.
func (r *Runtime) clientFor(actor string) *llm.Client {
	switch actor {
	case event.ActorTask:
		return r.taskClient
	case event.ActorNarrator:
		return r.narrClient
	}
	return r.orchClient
}

// routesFor returns an actor's ordered routes.
func (r *Runtime) routesFor(actor string) []llm.Endpoint {
	switch actor {
	case event.ActorTask:
		return r.taskRoutes
	case event.ActorNarrator:
		return r.narrRoutes
	}
	return r.orchRoutes
}

// completeActor calls the actor's current route and, when that route
// cannot serve the model at all (bad key, no access, no credits), moves to
// the next configured route and tries again. Every switch is logged in the
// session so replay and the UI know which model actually answered. Safe to
// call from any goroutine; several task workers may hit the same dead
// route at once and only the first switch counts.
func (r *Runtime) completeActor(ctx context.Context, actor, task string, req llm.Request) (*llm.Response, error) {
	for {
		var client *llm.Client
		r.sync(func() { client = r.clientFor(actor) })
		resp, err := client.Complete(ctx, req, r.observer(actor, task))
		if err == nil {
			return resp, nil
		}
		var ae *llm.APIError
		if !errors.As(err, &ae) || !ae.Unroutable() {
			return nil, err
		}
		var switched bool
		r.sync(func() {
			cur := r.clientFor(actor)
			if cur != client {
				switched = true // another call already moved on; use its route
				return
			}
			routes := r.routesFor(actor)
			for i, ep := range routes {
				if ep.BaseURL == cur.Endpoint.BaseURL && ep.Model == cur.Endpoint.Model && i+1 < len(routes) {
					next := routes[i+1]
					r.ui.Log("%s: %s is unavailable (%s); switching to %s", actor, cur.Endpoint, shortErr(err), next)
					nc := r.newClient(next, actor)
					switch actor {
					case event.ActorTask:
						r.taskClient = nc
					case event.ActorNarrator:
						r.narrClient = nc
					default:
						r.orchClient = nc
					}
					r.append(event.New(event.Route, event.ActorHarness, event.RouteData{
						Actor: actor, Provider: next.Protocol, BaseURL: next.BaseURL, Model: next.Model,
						Reason: "primary route unavailable: " + shortErr(err),
					}))
					switched = true
					return
				}
			}
		})
		if !switched {
			return nil, err
		}
	}
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
		// The log is the record; without it nothing can be trusted. End with
		// a failing exit code and reason rather than a quiet 0.
		r.ui.Log("FATAL: cannot append to session log: %v", err)
		r.beginShutdown("error", 1)
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
	if ev.Type == event.Assistant && ev.Actor == event.ActorOrchestrator && ev.Task == "" {
		var d event.AssistantData
		if ev.Decode(&d) == nil && d.SeenSeq > r.orchSawSeq {
			r.orchSawSeq = d.SeenSeq
		}
	}
	if r.userWaitSeq > 0 && ev.Seq > r.userWaitSeq && r.orchSawSeq >= r.userWaitSeq && orchestratorReacted(ev, r.userWaitQuestion) {
		// The orchestrator has done something about the user's message (in a
		// turn that had it in view); the narrator can now answer with that
		// instead of guessing.
		r.userWaitSeq = 0
		r.wakeNarrator(wakeUser)
	}
	switch ev.Type {
	case event.Yield:
		r.yieldSeenSeq = ev.Seq // a forced yield has no call of its own; the orchestrator overrides this for its own
	case event.UserMessage, event.UserAnswer, event.ScheduleFire, event.Dossier:
		r.lastWake = ev.Seq
		if ev.Type != event.Dossier {
			r.lastArrival = ev.Seq
		}
		r.narrFinal = false
		if ev.Type == event.UserMessage || ev.Type == event.UserAnswer {
			question := false
			if ev.Type == event.UserMessage {
				var d event.UserMessageData
				question = ev.Decode(&d) == nil && isQuestion(d.Text)
			}
			r.holdNarratorForOrchestrator(ev.Seq, question)
		}
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		if t := r.st.Tasks[d.ID]; t != nil && t.Kind != "dossier" {
			r.lastWake = ev.Seq
			r.lastArrival = ev.Seq
		}
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		if d.Notify && ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			r.lastWake = ev.Seq
			r.lastArrival = ev.Seq
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
	r.startRuntimeSettings()
	defer r.stopRuntimeSettings()
	stopPublisher := integration.StartPublisher(ctx, r.opts.Project, Version, r.ui.Log, r.sess.ID)
	defer stopPublisher()
	stopConnector := integration.StartConnector(ctx, r.opts.Project, r.ui.Log)
	defer stopConnector()
	go func() {
		<-ctx.Done()
		r.post(func() { r.beginShutdown("interrupted", 130) })
	}()
	r.rearmSchedules()
	r.armNarratorTimer()
	if r.st.Question != nil && r.opts.Interactive {
		q := r.st.Question
		r.ui.Ask(q.ID, q.Text, q.Options)
	}
	inbox := time.NewTicker(500 * time.Millisecond)
	defer inbox.Stop()
	r.pollInbox()
	r.pollRuntimeSettings()
	r.tick()
	for {
		select {
		case <-inbox.C:
			r.pollInbox()
			r.pollRuntimeSettings()
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
		if r.needsRollover() && r.startRollover() {
			// rolling, or paused once for a futile rollover
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
	if len(r.st.RunningTasks()) > 0 {
		return
	}
	if r.opts.Interactive {
		// Interactive sessions wait for the user (a schedule means "do not
		// end", not "do not accept input"). Make sure the narrator had its
		// say about the idle state, then show the prompt.
		if !r.narrFinal && r.narrLastSeen < r.st.LastYield.Seq && !r.narratorCoveredYield() {
			r.wakeNarrator(narratorReasonForYield(r.st))
			return
		}
		r.ui.Idle(true)
		return
	}
	if len(r.st.ActiveSchedules()) > 0 {
		return // a batch session with a schedule keeps running
	}
	if r.waitingOnPhone() {
		// A question is open on the user's phone: a batch session waits for
		// the answer (or the question's expiry) instead of ending.
		return
	}
	// Batch: give the narrator a final word, then end.
	if !r.narrFinal {
		r.narrFinal = true
		if r.narrSaidSeq > r.st.LastYield.Seq || r.narratorCoveredYield() {
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
		r.stopRuntimeSettings()
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
	// Everything the session started goes with it, including background
	// children whose shell already exited; a finished session must not
	// leave a dev server holding a port.
	r.procs.KillAll()
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
func (r *Runtime) onInputFrom(line, source string, atts ...event.Attachment) {
	line = strings.TrimSpace(line)
	if line == "" && len(atts) == 0 {
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
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: source, Attachments: atts}))
		return
	}
	r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: line, Source: source, Attachments: atts}))
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
	r.adoptCwd(p.Handle)
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

// maxTimerHop bounds one time.AfterFunc. Go timers run on the monotonic
// clock, which stops while the machine sleeps, so a single long hop would
// hold a deadline that no longer matches the schedule's wall-clock time.
// Hopping re-reads the wall clock every minute, so the error after a resume
// or a clock step is at most one hop.
const maxTimerHop = time.Minute

func (r *Runtime) armSchedule(id string, at time.Time) {
	if t := r.timers[id]; t != nil {
		t.Stop()
	}
	d := time.Until(at)
	if d < 0 {
		d = 0
	}
	if d > maxTimerHop {
		var t *time.Timer
		t = time.AfterFunc(maxTimerHop, func() {
			r.post(func() {
				if r.timers[id] == t { // still ours: not cancelled or re-armed meanwhile
					r.armSchedule(id, at)
				}
			})
		})
		r.timers[id] = t
		return
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
// narratorAckGrace bounds how long the narrator's reply to a user message
// waits for the orchestrator's first reaction.
var narratorAckGrace = 15 * time.Second

// holdNarratorForOrchestrator delays the narrator's acknowledgement of the
// user's message until the orchestrator has reacted to it, or the grace
// period passes. Loop goroutine only.
func (r *Runtime) holdNarratorForOrchestrator(seq int64, question bool) {
	r.userWaitSeq, r.userWaitQuestion = seq, question
	time.AfterFunc(narratorAckGrace, func() {
		r.post(func() {
			// Still unanswered: let the narrator say the message landed. The
			// hold stays, so the orchestrator's eventual reaction wakes it
			// again with the answer.
			if r.userWaitSeq == seq {
				r.wakeNarrator(wakeUser)
			}
		})
	})
}

// orchestratorReacted reports an event that shows the orchestrator acting on
// what the user said. For an instruction any reaction counts: a note, a
// tool result, a command, a delegation, a yield, a text-only reply, or its
// call failing. For a question only an answer note, the end of its turn
// (yield) or a failure counts, so the narrator never relays a half-formed
// answer assembled from the first command's output.
func orchestratorReacted(ev event.Event, question bool) bool {
	if ev.Type == event.Error {
		var d event.ErrorData
		return ev.Decode(&d) == nil && d.Where == event.ActorOrchestrator
	}
	if ev.Actor != event.ActorOrchestrator || ev.Task != "" {
		return false
	}
	switch ev.Type {
	case event.Note:
		if !question {
			return true
		}
		var d event.NoteData
		return ev.Decode(&d) == nil && d.Answer
	case event.Yield:
		return true
	case event.ToolResult, event.ProcStart, event.TaskCreate:
		return !question
	case event.Assistant:
		var d event.AssistantData
		return ev.Decode(&d) == nil && len(d.ToolCalls) == 0
	}
	return false
}

// isQuestion is the harness's cheap reading of whether a message asks
// something: it contains a question mark.
func isQuestion(text string) bool { return strings.Contains(text, "?") }

// workDir is where an actor's commands run and its relative paths resolve:
// the project root until a cd of its own moved it. Loop goroutine only.
func (r *Runtime) workDir(task string) string {
	if d := r.st.WorkDir(task); d != "" && d != r.st.Cwd {
		return d
	}
	return r.projectPath()
}

// writeBashEnv installs, for every command the actors run, an EXIT trap that
// reports the shell's final directory, so a cd persists across commands
// without rewriting what the model wrote. Returns "" when it cannot be
// written, in which case cd simply does not persist.
func writeBashEnv(project string) string {
	path := filepath.Join(project, ".agents", "eagent", "bash_env")
	const script = "# Written by eagent: report where a command's shell ended up, so a cd persists for later commands.\n" +
		"trap 'if [ -n \"$EAGENT_PWD_FILE\" ]; then printf %s \"$PWD\" > \"$EAGENT_PWD_FILE\" 2>/dev/null; fi' EXIT\n"
	if raw, err := os.ReadFile(path); err == nil && string(raw) == script {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return ""
	}
	return path
}

func (r *Runtime) projectPath() string {
	abs, err := filepath.Abs(r.opts.Project)
	if err != nil {
		return r.opts.Project
	}
	return abs
}
