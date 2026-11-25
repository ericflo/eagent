package harness

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/tools"
)

// The model loop.
//
// eagent's model is called when something may need it: the user wrote, the
// agent finished a turn or got stuck, a message did not arrive, or the
// agent has been working for a while and the user should hear about it.
// Each call gets the session's record as one conversation, rendered from
// the log (state.View), and after it a few lines that exist for that call
// only: why it was woken, what the agent is doing this second, how long the
// user has heard nothing (tail.go).
//
// What a call leaves behind depends on what the model does with it:
//
//   - It holds (or says nothing usable). Then nothing is recorded but its
//     cost. The conversation is exactly what it was, so the next call's
//     prompt starts with the same bytes and the provider's cache of them
//     still holds. Most looks while an agent works end this way, which is
//     what makes looking often affordable.
//   - It acts. Then the lines it was given are recorded as a steer, its
//     reply and the results of its tools follow, and all of it is part of
//     the conversation from then on. If it looked at something (the
//     agent's screen, the session's record) it gets another call to use
//     what it saw, up to max_calls_per_turn; a reply that only acts ends
//     the turn.
//
// Either way the system prompt and the tool list never change, and the
// conversation only grows at its end, until it is restarted from a
// dossier (dossier.go).

// wakeKind is why eagent's model is called, most pressing last.
type wakeKind int

const (
	wakeNotice    wakeKind = iota // the harness left it a notice
	wakeTurnEnd                   // an agent finished a turn
	wakeAttention                 // an agent needs something: a dialog, an exit, a message that did not arrive
	wakeUser                      // the user wrote
	wakeKinds
)

// wake is one reason to call the model, with what the tail needs to say so.
type wake struct {
	kind   wakeKind
	event  bool // something happened that asks for a look (kind says what)
	look   bool // a progress look while the agent works: nothing asked for it
	moved  bool // (look) something has happened since the user last heard
	steps  int  // (look) how many steps the agents had taken when it was started
	thread bool // the thread's title and summary may be out of date
	owed   bool // a message the user wrote to eagent is still unanswered
	final  bool // the session is about to end: the closing word
}

// reason names the wake for the turn's record: the most pressing thing
// that asked for the look. The steer that follows names all of them.
func (w wake) reason() string {
	switch {
	case w.event && w.kind == wakeUser:
		return "the user wrote"
	case w.event && w.kind == wakeAttention:
		return "the agent needs attention"
	case w.event && w.kind == wakeTurnEnd:
		return "the agent finished a turn"
	case w.event:
		return "a notice from eagent"
	case w.look:
		return "progress while the agent works"
	case w.owed:
		return "a message for eagent is still unanswered"
	case w.final:
		return "the session is ending"
	}
	return "the thread's title and summary"
}

// turnState is the loop's memory of the model loop. Loop goroutine only.
type turnState struct {
	busy       bool      // a model call (a turn, or the dossier) is in flight
	callAt     time.Time // when the call in flight started
	compacting bool      // the conversation is being restarted from a dossier

	// seen is the newest event eagent has looked at, whether or not the
	// look left a record. wakes holds, per kind, the newest event that asks
	// for a look; one newer than seen is still owed.
	seen   int64
	wakes  [wakeKinds]int64
	worthy int64 // the newest event that shows in the conversation and is not eagent's own
	settle *time.Timer

	// What the user last heard from eagent, and what eagent had seen when
	// it said it.
	saidAt   time.Time
	saidSeq  int64
	saidSeen int64
	lastSaid string

	// Progress looks while the agent works: when the last one ran, how many
	// steps the agents had taken by then, and when one last failed.
	lookedAt  time.Time
	lookSteps int
	ticker    *time.Timer
	tickGen   uint64

	// silent is the cost of the looks that left no record since the last
	// one that did, written to the log in one line now and then.
	silent event.ChecksData
	// promptTokens is the size of the last prompt, recorded or not.
	promptTokens int

	// After a failed call: how many in a row, when the next try is due, and
	// the newest event at the time (a message from the user after it does
	// not wait for the timer).
	failures  int
	retryAt   time.Time
	failSeq   int64
	failTold  bool // the user has been told the model cannot be reached
	retryTime *time.Timer

	// threadDue says the title and summary may be out of date and the next
	// call should be asked about them (inline summarizer).
	threadDue bool

	// The dossier (dossier.go): how many calls this subsession has made and
	// how long its first prompt was, whether the provider refused a prompt
	// as too long, and when rotating the files last failed.
	subCalls        int
	firstPrompt     int
	overflowed      bool
	futileLogged    bool
	dossierFailedAt time.Time
	// adopt: the log was written by the three-actor design, and the first
	// thing this loop does is write a dossier from its files (resume.go).
	adopt bool

	// owed is a message the user wrote to eagent itself that was looked at
	// and left with no answer and nothing done about it. Until that
	// changes every call is told so, and a call is made for it alone a few
	// times, further and further apart.
	owed      int64
	owedTries int
	owedTimer *time.Timer

	// capRuns counts the turns in a row that stopped at the call limit
	// with an agent still stuck on its screen.
	capRuns int

	// finalAsked: the closing word of a session with no terminal has been
	// asked for.
	finalAsked bool
}

// init reads, from a replayed log, what the loop would otherwise have had
// in memory. Called once, before the loop starts.
func (t *turnState) init(st *state.State) {
	for _, ev := range st.Events {
		switch {
		case ev.Type == event.Assistant && ev.Actor == event.ActorEagent:
			var d event.AssistantData
			if ev.Decode(&d) == nil && d.SeenSeq > t.seen {
				t.seen = d.SeenSeq
			}
		case ev.Type == event.Checks && ev.Actor == event.ActorEagent:
			var d event.ChecksData
			if ev.Decode(&d) == nil && d.SeenSeq > t.seen {
				t.seen = d.SeenSeq
			}
		case ev.Type == event.Message:
			var d event.MessageData
			if ev.Decode(&d) == nil {
				t.saidAt, t.saidSeq = ev.Time, ev.Seq
				if !d.Auto {
					t.lastSaid, t.saidSeen = d.Text, t.seen
				}
			}
		case ev.Type == event.Question:
			t.saidAt, t.saidSeq = ev.Time, ev.Seq
		}
		if shows(ev) {
			t.worthy = ev.Seq
		}
	}
	t.promptTokens = st.ContextTokens()
	if st.Legacy() && st.Calls[event.ActorEagent] == 0 {
		// A log from the three-actor design: its events were weighed by
		// actors that no longer exist. Nothing in it is owed a look; the
		// session picks up from a briefing (resume.go).
		t.seen = st.LastSeq()
		return
	}
	// Whatever arrived after the last look and before the previous run
	// stopped is still owed one.
	for _, ev := range st.Events {
		if ev.Seq > t.seen {
			if k, ok := wakeFor(ev); ok {
				t.wakes[k] = ev.Seq
			}
		}
	}
}

// wakeFor says whether an event asks for a look, and how pressingly.
func wakeFor(ev event.Event) (wakeKind, bool) {
	switch ev.Type {
	case event.UserMessage, event.UserAnswer:
		return wakeUser, true
	case event.RelayTurn:
		return wakeTurnEnd, true
	case event.RelayState, event.RelayDelivered, event.RelayLimit:
		if state.NeedsAttention(ev) {
			return wakeAttention, true
		}
	case event.HarnessMessage:
		if ev.Actor == event.ActorEagent && ev.Task == "" {
			return wakeNotice, true
		}
	case event.ScheduleFire:
		if ev.Actor == event.ActorHarness {
			return wakeNotice, true
		}
	case event.SessionResume:
		var d event.SessionResumeData
		if ev.Decode(&d) == nil && len(d.Closed) > 0 {
			return wakeNotice, true
		}
	}
	return 0, false
}

// shows reports an event that adds a line to eagent's conversation and is
// not eagent's own doing: what a progress look would have to weigh.
func shows(ev event.Event) bool {
	switch ev.Type {
	case event.UserMessage, event.UserAnswer, event.RelayStart, event.RelayProgress, event.RelayTurn, event.RelayEnd:
		return true
	case event.ScheduleFire:
		return ev.Actor == event.ActorHarness
	case event.RelayState, event.RelayDelivered, event.RelayLimit:
		return state.NeedsAttention(ev)
	}
	return false
}

// wakeSettle is how long the loop waits after something asks for a look
// before making the call, so events that arrive together (a turn's end,
// its usage line, the agent going idle) are read in one call. A variable
// so tests can run without it.
var wakeSettle = 400 * time.Millisecond

// noteWake tracks which events ask for a look. Loop goroutine (called from
// append).
func (r *Runtime) noteWake(ev event.Event) {
	t := &r.turns
	if shows(ev) {
		t.worthy = ev.Seq
	}
	k, ok := wakeFor(ev)
	if !ok {
		return
	}
	t.wakes[k] = ev.Seq
	if k == wakeUser || k == wakeTurnEnd {
		t.capRuns = 0
	}
	if k == wakeUser {
		// What they write now is looked at on its own; an older message
		// still unanswered is in the conversation above it.
		r.clearOwed()
		t.finalAsked = false
	}
	if r.ending || wakeSettle <= 0 {
		return
	}
	if t.settle != nil {
		t.settle.Stop()
	}
	t.settle = time.AfterFunc(wakeSettle, func() {
		r.post(func() { r.turns.settle = nil })
	})
}

// wakePending reports whether something still asks for a look.
func (r *Runtime) wakePending() bool {
	_, ok := r.dueWake()
	return ok
}

// dueWake is the most pressing thing that still asks for a look.
func (r *Runtime) dueWake() (wakeKind, bool) {
	t := &r.turns
	for k := wakeKinds - 1; k >= 0; k-- {
		if t.wakes[k] > t.seen {
			return k, true
		}
	}
	return 0, false
}

// maybeTurn starts a model call when one is due: first the dossier when
// the conversation is full, then whatever asks for a look, then a progress
// look, then a look at the thread's title and summary. Loop goroutine.
func (r *Runtime) maybeTurn() {
	t := &r.turns
	if t.busy || r.ending {
		return
	}
	if kind, ok := r.dueWake(); ok {
		if t.settle != nil {
			return // more may be on its way; the timer ticks the loop
		}
		if time.Now().Before(t.retryAt) && t.wakes[wakeUser] <= t.failSeq {
			return // the model was failing a moment ago; a message from the user does not wait
		}
		if r.needsDossier() {
			r.startDossier()
			return
		}
		r.startTurn(wake{kind: kind, event: true, thread: t.threadDue || r.summaryRipe()})
		return
	}
	if time.Now().Before(t.retryAt) {
		return
	}
	if r.needsDossier() {
		// Restart at the first quiet moment: the prompt is still in the
		// provider's cache, so the dossier is cheap to write now, and the
		// user's next message does not have to wait for it.
		r.startDossier()
		return
	}
	if t.owed > 0 && t.owedTimer == nil {
		r.startTurn(wake{owed: true, thread: t.threadDue || r.summaryRipe()})
		return
	}
	if t.threadDue {
		r.startTurn(wake{thread: true})
	}
}

// ---- a message for eagent is answered -------------------------------------

// owedRetryDelays space out the calls made only because a message the user
// wrote to eagent is still unanswered.
var owedRetryDelays = []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute}

// oweAnswer notes that the message at seq was looked at and left
// unanswered, and arranges another call for it. Loop goroutine.
func (r *Runtime) oweAnswer(seq int64) {
	t := &r.turns
	if t.owed != seq {
		t.owed, t.owedTries = seq, 0
	}
	if t.owedTimer != nil {
		t.owedTimer.Stop()
		t.owedTimer = nil
	}
	if t.owedTries >= len(owedRetryDelays) {
		r.ui.Log("eagent: a message addressed to it went unanswered after %d reminders", t.owedTries+1)
		t.owed, t.owedTries = 0, 0
		return
	}
	d := owedRetryDelays[t.owedTries]
	t.owedTries++
	t.owedTimer = time.AfterFunc(d, func() {
		r.post(func() { r.turns.owedTimer = nil })
	})
}

// clearOwed forgets the unanswered message: it was answered, acted on, or
// overtaken by a newer one. Loop goroutine.
func (r *Runtime) clearOwed() {
	t := &r.turns
	t.owed, t.owedTries = 0, 0
	if t.owedTimer != nil {
		t.owedTimer.Stop()
		t.owedTimer = nil
	}
}

// heldSince returns the newest message the user wrote that was kept for
// eagent and has not been looked at, or 0. Loop goroutine.
func (r *Runtime) heldSince(seen int64) int64 {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= seen {
			break
		}
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.Held != "" {
				return ev.Seq
			}
		}
	}
	return 0
}

// sentBack is the exchange that returns a reply to the model for another
// try without recording it: its reply, then why it will not do. A reply
// that called hold gets the reason as that call's result.
func sentBack(resp *llm.Response, why string) []llm.Message {
	out := []llm.Message{{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL}}
	if len(resp.ToolCalls) == 0 {
		return append(out, llm.Message{Role: "user", Text: "[eagent] " + why})
	}
	results := llm.Message{Role: "tool"}
	for _, c := range resp.ToolCalls {
		results.Results = append(results.Results, llm.ToolResult{CallID: c.ID, Name: c.Name, Output: "not accepted: " + why, IsError: true})
	}
	return append(out, results)
}

// ---- progress looks -------------------------------------------------------

// narrateGap is the least time between something eagent said and the next
// progress look, so two messages never land on top of each other.
var narrateGap = 45 * time.Second

// armTicker (re)starts the timer that considers a progress look. A
// generation rejects an expired callback already queued when a live
// settings change reschedules it. Loop goroutine.
func (r *Runtime) armTicker() {
	t := &r.turns
	t.tickGen++
	gen := t.tickGen
	if t.ticker != nil {
		t.ticker.Stop()
	}
	if r.ending {
		return
	}
	every := time.Duration(r.activeSettings.TickSeconds) * time.Second
	if every <= 0 {
		every = 30 * time.Second
	}
	t.ticker = time.AfterFunc(every, func() {
		r.post(func() {
			if gen != r.turns.tickGen || r.ending {
				return
			}
			r.maybeLook()
			r.armTicker()
		})
	})
}

// maybeLook starts a progress look when an agent is working and there is
// something for eagent to weigh: news since it last spoke, or a silence
// that has gone on too long. A tick with nothing new costs no model call.
// Loop goroutine.
func (r *Runtime) maybeLook() {
	t := &r.turns
	if t.busy || !r.st.RelayWorking() || r.wakePending() || time.Now().Before(t.retryAt) {
		return // something that happened comes first
	}
	limit, quiet := r.quietLimit(), r.quietFor()
	steps, stepped := r.relays.stepped(t.saidAt)
	// moved: something happened since the user last heard.
	// fresh: since eagent last looked.
	moved := t.worthy > t.saidSeen || stepped
	fresh := t.worthy > t.seen || steps > t.lookSteps
	gap := narrateGap
	if limit > 0 {
		gap = min(gap, limit)
	}
	switch {
	case moved && limit > 0 && quiet >= limit && time.Since(t.lookedAt) >= gap:
		// Overdue: it should speak.
	case moved && fresh && quiet >= gap:
		// Something new to tell, and the last message has had its moment.
	case !moved && limit > 0 && quiet >= limit && time.Since(t.lookedAt) >= limit:
		// Nothing new for a while: it says what is still running.
	default:
		return
	}
	if r.needsDossier() {
		r.startDossier()
		return
	}
	t.lookedAt = time.Now()
	r.startTurn(wake{look: true, moved: moved, steps: steps, thread: t.threadDue || r.summaryRipe()})
}

// quietLimit is the longest the user goes without a progress line while an
// agent works. Loop goroutine only.
func (r *Runtime) quietLimit() time.Duration {
	return time.Duration(r.activeSettings.QuietSeconds) * time.Second
}

// quietFor is how long the user has gone without hearing from eagent, or
// since the session started when there has been nothing. Loop goroutine.
func (r *Runtime) quietFor() time.Duration {
	if !r.turns.saidAt.IsZero() {
		return time.Since(r.turns.saidAt)
	}
	if !r.st.Started.IsZero() {
		return time.Since(r.st.Started)
	}
	return 0
}

// contextTokens is the size of eagent's last prompt.
func (r *Runtime) contextTokens() int {
	if r.turns.promptTokens > 0 {
		return r.turns.promptTokens
	}
	return r.st.ContextTokens()
}

// ---- a turn ---------------------------------------------------------------

func (r *Runtime) startTurn(w wake) {
	t := &r.turns
	t.busy, t.callAt = true, time.Now()
	if w.thread {
		t.threadDue = false
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.turn(w)
		r.post(func() {
			r.turns.busy, r.turns.callAt = false, time.Time{}
		})
	}()
}

// turn makes the model calls of one wake. It runs on its own goroutine.
func (r *Runtime) turn(w wake) {
	ctx := r.ctx
	maxCalls := max(r.cfg.MaxCallsPerTurn, 1)
	tc := &turnCalls{wake: w}
	recorded := false
	// extra is an exchange that belongs to this call alone and is never
	// recorded: a reply that had to be sent back for another try.
	var extra []llm.Message
	nudged, rewrote, cut, reminded := false, false, false, false
	end := "done"
	// tail is the end of the prompt for this turn: written once, and
	// recorded only if the model acts on it. held is settled with it: the
	// message kept for eagent that this turn has to answer (0 for none).
	var tail string
	var mustSpeak bool
	var held int64
	who := "the agent"
	for call := 1; call <= maxCalls; call++ {
		if ctx.Err() != nil {
			end = "interrupted"
			break
		}
		var msgs []llm.Message
		var seen int64
		r.sync(func() {
			msgs = r.st.View(&r.view)
			seen = r.st.LastSeq()
			if tail == "" {
				held = r.heldSince(r.turns.seen)
				if w.owed {
					held = r.turns.owed
				}
				mustSpeak = w.final && r.turns.saidAt.IsZero()
				if active := r.st.ActiveRelays(); len(active) > 0 {
					who = active[0].Who()
				}
				tail = r.tail(w, time.Now())
			}
			if !recorded {
				msgs = state.WithTail(msgs, tail)
			}
			r.turns.callAt = time.Now()
		})
		// Every call of a session is the same request but for its messages:
		// one system prompt, one tool list, one effort, one output ceiling.
		// Anything that varied would cost the provider's cache of the
		// conversation.
		req := llm.Request{System: r.system, Messages: append(msgs, extra...), Tools: r.toolset, CacheKey: r.sess.ID}
		resp, err := r.complete(ctx, event.ActorEagent, req, r.observer())
		r.ui.Stream("", "")
		if err != nil {
			if ctx.Err() != nil {
				end = "interrupted"
				break
			}
			r.sync(func() {
				r.turnFailed(err)
				r.turns.threadDue = r.turns.threadDue || w.thread
				if w.final {
					r.fallbackFinal()
				}
			})
			end = "error"
			break
		}
		acts := 0
		for _, c := range resp.ToolCalls {
			if c.Name != tools.Hold {
				acts++
			}
		}
		if acts == 0 {
			// A reply that reaches no one. Some are sent back once, off the
			// record, with the reason; after that it is taken for a hold.
			var back string
			switch {
			case resp.Truncated() && len(resp.ToolCalls) == 0 && !cut:
				cut = true
				back = "Your reply was cut off by the output limit before any tool call was made. Keep it short and act through tool calls."
			case strings.TrimSpace(resp.Text) != "" && len(resp.ToolCalls) == 0 && !nudged:
				nudged = true
				back = "That reply had no tool call, so nobody saw it. To tell the user something, call send_message with those words; to pass on or do something, call the tool for it; if there is nothing to say or do, call hold."
			case held > 0 && !reminded:
				reminded = true
				back = "The user's message was kept for you, so it has gone nowhere and nobody else will answer it. Answer it with send_message, or pass it on with relay_forward if it is for " + who + ". Holding is not an answer to a message addressed to you."
			case mustSpeak && !reminded:
				reminded = true
				back = "The session is ending and the user has heard nothing from you. Send the final report now with send_message."
			}
			if back != "" {
				r.sync(func() { r.noteSilent(resp, 0, wake{}) })
				extra = sentBack(resp, back)
				continue
			}
			r.sync(func() { r.noteSilent(resp, seen, w) })
			break
		}
		if leak := leakingMessage(resp); leak != "" && !rewrote {
			// The machinery has names inside; the user hears "I". One
			// rewrite is asked for, off the record, and nothing in the
			// reply is run; after that the words go out as they are.
			rewrote = true
			r.sync(func() { r.noteSilent(resp, 0, wake{}) })
			extra = []llm.Message{
				{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL},
				rewriteRequest(resp, leak),
			}
			continue
		}
		// It acts, so this turn goes on the record: the lines it was given,
		// then its reply.
		r.sync(func() {
			r.flushSilent()
			if !recorded {
				r.append(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: w.reason()}))
				r.append(event.New(event.Steer, event.ActorEagent, event.SteerData{Text: tail}))
			}
			r.append(event.New(event.Assistant, event.ActorEagent, event.AssistantData{
				Provider: resp.Protocol, Model: resp.Model, Host: resp.BaseURL, Text: resp.Text, Reasoning: summarizeReasoning(resp.Reasoning),
				ToolCalls: resp.ToolCalls, Native: resp.Native, Usage: resp.Usage,
				ElapsedMS: resp.Elapsed.Milliseconds(), SeenSeq: seen, Stop: resp.Stop,
			}))
			r.looked(seen, resp.Usage.Input, w)
		})
		recorded, extra = true, nil
		again := false
		for _, c := range resp.ToolCalls {
			out, isErr, more := "interrupted", true, false
			if ctx.Err() == nil {
				out, isErr, more = r.runTool(ctx, tc, c)
			}
			images := tc.images
			tc.images = nil
			r.sync(func() {
				r.append(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: c.ID, Name: c.Name, Output: out, IsError: isErr, Images: images}))
			})
			again = again || more
		}
		if !again {
			break
		}
		if call == maxCalls {
			end = "call limit"
		}
	}
	r.sync(func() {
		if recorded {
			r.append(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: end}))
		}
		switch {
		case end == "error" || end == "interrupted":
			// What asked for the look is still owed one.
		case tc.handled:
			r.clearOwed()
		case held > 0:
			r.oweAnswer(held)
		}
		if end == "call limit" {
			r.afterCallLimit(maxCalls)
		}
		if w.final && mustSpeak && !tc.spoke && end != "error" && end != "interrupted" {
			r.fallbackFinal()
		}
	})
}

// afterCallLimit handles a turn that was stopped at the call limit. If an
// agent is still stuck on its screen, the work of clearing it is not done:
// eagent is woken once more to carry on, and after that the user is told.
// Loop goroutine.
func (r *Runtime) afterCallLimit(maxCalls int) {
	t := &r.turns
	var stuck *state.Relay
	for _, rel := range r.st.ActiveRelays() {
		if rel.State == "blocked" {
			stuck = rel
			break
		}
	}
	if stuck == nil {
		return
	}
	t.capRuns++
	if t.capRuns <= 1 {
		r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: fmt.Sprintf("You were stopped after %d calls in a row, and %s is still stuck on its screen. Read the screen once more and either clear it or ask the user; do not repeat what has not worked.", maxCalls, stuck.Who())}))
		return
	}
	r.recordMessage(event.MessageData{Auto: true, Important: true, Text: fmt.Sprintf("%s is stuck on something on its screen and I could not clear it. You can look and type into it with `eagent relays attach %s`.", stuck.Who(), stuck.Name)})
}

// fallbackFinal makes sure a session with no terminal does not end in
// silence when the model could not give the closing word: eagent says
// where things stand in its own words. Loop goroutine.
func (r *Runtime) fallbackFinal() {
	var b strings.Builder
	b.WriteString("This session is ending and I could not write a proper closing report.")
	for _, name := range r.st.RelayOrder {
		rel := r.st.Relays[name]
		if rel == nil || strings.TrimSpace(rel.LastTurn) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n%s's last report (%s):\n%s", rel.Who(), r.when(rel.LastTurnAt), clipRunes(strings.TrimSpace(rel.LastTurn), 1500))
	}
	if r.st.Question != nil {
		b.WriteString("\n\nStill open: " + clipRunes(oneLine(r.st.Question.Text, 300), 300))
	}
	r.recordMessage(event.MessageData{Auto: true, Important: true, Text: b.String()})
}

// looked notes that a call came back having seen everything up to seen.
// Loop goroutine.
func (r *Runtime) looked(seen int64, promptTokens int, w wake) {
	t := &r.turns
	if seen > t.seen {
		t.seen = seen
	}
	if promptTokens > 0 {
		t.promptTokens = promptTokens
		if t.subCalls == 0 {
			t.firstPrompt = promptTokens
		}
	}
	t.subCalls++
	t.overflowed = false
	if w.look {
		t.lookSteps = w.steps
	}
	if w.thread && seen > r.summary.seen {
		r.summary.seen = seen // it was asked about the title and summary
	}
	if t.failures > 0 {
		t.failures, t.retryAt, t.failTold = 0, time.Time{}, false
		r.ui.Log("eagent: the model answers again")
	}
}

// noteSilent keeps the cost of a call that left no record, to be put in
// the log in one line with the others. seen is 0 for a reply that was sent
// back for another try: it weighed nothing yet. Loop goroutine.
func (r *Runtime) noteSilent(resp *llm.Response, seen int64, w wake) {
	t := &r.turns
	t.silent.Count++
	t.silent.Usage.Input += resp.Usage.Input
	t.silent.Usage.Output += resp.Usage.Output
	t.silent.Usage.Cached += resp.Usage.Cached
	t.silent.Usage.Reasoning += resp.Usage.Reasoning
	t.silent.ElapsedMS += resp.Elapsed.Milliseconds()
	if resp.Usage.Input > 0 {
		t.silent.PromptTokens = resp.Usage.Input
	}
	if seen > 0 {
		t.silent.SeenSeq = seen
		r.looked(seen, resp.Usage.Input, w)
	}
	if t.silent.Count >= 20 {
		r.flushSilent()
	}
}

// flushSilent records the looks that left no trace since the last record,
// so their tokens count toward the session's totals. Loop goroutine.
func (r *Runtime) flushSilent() {
	t := &r.turns
	if t.silent.Count == 0 {
		return
	}
	d := t.silent
	t.silent = event.ChecksData{}
	r.append(event.New(event.Checks, event.ActorEagent, d))
}

// errorRetryDelays space out the calls after the model failed: once soon,
// then further and further apart, hourly for as long as it takes. A session
// lives for days; a provider that comes back needs no resume.
var errorRetryDelays = []time.Duration{5 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}

// turnFailed handles a model call that did not come back. What asked for
// the look is still owed one, so the loop tries again later; a message from
// the user tries at once. Loop goroutine.
func (r *Runtime) turnFailed(err error) {
	t := &r.turns
	var ae *llm.APIError
	if errors.As(err, &ae) && ae.ContextOverflow() && !t.overflowed {
		// The prompt no longer fits: restart the conversation from a
		// dossier before anything else. If it does not fit even then, the
		// second refusal takes the ordinary path below.
		r.ui.Log("eagent: the model refused the prompt as too long (%s); restarting the conversation from a dossier", shortErr(err))
		t.overflowed = true
		t.promptTokens = max(t.promptTokens, r.cfg.RolloverTokens)
		return
	}
	t.failures++
	wait := errorRetryDelays[min(t.failures, len(errorRetryDelays))-1]
	t.retryAt, t.failSeq = time.Now().Add(wait), r.st.LastSeq()
	r.ui.Log("eagent: model call failed: %s; trying again in %s", shortErr(err), roundDur(wait))
	r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: event.ActorEagent, Text: err.Error()}))
	t.failSeq = r.st.LastSeq()
	if t.retryTime != nil {
		t.retryTime.Stop()
	}
	t.retryTime = time.AfterFunc(wait, func() { r.post(func() {}) })
	if t.failures == 2 && !t.failTold {
		// The user's messages still reach the agent; they should know why
		// nobody is telling them what it does.
		t.failTold = true
		who := "the agent"
		if active := r.st.ActiveRelays(); len(active) > 0 {
			who = active[0].Who()
		}
		if errors.As(err, &ae) && ae.ContextOverflow() {
			// Not an outage: the provider refuses the prompt as too long,
			// and restarting from a dossier did not make it fit.
			r.recordMessage(event.MessageData{Auto: true, Important: true, Text: fmt.Sprintf("My model refuses my conversation as too long, even after I restarted it from my notes, so I can't tell you what %s is doing. Your messages still go straight to %s. Shorten .agents/eagent/INSTRUCTIONS.md, or move me to a model with a larger context in this thread's settings.", who, who)})
			return
		}
		r.recordMessage(event.MessageData{Auto: true, Text: fmt.Sprintf("I can't reach my own model right now (%s), so I can't tell you what %s is doing. Your messages still go straight to %s. I keep trying and will pick up where I left off.", shortErr(err), who, who)})
	}
}

// stopTimers stops the loop's timers at the end of a run.
func (r *Runtime) stopTimers() {
	t := &r.turns
	for _, tm := range []*time.Timer{t.ticker, t.settle, t.retryTime, t.owedTimer} {
		if tm != nil {
			tm.Stop()
		}
	}
	for _, tm := range r.timers {
		tm.Stop()
	}
	for _, tm := range r.autopilotTimers {
		tm.Stop()
	}
	for _, tm := range r.autopilotChecks {
		tm.Stop()
	}
}

// observer forwards streaming deltas to the UI.
func (r *Runtime) observer() *llm.Observer {
	return &llm.Observer{
		Text:      func(d string) { r.ui.Stream("text", d) },
		Reasoning: func(d string) { r.ui.Stream("reasoning", d) },
		ToolCall:  func(n string) { r.ui.Stream("tool", n) },
		Reset:     func() { r.ui.Stream("", "") },
	}
}

// summarizeReasoning keeps reasoning text for humans without bloating the log.
func summarizeReasoning(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4000 {
		return s[:4000] + "..."
	}
	return s
}

// ---- what the user is told ------------------------------------------------

// mechanicsWords are the names of the machinery the user never sees; eagent
// speaks as "I" and a message naming them is sent back once.
var mechanicsWords = regexp.MustCompile(`(?i)\b(harness|dossier|subsession|rollover|context reset|tool call)s?\b`)

// mechanicsLeak lists the internal names a message uses, or "".
func mechanicsLeak(text string) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mechanicsWords.FindAllString(text, -1) {
		w := strings.ToLower(m)
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return strings.Join(out, ", ")
}

// leakingMessage reports the internal names in the words a reply would
// send the user (a message or a question), or "".
func leakingMessage(resp *llm.Response) string {
	for _, c := range resp.ToolCalls {
		if c.Name != tools.SendMessage && c.Name != tools.AskUser {
			continue
		}
		if args, err := llm.ArgsObject(c.Args); err == nil {
			if text, _ := args["text"].(string); text != "" {
				if leak := mechanicsLeak(text); leak != "" {
					return leak
				}
			}
		}
	}
	return ""
}

// rewriteRequest answers a reply whose words named the machinery: nothing
// in it was run, and it is asked for again.
func rewriteRequest(resp *llm.Response, leak string) llm.Message {
	m := llm.Message{Role: "tool"}
	for _, c := range resp.ToolCalls {
		out := "not run: another call in this reply has to be rewritten first. Make this call again in your next reply."
		if c.Name == tools.SendMessage || c.Name == tools.AskUser {
			out = fmt.Sprintf("not sent: it names internal machinery (%s), which the user never sees. Say it as \"I\", in your own words, and send it again.", leak)
		}
		m.Results = append(m.Results, llm.ToolResult{CallID: c.ID, Name: c.Name, Output: out, IsError: true})
	}
	return m
}

// leakedToken matches provider control markup that occasionally escapes into
// model text, e.g. DeepSeek's <｜DSML｜…> tool markup or <|im_end|>.
var leakedToken = regexp.MustCompile(`</?[｜|][^<>]{0,80}>`)

// cleanNarration removes leaked control tokens and trailing junk.
func cleanNarration(text string) string {
	text = leakedToken.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// recordMessage records and shows something the user is told. Loop
// goroutine only.
func (r *Runtime) recordMessage(d event.MessageData) {
	d.Text = cleanNarration(d.Text)
	if d.Text == "" {
		return
	}
	t := &r.turns
	ev := r.append(event.New(event.Message, event.ActorEagent, d))
	t.saidAt, t.saidSeq = time.Now(), ev.Seq
	if !d.Auto {
		t.lastSaid, t.saidSeen = d.Text, t.seen
	}
	text := d.Text
	if s := attachmentSummary(d.Attachments); s != "" {
		text += "\n" + s
	}
	r.ui.Say(text)
}

// tell says something to the user in the harness's own words, with no
// model call: the outcome of a command they typed. It is recorded as said
// by eagent and shown to the model as a notice. Any goroutine but the loop.
func (r *Runtime) tell(text string, important bool) {
	r.sync(func() { r.recordMessage(event.MessageData{Text: text, Important: important, Auto: true}) })
}

// ---- small helpers --------------------------------------------------------

func since(t, now time.Time) string {
	d := now.Sub(t).Round(time.Second)
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// roundDur is a human duration: 45s, 3m, 1h10m.
func roundDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
