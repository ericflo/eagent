package harness

import (
	"context"
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
	// wait bounds the whole turn: the last title and summary of a session
	// that is about to end must not hold the end for long.
	wait time.Duration
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
	// owedAnswer: what is owed is not a message but the user's answer to
	// a question of eagent's, held on in a turn that had no call left to
	// say it has reached nobody else. It is said once, later, and a hold
	// after that stands: an answer can need nothing done.
	owedAnswer bool

	// capRuns counts the turns in a row that stopped at the call limit
	// with an agent still stuck on its screen.
	capRuns int

	// finalAsked: the closing word of a session with no terminal has been
	// asked for. finalBusy: that call is in flight (finish waits for it).
	finalAsked bool
	finalBusy  bool
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
	// How many calls the conversation in the current file has made, and how
	// long its first prompt was: the rollover guard (needsDossier) reads
	// them to tell a conversation that was too long from its first call,
	// which restarting cannot shrink, from one that grew. Left at zero, the
	// first call of this process would be taken for the first call of the
	// conversation, and a resumed session whose prompt the provider then
	// refused would never be restarted from a dossier again.
	if cur := st.Current(); cur != nil {
		for _, ev := range st.Events {
			if ev.Seq < cur.StartSeq || ev.Actor != event.ActorEagent || ev.Task != "" {
				continue
			}
			switch ev.Type {
			case event.Assistant:
				var d event.AssistantData
				if ev.Decode(&d) == nil {
					if t.subCalls == 0 {
						t.firstPrompt = d.Usage.Input
					}
					t.subCalls++
				}
			case event.Checks:
				var d event.ChecksData
				if ev.Decode(&d) == nil && d.Count > 0 {
					if t.subCalls == 0 {
						// The looks that left no record are kept as a
						// total. The first prompt is known when the record
						// says it, or when there was one look. A record
						// from before it said so leaves it unknown (0):
						// the guard then lets a refused conversation be
						// restarted, which costs one dossier if it was in
						// fact too long from the start, where a guess that
						// was too high would switch restarts off.
						switch {
						case d.FirstPrompt > 0:
							t.firstPrompt = d.FirstPrompt
						case d.Count == 1:
							t.firstPrompt = d.Usage.Input
						}
					}
					t.subCalls += d.Count
				}
			}
		}
	}
	if st.Legacy() && !st.Upgraded() {
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
		// A message of theirs still unanswered stays owed: the look at
		// what they write now is told about it too (tail.go).
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
	if t.owedAnswer {
		// A message kept for eagent comes before a reminder about an
		// answer, and is what the calls are now made for.
		t.owed, t.owedTries, t.owedAnswer = 0, 0, false
	}
	switch {
	case t.owed == 0:
		t.owed, t.owedTries = seq, 0
	case seq > t.owed:
		// A newer one joins the one still unanswered: the oldest stays the
		// mark (the reminder names every message since), and the
		// reminders start over.
		t.owedTries = 0
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

// oweReminder arranges one later call about the user's answer at seq,
// which a turn with no call left held on without being told that nobody
// else has seen it. A message kept for eagent that is already owed comes
// first, and its calls are told of the answer too. Loop goroutine.
func (r *Runtime) oweReminder(seq int64) {
	t := &r.turns
	if t.owed != 0 {
		return
	}
	t.owed, t.owedTries, t.owedAnswer = seq, 1, true
	if t.owedTimer != nil {
		t.owedTimer.Stop()
	}
	t.owedTimer = time.AfterFunc(owedRetryDelays[0], func() {
		r.post(func() { r.turns.owedTimer = nil })
	})
}

// clearOwed forgets the unanswered message: it was answered or acted on.
// Loop goroutine.
func (r *Runtime) clearOwed() {
	t := &r.turns
	t.owed, t.owedTries, t.owedAnswer = 0, 0, false
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

// answeredSince returns the newest answer the user gave to a question of
// eagent's after seen, or 0. Loop goroutine.
func (r *Runtime) answeredSince(seen int64) int64 {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Seq <= seen {
			break
		}
		if ev.Type == event.UserAnswer {
			return ev.Seq
		}
	}
	return 0
}

// sentBack is the exchange that returns a reply to the model for another
// try without recording it: its reply, then why it will not do. A reply
// that called hold gets the reason as that call's result.
func sentBack(resp *llm.Response, why string) []llm.Message {
	out := []llm.Message{{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL, NativeModel: resp.By()}}
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
	t.finalBusy = w.final
	if w.thread {
		t.threadDue = false
		if r.summary.final && !w.event && !w.owed && !w.look && !w.final {
			w.wait = summaryFinalWait // as the dedicated call is (summary.go)
		}
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.turn(w)
		r.post(func() {
			r.turns.busy, r.turns.callAt, r.turns.finalBusy = false, time.Time{}, false
		})
	}()
}

// turn makes the model calls of one wake. It runs on its own goroutine.
func (r *Runtime) turn(w wake) {
	ctx := r.ctx
	if w.final {
		// The closing word gets a minute of its own: it is still written
		// when the session is being shut down, and a provider that cannot
		// be reached does not keep a finished session open.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), finalReportTimeout)
		defer cancel()
	} else if w.wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.ctx, w.wait)
		defer cancel()
	}
	maxCalls := max(r.cfg.MaxCallsPerTurn, 1)
	tc := &turnCalls{wake: w}
	recorded := false
	// extra is what belongs to this turn's calls alone and is never
	// recorded: replies that had to be sent back for another try, each
	// with the reason, in the order they happened.
	var extra []llm.Message
	nudged, rewrote, cut, reminded, remindedAnswer, remindedFinal := false, false, false, false, false, false
	end := "done"
	// tail is the end of the prompt: written when the turn starts, and
	// again when something that asks for a look arrives while the turn is
	// running (the user writes, the agent ends a turn), so that nothing
	// reaches the model without being told what it calls for. A tail is
	// recorded only if the model acts on it (tailKept). Two things are
	// settled with each tail: held is the message kept for eagent that
	// the turn has to answer (0 for none), and answered says the user
	// answered a question.
	//
	// base is the conversation as rendered for a call, and covered the
	// newest event in it. A reply is recorded with covered, and the
	// renderer puts everything newer after that reply (state.View), so
	// the two must be taken together: a later prompt then has every line
	// exactly where this call saw it. base is rendered again only when the
	// record has changed (refresh). A reply sent back for another try is
	// answered from the same base, so that call is the last one plus the
	// exchange and nothing has moved under it.
	var tail string
	tailKept := false
	asked := w.thread // the turn was asked about the title and summary
	var mustSpeak, answered bool
	var held, covered int64
	// answerSeq is the answer this turn was woken for (0 for none), and
	// answerDebt says the turn was told of an answer owed a reminder.
	var answerSeq int64
	answerDebt := false
	var base []llm.Message
	refresh := true
	who := "the agent"
	stopped := func() string { return stopReason(ctx) }
	// owes returns the reminder for what the wake asked for and the turn
	// has not done, each given once: a message kept for eagent, an answer
	// from the user, the closing word. acted says the reply did something
	// else instead (set the title, say) rather than hold.
	owes := func(acted bool) string {
		switch {
		case held > 0 && !reminded && !tc.handled:
			reminded = true
			last := " Holding is not an answer to a message addressed to you."
			if acted {
				last = " What you just did is not an answer to it."
			}
			return "The user's message was kept for you, so it has gone nowhere and nobody else will answer it. Answer it with send_message, or pass it on with relay_forward if it is for " + who + "." + last
		case answered && !remindedAnswer && !tc.handled:
			remindedAnswer = true
			last := " Hold again only if the answer needs nothing done."
			if acted {
				last = " Hold only if the answer needs nothing done."
			}
			return "The user's answer has reached nobody but you. If " + who + " is waiting on it, give it to " + who + " with relay_send in words it can act on; if the question was only yours, do what the answer calls for, or tell the user what happens next." + last
		case mustSpeak && !remindedFinal && !tc.spoke:
			remindedFinal = true
			if acted {
				return "The session is ending and the user is owed a last word, which they have not had. Send the final report now with send_message."
			}
			return "Holding is not allowed here: the session is ending and the user is owed a last word. Send the final report now with send_message."
		}
		return ""
	}
	for call := 1; call <= maxCalls; call++ {
		if ctx.Err() != nil {
			end = stopped()
			break
		}
		var msgs []llm.Message
		r.sync(func() {
			defer func() { r.turns.callAt = time.Now() }()
			msgs = base
			if !tailKept {
				msgs = state.WithTail(base, tail)
			}
			if !refresh {
				return
			}
			refresh = false
			base, covered = r.st.View(&r.view), r.st.LastSeq()
			fresh := tail == ""
			if !fresh {
				// Only something that asks for a look gets a tail of its
				// own. Anything else that came (a remark from the agent,
				// what this turn's own calls put on the record) is in the
				// conversation the call is given, and counts as seen.
				if k, ok := r.dueWake(); ok {
					w = wake{kind: k, event: true, final: w.final, thread: asked && !tc.threaded, wait: w.wait}
					tc.wake, fresh = w, true
				}
			}
			if fresh {
				first := tail == ""
				if h := r.heldSince(r.turns.seen); h > 0 {
					// Not answered by what the turn did before it came.
					held, tc.handled = h, tc.handled && first
				}
				if first && r.turns.owed > 0 && r.turns.owedAnswer {
					// Its tail says the answer has reached nobody else
					// (owedTail), in the call made for that alone or in
					// one made for something else. That is the reminder:
					// a hold is not sent back with it again.
					answerDebt, remindedAnswer = true, true
				} else if first && w.owed {
					held = r.turns.owed
				}
				if seq := r.answeredSince(r.turns.seen); seq > 0 {
					answered, answerSeq = true, seq
				}
				// The closing word is owed whenever the user has not heard
				// from eagent since they last wrote (or ever).
				mustSpeak = w.final && r.turns.saidSeq <= r.st.LastUserSeq
				if active := r.st.ActiveRelays(); len(active) > 0 {
					who = active[0].Who()
				}
				tail, tailKept = r.tail(w, time.Now()), false
			}
			msgs = base
			if !tailKept {
				msgs = state.WithTail(base, tail)
			}
		})
		// Every call of a session is the same request but for its messages:
		// one system prompt, one tool list, one effort, one output ceiling.
		// Anything that varied would cost the provider's cache of the
		// conversation.
		req := llm.Request{System: r.system, Messages: append(msgs, extra...), Tools: r.toolset, CacheKey: r.sess.ID}
		resp, err := r.complete(ctx, event.ActorEagent, req, r.observer())
		r.ui.Stream("", "")
		if err != nil {
			if ctx.Err() != nil && !w.final {
				end = stopped() // on a timeout the row keeps the summary it had
				break
			}
			r.sync(func() {
				r.turnFailed(err)
				r.turns.threadDue = r.turns.threadDue || w.thread
				if w.final && mustSpeak && !tc.spoke {
					// The user must still get something.
					r.fallbackFinal()
				}
			})
			end = "error"
			break
		}
		acts, fits := 0, false
		for _, c := range resp.ToolCalls {
			switch {
			case c.Name == tools.Hold:
			case keepsThread(c):
				// "The title and summary still fit" changes nothing, so
				// like a hold it leaves no record.
				fits = true
			default:
				acts++
			}
		}
		if acts == 0 && mustSpeak && len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Text) != "" {
			// The closing report came as plain text: the text is the report.
			r.sync(func() {
				r.flushSilent()
				if !recorded {
					r.append(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: w.reason()}))
				}
				if !tailKept {
					r.append(event.New(event.Steer, event.ActorEagent, event.SteerData{Text: tail}))
				}
				r.append(event.New(event.Assistant, event.ActorEagent, event.AssistantData{
					Provider: resp.Protocol, Model: resp.Model, Asked: resp.Asked, Host: resp.BaseURL, Text: resp.Text, Reasoning: summarizeReasoning(resp.Reasoning),
					Native: resp.Native, Usage: resp.Usage, ElapsedMS: resp.Elapsed.Milliseconds(), SeenSeq: covered, Stop: resp.Stop,
				}))
				r.looked(covered, resp.Usage.Input, w)
				r.recordMessage(event.MessageData{Text: resp.Text, Important: true})
			})
			recorded, tailKept, tc.spoke, tc.handled = true, true, true, true
			break
		}
		// last: no call is left in this turn. A reply sent back now would
		// never be answered, and the wake would stay unsettled, so the next
		// turn would start at once with the same wake and its reminders
		// unspent: with max_calls_per_turn at 1, a model that held on a
		// message kept for eagent was called again without end. On the last
		// call a reply stands as it came, and what is still owed is asked
		// for again later (oweAnswer, the closing word's fallback).
		last := call == maxCalls
		if acts == 0 {
			// A reply that reaches no one. Some are sent back once, off the
			// record, with the reason; after that it is taken for a hold.
			var back string
			switch {
			case last:
			case resp.Truncated() && len(resp.ToolCalls) == 0 && !cut:
				cut = true
				back = "Your reply was cut off by the output limit before any tool call was made. Keep it short and act through tool calls."
			case strings.TrimSpace(resp.Text) != "" && len(resp.ToolCalls) == 0 && !nudged:
				nudged = true
				back = "That reply had no tool call, so nobody saw it. To tell the user something, call send_message with those words; to pass on or do something, call the tool for it; if there is nothing to say or do, call hold."
			default:
				back = owes(false)
			}
			if back != "" {
				r.sync(func() { r.noteSilent(resp, 0, wake{}) })
				extra = append(extra, sentBack(resp, back)...)
				continue
			}
			r.sync(func() {
				r.noteSilent(resp, covered, w)
				if fits {
					tc.threaded = true
					r.threadKept()
				}
			})
			break
		}
		if leak := leakingMessage(resp); leak != "" && !rewrote && !last {
			// The machinery has names inside; the user hears "I". One
			// rewrite is asked for, off the record, and nothing in the
			// reply is run; after that the words go out as they are.
			rewrote = true
			r.sync(func() { r.noteSilent(resp, 0, wake{}) })
			extra = append(extra,
				llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL, NativeModel: resp.By()},
				rewriteRequest(resp, leak),
			)
			continue
		}
		// It acts, so this turn goes on the record: the lines it was given,
		// then its reply.
		r.sync(func() {
			r.flushSilent()
			if !recorded {
				r.append(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: w.reason()}))
			}
			if !tailKept {
				r.append(event.New(event.Steer, event.ActorEagent, event.SteerData{Text: tail}))
			}
			r.append(event.New(event.Assistant, event.ActorEagent, event.AssistantData{
				Provider: resp.Protocol, Model: resp.Model, Asked: resp.Asked, Host: resp.BaseURL, Text: resp.Text, Reasoning: summarizeReasoning(resp.Reasoning),
				ToolCalls: resp.ToolCalls, Native: resp.Native, Usage: resp.Usage,
				ElapsedMS: resp.Elapsed.Milliseconds(), SeenSeq: covered, Stop: resp.Stop,
			}))
			r.looked(covered, resp.Usage.Input, w)
		})
		recorded, tailKept, extra, refresh = true, true, nil, true
		again := false
		tc.said = false
		for _, c := range resp.ToolCalls {
			out, isErr, more := "interrupted", true, false
			if ctx.Err() == nil {
				out, isErr, more = r.runTool(ctx, tc, c)
			} else {
				// Decided and not done. When the session was stopped under
				// it the turn was cut off, and says so, so that a resumed
				// session takes it up again (resume.go).
				end = stopped()
			}
			images := tc.images
			tc.images = nil
			r.sync(func() {
				r.append(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: c.ID, Name: c.Name, Output: out, IsError: isErr, Images: images}))
			})
			again = again || more
		}
		if w.final && mustSpeak {
			// The closing word belongs to a session that is ending. One
			// that started an agent in this very turn is not: nothing is
			// owed, and nothing may say "nothing is running now".
			r.sync(func() { mustSpeak = len(r.st.ActiveRelays()) == 0 })
		}
		if !again && end == "done" && call < maxCalls {
			// The turn would end here. A reply that did something else
			// than what the wake asked for (it set the title and summary,
			// and the user's answer or their message to eagent went
			// nowhere) is told so once, off the record, like a hold is.
			if back := owes(true); back != "" {
				extra, again = []llm.Message{{Role: "user", Text: back}}, true
			}
		}
		if !again || end == "interrupted" || end == "timeout" {
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
		case end == "error" || end == "interrupted" || end == "timeout":
			// What asked for the look is still owed one.
		case tc.handled:
			r.clearOwed()
		case held > 0:
			r.oweAnswer(held)
		case answerDebt:
			// It was told, in this turn's tail, that the answer has
			// reached nobody else. A hold after that stands.
			r.clearOwed()
		case answerSeq > 0 && !remindedAnswer:
			// It held on the user's answer and the turn had no call left
			// to send that back (owes): it is said once, later.
			r.oweReminder(answerSeq)
		}
		if end == "call limit" {
			r.afterCallLimit(maxCalls)
		}
		if asked && end != "error" && end != "interrupted" && end != "timeout" {
			r.threadAsked(tc.threaded)
		}
		if w.final && mustSpeak && !tc.spoke && end != "error" {
			r.fallbackFinal()
		}
	})
}

// stopReason names why a turn whose context is done ended: its own time
// ran out (the closing word's minute, the last summary's 45 seconds), or
// the session was stopped under it. Only the second is a turn cut off,
// which a resume takes up again (resume.go).
func stopReason(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "interrupted"
}

// keepsThread reports an update_thread call that sets nothing: the model's
// way of saying the title and summary still fit.
func keepsThread(c event.ToolCall) bool {
	if c.Name != tools.UpdateThread {
		return false
	}
	args, err := llm.ArgsObject(c.Args)
	if err != nil {
		return false
	}
	title, _ := args["title"].(string)
	summary, _ := args["summary"].(string)
	return strings.TrimSpace(title) == "" && strings.TrimSpace(summary) == ""
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
	// It does not retry on its own once it has ended.
	if r.phone != nil {
		b.WriteString("\n\nNothing is running now. A reply here picks the session back up.")
	} else {
		b.WriteString("\n\nNothing is running now. `eagent -c` picks the session back up.")
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
	}
	r.called(promptTokens)
	t.overflowed = false
	if w.look {
		t.lookSteps = w.steps
	}
	if t.failures > 0 {
		t.failures, t.retryAt, t.failTold = 0, time.Time{}, false
		r.ui.Log("eagent: the model answers again")
	}
}

// called counts one answered call of the conversation in the current
// file, and keeps the size of its first prompt. The rollover guard
// (needsDossier) reads both, and a resumed session rebuilds them from the
// record (turnState.init), so every call that is recorded there, alone or
// in a batch of silent ones, is counted here. Loop goroutine.
func (r *Runtime) called(promptTokens int) {
	t := &r.turns
	if promptTokens > 0 && t.subCalls == 0 {
		t.firstPrompt = promptTokens
	}
	t.subCalls++
}

// noteSilent keeps the cost of a call that left no record, to be put in
// the log in one line with the others. seen is 0 for a reply that was sent
// back for another try: it weighed nothing yet, but it was a call of the
// conversation, and when it was the first, its prompt is the first prompt
// (the next call's carries the exchange on top). Loop goroutine.
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
		if t.silent.FirstPrompt == 0 {
			t.silent.FirstPrompt = resp.Usage.Input
		}
	}
	if seen > 0 {
		t.silent.SeenSeq = seen
		r.looked(seen, resp.Usage.Input, w)
	} else {
		r.called(resp.Usage.Input)
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

// errorRetryDelays space out the calls after the model failed: the first
// failure is tried again at once (the client has already waited out what
// looked passing), then further and further apart, hourly for as long as
// it takes. A session lives for days; a provider that comes back needs no
// resume.
var errorRetryDelays = []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}

// finalReportTimeout bounds the closing word of a session with no
// terminal; finalReportGrace is how long shutdown waits for one in flight.
// Variables so tests need not wait a minute for a model that is gone.
var (
	finalReportTimeout = 60 * time.Second
	finalReportGrace   = 75 * time.Second
)

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
		t.promptTokens = max(t.promptTokens, r.rolloverTokens())
		return
	}
	t.failures++
	wait := errorRetryDelays[min(t.failures, len(errorRetryDelays))-1]
	t.retryAt, t.failSeq = time.Now().Add(wait), r.st.LastSeq()
	if wait <= 0 {
		r.ui.Log("eagent: model call failed: %s; trying again now", shortErr(err))
	} else {
		r.ui.Log("eagent: model call failed: %s; trying again in %s", shortErr(err), roundDur(wait))
	}
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
var mechanicsWords = regexp.MustCompile(`(?i)\b(orchestrator|narrator|task worker|dossier|subsession|rollover|context reset|harness|tool call|long think)s?\b`)

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
