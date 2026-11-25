// Package harness runs a session. A session is one coding agent in a
// terminal eagent owns (the relay) and one model loop that stands between
// that agent and the user: it passes the user's words on, says what the
// agent is doing, answers what its terminal asks, and keeps the session's
// record.
//
// Concurrency model: one loop goroutine owns the store and the state. A
// turn of the model runs on its own goroutine and changes state only
// through sync(), which runs a closure on the loop goroutine. Tools (which
// may block for seconds) run on the turn's goroutine.
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

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/netx"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
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
	// PromptClient declares what context the client that sent Prompt or
	// Answer can supply. It travels into the appended user.message or
	// user.answer event so caps survive a resume.
	PromptClient *clientcaps.Caps
	// PromptPhoneID, PromptRoute and PromptDelivery are, for a Prompt the
	// user sent from the phone, that thread message's id and the composer's
	// "to eagent / to the agent" and "queue / interrupt / after this turn"
	// choices, as a running session reads them off the message.
	PromptPhoneID, PromptRoute, PromptDelivery string
	// StartDir, when set, is the folder the session works in instead of the
	// project root: where its agent starts.
	StartDir string
	// Relay names the relay profile ("codex", "claude", "pi") the new
	// session starts in StartDir (or the project). Empty starts the
	// configured default, else the first agent that is installed.
	Relay string
	// RelayModel and RelayEffort, when set, are that agent's model and
	// reasoning effort instead of the profile's (or the agent's own).
	RelayModel, RelayEffort string
}

// modelRoute is one model eagent calls: its ordered routes (the configured
// one, then its fallbacks) and the client in use. The client moves along
// the routes when one turns out to be unroutable. Read and swapped on the
// loop goroutine.
type modelRoute struct {
	routes []llm.Endpoint
	client *llm.Client
	// conf is the configured chain the routes came from: it knows each
	// model's context window and reply ceiling, which an endpoint does not.
	conf config.ModelRoute
}

// Runtime is one live session.
type Runtime struct {
	cfg  config.Config
	opts Options
	ui   UI

	sess    *store.Session
	st      *state.State
	archive tools.Archive
	prompts *prompts.Set

	// The start of every request, fixed for the life of this process: the
	// system prompt, the tool list and how long texts are clipped. Nothing
	// that changes while the session runs may reach them.
	system  string
	toolset []llm.Tool
	view    state.ViewOptions

	// models are the routes by who calls them: eagent always, the
	// summarizer only when it is dedicated.
	models map[string]*modelRoute

	// listenUntil is how long a batch session keeps listening after the
	// user dismissed a question (the question's own deadline).
	listenUntil time.Time

	loop chan func()
	// later holds work the loop queued for itself, run once the current
	// step is done. The loop never sends on r.loop: when that channel is
	// full, the only goroutine that drains it would wait on itself.
	later []func()
	ctx   context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup
	// relayOps are the starts and stops of agents under way off the loop
	// goroutine (relay.go). finish waits for them after the loop has
	// stopped, so the process never exits with an agent coming up or going
	// down that the record does not account for. Added to on the loop only.
	relayOps sync.WaitGroup

	turns          turnState             // the model loop (loop.go); loop-owned
	summary        summaryKeeper         // the thread's title and summary (summary.go); loop-owned
	activeSettings runtimecontrol.Values // loop-owned; cfg remains immutable
	runtimeControl *runtimecontrol.Controller
	remoteStop     bool   // the app asked this session to stop; see acceptRemoteStop
	phone          *phone // Finalechat mirror; nil when off

	relays relaySeats
	timers map[string]*time.Timer // timer id -> the timer eagent set with schedule; loop-owned
	// viewed: the pictures view_image has put in the current conversation,
	// by path, with each file's size and time. Loop goroutine.
	viewed          map[string]string
	autopilotTimers map[string]*time.Timer // relay name -> autopilot deadline; loop-owned
	autopilotChecks map[string]*time.Timer // relay name -> idle check while on autopilot; loop-owned
	relayNotifyDone map[string]bool        // relay name -> buzz when its current turn ends (/notify-done)
	// startFailed: the last attempt to start an agent failed (startRelay).
	startFailed bool
	relayRepick map[string]relayPick // relay name -> model/effort change waiting for its turn to end

	ending       bool
	endReason    string
	shutdownOnce sync.Once
	exitCode     int
	closed       chan struct{} // closed when the loop has fully stopped
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
	// The routes actually in use, which differ from the configured ones
	// when a fallback took over at startup.
	models, endpoints, efforts := map[string]string{}, map[string]string{}, map[string]string{}
	for who, m := range r.models {
		models[who], endpoints[who], efforts[who] = m.client.Endpoint.Model, m.client.Endpoint.BaseURL, m.client.Endpoint.ReasoningEffort
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Preset
	}
	r.append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{
		Session: sess.ID, Cwd: opts.Project, Version: Version, Interactive: opts.Interactive, Models: models, Endpoints: endpoints, Efforts: efforts, Config: name, TZ: localZone(),
	}))
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: sess.Current(), Index: 0, Reason: "new"}))
	if dir := strings.TrimSpace(opts.StartDir); dir != "" && !sameDir(dir, r.projectPath()) {
		r.append(event.New(event.CwdChange, event.ActorHarness, event.CwdChangeData{Path: dir, Previous: r.projectPath()}))
	}
	r.startFirstRelay()
	if strings.TrimSpace(opts.Prompt) != "" || len(opts.PromptAttachments) > 0 {
		// Before the mirror exists: the thread opens with these words as
		// the user's (startPhone), and its receipt is set from the record.
		r.userMessage(event.UserMessageData{Text: opts.Prompt, Source: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments})
	}
	r.startPhone()
	r.renderSystem()
	return r, nil
}

// startFirstRelay starts the agent a new session is for: the profile it
// was asked for, else the configured default, else the first agent that is
// installed. A session whose agent cannot be started still starts, so the
// user hears why wherever they are reading. Before the loop starts.
func (r *Runtime) startFirstRelay() {
	profile := strings.TrimSpace(r.opts.Relay)
	if profile == "" {
		profile = r.defaultProfile()
	}
	if profile == "" {
		r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: "no coding agent is installed on this machine (looked for " + strings.Join(r.cfg.Relay.RelayProfileNames(), ", ") + ")"}))
		r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] No agent could be started: none of the coding agents eagent knows is installed here. Tell the user which to install (Claude Code, Codex, Antigravity or pi), in one message."}))
		r.startFailed = true
		return
	}
	pick := relayPick{strings.TrimSpace(r.opts.RelayModel), strings.TrimSpace(r.opts.RelayEffort)}
	if _, err := r.startRelay(profile, r.workDir(), "", false, pick); err != nil {
		r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: err.Error()}))
		r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: fmt.Sprintf("[eagent] The %s agent this session was started for could not be started: %v. Tell the user, in one message, what went wrong and what would fix it.", profile, err)}))
	}
}

// defaultProfile is the profile a session starts when none was named:
// relay.default, else the first built-in agent whose program is installed.
func (r *Runtime) defaultProfile() string {
	if d := strings.TrimSpace(r.cfg.Relay.Default); d != "" {
		return d
	}
	profiles := r.cfg.Relay.RelayProfiles()
	for _, name := range []string{"claude", "codex", "antigravity", "pi"} {
		if p, ok := profiles[name]; ok && agentInstalled(p.Agent) {
			return name
		}
	}
	for _, name := range r.cfg.Relay.RelayProfileNames() {
		if agentInstalled(profiles[name].Agent) {
			return name
		}
	}
	return ""
}

// agentInstalled reports whether an agent's program is on this machine;
// tests replace it.
var agentInstalled = relay.Installed

// localZone is the IANA name of this machine's time zone, so clock times in
// the conversation read the same wherever the log is replayed. "" when the
// zone has no name (a fixed offset); the reader then uses its own.
func localZone() string {
	if name := time.Local.String(); name != "" && name != "Local" {
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(link, "zoneinfo/"); i >= 0 {
			name := link[i+len("zoneinfo/"):]
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return ""
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
	r.settleDismissedWhileAway()
	if a := strings.TrimSpace(opts.Answer); a != "" {
		if st.Question != nil {
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: st.Question.ID, Text: a, Source: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments}))
		} else {
			// Answered elsewhere first (the phone, another resume): the words
			// still count, as a message, rather than vanishing with exit 0.
			r.userMessage(event.UserMessageData{Text: a, Source: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments})
		}
	}
	r.startPhone()
	if strings.TrimSpace(opts.Prompt) != "" || len(opts.PromptAttachments) > 0 {
		// The message the session is resumed with is taken exactly as a
		// running session takes one from the same place: through the inbox's
		// intake, so a slash command runs as a command (never typed into the
		// agent), the app's route and delivery and the message's id are kept,
		// and a session that stopped on a question takes it as the answer.
		// It comes after the mirror starts, so what it does reaches the phone
		// (a screen, a receipt) as it would while running.
		from := len(r.st.Events)
		in := InboxMessage{Type: "message", Text: opts.Prompt, From: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments, Route: opts.PromptRoute, Delivery: opts.PromptDelivery, PhoneID: opts.PromptPhoneID}
		if text := strings.TrimSpace(opts.Prompt); strings.HasPrefix(text, "/") && len(r.st.ActiveRelays()) == 0 && !eagentCommand(text) {
			// Words that only begin with a slash (a path, say) and no
			// agent running to take them. A running session calls that an
			// unknown command and says so at the terminal. This session
			// was woken for these words and nobody is at a terminal, so
			// they are what they were before: a message, or the answer.
			r.ui.Idle(false)
			if q := r.st.Question; q != nil {
				r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: in.From, Client: in.Client, Attachments: in.Attachments}))
			} else {
				r.userMessage(event.UserMessageData{Text: text, Source: in.From, Client: in.Client, Attachments: in.Attachments, Route: in.Route, Delivery: in.Delivery, PhoneID: in.PhoneID})
			}
		} else {
			r.handleInbox(in)
		}
		if r.phone != nil {
			r.phone.withdrawAnswered(r, r.st.Events[from:])
		}
	}
	r.renderSystem()
	return r, nil
}

func build(cfg config.Config, opts Options, ui UI, sess *store.Session, st *state.State) (*Runtime, error) {
	if opts.Project == "" {
		return nil, errors.New("project directory is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		cfg: cfg, opts: opts, ui: ui, sess: sess, st: st,
		activeSettings: runtimecontrol.FromConfigFor(opts.Project, cfg),
		archive:        tools.Archive{Path: sess.Path},
		toolset:        tools.All(),
		view:           state.DefaultViewOptions,
		models:         map[string]*modelRoute{},
		loop:           make(chan func(), 1024),
		ctx:            ctx, stop: cancel,
		closed: make(chan struct{}),
	}
	if cfg.ReportMaxChars > 0 {
		r.view.ReportChars = cfg.ReportMaxChars
	}
	var err error
	if r.prompts, err = prompts.Load(opts.Project); err != nil {
		cancel()
		return nil, fmt.Errorf("prompts: %w", err)
	}
	st.SetPrompts(r.prompts)
	for _, p := range prompts.Ignored(opts.Project) {
		ui.Log("%s is no longer read: eagent has one prompt now (EAGENT.md, with PERSONA.md). Move what you still want into an override of those, then remove the file (the settings page lists it under Prompts).", p)
	}
	routes, err := cfg.Model.Routes()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("model: %w", err)
	}
	r.models[event.ActorEagent] = &modelRoute{routes: routes, client: r.newClient(routes[0], event.ActorEagent), conf: cfg.Model}
	if cfg.Summarizer.Mode == config.SummarizerDedicated {
		routes, err := cfg.Summarizer.Route(cfg.Model).Routes()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("summarizer: %w", err)
		}
		r.models[event.ActorSummarizer] = &modelRoute{routes: routes, client: r.newClient(routes[0], event.ActorSummarizer)}
	}
	r.turns.init(st)
	return r, nil
}

// renderSystem writes the system prompt, once, after the phone mirror is
// known to be on or off. The same bytes open every request of this run.
func (r *Runtime) renderSystem() {
	persona := strings.TrimSpace(r.cfg.Persona)
	if persona == "" {
		persona = strings.TrimSpace(r.prompts.Render("PERSONA.md", nil))
	}
	phone := ""
	if r.phone != nil {
		phone = PhoneStatus()
	}
	r.system = r.prompts.Render("EAGENT.md", prompts.EagentData{
		Persona: persona, Phone: phone, Project: r.projectPath(), Interactive: r.opts.Interactive, Instructions: strings.TrimSpace(r.cfg.Instructions),
	})
}

// complete calls a model through its current route and, when that route
// cannot serve the model at all (bad key, no access, no credits), moves to
// the next configured route and tries again. Every switch is logged in the
// session so replay and the UI know which model actually answered. Safe to
// call from any goroutine.
func (r *Runtime) complete(ctx context.Context, who string, req llm.Request, obs *llm.Observer) (*llm.Response, error) {
	offline := 0 // consecutive calls the network kept from the provider
	for {
		var client *llm.Client
		var known []string
		r.sync(func() {
			if m := r.models[who]; m != nil {
				client = m.client
			}
			for _, m := range req.Messages {
				for _, im := range m.Images {
					if !im.Refused && r.st.RefusedImages[im.Path] {
						known = append(known, im.Path)
					}
				}
			}
		})
		if client == nil {
			return nil, fmt.Errorf("no model is configured for %s", who)
		}
		// A request may have been rendered before a refusal was written
		// down: a call sent back within its turn starts from the turn's
		// first render, and the pass below may itself record one and go
		// on to another route. It goes out as the record renders it now,
		// so no client is sent a picture the record says was refused.
		req = req.WithRefused(known)
		resp, err := client.Complete(ctx, req, obs)
		// Pictures the provider would not take, whether the call then
		// went through without them or failed for another reason.
		// Written down, so the conversation is rendered without them by
		// any client from here on (another route, this session resumed)
		// and they are not uploaded to be refused again.
		refused := client.TakeRefusedImages()
		if err == nil {
			refused = append(resp.RefusedImages, refused...)
		}
		if len(refused) > 0 {
			r.sync(func() {
				r.append(event.New(event.ImagesRefused, event.ActorHarness, event.ImagesRefusedData{Paths: refused}))
			})
		}
		if err == nil {
			if offline > 0 {
				r.ui.Log("%s: %s is reachable again", who, client.Endpoint.BaseURL)
			}
			return resp, nil
		}
		if ctx.Err() == nil && networkDown(err) {
			// The provider cannot be reached at all (no DNS, no route, the
			// connection refused or dropped). That passes: wait for the
			// network instead of failing the turn.
			offline++
			wait := min(time.Duration(offline)*offlineWait, 4*offlineWait)
			if offline == 1 {
				r.ui.Log("%s: cannot reach %s (%s); waiting for the network", who, client.Endpoint.BaseURL, shortErr(err))
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		var ae *llm.APIError
		if !errors.As(err, &ae) || !ae.Unroutable() {
			return nil, err
		}
		var switched bool
		r.sync(func() {
			m := r.models[who]
			if m.client != client {
				switched = true // another call already moved on; use its route
				return
			}
			for i, ep := range m.routes {
				if ep.BaseURL == client.Endpoint.BaseURL && ep.Model == client.Endpoint.Model && i+1 < len(m.routes) {
					next := m.routes[i+1]
					r.ui.Log("%s: %s is unavailable (%s); switching to %s", who, client.Endpoint, shortErr(err), next)
					m.client = r.newClient(next, who)
					r.flushSilent() // the looks so far were made on the old route
					r.append(event.New(event.Route, event.ActorHarness, event.RouteData{
						Actor: who, Provider: next.Protocol, BaseURL: next.BaseURL, Model: next.Model,
						Reason: "the configured route is unavailable: " + shortErr(err),
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

// offlineWait paces calls while the network is down (up to four times it).
var offlineWait = 15 * time.Second

// networkDown reports a failure to reach the provider at all, which waiting
// fixes. A whole call running past its limit is not one: the provider was
// reached and is slow, and the turn's own error handling decides.
func networkDown(err error) bool {
	return netx.Transient(err) && !errors.Is(err, context.DeadlineExceeded)
}

func (r *Runtime) newClient(ep llm.Endpoint, who string) *llm.Client {
	c := llm.NewClient(ep)
	c.OnRetry = func(attempt int, err error, wait time.Duration) {
		r.ui.Log("%s: %v; retrying in %s (attempt %d)", who, shortErr(err), wait.Round(time.Second), attempt+1)
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

// ---- loop plumbing --------------------------------------------------------

// sync runs fn on the loop goroutine and waits for it. During shutdown the
// loop keeps serving closures until the turn in flight has stopped, so late
// events (a last message, a tool result) are still recorded.
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
	r.noteSummary(ev)
	if r.phone != nil {
		r.phone.observe(r, ev)
	}
	r.ui.Trace(ev)
	return ev
}

// Run drives the session until it ends or ctx is cancelled. It returns the
// process exit code.
func (r *Runtime) Run(ctx context.Context) int {
	if n := len(r.cfg.Migrated); n > 0 {
		// Said once per run, in one line: the list is long and is the same
		// every time until the configuration is brought up to date.
		r.ui.Log("this project's configuration still has %d setting(s) of the eagent that ran three models; each was carried over, changed or dropped. `eagent doctor` lists them.", n)
	}
	r.startRuntimeSettings()
	defer r.stopRuntimeSettings()
	// The publisher re-reads the project config from disk and resolves its
	// own credentials, so it is only started when this run's configuration
	// enables artifact publication. Tests pin artifacts off (in memory and
	// on disk), which keeps them from ever uploading session artifacts to
	// a production account.
	stopPublisher := func() {}
	if r.cfg.Finalechat.Artifacts && !finalechat.Disabled() {
		stopPublisher = integration.StartPublisher(ctx, r.opts.Project, Version, r.ui.Log, r.sess.ID)
	}
	defer stopPublisher()
	stopConnector := integration.StartConnector(ctx, r.opts.Project, r.ui.Log)
	defer stopConnector()
	go func() {
		<-ctx.Done()
		r.post(func() { r.beginShutdown("interrupted", 130) })
	}()
	r.armAutopilots()
	r.rearmSchedules()
	r.resumeOwed()
	r.armTicker()
	r.startSummaries()
	defer r.stopSummaries()
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
	for len(r.later) > 0 {
		fn := r.later[0]
		r.later = r.later[1:]
		fn()
	}
	if r.ending {
		return
	}
	r.maybeTurn()
	r.maybeEnd()
	r.publishStatus()
}

func (r *Runtime) publishStatus() {
	s := Status{
		Busy:          r.turns.busy,
		ContextTokens: r.contextTokens(),
		Compacting:    r.turns.compacting,
		Waiting:       !r.turns.busy && r.st.Waiting() && r.st.Question == nil && !r.wakePending(),
		Phone:         r.phone != nil,
	}
	if r.turns.busy && !r.turns.callAt.IsZero() {
		s.BusyFor = since(r.turns.callAt, time.Now())
	}
	for _, rel := range r.st.ActiveRelays() {
		s.Agents = append(s.Agents, AgentStatus{Name: rel.Name, Who: rel.Who(), State: rel.State})
	}
	r.ui.Status(s)
	if r.phone != nil {
		// The phone's status line follows the same state. Starting a model
		// call or a dossier pass records nothing, and a call turns into a
		// long one with no event at all, so the line cannot be worked out
		// only when something is recorded (phone.observe).
		r.phone.noteState(r)
	}
}

// maybeEnd decides whether the session is finished. A session with an
// agent never ends by itself: the user talks to it for days. One whose
// agent was stopped on purpose (or never started) ends once eagent has had
// its say and nothing is waiting on the user.
func (r *Runtime) maybeEnd() {
	t := &r.turns
	if r.ending || t.busy || t.compacting {
		return
	}
	// down: the model has failed twice running, and the user has been told.
	down := t.failures >= 2
	if r.wakePending() && (r.opts.Interactive || !down) {
		return
	}
	if r.opts.Interactive {
		// An interactive session waits for the user.
		r.ui.Idle(r.st.Waiting())
		return
	}
	// unanswerable: a question is open, and nobody can answer it while the
	// session runs: there is no terminal and no phone. eagent was told
	// that asking ends such a session until the user resumes it (the
	// prompt, and ask_user's own result), so it ends even with an agent
	// running. The agent keeps running in its own terminal, and the
	// resumed session picks it up.
	unanswerable := r.st.Question != nil && r.phone == nil
	if len(r.st.ActiveRelays()) > 0 && !unanswerable {
		return
	}
	if len(r.st.ActiveSchedules()) > 0 {
		return // a timer is set: the session stays to keep it
	}
	if r.waitingOnPhone() {
		// A question is open on the user's phone: the session waits for the
		// answer (or the question's expiry) instead of ending.
		return
	}
	if r.listeningAfterDismissal() {
		// They closed the question to say something else; wait for it.
		return
	}
	switch {
	case t.finalAsked:
	case down:
		// Nothing is running, nobody is at a terminal and the model cannot
		// be reached: retrying for hours would watch over nothing. The
		// session closes in eagent's own words; a reply brings it back.
		t.finalAsked = true
		r.fallbackFinal()
	case r.st.Question == nil:
		// A session with no terminal does not end in silence: one more
		// look, for the closing report or to find the last message
		// already covers it.
		t.finalAsked = true
		r.startTurn(wake{final: true})
		return
	}
	if r.finalSummary() {
		return // the thread list gets how it ended, then the session ends
	}
	// How it ended. "done" is for a session whose agent was stopped with
	// nothing waiting. One that stopped short of that waits on the user,
	// and says so (the phone's closing note and the exit code follow the
	// reason): a question is open or was dismissed and never taken up, the
	// model could not be reached, or no agent could be started at all.
	if r.st.Question != nil || down || r.st.DismissalStands() || (len(r.st.RelayOrder) == 0 && r.startFailed) {
		r.beginShutdown("awaiting-input", 2)
		return
	}
	r.beginShutdown("done", 0)
}

// beginShutdown records the end and stops the turn; finish() completes it.
func (r *Runtime) beginShutdown(reason string, code int) {
	r.shutdownOnce.Do(func() {
		r.ending = true
		r.stopRuntimeSettings()
		r.endReason = reason
		r.exitCode = code
		r.stop()
	})
}

// finish runs after the loop exits: waits for the turn in flight, records
// session.end, closes the store.
func (r *Runtime) finish() {
	waitDone := make(chan struct{})
	go func() { r.wg.Wait(); close(waitDone) }()
	grace := 5 * time.Second
	if r.turns.busy && r.turns.finalBusy {
		// The closing word is being written: it gets its time even when
		// the session was told to stop.
		grace = finalReportGrace
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
drain:
	for {
		select {
		case fn := <-r.loop:
			fn() // a turn finishing up still records its events
		case <-waitDone:
			break drain
		case <-deadline.C:
			r.ui.Log("a model call did not stop in time")
			break drain
		}
	}
	close(r.closed)
	// A start still under way stops the agent it brings up (abandonLaunch),
	// as nothing would watch it. Nothing is under way in an ordinary
	// shutdown, so this costs nothing then.
	opsDone := make(chan struct{})
	go func() { r.relayOps.Wait(); close(opsDone) }()
	opsDeadline := time.NewTimer(relayOpsGrace)
	select {
	case <-opsDone:
	case <-opsDeadline.C:
		r.ui.Log("an agent start or stop did not finish in time")
	}
	opsDeadline.Stop()
	// The agents are not the session's children: they run in terminals of
	// their own and outlive this process, so a resumed session (or the
	// user, attached) picks up where they are. Only stop watching them.
	r.closeRelays()
	r.stopTimers()
	// Direct writes: the loop is no longer running.
	if r.turns.silent.Count > 0 {
		r.appendDirect(event.New(event.Checks, event.ActorEagent, r.turns.silent))
		r.turns.silent = event.ChecksData{}
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

// relayOpsGrace bounds the wait for agent starts and stops at the end of a
// session. A start can take a minute on its git pull, and closing a
// terminal's watcher is bounded at 30 s; this covers both.
const relayOpsGrace = 2 * time.Minute

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

func (r *Runtime) onInput(line string) { r.onInputFrom(line, "", nil) }

// onInputFrom records a line from the user; source is "" for the terminal,
// otherwise "web" or "finalechat".
func (r *Runtime) onInputFrom(line, source string, client *clientcaps.Caps, atts ...event.Attachment) {
	line = strings.TrimSpace(line)
	if line == "" && len(atts) == 0 {
		return
	}
	if r.ownCommand(line, source) {
		r.slashCommandFrom(line, client)
		return
	}
	r.ui.Idle(false)
	if q := r.st.Question; q != nil {
		text := line
		if n := optionIndex(line, q.Options); n >= 0 {
			text = q.Options[n]
		}
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: source, Client: client, Attachments: atts}))
		return
	}
	r.userMessage(event.UserMessageData{Text: line, Source: source, Client: client, Attachments: atts})
}

// userMessage records something the user wrote and, unless it is held back
// for eagent, types it into the agent's terminal at once, word for word.
// Whether it was held is decided before it is recorded, so the record says
// what became of it from its first line. Loop goroutine (or before the loop
// starts).
func (r *Runtime) userMessage(d event.UserMessageData) {
	rel, held := r.routeUserMessage(d)
	d.Held = held
	ev := r.append(event.New(event.UserMessage, event.ActorUser, d))
	if held != "" {
		if r.phone != nil && len(r.st.ActiveRelays()) > 0 {
			r.phone.setReceipt(r, ev.Seq, "eagent")
		}
		return
	}
	r.forwardUserMessage(rel, ev.Seq, d)
}

// dismissQuestion closes the open question with no answer. Nothing is sent
// to anyone and nobody is woken: the session stays where it was until the
// user writes. A batch session keeps listening for that message for as long
// as it would have waited for the answer. Loop goroutine only.
func (r *Runtime) dismissQuestion(qid, source string) {
	q := r.st.Question
	if q == nil || (qid != "" && q.ID != qid) {
		return
	}
	asked := time.Now()
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		if r.st.Events[i].Seq == q.Seq {
			asked = r.st.Events[i].Time
			break
		}
	}
	r.listenUntil = asked.Add(time.Duration(r.cfg.Finalechat.QuestionTimeout()) * time.Second)
	r.append(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: q.ID, Source: source}))
	r.ui.Log("question %s dismissed without an answer; nothing was sent to the agent", q.ID)
}

// settleDismissedWhileAway records a dismissal the session was not running
// to hear: the user closed the open question's card on the phone while the
// session was down. Without this, what they wrote next (the message that
// resumes the session) would be taken for the answer. It is one short call,
// made only when a question is open and has a card; if the service cannot
// be reached the question stays open. Called before the loop starts.
func (r *Runtime) settleDismissedWhileAway() {
	q := r.st.Question
	if q == nil {
		return
	}
	card := ""
	for _, ev := range r.st.Events {
		if ev.Type == event.PhoneQuestion {
			var d event.PhoneQuestionData
			_ = ev.Decode(&d)
			if d.QuestionID == q.ID {
				card = d.CardID
			}
		}
	}
	fc := r.cfg.Finalechat
	if card == "" || !fc.Wanted() || finalechat.Disabled() {
		return
	}
	client, ok := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL)
	if !ok {
		return
	}
	client.UserAgent = "eagent/" + Version
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got, err := client.Question(ctx, card, 0); err != nil || got.Status != "dismissed" {
		return
	}
	r.append(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: q.ID, Source: "finalechat"}))
}

// listeningAfterDismissal reports that the user closed a question a while
// ago and a batch session is still giving them the question's time to
// write instead. Loop goroutine only.
func (r *Runtime) listeningAfterDismissal() bool {
	return r.phone != nil && r.st.DismissalStands() && time.Now().Before(r.listenUntil)
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

// slashCommandFrom runs a command; client is what the sending client
// declared (its time zone reads "/autopilot until 10:00" in the user's time,
// not the machine's).
func (r *Runtime) slashCommandFrom(line string, client *clientcaps.Caps) {
	fields := strings.Fields(line)
	// Phone keyboards capitalise the first letter: "/Interrupt" is /interrupt.
	fields[0] = strings.ToLower(fields[0])
	if relayCommands[fields[0]] {
		// A model's name can hold spaces; quoted, it is one argument.
		if quoted := splitArgs(line); len(quoted) > 0 {
			quoted[0] = fields[0]
			fields = quoted
		}
		r.relayCommand(fields, client)
		return
	}
	switch fields[0] {
	case "/quit", "/exit", "/q":
		r.beginShutdown("quit", 0)
	case "/dismiss":
		if q := r.st.Question; q != nil {
			source := ""
			if client != nil {
				source = client.Source
			}
			r.dismissQuestion(q.ID, source)
		} else {
			r.ui.Log("no question is open")
		}
	case "/status", "/s":
		r.ui.Log("%s", r.st.Summary())
	case "/schedules":
		r.listSchedules()
	case "/help", "/?":
		r.ui.Log("eagent: /status /schedules /dismiss /quit  (anything else goes to the agent, word for word; start a line with @e to talk to eagent instead)")
		r.ui.Log("the agent: /relay /screen /notify-done /interrupt /restart /model <model> [effort] /upgrade /switch <profile> [model] [effort] /autopilot [until 10:00] [order] | off  /stop-relay")
	case "/tasks", "/procs", "/kill":
		// Commands of the eagent that ran its own tasks and processes.
		r.ui.Log("%s went with eagent's own tasks and processes: the agent does the work now. /relay shows the agent, /screen its terminal, /interrupt stops its turn and /stop-relay stops it.", fields[0])
	default:
		r.ui.Log("unknown command %s (try /help)", fields[0])
	}
}

// workDir is the folder the session works in: the project root unless it
// was started somewhere else. Loop goroutine only.
func (r *Runtime) workDir() string {
	if d := r.st.WorkDir(); d != "" && d != r.st.Cwd {
		return d
	}
	return r.projectPath()
}

// projectName is the project directory's own name.
func (r *Runtime) projectName() string { return filepath.Base(r.projectPath()) }

// projectPath is the absolute project directory.
func (r *Runtime) projectPath() string {
	abs, err := filepath.Abs(r.opts.Project)
	if err != nil {
		return r.opts.Project
	}
	return abs
}

// sameDir reports whether two paths name the same directory.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
