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
	"maps"
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
	// PromptAnswers is, for a Prompt the user wrote on a question's card on
	// the phone, the card's id (the message's meta says so): the Prompt is
	// that question's answer.
	PromptAnswers string
	// PromptRefused are, for a Prompt from the phone, the message's files
	// that could not be fetched: recorded refused (phone.refused) in the
	// step that takes the message, so the mirror tells the sender.
	PromptRefused []integration.RefusedFile
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
	// at is the position in routes of the one the client is on. Two routes
	// can name the same model at the same address (another key, say), so
	// the position is kept rather than found again by name.
	at int
	// conf is the configured chain the routes came from: it knows each
	// model's context window and reply ceiling, which an endpoint does not.
	conf config.ModelRoute
	// told: the user has heard that the route in use cannot be reached.
	// Cleared when it answers.
	told bool
}

// confAt is the configured route the client is on: the at-th of the chain
// among those Routes keeps (the ones whose key is set). When that does not
// name the client's model (a key set or unset since the routes were made),
// it is the first route of the chain that does, or nil.
func (m *modelRoute) confAt() *config.ModelRoute {
	if m.client == nil {
		return nil
	}
	ep := m.client.Endpoint
	same := func(cur *config.ModelRoute) bool {
		return llm.SameBaseURL(cur.BaseURL, ep.BaseURL) && cur.Model == ep.Model
	}
	i := 0
	for cur := &m.conf; cur != nil; cur = cur.Fallback {
		if _, err := cur.Endpoint(); err != nil {
			continue
		}
		if i == m.at && same(cur) {
			return cur
		}
		i++
	}
	for cur := &m.conf; cur != nil; cur = cur.Fallback {
		if same(cur) {
			return cur
		}
	}
	return nil
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
	tokenScale     int                   // provider tokens per 1000 estimated (dossier.go); loop-owned
	tokenScaleOn   string                // the route tokenScale was learned on (dossier.go); loop-owned
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
	commands        int                    // commands whose work is under way in a session with no terminal (relayCommand); loop-owned
	// startFailed: the last attempt to start an agent failed (startRelay).
	startFailed bool
	// inboxTaken: the ids of the inbox submissions this run handled (pollInbox). Loop goroutine.
	inboxTaken map[string]bool
	// relayNext: relay name -> a change of model or effort that waits for
	// the relay to be between turns (waitingPick, applyWaitingRepick).
	relayNext map[string]relayPick

	ending bool
	// closings counts the times this run started ending (stillClosing
	// turning true, as noteClosing sees it at each loop step), and
	// wasClosing says whether it was ending when last seen. refusedIn is
	// the closing in which the provider refused a call (4xx), 0 for none:
	// the refusal is final for that closing alone (refusedWhileClosing).
	// Per run, as the closing is (finalAsked, summary.final). Loop
	// goroutine.
	closings, refusedIn int
	wasClosing          bool
	// recordFailed: an event could not be written to the record (append).
	// The run ends on it, and takes nothing more off the inbox. Loop
	// goroutine.
	recordFailed bool
	endReason    string
	shutdownOnce sync.Once
	exitCode     int
	closed       chan struct{} // closed when the loop has fully stopped
	// loopDone is closed as finish begins (the loop has stopped); finish
	// reads the loop's queue until it ends it (queueEnded, endQueue).
	loopDone   chan struct{}
	queueMu    sync.RWMutex
	queueEnded bool
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
		Session: sess.ID, Cwd: opts.Project, Version: Version, Interactive: opts.Interactive, Models: models, Endpoints: endpoints, Efforts: efforts, Config: name, TZ: localZone(), PhoneLedger: true,
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
	// The lock first, then the record: what a run still finishing writes
	// before it lets go is read too (store.Open).
	sess, events, err := store.Open(sessionPath, time.Now())
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		sess.Close()
		return nil, fmt.Errorf("%s has no events", sessionPath)
	}
	st := state.Replay(events)
	if opts.Project == "" {
		opts.Project = st.Cwd
	}
	r, err := build(cfg, opts, ui, sess, st)
	if err != nil {
		sess.Close()
		return nil, err
	}
	r.closeInterrupted()
	if l := integration.ReadPhoneLedger(r.st.Events); opts.PromptPhoneID != "" && l.Taken[opts.PromptPhoneID] || opts.PromptAnswers != "" && l.Cards[opts.PromptAnswers] {
		// A phone message the record already holds (another process's
		// watcher resumed the session with it first), or the answer
		// written on a card whose answer the record holds: the session
		// resumes as with no words, and it is not taken or typed a second
		// time.
		for _, o := range []*Options{&opts, &r.opts} {
			o.Prompt, o.PromptSource, o.PromptAttachments, o.PromptClient = "", "", nil, nil
			o.PromptPhoneID, o.PromptRoute, o.PromptDelivery, o.PromptAnswers = "", "", "", ""
			o.PromptRefused = nil
		}
	}
	// What the cards of an open question say, and so what the resuming
	// words become, is decided in one place (resumecards.go).
	// What the resume records from here on closes the question's cards on
	// the phone, in every thread (withdrawAnswered, below).
	planFrom := len(r.st.Events)
	plan := r.planResume(opts)
	holdBack := plan.Words == wordsMessage
	if a := strings.TrimSpace(opts.Answer); a != "" {
		if st.Question != nil && !holdBack {
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: st.Question.ID, Text: a, Source: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments}))
		} else {
			// Answered elsewhere first (the phone, another resume): the words
			// still count, as a message, rather than vanishing with exit 0.
			r.userMessage(event.UserMessageData{Text: a, Source: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments})
		}
	}
	r.startPhone()
	wordsFrom := len(r.st.Events)
	if strings.TrimSpace(opts.Prompt) != "" || len(opts.PromptAttachments) > 0 {
		// The message the session is resumed with is taken exactly as a
		// running session takes one from the same place: through the inbox's
		// intake, so a slash command runs as a command (never typed into the
		// agent), the app's route and delivery and the message's id are kept,
		// and a session that stopped on a question takes it as the answer.
		// It comes after the mirror starts, so what it does reaches the phone
		// (a screen, a receipt) as it would while running.
		in := InboxMessage{Type: "message", Text: opts.Prompt, From: opts.PromptSource, Client: opts.PromptClient, Attachments: opts.PromptAttachments, Route: opts.PromptRoute, Delivery: opts.PromptDelivery, PhoneID: opts.PromptPhoneID}
		// Words written on the open question's card are its answer, verbatim,
		// as the running poll takes a card's answer: never a command.
		onCard := r.st.Question != nil && opts.PromptAnswers != "" && !holdBack
		if !onCard && r.ownCommand(strings.TrimSpace(opts.Prompt), in.From) {
			r.tookPhoneMessage(opts.PromptPhoneID, wordsFrom) // a command is recorded as taken before it runs, as the poll does
		}
		if onCard {
			in.Type, in.QuestionID, in.CardID = "answer", r.st.Question.ID, plan.WordsCard
			r.handleInbox(in)
		} else if text := strings.TrimSpace(opts.Prompt); strings.HasPrefix(text, "/") && len(r.st.ActiveRelays()) == 0 && !eagentCommand(text) {
			// Words that only begin with a slash (a path, say) and no
			// agent running to take them. A running session calls that an
			// unknown command and says so at the terminal. This session
			// was woken for these words and nobody is at a terminal, so
			// they are what they were before: a message, or the answer.
			r.ui.Idle(false)
			if q := r.st.Question; q != nil && !holdBack {
				r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: in.From, Client: in.Client, Attachments: in.Attachments, PhoneID: in.PhoneID}))
			} else {
				r.userMessage(event.UserMessageData{Text: text, Source: in.From, Client: in.Client, Attachments: in.Attachments, Route: in.Route, Delivery: in.Delivery, PhoneID: in.PhoneID})
			}
		} else if text := strings.TrimSpace(opts.Prompt); holdBack && r.st.Question != nil && !r.ownCommand(text, in.From) {
			// Words that are not the open question's answer (planResume):
			// a message, and the question stays open.
			r.ui.Idle(false)
			r.userMessage(event.UserMessageData{Text: text, Source: in.From, Client: in.Client, Attachments: in.Attachments, Route: in.Route, Delivery: in.Delivery, PhoneID: in.PhoneID})
		} else {
			r.handleInbox(in)
		}
	}
	// The app message the session was resumed with is taken in now, also
	// when it held nothing (its files all refused): the ledger goes on
	// after it, and the poll reads on from it.
	r.tookPhoneMessage(opts.PromptPhoneID, wordsFrom)
	// Its files that could not be fetched, after it, as the poll records
	// a message's: the mirror posts a note for each.
	for _, f := range opts.PromptRefused {
		r.append(event.New(phoneRefusedEvent, event.ActorHarness, phoneRefusedData{MessageID: opts.PromptPhoneID, AttachmentID: f.AttachmentID, Text: f.Text}))
	}
	if plan.WordsCard != "" {
		r.markAnswerTaken(plan.WordsCard) // now that the words are recorded
	}
	if r.phone != nil {
		r.phone.withdrawAnswered(r, r.st.Events[planFrom:])
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
		closed:   make(chan struct{}),
		loopDone: make(chan struct{}),
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
		r.models[event.ActorSummarizer] = &modelRoute{routes: routes, client: r.newClient(routes[0], event.ActorSummarizer), conf: cfg.Summarizer.Route(cfg.Model)}
	}
	// A route whose key is not set was left out, so the first one in use
	// may be a fallback: the live settings show the one that runs.
	for who := range r.models {
		r.followRoute(who)
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
// the next configured route and tries again. When the route cannot be
// reached it waits for the network, and past offlineSwitch the user is
// told once. Every switch is logged in the session so replay and the UI
// know which model actually answered. Safe to call from any goroutine.
func (r *Runtime) complete(ctx context.Context, who string, req llm.Request, obs *llm.Observer) (*llm.Response, error) {
	offline := 0        // calls in a row the network kept from the provider
	var since time.Time // when the first of them came back
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
		r.calibrate(who, client.Endpoint, req, resp, err)
		// Pictures the provider would not take, whether the call then
		// went through without them or failed for another reason.
		// Written down, so the conversation is rendered without them by
		// any client from here on (another route, this session resumed)
		// and they are not uploaded to be refused again. A call that came
		// back after the route moved does not speak for the session.
		refused := client.TakeRefusedImages()
		if err == nil {
			refused = append(resp.RefusedImages, refused...)
		}
		r.sync(func() {
			m := r.models[who]
			if m == nil || m.client == nil || !sameRoute(m.client.Endpoint, client.Endpoint) {
				return // the route moved on while the call was out
			}
			if len(refused) > 0 {
				r.append(event.New(event.ImagesRefused, event.ActorHarness, event.ImagesRefusedData{Paths: refused}))
				// A refused picture is not in the conversation, so a look at
				// its file again must not be told it is. Which file a copy
				// came from is not kept, so every look is forgotten: at
				// worst a picture that did go in is sent once more.
				r.viewed = nil
			}
			if err == nil || !networkDown(err) {
				m.told = false // it answered
			}
		})
		if err == nil {
			if offline > 0 {
				r.ui.Log("%s: %s is reachable again", who, client.Endpoint.BaseURL)
			}
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, err // stopped, or out of time
		}
		if networkDown(err) {
			// The provider cannot be reached at all (no DNS, no route, the
			// connection refused or dropped). That passes: wait for the
			// network instead of failing the turn.
			offline++
			if offline == 1 {
				since = time.Now()
				r.ui.Log("%s: cannot reach %s (%s); waiting for the network", who, client.Endpoint.BaseURL, shortErr(err))
			}
			if who == event.ActorEagent && offline > 1 && time.Since(since) >= offlineSwitch {
				// Only eagent's own model speaks for the session, once
				// for as long as the route stays out of reach.
				r.sync(func() {
					if m := r.models[who]; m != nil && m.client == client && !m.told {
						m.told = true
						r.recordMessage(r.unreachableNotice(err))
					}
				})
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(min(time.Duration(offline)*offlineWait, 4*offlineWait)):
			}
			continue
		}
		var ae *llm.APIError
		if !errors.As(err, &ae) || !ae.Unroutable() {
			return nil, callErr{err, client.Endpoint}
		}
		// The route answered that it cannot serve the model: the next one
		// of the chain, each tried once. The last one failing ends the call.
		switched := false
		r.sync(func() {
			m := r.models[who]
			switch {
			case m == nil || m.client == nil:
			case m.client != client:
				switched = true // another call, or the settings, already moved on: use its route
			case m.at+1 < len(m.routes):
				r.takeRoute(who, m, m.at+1, r.newClient(m.routes[m.at+1], who), "is unavailable", err)
				switched = true
			}
		})
		if !switched {
			return nil, callErr{err, client.Endpoint}
		}
	}
}

// callErr is a model call's failure with the route the call was made on,
// which need not be the one its actor's calls are on by the time the
// failure is read (onRouteInUse).
type callErr struct {
	error
	on llm.Endpoint
}

func (e callErr) Unwrap() error { return e.error }

// onRouteInUse says a failed call of eagent's was made on the route its
// calls are on now: one made before a pick in the settings moved them (it
// was still out then) was not, and its failure says nothing of the model
// in use. A fallback taken within the call is the route in use, and its
// failure is its own. Loop goroutine.
func (r *Runtime) onRouteInUse(err error) bool {
	var ce callErr
	m := r.models[event.ActorEagent]
	return !errors.As(err, &ce) || (m != nil && m.client != nil && sameRoute(m.client.Endpoint, ce.on))
}

// takeRoute moves who's calls to the at-th route of its chain through
// client, after the one in use failed as why says (with err), and writes
// it down: the log, the Route event replay and the phone's model chip
// read, and the live settings. Loop goroutine.
func (r *Runtime) takeRoute(who string, m *modelRoute, at int, client *llm.Client, why string, err error) {
	next := m.routes[at]
	r.ui.Log("%s: %s %s (%s); switching to %s", who, m.client.Endpoint, why, shortErr(err), next)
	moved := !sameRoute(m.client.Endpoint, next)
	m.at, m.client = at, client
	m.told = false  // nothing is known of the new route yet
	r.flushSilent() // the looks so far were made on the old route
	if who == event.ActorEagent && moved {
		r.newModel() // as for a model picked in settings (swapRoute)
	}
	r.recordRoute(event.RouteData{
		Actor: who, Provider: next.Protocol, BaseURL: next.BaseURL, Model: next.Model,
		Reason: "the configured route " + why + ": " + shortErr(err),
	})
	r.followRoute(who)
}

// recordRoute writes down the model an actor's calls go to now. When that
// puts eagent on another model or address, the size of the conversation's
// first call is forgotten: it was measured on a briefing sized for the old
// one, and says nothing about whether a restart can make room on this one.
// A resumed run does the same from the record (turnState.init). Loop
// goroutine (or before the loop starts).
func (r *Runtime) recordRoute(d event.RouteData) {
	was := r.st.Moved()
	r.append(event.New(event.Route, event.ActorHarness, d))
	if r.st.Moved() != was {
		r.turns.firstPrompt = 0
	}
}

// sameRoute says two endpoints are the same model at the same address: a
// change of anything else (the effort) keeps what is known of the route.
func sameRoute(a, b llm.Endpoint) bool {
	return llm.SameBaseURL(a.BaseURL, b.BaseURL) && a.Model == b.Model
}

// followRoute brings the live settings' pick for who up to date with the
// route its calls go to, after a fallback took over: the app's settings
// sheet shows the model that answers, a preset saved from it is that
// model, and a change to something else keeps the session on it (a pick
// that did not change keeps its route, see routeChanges). Loop goroutine.
func (r *Runtime) followRoute(who string) {
	m := r.models[who]
	shown, ok := r.activeSettings.Routes[who]
	if m == nil || m.client == nil || !ok {
		return
	}
	ep := m.client.Endpoint
	now := runtimecontrol.Route{Model: ep.Model, Effort: ep.ReasoningEffort}
	if pr, ok := config.ProviderForURLFor(r.opts.Project, ep.BaseURL); ok {
		now.Provider = pr.ID
	}
	if now == shown {
		return
	}
	// A new map: the controller's copy of the values may share this one.
	routes := maps.Clone(r.activeSettings.Routes)
	routes[who] = now
	r.activeSettings.Routes = routes
	if r.runtimeControl != nil {
		if err := r.runtimeControl.Update(r.activeSettings); err != nil {
			r.ui.Log("live settings: %v", err)
		}
	}
}

// offlineWait paces calls while the network is down (up to four times it).
var offlineWait = 15 * time.Second

// offlineSwitch is how long eagent's model may stay out of reach, from
// when a call first came back unable to reach it, before the user is told.
// Long enough for a local model server to reload.
var offlineSwitch = 45 * time.Second

// loopPoll is how often the loop reads its inbox and live settings and
// looks at the time-based parts of its state; a variable so tests can
// shorten it.
var loopPoll = 500 * time.Millisecond

// afterRun is nil outside tests. The harness tests set it to compare the
// loop's state with the record once a session has finished.
var afterRun func(*Runtime)

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

// enqueue hands fn to the loop's one queue, which the loop reads while it
// runs and finish reads from its first step until it ends the queue:
// every fn it takes is run. False once finish has stopped reading
// it (endQueue): fn is not run, and the caller is told.
func (r *Runtime) enqueue(fn func()) bool {
	r.queueMu.RLock()
	defer r.queueMu.RUnlock()
	if r.queueEnded {
		return false
	}
	r.loop <- fn // finish keeps reading while it waits to end the queue
	return true
}

// endQueue stops the queue taking work, reading it meanwhile so a caller
// waiting to hand work over is not stuck, then runs what it took. Finish's
// goroutine.
func (r *Runtime) endQueue() {
	for !r.queueMu.TryLock() {
		select {
		case fn := <-r.loop:
			fn()
		case <-time.After(time.Millisecond):
		}
	}
	r.queueEnded = true
	r.queueMu.Unlock()
	for {
		select {
		case fn := <-r.loop:
			fn()
		default:
			return
		}
	}
}

// sync runs fn on the loop goroutine and waits for it, and reports that it
// ran. During shutdown finish keeps running what the queue took
// (enqueue), so late events (a last message, a tool result, a send's end)
// are still recorded; once it has ended the queue, fn is not run and sync
// returns false at once: the session has ended, and a turn does nothing
// more (turn).
func (r *Runtime) sync(fn func()) bool {
	done := make(chan struct{})
	if r.enqueue(func() { fn(); close(done) }) {
		<-done
		return true
	}
	return false
}

// post runs fn on the loop goroutine without waiting; once finish has ended
// the queue it is not run. Nothing is recorded after session.end, so what
// is refused then (a seat's last news as it is closed, a timer that came
// due) would not be recorded either.
func (r *Runtime) post(fn func()) {
	r.enqueue(fn)
}

// append writes an event and folds it into state. Loop goroutine only
// (or before the loop starts).
func (r *Runtime) append(ev event.Event) event.Event {
	ev, err := r.sess.Append(ev)
	if err != nil {
		// The log is the record; without it nothing can be trusted. End with
		// a failing exit code and reason rather than a quiet 0.
		r.ui.Log("FATAL: cannot append to session log: %v", err)
		r.recordFailed = true
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
	r.armTicker()
	r.startSummaries()
	defer r.stopSummaries()
	if r.st.Question != nil && r.opts.Interactive {
		q := r.st.Question
		r.ui.Ask(q.ID, q.Text, q.Options)
	}
	inbox := time.NewTicker(loopPoll)
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
			if afterRun != nil {
				afterRun(r)
			}
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
	r.noteClosing()
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
		ContextTokens: int(r.contextTokens()),
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
// endsAtQuiet reports what maybeEnd decides when nothing is due and no
// call is out: a session with no terminal that nothing keeps open
// (keptOpen) asks its closing word or ends. maybeTurn asks it before a
// quiet-moment restart, which such a session would pay for only to end.
// Loop goroutine.
func (r *Runtime) endsAtQuiet() bool {
	if r.opts.Interactive {
		return false
	}
	kept, _ := r.keptOpen()
	return !kept
}

// endPlan is what the session does now (sessionPlan).
type endPlan struct {
	// waits: a look is due first (a wake, or the failed look of an
	// ordinary turn, owed again after its wait). kept: something keeps
	// the session open (keptOpen; agents: an agent). With neither, and no
	// terminal, the session ends.
	waits, kept, agents bool
	ends                bool
	// down: it ends as one whose model fails: eagent's looks have failed
	// twice running, whatever came back first in them.
	down bool
	// The words still owed to the user before it ends: the closing word
	// asked of the model (a closing turn, after which the session is
	// asked again); eagent's own closing words (fallbackFinal), with why
	// its model fails when down; or why its model fails alone (downWhy),
	// when the closing word was had and that was not told.
	askWord, ownWord, why bool
	// How it ends: "done" (exit 0) for a session whose agent was stopped
	// with nothing waiting, "awaiting-input" (exit 2) for one that waits
	// on the user: a question is open or was dismissed and never taken
	// up, its model fails, or no agent could be started at all.
	reason string
	code   int
}

// sessionPlan says what the session does now, as one answer for all who
// act on it or speak of it: turnFailed's log and notice, the closing
// turn's close-out, the closing words and maybeEnd. failed says a call of
// a turn woken by w has just failed (turnFailed, with failures counting
// it); maybeEnd asks with no turn out. A session that is not ending and
// whose look failed keeps trying; down needs two failed looks in a row,
// no terminal, nothing keeping it open (endsDown) and no word from the
// user since the last failed call was shown, which is looked at first.
// A refusal while the session is ending is final for it
// (refusedWhileClosing): what asked for the look is left. Loop goroutine.
func (r *Runtime) sessionPlan(w wake, failed bool) endPlan {
	t := &r.turns
	var p endPlan
	p.down = t.failures >= 2 && r.endsDown()
	// The failed look of an ordinary turn is owed again (its turn's end,
	// "error", wakes the loop); a closing turn's, or the last title and
	// summary's, is not.
	owed := failed && !w.final && w.wait == 0
	if (owed || r.wakePending()) && !p.down && !r.refusedWhileClosing() {
		p.waits = true
		return p
	}
	if r.opts.Interactive {
		return p
	}
	if p.kept, p.agents = r.keptOpen(); p.kept {
		return p
	}
	p.ends = true
	switch {
	case t.finalAsked:
		p.why = p.down && !t.failTold
	case p.down:
		p.ownWord = true
	case r.st.Question == nil:
		p.askWord = true
	}
	p.reason, p.code = "done", 0
	if r.st.Question != nil || p.down || r.st.DismissalStands() || (len(r.st.RelayOrder) == 0 && r.startFailed) {
		p.reason, p.code = "awaiting-input", 2
	}
	return p
}

// tellDown says why the session ends as down (downWhy), when its closing
// word was had before its model failed. Loop goroutine.
func (r *Runtime) tellDown() {
	text := r.downWhy(r.lastFailedAnswered()) + " This session ends here; `eagent -c` picks it back up."
	if r.phone != nil {
		text = r.downWhy(r.lastFailedAnswered()) + " This session ends here; a reply here picks it back up."
	}
	r.turns.failTold = true
	r.recordMessage(event.MessageData{Auto: true, Important: true, Text: text})
}

func (r *Runtime) maybeEnd() {
	t := &r.turns
	if r.ending || t.busy || t.compacting {
		return
	}
	p := r.sessionPlan(wake{}, false)
	if !p.ends {
		switch {
		case p.waits:
		case r.opts.Interactive:
			// An interactive session waits for the user.
			r.ui.Idle(r.st.Waiting())
		case p.agents:
			r.checkIdleAgents()
		}
		return
	}
	// Every word still owed to the user is said before it ends.
	switch {
	case p.askWord:
		// A session with no terminal does not end in silence: one more
		// look, for the closing report or to find the last message
		// already covers it.
		t.finalAsked = true
		r.startTurn(wake{final: true})
		return
	case p.ownWord:
		// Nothing is running, nobody is at a terminal and the model
		// fails: retrying for hours would watch over nothing. The
		// session closes in eagent's own words; a reply brings it back.
		t.finalAsked = true
		r.fallbackFinal(r.downWhy(r.lastFailedAnswered()))
	case p.why:
		r.tellDown()
	}
	if r.finalSummary() {
		return // the thread list gets how it ended, then the session ends
	}
	r.beginShutdown(p.reason, p.code)
}

// freshWord reports that the user has written or answered since what the
// last failed call was shown (failSeq), and no call has seen it: it is
// looked at first, without the wait after the failures (maybeTurn), and a
// session whose model is down stays open for it (maybeEnd). Loop
// goroutine.
func (r *Runtime) freshWord() bool {
	t := &r.turns
	return t.wakes[wakeUser] > t.seen && t.wakes[wakeUser] > t.failSeq
}

// endsDown reports that a session with no terminal whose model is down
// ends at its next step (maybeEnd): nothing keeps it open, and no word
// from the user waits for a look. Loop goroutine.
func (r *Runtime) endsDown() bool {
	kept, _ := r.keptOpen()
	return !r.opts.Interactive && !kept && !r.freshWord()
}

// keptOpen reports that something keeps a session with no terminal from
// ending (maybeEnd), and whether that is an agent. Loop goroutine.
func (r *Runtime) keptOpen() (kept, agents bool) {
	// unanswerable: a question is open, and nobody can answer it while the
	// session runs: there is no terminal and no phone. eagent was told
	// that asking ends such a session until the user resumes it (the
	// prompt, and ask_user's own result), so it ends even with an agent
	// running or a timer set. The agent keeps running in its own terminal,
	// and the resumed session picks it up and sets the timer again.
	unanswerable := r.st.Question != nil && r.phone == nil
	// An agent being started (a command's /restart of a stopped one, say)
	// counts as running, as for the check on idle agents: a session that
	// ended under the start would stop the agent it brings up.
	r.relays.mu.Lock()
	starting := len(r.relays.launching) > 0
	r.relays.mu.Unlock()
	if (len(r.st.ActiveRelays()) > 0 || starting) && !unanswerable {
		return true, true
	}
	// A timer is set: the session stays to keep it. A question is open on
	// the user's phone: it waits for the answer (or the question's expiry).
	// They closed the question to say something else: it waits for that.
	// A command's work is done and told first (relayCommand).
	return len(r.st.ActiveSchedules()) > 0 && !unanswerable || r.waitingOnPhone() || r.listeningAfterDismissal() || r.commands > 0, false
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

// turnStopGrace is how long finish waits for the turn in flight to stop (a
// closing report gets finalReportGrace). A variable so tests can shorten it.
var turnStopGrace = 5 * time.Second

// finish runs after the loop exits: waits for the turn in flight, records
// session.end, closes the store.
func (r *Runtime) finish() {
	close(r.loopDone) // finish reads the loop's queue from here (endQueue)
	waitDone := make(chan struct{})
	go func() { r.wg.Wait(); close(waitDone) }()
	grace := turnStopGrace
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
			r.ui.Log("the turn did not stop in time")
			break drain
		}
	}
	// What the loop has been handed so far (a seat's news among it) runs
	// now. The queue stays open: what is handed to it later runs in the
	// reads below, up to its end (endQueue).
queued:
	for {
		select {
		case fn := <-r.loop:
			fn()
		default:
			break queued
		}
	}
	close(r.closed)
	// One last look at the inbox, the loop gone: words written there since
	// the loop's last look (by the web UI, for a session a terminal runs)
	// are on the record, kept for the next run, not left unread; anything
	// else waits for the next run (pollInbox), as does a file written
	// after this.
	r.pollInbox()
	// The queue's end: what it took is run; from here a post is refused
	// (its caller is told), and a start that ends later is recorded below
	// (abandonLaunch).
	r.endQueue()
	// A start still under way is waited for: one whose agent comes up is
	// recorded below (abandonLaunch). Nothing is under way in an ordinary
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
	// Direct writes from here: the loop is no longer running. The starts
	// the end overtook, in the order their agents came up, one at a time
	// (finishLaunch, as the loop would have); one after this is not
	// recorded (late).
	r.relays.mu.Lock()
	overtaken := r.relays.overtaken
	r.relays.overtaken, r.relays.late = nil, true
	r.relays.mu.Unlock()
	for _, job := range overtaken {
		r.relayOps.Add(1) // finishLaunch's, given back by abandonLaunch
		_, _ = r.finishLaunch(job)
	}
	// The agents are not the session's children: they run in terminals of
	// their own and outlive this process, so a resumed session (or the
	// user, attached) picks up where they are. Only stop watching them.
	r.closeRelays()
	r.stopTimers()
	// A send being typed ends now that its terminal is closed, in the
	// seat's own bounded steps, as the close does: one that was never
	// typed is back in its queue (drainSends), read below.
	var typing []chan struct{}
	r.relays.mu.Lock()
	for _, q := range r.relays.queues {
		if q.running {
			typing = append(typing, q.idle)
		}
	}
	r.relays.mu.Unlock()
	for _, idle := range typing {
		<-idle
	}
	// Direct writes: the loop is no longer running.
	// A send held for a start the session's end gave up (abandonLaunch),
	// or still in a seat's queue (nothing is taken from one now), was never
	// typed and never will be: it did not go. In the order they were sent.
	untyped := map[string]bool{}
	r.relays.mu.Lock()
	for _, job := range r.relays.launching {
		for _, h := range job.held {
			untyped[h.id] = true
		}
		for _, id := range job.takenBack { // a start not recorded (late)
			untyped[id] = true
		}
	}
	for _, q := range r.relays.queues {
		for _, h := range q.items {
			untyped[h.id] = true
		}
	}
	// A switch's brief among them never took the user's messages it
	// quoted to the agent that took over (briefDidNotGo).
	quoted := map[string]bool{}
	for id, brief := range r.relays.carried {
		quoted[id] = untyped[brief]
	}
	r.relays.mu.Unlock()
	for _, ev := range r.st.Events {
		var d event.RelaySendData
		if ev.Type != event.RelaySend || ev.Decode(&d) != nil || !untyped[d.ID] && !quoted[d.ID] || r.hasOutcome(d.ID) {
			continue
		}
		name := sendHolder(r.st, d.ID, d.Name)
		detail := "the session is ending"
		if quoted[d.ID] {
			detail = briefLost
		}
		gone := event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: d.ID, OK: false, Outcome: event.DeliveryNotSent, Detail: detail})
		if rel := r.st.Relays[name]; rel != nil {
			// At the relay's last news, not now: a reattach takes what its
			// transcript shows after that as new, and what the agent did
			// since the loop closed is not on the record.
			gone.Time = rel.LastEventAt
		}
		r.appendOutcomeDirect(gone)
	}
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
var relayOpsGrace = 2 * time.Minute

// appendOutcomeDirect writes how a send went once the loop is no longer
// running (finish's goroutine).
func (r *Runtime) appendOutcomeDirect(ev event.Event) {
	out, err := r.sess.Append(ev)
	if err != nil {
		r.ui.Log("append: %v", err)
		return
	}
	r.st.Apply(out)
	r.noteWake(out) // the look it is owed, as a resume reads it from the record
	if r.phone != nil {
		r.phone.observe(r, out) // its receipt turns to failed, before the closing note
	}
	r.ui.Trace(out)
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

func (r *Runtime) onInput(line string) { r.onInputFrom(line, "", "", "", nil) }

// onInputFrom records a line from the user; source is "" for the terminal,
// otherwise "web" or "finalechat". phoneID is the app message it came in,
// which an answer carries so the record names the message in one write;
// inbox is the inbox file it came in, "" when none.
func (r *Runtime) onInputFrom(line, source, phoneID, inbox string, client *clientcaps.Caps, atts ...event.Attachment) {
	line = strings.TrimSpace(line)
	if line == "" && len(atts) == 0 {
		return
	}
	if r.ownCommand(line, source) {
		r.slashCommandFrom(line, source, client)
		return
	}
	r.ui.Idle(false)
	if q := r.st.Question; q != nil {
		text := line
		if n := optionIndex(line, q.Options); n >= 0 {
			text = q.Options[n]
		}
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: source, Client: client, Attachments: atts, PhoneID: phoneID, Inbox: inbox}))
		return
	}
	r.userMessage(event.UserMessageData{Text: line, Source: source, Client: client, Attachments: atts, PhoneID: phoneID, Inbox: inbox})
}

// userMessage records something the user wrote and, unless it is held back
// for eagent, types it into the agent's terminal at once, word for word.
// Whether it was held is decided before it is recorded, so the record says
// what became of it from its first line. Loop goroutine (or before the loop
// starts).
func (r *Runtime) userMessage(d event.UserMessageData) {
	rel, held := r.routeUserMessage(d)
	d.Held = held
	failed := r.recordFailed
	ev := r.append(event.New(event.UserMessage, event.ActorUser, d))
	if !failed && r.recordFailed && (d.Inbox != "" || d.PhoneID != "") {
		// It could not go on the record, and it is still where it came
		// from (its inbox file, the phone's thread): the next run takes
		// it from there and passes it on, once. (One typed at the
		// terminal is kept nowhere else, and still goes to the agent.)
		return
	}
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
	r.listenUntil = asked.Add(time.Duration(r.cfg.Finalechat.QuestionTimeout()) * questionSecond)
	r.append(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: q.ID, Source: source}))
	r.ui.Log("question %s dismissed without an answer; nothing was sent to the agent", q.ID)
}

// questionSecond is one second of a question's time as a batch session
// listens after a dismissal; a variable so tests can run it in
// milliseconds.
var questionSecond = time.Second

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

// slashCommandFrom runs a command; source is where it was typed ("" for
// eagent's terminal) and client is what the sending client declared (its
// time zone reads "/autopilot until 10:00" in the user's time, not the
// machine's).
func (r *Runtime) slashCommandFrom(line, source string, client *clientcaps.Caps) {
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
			// Where it was typed, as the client names itself when it does.
			if client != nil && client.Source != "" {
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
		// From the phone or the web only /kill comes here (ownCommand),
		// and it is answered there, in the conversation.
		note := fields[0] + " went with eagent's own tasks and processes: the agent does the work now. /relay shows the agent, /screen its terminal, /interrupt stops its turn and /stop-relay stops it."
		if source == "" {
			r.ui.Log("%s", note)
		} else {
			r.recordMessage(event.MessageData{Text: note, Auto: true})
		}
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
