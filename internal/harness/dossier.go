package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/tools"
)

// The dossier.
//
// The conversation grows with everything that happens. When its prompt
// reaches half the model's context window (or the rollover_tokens a
// configuration fixes) it is restarted, and the next subsession opens
// with a dossier instead of the history. The record itself is never
// shortened: every subsession file stays on disk, and session_search and
// session_read reach all of it.
//
// A dossier is two things. It says where the session stands (what the user
// wants, where the work is, what was decided, what is open, what the user
// last heard), and it is a MAP: for every topic that matters, the places in
// the record (file:line) where the source can be read. The map covers the
// whole session, across every subsession file, so that when the user comes
// back to something old, or the work turns somewhere unexpected, the model
// goes to the source instead of trusting a summary of a summary.
//
// The model that has been reading the session writes it, at the end of the
// conversation it already has (so the request reuses the cached prompt). It
// is given a table of every record file and an index, by place, of the
// whole record: every stretch before its last dossier in outline (the
// user's words, the agent's turn ends, where each earlier dossier sits)
// and the stretch since line by line. It studies the record before it
// writes, with session_search and session_read on any file; those lookups
// belong to the writing and are not recorded. The dossier is rebuilt from
// the record every time. The map it wrote last time is one of the sources,
// to be checked against the lines it cites, never copied forward as it
// stands: a summary of a summary drifts, and the record does not.
//
// The harness then checks the result: it must be a real text, and its
// citations must point at lines that exist. A dossier that fails is sent
// back once with what was wrong. After the model's text come the facts the
// harness states from its own records, exactly, each with its place: the
// table of files, every agent and how to reach it, the user's messages
// word for word, every turn the agent finished. Those hold whatever the
// model's part left out.
//
// Nothing is lost at the seam. The dossier names the last event it took in
// (covers_seq); the new conversation shows every event after it, including
// those that arrived while the dossier was being written.

var (
	dossierTimeout  = 4 * time.Minute  // one model call of the pass
	dossierMinChars = 200              // shorter than this is not a dossier
	dossierMinCites = 3                // fewer places than this is not a map
	dossierMaxCalls = 40               // lookups plus the writing
	dossierRetry    = 10 * time.Minute // after the files could not be rotated
	// The pass reads for dossierPassTime; then the model is told to write
	// with what it has, and the writing has dossierWriteTime more. That is
	// one deadline for the whole pass (every call, the wait before a call
	// is made again, the writing). The writing time is two calls long: a
	// call begun in the reading time runs its full time (it may be the
	// model writing its briefing unasked), and a writing call asked for
	// after it still has a call's time of its own. A pass that reaches the
	// deadline with a draft in hand keeps the draft; with none, the
	// harness writes the briefing. One failed call is made again after
	// dossierErrorPause.
	dossierPassTime   = 15 * time.Minute
	dossierWriteTime  = 2 * dossierTimeout
	dossierErrorPause = 5 * time.Second
)

// Bounds on what one dossier pass may read back from the record.
const (
	dossierReadChars  = 24000  // one lookup's result
	dossierReadMin    = 60000  // all of them together, at the least
	dossierReadMax    = 300000 // and at the most, when the conversation is in the prompt too
	dossierFreshMax   = 600000 // and when it is not
	dossierIndexLines = 300    // lines of the index of the stretch since the last dossier
	dossierOlderLines = 400    // lines of the outline of everything before it
)

// dossierBudget is how many characters of the record one pass may read
// back: what the model's window has left after the prompt it already
// holds and the room it needs to write (the reply ceiling, or the 16,384
// tokens a call asks for when the route sets none), up to a fixed bound.
// It is small or negative when the prompt nearly fills the window
// (rollover_tokens fixed close to it, or one turn that took the
// conversation far past the limit); such a pass is not written inside the
// conversation (startDossier). Loop goroutine only.
func (r *Runtime) dossierBudget(promptTokens int, fresh bool) int {
	input, _ := r.inputRoom()
	limit := dossierReadMax
	if fresh {
		limit, promptTokens = dossierFreshMax, 8000
	}
	free := input - promptTokens - 8000
	// About three characters of the record to a token, with a fifth held back.
	return min(limit, free*3*4/5)
}

// window is the context window of the model eagent's calls go to right now,
// and the reply ceiling of those calls: the configured model's, the one a
// runtime setting picked, or a fallback's once the route has moved to it.
// Loop goroutine only.
func (r *Runtime) window() (tokens, reply int) {
	tokens, reply = r.cfg.Model.ContextTokens, r.cfg.Model.MaxTokens
	if m := r.models[event.ActorEagent]; m != nil && m.client != nil {
		ep := m.client.Endpoint
		if cur := m.confAt(); cur != nil {
			tokens, reply = cur.ContextTokens, cur.MaxTokens
		}
		if tokens <= 0 {
			if known, ok := config.Lookup(ep.BaseURL, ep.Model); ok {
				tokens = known.Context
			}
		}
	}
	if tokens <= 0 {
		tokens = 200_000
	}
	return tokens, reply
}

// rolloverTokens is the prompt size at which the conversation restarts from
// a dossier: the number the configuration fixes, or by default half the
// window of the model in use (config.AutoRollover), so it moves with the
// model. Loop goroutine only.
func (r *Runtime) rolloverTokens() int {
	if n := r.cfg.RolloverTokens; n > 0 {
		return n
	}
	window, reply := r.window()
	_, fixed := r.inputRoom()
	if _, small := r.room(); small {
		fixed = r.provider(fixed)
	}
	return config.AutoRolloverWithin(window, reply, fixed)
}

// A small route's sizes are held against what its provider counts, which
// can be well above eagent's estimate (llm.EstimateTokens): lists of line
// numbers, file names and JSON split into many more tokens than prose. The
// ratio is learned from eagent's own calls (calibrate): the provider's
// reported prompt size over the estimate of the same request, and, where a
// provider reports no sizes, from a request the estimate said fit and the
// provider refused as too long. Until a call has taught it (the first call
// of a run, a provider that reports nothing and has refused nothing) the
// ratio is 1. It is never taken below 1, so a provider that counts less
// than the estimate leaves the sizes as the estimate gives them, nor above
// 4. Ordinary routes never use it.
const (
	scaleUnit = 1000
	scaleMax  = 4 * scaleUnit
)

// scale is the ratio learned so far on the route in use, in thousandths.
// One learned on another route (before a fallback took over, or before a
// model was picked in the thread's settings) says nothing about this one,
// which starts at 1 again. Loop goroutine only.
func (r *Runtime) scale() int {
	if r.tokenScaleOn != r.routeKey() {
		return scaleUnit
	}
	return max(r.tokenScale, scaleUnit)
}

// routeKey names the route eagent's calls go to right now. Loop goroutine
// only.
func (r *Runtime) routeKey() string {
	if m := r.models[event.ActorEagent]; m != nil && m.client != nil {
		return m.client.Endpoint.BaseURL + " " + m.client.Endpoint.Model
	}
	return ""
}

// provider is an estimate in the provider's count; estimate is the
// reverse. Loop goroutine only.
func (r *Runtime) provider(est int) int    { return est * r.scale() / scaleUnit }
func (r *Runtime) estimate(tokens int) int { return tokens * scaleUnit / r.scale() }

// calibrate learns from one of eagent's calls how its provider counts
// against the estimate (see scale). Any goroutine: what it learns is
// applied on the loop, to the route in use then (a call that moves to a
// fallback route posts this before the move).
//
// A picture that goes out with the request teaches nothing: a provider
// counts one as anything from a few hundred tokens to many thousands, and
// the estimate does not see it. One that does not go out (refused before,
// so a note stands in its place, or too large to be sent) is no reason to
// learn nothing. Reasoning replayed verbatim (Native) is counted by some
// providers and dropped by others; its estimate is the most it can be, so
// a request that carries it only teaches a ratio at least this high, and
// never lowers one learned before. Nor does a request too small to measure
// by teach anything.
func (r *Runtime) calibrate(who string, req llm.Request, resp *llm.Response, err error) {
	if who != event.ActorEagent {
		return
	}
	est := llm.EstimateTokens(req.System) + toolTokens()
	unknown := 0
	for _, m := range req.Messages {
		for _, im := range m.Images {
			if sentPicture(im) {
				return
			}
		}
		est += llm.EstimateTokens(m.Text)
		for _, c := range m.ToolCalls {
			est += llm.EstimateTokens(c.Name + string(c.Args))
		}
		for _, res := range m.Results {
			est += llm.EstimateTokens(res.Output)
		}
		if len(m.Native) > 0 {
			unknown += llm.EstimateTokens(string(m.Native))
		}
	}
	if est < 1000 {
		return
	}
	// learn sets the ratio to k, or (raise) only raises it. On the loop.
	learn := func(k int, raise bool) {
		k = min(k, scaleMax)
		if key := r.routeKey(); r.tokenScaleOn != key {
			r.tokenScale, r.tokenScaleOn = 0, key
		}
		if raise && k <= r.scale() {
			return
		}
		r.tokenScale = k
	}
	var ae *llm.APIError
	switch {
	case err == nil && resp != nil && resp.Usage.Input > 0:
		k := max(resp.Usage.Input-unknown, 0) * scaleUnit / est
		r.post(func() { learn(k, unknown > 0) })
	case errors.As(err, &ae) && ae.ContextOverflow():
		// Refused as too long: the provider counts at least the room it
		// has for input in this request. A tenth more is held back, as
		// the refusal says only that it was over.
		r.post(func() {
			input, _ := r.inputRoom()
			learn(max(input-unknown, 0)*11/10*scaleUnit/est, true)
		})
	}
}

// sentPicture reports that a picture in a request goes out as a picture:
// it is not marked refused, and the file is there and small enough to be
// inlined (llm.Image).
func sentPicture(im llm.Image) bool {
	if im.Refused {
		return false
	}
	st, err := os.Stat(im.Path)
	return err == nil && st.Size() > 0 && st.Size() <= 10<<20
}

// smallBudget is how a small route's room is shared out, in the
// provider's count: span is what the conversation may fill past the system
// prompt and the tools before it restarts (the threshold, or what the
// window takes as input if that is less), of which the dossier takes at
// most a third and what is carried over whole at most a sixth, which
// leaves at least half for the conversation to grow in. Loop goroutine
// only.
func (r *Runtime) smallBudget() (span, dossier, carry int) {
	input, fixed := r.inputRoom()
	span = max(0, min(r.rolloverTokens(), input)-r.provider(fixed))
	return span, span / 3, span / 6
}

// inputRoom is what one request on the route in use can hold as input (its
// window less the reply ceiling, or less the 16,384 tokens a call asks for
// when the route sets none), and how much of that the system prompt and
// the tool list take, which open every request. Both are counted with the
// estimate used before the provider has reported a size. Loop goroutine
// only.
func (r *Runtime) inputRoom() (input, fixed int) {
	window, reply := r.window()
	if reply <= 0 {
		reply = 16384
	}
	return window - reply, llm.EstimateTokens(r.system) + toolTokens()
}

// toolTokens estimates the tool list every request carries.
var toolTokens = sync.OnceValue(func() int {
	b, _ := json.Marshal(tools.All())
	return llm.EstimateTokens(string(b))
})

// needsDossier reports that the conversation should be restarted from a
// dossier: its prompt has grown past the threshold, or the provider refused
// it as too long. Loop goroutine only.
func (r *Runtime) needsDossier() bool {
	t := &r.turns
	if t.compacting || time.Since(t.dossierFailedAt) < dossierRetry {
		return false
	}
	if t.adopt {
		return true // a session of the three-actor design: see resume.go
	}
	if r.st.Unfinished() {
		// The last run stopped in the middle of a restart: the new file was
		// begun and its dossier never written. That restart is finished
		// before anything else, from the conversation it was replacing.
		return true
	}
	limit := r.rolloverTokens()
	if !t.overflowed && r.contextTokens() < limit {
		return false
	}
	if cur := r.st.Current(); cur != nil && (cur.Reason == "rollover" || cur.Reason == "upgrade") && (t.firstPrompt >= limit || (t.overflowed && t.subCalls == 0)) && r.restartFull(limit) {
		// The conversation was this long on its first call after the last
		// restart: the dossier and the standing prompt alone are too much.
		// Restarting again would only rebuild the same prompt. A session
		// of the earlier design picked up from a dossier ("upgrade") is in
		// the same place.
		if !t.futileLogged {
			t.futileLogged = true
			if span, _, _ := r.smallBudget(); r.smallRoute() && span <= 0 {
				input, fixed := r.inputRoom()
				if at := r.cfg.RolloverTokens; at > 0 && at < input && input > r.provider(fixed) {
					// The window has the room; the restart size set
					// by hand does not.
					r.ui.Log("rollover_tokens is %d, and this model's provider counts eagent's system prompt and tools alone as about %d tokens, so no restart can make room; raise rollover_tokens (or remove it) or shorten .agents/eagent/INSTRUCTIONS.md", at, r.provider(fixed))
				} else {
					r.ui.Log("this model's window leaves %d tokens for a request, and its provider counts eagent's system prompt and tools alone as about %d of them, so no restart can make room; pick a model with a larger window or shorten .agents/eagent/INSTRUCTIONS.md", input, r.provider(fixed))
				}
			} else {
				r.ui.Log("the conversation is too long right after a restart, so restarting again cannot shrink it; raise rollover_tokens or shorten .agents/eagent/INSTRUCTIONS.md")
			}
			// The user is told, once: it is theirs to fix, and nothing
			// else will say why eagent grows slower and dearer.
			if !r.toldFull() {
				r.recordMessage(event.MessageData{Auto: true, Important: true, Text: "My conversation is full again right after I restarted it from my notes: the notes and this project's standing instructions alone reach the limit, so another restart cannot make room. Raise `rollover_tokens` in .agents/eagent/config.json or shorten .agents/eagent/INSTRUCTIONS.md. Until then I carry on with a conversation longer than it should be."})
			}
		}
		return false
	}
	return true
}

// restartFull reports, once the first call after a restart was already too
// long, that the restart itself is what fills it. On an ordinary route the
// first call tells: what arrives in the moment after a restart is small
// beside the limit. On a small route one message can be a good part of the
// limit, so there the conversation's own opening is measured instead (the
// system prompt, the tools and the briefing): when that fits, another
// restart takes in what came after it and does make room. Loop goroutine
// only.
func (r *Runtime) restartFull(limit int) bool {
	if _, small := r.room(); !small {
		return true
	}
	if r.refusedFirst() {
		// Refused before any call of this conversation went through. When
		// it holds more than its opening (what the restart carried whole,
		// what came while the dossier was written), the next restart takes
		// that in by place and carries nothing (startDossier), so it can
		// make room. When it holds its opening alone, no restart can. Once
		// that is decided it stands until a call goes through: what is
		// added after the refusal (the notice that says so, a message)
		// cannot make the opening fit.
		return r.turns.futileLogged || len(r.st.View(&r.view)) <= 1
	}
	_, fixed := r.inputRoom()
	if v := r.st.View(&r.view); len(v) > 0 && r.st.Current().Dossier != "" {
		fixed += llm.EstimateTokens(v[0].Text)
	}
	// Room for one message beside the opening, in the provider's count:
	// without it another restart could only open on the same full prompt.
	span, _, carry := r.smallBudget()
	return span <= 0 || r.provider(fixed)+carry >= limit
}

// refusedFirst reports that the conversation was begun by a restart and
// the provider refused it as too long before any of its calls went
// through. Loop goroutine only.
func (r *Runtime) refusedFirst() bool {
	t := &r.turns
	cur := r.st.Current()
	return t.overflowed && t.subCalls == 0 && cur != nil && (cur.Reason == "rollover" || cur.Reason == "upgrade")
}

// fullNotice opens the message that tells the user the conversation cannot
// be made shorter by restarting it.
const fullNotice = "My conversation is full again right after I restarted it"

// toldFull reports that the user was already told so since the
// conversation was last restarted. The record remembers it, so a session
// that is stopped and resumed does not say (and buzz) it again every run.
func (r *Runtime) toldFull() bool {
	cur := r.st.Current()
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if cur != nil && ev.Seq < cur.StartSeq {
			break
		}
		if ev.Type == event.Message {
			var d event.MessageData
			if ev.Decode(&d) == nil && d.Auto && strings.HasPrefix(d.Text, fullNotice) {
				return true
			}
		}
	}
	return false
}

// dossierJob is what one dossier pass works from, gathered on the loop.
type dossierJob struct {
	msgs   []llm.Message  // the conversation, ending with the request
	lines  map[string]int // lines in each record file, to check citations (brought up to date before each check)
	budget int            // characters of lookups the pass may read back
	covers int64
	tokens int
	adopt  bool // the record is the three-actor design's (resume.go)
	fresh  bool // written from the files, without the conversation
}

// dossierResult is what a pass came back with.
type dossierResult struct {
	noMap     string // why the text kept is not a map, when it is not
	text      string
	ok        bool
	usage     event.Usage
	elapsed   time.Duration
	calls     int
	consulted []string // the lookups it made, for the record
	bad       []string // citations that point nowhere
}

// startDossier begins the dossier pass. The loop makes no other model call
// until it lands. Loop goroutine only.
func (r *Runtime) startDossier() {
	t := &r.turns
	t.busy, t.compacting, t.callAt, t.rereadTold = true, true, time.Now(), false
	r.flushSilent()
	job := dossierJob{covers: r.st.LastSeq(), tokens: r.contextTokens(), lines: r.recordLines(), adopt: t.adopt}
	if r.st.Unfinished() {
		// The new file's start set the prompt's size back to nothing; the
		// conversation being replaced is as long as when it was closed.
		job.tokens = max(job.tokens, r.closedTokens())
	}
	if _, due := r.dueWake(); due && t.seen < job.covers && r.unseenBytes() <= r.carryBytes() && !r.refusedFirst() {
		// Something arrived that no look has taken in yet: the message
		// or the turn end that set this restart off. The briefing does
		// not stand in for it. The new conversation shows it whole, with
		// its pictures, because it starts after the last thing seen.
		// (Unless a great deal piled up unseen, as when the model was
		// unreachable for hours: carrying all of that over would undo
		// the restart, and the facts list it with its places. Nor when
		// the conversation being replaced was itself begun by a restart
		// and refused on its first call: what that restart carried did
		// not fit, and carrying it again would only be refused again.)
		job.covers = t.seen
	}
	switch {
	case t.adopt:
		// A session written by the three-actor design. Its conversation
		// was the orchestrator's and has no rendering here, so the model
		// writes from the files, as a writer with no memory would.
		r.dossierFromFiles(&job, "")
		r.ui.Log("this session was written by an earlier eagent; writing a dossier from its record to continue from")
	case t.overflowed:
		// The provider refused the conversation as too long, so it cannot
		// be the prompt. The model works from the files instead.
		r.dossierFromFiles(&job, dossierRefused)
		r.ui.Log("the model refused the conversation as too long; restarting it from a dossier")
	case r.dossierBudget(job.tokens, false) < dossierReadChars:
		// The conversation leaves the window too little room to read even
		// one lookup on top of it. Written inside it, the pass would be
		// refused as too long at its first lookups, or write a map
		// without having read anything. (The bar is one lookup, not
		// dossierReadMin: an automatic rollover holds back about that
		// much, and the prompt that sets it off is always somewhat past
		// the threshold, so a bar of dossierReadMin would send every pass
		// of a window under ~130k tokens to the files. A pass that reads
		// more than fits is made smaller in writeDossier.)
		r.dossierFromFiles(&job, "the conversation fills too much of the model's context window to write a briefing inside it")
		r.ui.Log("the conversation reached %dk tokens, too near the model's window to write a dossier inside it; writing one from the record", job.tokens/1000)
	default:
		data := prompts.DossierData{Files: r.fileTable(""), Index: r.dossierIndex()}
		job.budget = r.dossierBudget(job.tokens, false)
		job.msgs = state.WithTail(r.st.View(&r.view), r.prompts.Render("DOSSIER.md", data))
		r.ui.Log("the conversation reached %dk tokens; restarting it from a dossier", job.tokens/1000)
	}
	r.tellRereading()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		res := r.writeDossier(job)
		r.post(func() { r.finishDossier(job, res) })
	}()
}

// rereadNotice is what the user is told, in the harness's own words, when
// what they wrote to eagent waits on a dossier pass: the pass takes
// minutes, and eagent makes no other model call until it is over.
const rereadNotice = "I'm rereading the session so far, to carry on with a clear head. I'll answer you as soon as that's done."

// tellRereading tells the user once per pass, as soon as it is so, that a
// message or an answer of theirs for eagent waits on the pass: one written
// during it, or one no look had taken in when it began. A message typed
// straight into the agent's terminal is not waiting on eagent and says
// nothing. It is recorded as eagent's words and shown in the next
// conversation, so the model knows what the user was told; it is not
// taken for an answer (saidAt and saidSeq stay as they were). Loop
// goroutine only.
func (r *Runtime) tellRereading() {
	t := &r.turns
	if !t.compacting || t.rereadTold || r.ending {
		return
	}
	// Newest first, back to the last look: a message or an answer for
	// eagent since the last time they were told (by an earlier pass that
	// ended without a look, as when the next restart follows at once).
	waiting := false
scan:
	for i := len(r.st.Events) - 1; i >= 0 && r.st.Events[i].Seq > t.seen; i-- {
		ev := r.st.Events[i]
		switch ev.Type {
		case event.UserAnswer:
			waiting = true
			break scan
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.Held != "" {
				waiting = true
				break scan
			}
		case event.Message:
			var d event.MessageData
			if ev.Actor == event.ActorEagent && ev.Decode(&d) == nil && d.Auto && d.Text == rereadNotice {
				break scan
			}
		}
	}
	if !waiting {
		return
	}
	t.rereadTold = true
	if r.phone != nil {
		// The post carries the status line; it is the pass's from the
		// first moment, not the one from before it.
		r.phone.noteState(r)
	}
	r.append(event.New(event.Message, event.ActorEagent, event.MessageData{Auto: true, Text: rereadNotice}))
	r.ui.Say(rereadNotice)
}

// dossierRefused is why a pass is written from the files after the
// provider would not take the conversation.
const dossierRefused = "the model refused the conversation as too long"

// dossierFromFiles makes a pass one that is written from the files alone,
// with why (the conversation cannot be the prompt). The model starts from
// the dossier this subsession opened with, which is the first message of
// the conversation, and has the whole window to read the record in. One
// lookup is always allowed: a writer that could read nothing could not
// write a map. Loop goroutine only.
func (r *Runtime) dossierFromFiles(job *dossierJob, reason string) {
	data := prompts.DossierData{Files: r.fileTable(""), Fresh: true, Legacy: job.adopt, Reason: reason}
	var msgs []llm.Message
	if cur := r.st.Conversation(); !job.adopt && cur != nil && cur.Dossier != "" {
		if all := r.st.View(&r.view); len(all) > 0 {
			msgs = all[:1:1]
		}
	}
	// The index is what the request has room for beside the rest, with a
	// thousand tokens to spare: on a small route its oldest lines give way
	// (session_search finds them), so the pass can be written at all.
	input, fixed := r.inputRoom()
	if _, small := r.room(); small {
		// The provider's count, brought back to the estimate's.
		input = r.estimate(input)
		data.Files = r.fileTableChained("")
	}
	used := fixed + llm.EstimateTokens(r.prompts.Render("DOSSIER.md", data)) + 1000
	for _, m := range msgs {
		used += llm.EstimateTokens(m.Text)
	}
	data.Index = r.dossierIndexWithin((input - used) * 7 / 2)
	job.fresh = true
	job.budget = max(dossierReadChars, r.dossierBudget(job.tokens, true))
	job.msgs = state.WithTail(msgs, r.prompts.Render("DOSSIER.md", data))
}

// dossierUnseenBytes is the most that may have arrived since the last look
// for all of it to be carried whole into the new conversation.
const dossierUnseenBytes = 80_000

// carryBytes is the most that may be carried whole: dossierUnseenBytes,
// or on a small route a sixth of what a request has room for beside the
// system prompt and the tools, so the new conversation still fits with
// the dossier (dossierBody) and room to go on before the next restart.
// Loop goroutine only.
func (r *Runtime) carryBytes() int {
	if _, small := r.room(); !small {
		return dossierUnseenBytes
	}
	_, _, carry := r.smallBudget()
	return r.estimate(carry) * 7 / 2
}

// smallRoom is the room below which a route is small: what a request can
// hold beside the system prompt and the tools is under this many tokens.
// Every ordinary cloud model (a window of 128k or more) has more.
const smallRoom = 64_000

// room is what a request on the route in use has room for beside the
// system prompt and the tools, in tokens, and whether that makes the route
// small (smallRoom). Loop goroutine only.
func (r *Runtime) room() (int, bool) {
	input, fixed := r.inputRoom()
	room := max(0, input-fixed)
	return room, room < smallRoom
}

// smallRoute reports that the route in use is small (room). Loop
// goroutine only.
func (r *Runtime) smallRoute() bool {
	_, small := r.room()
	return small
}

// unseenBytes sizes what the record holds after the last look, with each
// picture a message carries (and that is not refused) as pictureTokens:
// the record holds only its path, and the new conversation would carry the
// picture itself. Loop goroutine only.
func (r *Runtime) unseenBytes() int {
	n := 0
	for i := len(r.st.Events) - 1; i >= 0 && r.st.Events[i].Seq > r.turns.seen; i-- {
		ev := r.st.Events[i]
		n += len(ev.Data)
		var d struct {
			Attachments []event.Attachment `json:"attachments"`
		}
		if (ev.Type == event.UserMessage || ev.Type == event.UserAnswer) && ev.Decode(&d) == nil {
			for _, a := range d.Attachments {
				if a.IsImage() && !r.st.RefusedImages[a.Path] {
					n += pictureTokens * 7 / 2
				}
			}
		}
	}
	return n
}

// pictureTokens is about what a provider counts for one picture: Anthropic
// counts about 1,600 tokens for one at the size it scales pictures down
// to, and OpenAI's high-detail tiles come to less. Some count more; a
// carried picture that turns out not to fit is taken care of by the next
// restart (startDossier).
const pictureTokens = 1600

// citation matches a place in the record: file:line or file:line-line.
var citation = regexp.MustCompile(`(\d{10,}\.jsonl):(\d+)(?:-(\d+))?`)

// checkCitations counts the distinct places a text cites that exist, and
// lists those that do not: a file the session does not have, or a line
// past its end. A place is a file and the line reading starts at, so
// file:10, file:10-20 and file:10 again are one place; file:12 is another.
func checkCitations(text string, lines map[string]int) (n int, bad []string) {
	seen := map[string]bool{}
	for _, m := range citation.FindAllStringSubmatch(text, -1) {
		from, _ := strconv.Atoi(m[2])
		to := from
		if m[3] != "" {
			to, _ = strconv.Atoi(m[3])
		}
		if max, ok := lines[m[1]]; ok && from >= 1 && to >= from && to <= max {
			if place := m[1] + ":" + m[2]; !seen[place] {
				seen[place] = true
				n++
			}
			continue
		}
		if !seen[m[0]] {
			seen[m[0]] = true
			bad = append(bad, m[0])
		}
	}
	return n, bad
}

// mapProblem says why a text's citations do not make a map ("" when they
// do): fewer than dossierMinCites distinct places that exist, or places
// that do not exist.
func mapProblem(text string, lines map[string]int) string {
	cites, bad := checkCitations(text, lines)
	var why []string
	if cites < dossierMinCites {
		noun := "places"
		if cites == 1 {
			noun = "place"
		}
		w := fmt.Sprintf("its MAP cites %d %s in the record", cites, noun)
		if all := len(citation.FindAllString(text, -1)); all > cites+len(bad) {
			w += fmt.Sprintf(" (%d citations, but the same place cited again is still one place)", all)
		}
		why = append(why, w+"; a map names the distinct place of every topic that matters, as file:line")
	}
	if len(bad) > 0 {
		why = append(why, "these places do not exist in the record: "+strings.Join(bad[:min(len(bad), 12)], ", ")+". Cite only places you were shown, and check with session_read if unsure")
	}
	return strings.Join(why, "; and ")
}

// draftBeats reports whether draft a is the one to keep over b, when
// neither passed. One long enough to be a briefing beats one that is not;
// then the one whose map cites more places that exist; then the fuller
// one. Length alone would keep a long draft that cites nothing over the
// rewrite that put the map right but for one place.
func draftBeats(a, b string, lines map[string]int) bool {
	if al, bl := len(a) >= dossierMinChars, len(b) >= dossierMinChars; al != bl {
		return al
	}
	an, _ := checkCitations(a, lines)
	bn, _ := checkCitations(b, lines)
	if an != bn {
		return an > bn
	}
	return len(a) > len(b)
}

// noMapReason says why a text kept after it was sent back is a summary and
// not a map ("" when it is a map, if a partly wrong one).
func noMapReason(text string, lines map[string]int) string {
	if n, _ := checkCitations(text, lines); n < dossierMinCites {
		return fmt.Sprintf("it cites %d distinct places that exist in the record", n)
	}
	return ""
}

// writeDossier runs the pass: the model looks up what it needs in the
// record and then writes. A reply that is not a dossier (too short, no
// map, places that do not exist) is sent back once with what was wrong.
// The lookups and the exchange are part of the writing and are not
// recorded. It runs on its own goroutine.
func (r *Runtime) writeDossier(job dossierJob) (res dossierResult) {
	msgs := job.msgs[:len(job.msgs):len(job.msgs)]
	read, rejected, retried, last := 0, false, false, false
	// lookedAt is where the last lookups begin in msgs (0 for none), and
	// dropped says a round of them was already taken out again.
	lookedAt, dropped := 0, false
	// readEnd is when the writer is told to write; passEnd is when the pass
	// is over whatever it is doing. A call made before the writer is told
	// may run at most half the writing time past readEnd (a whole call,
	// so nothing that was going to finish is cut off), which leaves a
	// writing call asked for after it the other half.
	readEnd := time.Now().Add(dossierPassTime)
	passEnd := readEnd.Add(dossierWriteTime)
	// settle ends a pass that cannot go on: its time ran out, or the model
	// failed twice. A draft that was sent back for another try is kept as
	// it stands, as one that came back wrong twice is: a briefing with a
	// thin or partly wrong map still beats the harness's.
	settle := func() dossierResult {
		if !res.ok && len(res.text) >= dossierMinChars {
			res.ok, res.noMap = true, noMapReason(res.text, job.lines)
		}
		return res
	}
	for res.calls < dossierMaxCalls {
		bound := passEnd
		if !last && !rejected {
			bound = readEnd.Add(dossierWriteTime / 2)
		}
		deadline := time.Now().Add(dossierTimeout)
		if deadline.After(bound) {
			deadline = bound
		}
		ctx, cancel := context.WithDeadline(r.ctx, deadline)
		resp, err := r.complete(ctx, event.ActorEagent, llm.Request{System: r.system, Messages: msgs, Tools: r.toolset, CacheKey: r.sess.ID}, nil)
		cancel()
		if err != nil {
			if r.ctx.Err() != nil {
				return res
			}
			if deadline.Equal(bound) && !time.Now().Before(bound) {
				// The pass's time ran out during the call.
				if last || rejected {
					r.ui.Log("dossier: the pass ran out of time before a briefing was written")
					return settle()
				}
				last = true
				msgs = withNote(msgs, "[eagent] The time for looking things up is over, and your last call was cut off. Write the briefing now from what is above, as your whole reply, with no tool call.")
				r.ui.Log("dossier: the time for reading ran out; asking for the briefing")
				continue
			}
			r.ui.Log("dossier: model call failed: %s", shortErr(err))
			var ae *llm.APIError
			if errors.As(err, &ae) && ae.ContextOverflow() {
				// The prompt does not fit. The same request would be
				// refused again, so it is made smaller instead.
				switch {
				case lookedAt > 0 && !dropped:
					// What it just read back was too much: those results
					// go, and it writes with what it had before them.
					dropped, last = true, true
					msgs = append(msgs[:lookedAt:lookedAt], llm.Message{Role: "user", Text: "[eagent] What you looked up last did not fit in the model's context window with the rest, so those results were taken out again. Do not look anything else up. Write the briefing now from what is above, as your whole reply, with no tool call."})
					r.ui.Log("dossier: the last lookups did not fit; asking for the briefing without them")
					continue
				case !job.fresh:
					// The conversation it was to be written in is too
					// long: from the files, as after a refused turn.
					r.sync(func() { r.dossierFromFiles(&job, dossierRefused) })
					msgs = job.msgs[:len(job.msgs):len(job.msgs)]
					// A new request: it gets its own send-back. A draft
					// already in hand stays one to choose from.
					read, last, lookedAt, dropped, rejected = 0, false, 0, false, false
					r.ui.Log("dossier: the conversation is too long to write in; writing from the record")
					continue
				}
				return settle()
			}
			if retried || time.Now().Add(dossierErrorPause).After(passEnd) {
				return settle()
			}
			// One hiccup does not cost the session its map: the call is
			// made once more before the harness writes what it can.
			retried = true
			select {
			case <-time.After(dossierErrorPause):
			case <-r.ctx.Done():
				return res
			}
			continue
		}
		res.calls++
		res.usage.Input += resp.Usage.Input
		res.usage.Output += resp.Usage.Output
		res.usage.Cached += resp.Usage.Cached
		res.usage.Reasoning += resp.Usage.Reasoning
		res.elapsed += resp.Elapsed
		if len(resp.ToolCalls) > 0 {
			lookedAt = len(msgs)
		}
		msgs = append(msgs, llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL, NativeModel: resp.By()})
		if len(resp.ToolCalls) > 0 {
			// Looking things up before writing.
			results := llm.Message{Role: "tool"}
			for _, c := range resp.ToolCalls {
				out, isErr := r.dossierLookup(c, &read, job.budget, &res)
				results.Results = append(results.Results, llm.ToolResult{CallID: c.ID, Name: c.Name, Output: out, IsError: isErr})
			}
			msgs = append(msgs, results)
			if last {
				break // told to write and looked things up again instead
			}
			if res.calls == dossierMaxCalls-1 || !time.Now().Before(readEnd) {
				// The pass is bounded in calls and in time: while it runs
				// eagent says nothing else to the user.
				last = true
				msgs = append(msgs, llm.Message{Role: "user", Text: "[eagent] That was the last lookup. Write the briefing now, as your whole reply, with no tool call."})
			}
			continue
		}
		text := strings.TrimSpace(resp.Text)
		// The record went on growing while the pass ran, and its lookups
		// read it as it is now: a place written since the pass began is
		// one the briefing may cite.
		r.sync(func() { job.lines = r.recordLines() })
		_, bad := checkCitations(text, job.lines)
		why := mapProblem(text, job.lines)
		if len(text) < dossierMinChars {
			why = fmt.Sprintf("it is too short to be a briefing (%d characters)", len(text))
		}
		if why == "" || draftBeats(text, res.text, job.lines) {
			res.text, res.bad = text, bad
		}
		if why == "" {
			res.ok = true
			return res
		}
		if rejected {
			// Twice wrong. A text with a thin or partly wrong map still
			// beats none: it is kept when it reads as a briefing, with the
			// wrong places named, and one with no map is labelled as that.
			res.ok = len(res.text) >= dossierMinChars
			res.noMap = noMapReason(res.text, job.lines)
			return res
		}
		rejected = true
		r.ui.Log("dossier: sent back once: %s", why)
		msgs = append(msgs, llm.Message{Role: "user", Text: "[eagent] That cannot be used: " + why + ". Write the whole briefing again, all six parts, as plain text."})
	}
	res.ok = len(res.text) >= dossierMinChars
	if res.ok {
		res.noMap = noMapReason(res.text, job.lines)
	}
	return res
}

// withNote adds a note from eagent to the end of a pass's messages: as a
// message of its own after tool results, or added to the request or
// send-back it follows, so no two user messages are in a row. msgs is
// not changed.
func withNote(msgs []llm.Message, note string) []llm.Message {
	n := len(msgs)
	if n > 0 && msgs[n-1].Role == "user" {
		m := msgs[n-1]
		m.Text = strings.TrimRight(m.Text, "\n") + "\n\n" + note
		return append(msgs[:n-1:n-1], m)
	}
	return append(msgs[:n:n], llm.Message{Role: "user", Text: note})
}

// dossierLookup runs one tool call of a dossier pass. Only the two that
// read the record run.
func (r *Runtime) dossierLookup(c event.ToolCall, read *int, budget int, res *dossierResult) (string, bool) {
	if c.Name != tools.SessionSearch && c.Name != tools.SessionRead {
		return "not run: while the briefing is being written only session_search and session_read run. Do this after the restart if it is still needed, and say so under OPEN.", true
	}
	if *read >= budget {
		return "not run: you have read as much of the record as one briefing may. Write the briefing now with what you have.", true
	}
	args, err := llm.ArgsObject(c.Args)
	if err != nil {
		return "Tool call rejected: " + err.Error(), true
	}
	str, num := argStr(args), argNum(args)
	var out string
	if c.Name == tools.SessionSearch {
		res.consulted = append(res.consulted, "search "+clipRunes(str("query"), 80))
		out, err = r.archive.Search(str("query"), str("file"), num("max_results", 40))
	} else {
		raw, _ := args["raw"].(bool)
		offset := num("offset", -1)
		place := fmt.Sprintf("read %s:%d-%d", str("file"), num("from", 1), num("to", 0))
		if offset >= 0 {
			place = fmt.Sprintf("read %s:%d from offset %d", str("file"), num("from", 1), offset)
		}
		res.consulted = append(res.consulted, place)
		out, err = r.archive.Read(str("file"), num("from", 1), num("to", 0), raw, offset, dossierReadChars)
	}
	if err != nil {
		return err.Error(), true
	}
	if len(out) > dossierReadChars+400 {
		out = clipRunes(out, dossierReadChars) + "\n[... cut here; narrow the search or read fewer lines ...]"
	}
	*read += len(out)
	return out, false
}

// finishDossier closes the subsession, opens the next, and records the
// dossier that begins it. Loop goroutine only.
func (r *Runtime) finishDossier(job dossierJob, res dossierResult) {
	t := &r.turns
	t.busy, t.compacting, t.callAt = false, false, time.Time{}
	if r.ending {
		return
	}
	by, text := "eagent", res.text
	switch {
	case !res.ok && job.adopt:
		by, text = "harness", r.legacyBriefing(res.text)
	case !res.ok:
		// The session must not be left without its memory because a model
		// call failed: the harness writes what it can. The map below is
		// still whole, since it comes from the record.
		by, text = "harness", r.fallbackBriefing(res.text)
	case res.noMap != "":
		// Never passed off as a map: the next conversation is told what
		// it is holding, at the top, and the notice above it says the same.
		text = "(eagent checked this briefing and it is not a map: " + res.noMap + ", after being sent back once. Read it as a summary. The places are in the facts at the end, which list every message from the user and every turn the agent finished with its file:line, and session_search finds the rest.)\n\n" + text
		if len(res.bad) > 0 {
			text += "\n\n(These places it cites do not exist in the record, so disregard them: " + strings.Join(res.bad, ", ") + ")"
		}
	case len(res.bad) > 0:
		text += "\n\n(eagent checked the places cited above. These do not exist in the record, so disregard them: " + strings.Join(res.bad, ", ") + ")"
	}
	endReason, startReason := "context full", "rollover"
	if job.adopt {
		endReason, startReason = "resumed by an eagent with one loop", "upgrade"
	}
	next, err := r.openSubsession(endReason, startReason, job.tokens)
	if err != nil {
		r.ui.Log("dossier: %v; the conversation continues as it is for now (the briefing that was written, %d calls, is not kept)", err, res.calls)
		t.dossierFailedAt = time.Now()
		return
	}
	// The facts are written now that the next file is open, so the table
	// of files shows the closed one whole and says where this dossier sits.
	body := r.dossierBody(text, job, res, by, next)
	d := event.DossierData{Text: body, CoversSeq: job.covers, CoversNone: job.covers == 0, By: by, NoMap: by == "eagent" && res.noMap != "", ElapsedMS: res.elapsed.Milliseconds(), Calls: res.calls, Consulted: res.consulted}
	if res.usage != (event.Usage{}) {
		d.Usage = &res.usage
	}
	r.append(event.New(event.Dossier, event.ActorHarness, d))
	t.promptTokens, t.firstPrompt, t.subCalls, t.overflowed, t.adopt = 0, 0, 0, false, false
	// A new conversation: if this one turns out too long from its first
	// call as well, the user is told again.
	t.futileLogged = false
	cites, _ := checkCitations(res.text, r.recordLines())
	r.ui.Log("subsession %d begins with a dossier written by %s (%d characters, %d places cited, %d lookups)", len(r.st.Subsessions), by, len(body), cites, len(res.consulted))
}

// dossierBody is the dossier's text: the briefing, then the facts. It
// takes at most half of what a request on the route in use has room for
// beside the system prompt and the tools (a third on a small route), so
// the conversation it opens fits with room to go on. Every ordinary model
// has room for the widest facts and keeps them; on a small route the facts
// are written within the first of factLevels that fits (and a briefing the
// harness wrote is cut down with them). Loop goroutine only.
func (r *Runtime) dossierBody(text string, job dossierJob, res dossierResult, by, next string) string {
	room, small := r.room()
	limit := room / 2
	if small {
		_, d, _ := r.smallBudget()
		limit = r.estimate(d)
	}
	body := strings.TrimSpace(text) + "\n\n" + r.dossierFacts(job.covers, next)
	if llm.EstimateTokens(body) <= limit {
		return body
	}
	level := 0
	for i, lim := range factLevels[1:] {
		level = i + 1
		if by == "harness" && !job.adopt {
			switch {
			case i < 2:
				text = r.fallbackBriefingWith(res.text, 3, 300, 1000)
			default:
				text = r.fallbackBriefingWith(res.text, 1, 200, 300)
			}
		}
		body = strings.TrimSpace(text) + "\n\n" + r.dossierFactsWith(job.covers, next, lim)
		if llm.EstimateTokens(body) <= limit {
			break
		}
	}
	n := llm.EstimateTokens(body)
	switch file, line, _, linked := r.chainLink(); {
	case level == len(factLevels)-1 && n > limit:
		r.ui.Log("dossier: written at its narrowest and still %d tokens (by eagent's estimate) of the %d it may take; the briefing itself is most of it", n, limit)
	case level == len(factLevels)-1:
		r.ui.Log("dossier: written at its narrowest (%d tokens of %d): what came before is left to the briefing at %s:%d, and the rest is given by place", n, limit, file, line)
	case factLevels[level].chain && linked:
		r.ui.Log("dossier: the model's window leaves the dossier about %d tokens; it leaves the record before %s:%d to the briefing there and gives older messages by place (%d tokens)", limit, file, line, n)
	default:
		r.ui.Log("dossier: the model's window leaves the dossier about %d tokens; its facts give the older messages and turn ends by place (%d tokens)", limit, n)
	}
	return body
}

// openSubsession ends the current subsession file and starts the next.
// Loop goroutine (or before the loop starts).
func (r *Runtime) openSubsession(endReason, startReason string, tokens int) (string, error) {
	// The next file is made before the record says this one has ended: a
	// session that cannot make it (a full disk, a directory it may no
	// longer write) keeps its conversation and its record as they are.
	next, err := r.sess.CreateSubsession(time.Now())
	if err != nil {
		return "", err
	}
	r.append(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: endReason, NextFile: next, InputTokens: tokens}))
	if _, err := r.sess.OpenSubsession(next); err != nil {
		return "", err
	}
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: len(r.st.Subsessions), Reason: startReason}))
	r.viewed = nil // the new conversation holds none of the pictures looked at before
	return next, nil
}

// closedTokens is the prompt size the last subsession.end recorded: how
// long the conversation was when its file was closed. Loop goroutine only.
func (r *Runtime) closedTokens() int {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		if ev := r.st.Events[i]; ev.Type == event.SubsessionEnd {
			var d event.SubsessionEndData
			_ = ev.Decode(&d)
			return d.InputTokens
		}
	}
	return 0
}

// finishOpening completes a restart the last run stopped between closing
// its file and starting the next: the record says the subsession ended and
// no other began. The file it named is begun now, so the session continues
// in it, and the restart is then finished like one stopped before its
// dossier (needsDossier). Before anything else is written on resume.
func (r *Runtime) finishOpening() {
	cur := r.st.Current()
	if cur == nil || cur.EndSeq == 0 {
		return
	}
	// The end is the last thing the record holds: the stop came right after
	// it. (Found by number, not by place: the files are read in the order
	// their names sort.)
	var last *event.Event
	for i := range r.st.Events {
		if ev := &r.st.Events[i]; last == nil || ev.Seq > last.Seq {
			last = ev
		}
	}
	var end event.SubsessionEndData
	if last == nil || last.Type != event.SubsessionEnd || last.Decode(&end) != nil {
		return
	}
	reason := "rollover"
	if end.Reason == "resumed by an eagent with one loop" {
		reason = "upgrade"
	}
	next := end.NextFile
	if next != "" && next != r.sess.Current() {
		if _, err := r.sess.OpenSubsession(next); err != nil {
			next = ""
		}
	}
	if next == "" {
		name, err := r.sess.CreateSubsession(time.Now())
		if err == nil {
			_, err = r.sess.OpenSubsession(name)
		}
		if err != nil {
			// The session goes on in the file it has. Its record says the
			// subsession ended; the conversation is the same either way.
			r.ui.Log("could not begin the file the last restart was opening: %v", err)
			return
		}
		next = name
	}
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: len(r.st.Subsessions), Reason: reason}))
	r.ui.Log("the last run stopped while restarting the conversation; finishing that restart")
}

// recordFile is one subsession file of the record, as the log shows it.
type recordFile struct {
	name        string
	lines       int
	first, last time.Time
	dossierLine int    // the line of the dossier it opens with; 0 for none
	dossierBy   string // who wrote that dossier
	legacy      bool   // written by the three-actor design
}

// recordFiles describes every subsession file, oldest first, from the
// events in memory (each knows its file and line). Loop goroutine only.
func (r *Runtime) recordFiles() []*recordFile {
	var out []*recordFile
	byName := map[string]*recordFile{}
	for _, ev := range r.st.Events {
		name := ev.Source.File
		if name == "" {
			continue
		}
		f := byName[name]
		if f == nil {
			f = &recordFile{name: name, first: ev.Time}
			byName[name] = f
			out = append(out, f)
		}
		f.lines, f.last = max(f.lines, ev.Source.Line), ev.Time
		switch {
		case ev.Type == event.Dossier && f.dossierLine == 0:
			var d event.DossierData
			_ = ev.Decode(&d)
			f.dossierLine, f.dossierBy = ev.Source.Line, firstNonEmpty(d.By, "a helper of the earlier eagent")
		case event.IsLegacy(ev):
			f.legacy = true
		}
	}
	return out
}

// recordLines is how many lines each record file has, to check citations.
func (r *Runtime) recordLines() map[string]int {
	out := map[string]int{}
	for _, f := range r.recordFiles() {
		out[f.name] = f.lines
	}
	return out
}

// fileTable lists the record's files for a dossier: the top of the map.
// opens names the file the dossier being written opens ("" while it is
// still being asked for, when the last file is the one being written).
// Loop goroutine only.
func (r *Runtime) fileTable(opens string) string {
	var b strings.Builder
	files := r.recordFiles()
	for i, f := range files {
		if f.name == opens {
			fmt.Fprintf(&b, "- %s: opens with this briefing (%s:2); what happens from here on is recorded in it\n", f.name, f.name)
			continue
		}
		fmt.Fprintf(&b, "- %s: lines 1-%d, %s to %s", f.name, f.lines, r.when(f.first), r.when(f.last))
		if f.dossierLine > 0 {
			fmt.Fprintf(&b, "; opens with the briefing written then (%s:%d, by %s)", f.name, f.dossierLine, f.dossierBy)
		}
		if f.legacy {
			b.WriteString("; from the earlier eagent that did the work itself")
		}
		if opens == "" && i == len(files)-1 {
			b.WriteString("; the file being written now")
		}
		b.WriteString("\n")
	}
	if len(files) == 0 {
		return "(no files yet)"
	}
	return strings.TrimRight(b.String(), "\n")
}

// dossierIndex is an index of the whole record by place (file:line), for
// the model to build the map from. The stretch since the last dossier is
// listed line by line: the conversation shows those events without their
// places. Everything before it is listed in outline (the user's words, the
// agent's turn ends, where each earlier dossier sits), so the map can be
// rebuilt from the record instead of from the map before it. Loop
// goroutine only.
func (r *Runtime) dossierIndex() string {
	return r.dossierIndexOf(dossierOlderLines, dossierIndexLines)
}

// dossierIndexWithin is dossierIndex in at most chars characters, where it
// can be: the outline of the older stretches gives way first, then the
// oldest lines of the stretch since. Loop goroutine only.
func (r *Runtime) dossierIndexWithin(chars int) string {
	older, recent := dossierOlderLines, dossierIndexLines
	idx := r.dossierIndexOf(older, recent)
	for len(idx) > chars && (older > 0 || recent > 10) {
		if older > 0 {
			older /= 2
		} else {
			recent /= 2
		}
		idx = r.dossierIndexOf(older, recent)
	}
	return idx
}

// dossierIndexOf is dossierIndex with at most olderLines lines of outline
// and recentLines of the stretch since the last dossier.
func (r *Runtime) dossierIndexOf(olderLines, recentLines int) string {
	var from int64
	if cur := r.st.Conversation(); cur != nil {
		from = cur.StartSeq
		if cur.CoversSeq > 0 || cur.CoversNone {
			from = cur.CoversSeq + 1
		}
	}
	var at int64 // the event being listed: a name may have had another agent then
	who := func(name string) string {
		if rel := r.st.RelayAt(name, at); rel != nil {
			return rel.Who()
		}
		return firstNonEmpty(name, "the agent")
	}
	type line struct {
		text string
		user bool // the user's own words: the last thing an outline drops
	}
	var older, recent []line
	for _, ev := range r.st.Events {
		if ev.Source.File == "" {
			continue
		}
		at = ev.Seq
		var what string
		outline, user := false, false // outline: listed for the older stretches too
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			what, outline, user = "the user wrote: "+d.Text, true, true
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			what, outline, user = "the user answered: "+d.Text, true, true
		case event.UserDismiss:
			what = "the user dismissed your question without answering"
		case event.Message:
			var d event.MessageData
			_ = ev.Decode(&d)
			what = "you told the user: " + d.Text
			if event.IsLegacy(ev) {
				what = "the user was told: " + d.Text
			}
		case event.Question:
			var d event.QuestionData
			_ = ev.Decode(&d)
			what, outline = "the user was asked: "+d.Text, true
		case event.Dossier:
			var d event.DossierData
			_ = ev.Decode(&d)
			what, outline = "a briefing was written here (by "+firstNonEmpty(d.By, "a helper of the earlier eagent")+"), with its own MAP", true
		case event.RelayStart:
			var d event.RelayStartData
			_ = ev.Decode(&d)
			what, outline = "started "+firstNonEmpty(d.Label, d.Agent)+" in "+d.Dir, true
		case event.RelayEnd:
			var d event.RelayEndData
			_ = ev.Decode(&d)
			what, outline = "stopped "+who(d.Name)+" "+d.Reason, true
		case event.RelaySend:
			var d event.RelaySendData
			_ = ev.Decode(&d)
			if d.From == "user" {
				continue // the user's own message is the line before
			}
			what = "you sent " + who(d.Name) + ": " + d.Text
		case event.RelayTurn:
			var d event.RelayTurnData
			_ = ev.Decode(&d)
			what, outline = who(d.Name)+" finished a turn: "+d.Text, true
		case event.RelayState:
			var d event.RelayStateData
			_ = ev.Decode(&d)
			if d.State != "blocked" && d.State != "exited" {
				continue
			}
			what = who(d.Name) + " " + d.State + ": " + d.Detail
		case event.RelayAutopilot:
			var d event.RelayAutopilotData
			_ = ev.Decode(&d)
			what, outline = "autopilot for "+who(d.Name)+" on: "+d.Instruction, true
			if d.Off {
				what = "autopilot for " + who(d.Name) + " off"
			}
		case event.ScheduleCreate:
			var d event.ScheduleCreateData
			_ = ev.Decode(&d)
			what = "timer " + d.ID + " set (" + d.Spec + "): " + d.Note
		case event.Error:
			var d event.ErrorData
			_ = ev.Decode(&d)
			what = "error (" + d.Where + "): " + d.Text
		case event.LegacyTaskEnd:
			var d struct{ ID, Status, Summary string }
			_ = ev.Decode(&d)
			what, outline = "a helper's task "+d.ID+" ended ("+d.Status+"): "+d.Summary, true
		case event.LegacyYield:
			var d event.LegacyYieldData
			_ = ev.Decode(&d)
			what, outline = "the earlier eagent stopped to wait: "+d.Reason, true
			if d.Done {
				what = "the earlier eagent called the work done: " + d.Reason
			}
		case event.LegacyNote:
			var d struct{ Text string }
			_ = ev.Decode(&d)
			what = "the earlier eagent noted: " + d.Text
		default:
			continue
		}
		l := line{fmt.Sprintf("- %s %s %s", ev.Source, ev.Time.In(r.st.Location()).Format("Jan 2 15:04"), clipRunes(oneLine(what, 150), 150)), user}
		switch {
		case ev.Seq >= from:
			recent = append(recent, l)
		case outline:
			older = append(older, l)
		}
	}
	var b strings.Builder
	if len(older) > 0 {
		// Too long an outline loses the agent's turn ends first, oldest
		// first, and the user's words last.
		dropped := 0
		for drop := len(older) - olderLines; drop > 0; {
			cut := -1
			for i, l := range older {
				if !l.user {
					cut = i
					break
				}
			}
			if cut < 0 {
				cut = 0
			}
			older = append(older[:cut], older[cut+1:]...)
			dropped++
			drop--
		}
		b.WriteString("Before your last briefing, in outline (the user's words, the agent's turn ends, and where each earlier briefing sits):\n")
		if dropped > 0 {
			fmt.Fprintf(&b, "(%d of the oldest lines are not listed; session_search finds them)\n", dropped)
		}
		for _, l := range older {
			b.WriteString(l.text + "\n")
		}
		b.WriteString("\nSince your last briefing, line by line:\n")
	}
	if len(recent) == 0 {
		b.WriteString("(nothing yet)\n")
	}
	if skipped := len(recent) - recentLines; skipped > 0 {
		fmt.Fprintf(&b, "(%d earlier lines are not listed; session_search finds them)\n", skipped)
		recent = recent[skipped:]
	}
	for _, l := range recent {
		b.WriteString(l.text + "\n")
	}
	b.WriteString("What the agent said in the middle of a turn sits between these lines; session_read shows it.")
	return b.String()
}

// fallbackBriefing stands in for the model's part of a dossier when the
// model could not write it: what the user was last told and the agent's
// last report, in their own words. partial is whatever the model did write.
func (r *Runtime) fallbackBriefing(partial string) string {
	return r.fallbackBriefingWith(partial, 5, 600, 3000)
}

// fallbackBriefingWith is fallbackBriefing with the last told messages of
// up to chars each, and each agent's last report up to report characters.
func (r *Runtime) fallbackBriefingWith(partial string, told, chars, report int) string {
	var b strings.Builder
	b.WriteString("(Your model could not write this briefing, so eagent assembled it from its records. It has no judgement in it and no MAP by topic: what the user wants and where the work stands are in their messages and the agent's reports below, each with its place in the record, and the table of files shows where every earlier briefing sits. Read those places with session_read before you rely on anything here.)\n")
	var heard []event.Event
	for _, ev := range r.st.Events {
		if ev.Type == event.Message {
			heard = append(heard, ev)
		}
	}
	if len(heard) > 0 {
		b.WriteString("\nWHAT THE USER LAST HEARD, oldest first:\n")
		for _, ev := range heard[max(0, len(heard)-told):] {
			var d event.MessageData
			_ = ev.Decode(&d)
			fmt.Fprintf(&b, "- [%s] %s\n", r.when(ev.Time), clipRunes(oneLine(d.Text, chars), chars))
		}
	}
	for _, name := range r.st.RelayOrder {
		if rel := r.st.Relays[name]; rel != nil && rel.LastTurn != "" {
			fmt.Fprintf(&b, "\n%s'S LAST REPORT (%s):\n%s\n", strings.ToUpper(rel.Who()), r.when(rel.LastTurnAt), clipRunes(rel.LastTurn, report))
		}
	}
	if strings.TrimSpace(partial) != "" {
		b.WriteString("\nWHAT THE MODEL DID WRITE:\n" + strings.TrimSpace(partial) + "\n")
	}
	return b.String()
}

// when renders a moment with its date, in the session's zone.
func (r *Runtime) when(t time.Time) string {
	return t.In(r.st.Location()).Format("Jan 2 15:04")
}

// Bounds on the lists a dossier carries.
const (
	dossierUserHead  = 3    // the first messages: what the session is for
	dossierUserTail  = 25   // and the latest
	dossierUserChars = 1500 // of each
	dossierTurns     = 20   // the latest turn ends
	// The messages between the first and the latest: one line each, the
	// newest dossierUserMidLines of them; older ones by place alone.
	dossierUserMidChars = 240
	dossierUserMidLines = 150
)

// dossierFacts is the part of a dossier the harness writes from the record,
// so it holds no matter what the model's part kept: the agents and how to
// reach them, the user's own words, and where each of the agent's turns
// ended. It covers events up to covers; later ones are in the conversation
// itself. Loop goroutine only.
func (r *Runtime) dossierFacts(covers int64, opens string) string {
	return r.dossierFactsWith(covers, opens, factLevels[0])
}

// factLimits bound the lists of a dossier's facts: the user's first
// messages and latest ones word for word, each up to chars; the ones
// between in a line each, the newest midLines of them, the rest by place;
// the latest turns turn ends, and with turnPlaces the earlier ones by
// place (else they are left to session_search, as they always were).
//
// With chain, what came before the briefing the conversation opened with
// is left to that briefing (chainLink): the files before it as one range,
// and only the agents, messages and turn ends since it. With ranges, runs
// of consecutive places are written as one (lines 9-14).
type factLimits struct {
	head, tail, chars, midLines, midChars, turns int
	turnPlaces, chain, ranges                    bool
}

// factLevels are the bounds a dossier's facts are written within, widest
// first. The first is the one every dossier had before the facts were
// sized to the window, and the one any dossier that fits keeps. A route
// too small for it (dossierBody) takes the next that fits: the older and
// middle messages give way first, to their places, then the latest are
// fewer and shorter, and the agent's turn ends the same. Nothing given up
// is left without its file and line.
//
// From the third level on, the record before the conversation's own
// briefing is left to that briefing, which maps it (chainLink): each
// briefing covers everything before it, by its own lists or by the one it
// points at in turn, back to the start. So the narrowest level does not
// grow with the record: it holds one conversation's worth of places and
// the pointer.
var factLevels = []factLimits{
	{dossierUserHead, dossierUserTail, dossierUserChars, dossierUserMidLines, dossierUserMidChars, dossierTurns, false, false, false},
	{dossierUserHead, dossierUserTail, dossierUserChars, 40, dossierUserMidChars, dossierTurns, true, false, true},
	{dossierUserHead, dossierUserTail, dossierUserChars, 40, dossierUserMidChars, dossierTurns, true, true, true},
	{dossierUserHead, 10, 600, 0, dossierUserMidChars, 10, true, true, true},
	{1, 3, 300, 0, dossierUserMidChars, 3, true, true, true},
	{1, 1, 200, 0, dossierUserMidChars, 1, true, true, true},
	{0, 0, 0, 0, 0, 0, true, true, true},
}

// factsHeader opens the facts of every dossier the one loop writes.
const factsHeader = "FROM EAGENT'S OWN RECORDS"

// chainLink is the briefing the conversation opened with, as a place,
// when it can stand for the record before it: one the one loop wrote
// (by the model or by the harness, as a rollover or an upgrade), with its
// facts, which say what it covers. Such a briefing lists every file, agent
// and message from the user up to the last event it took in, or points at
// the briefing before it for those, and so on back to the start. A
// briefing the earlier design wrote carries no facts and is not one.
// Called while the next dossier is written (the new file is open and not
// yet briefed, so the conversation is still the old one) or asked for.
// Loop goroutine only.
func (r *Runtime) chainLink() (file string, line int, covers int64, ok bool) {
	conv := r.st.Conversation()
	if conv == nil || !conv.Briefed {
		return "", 0, 0, false
	}
	for _, ev := range r.st.Events {
		if ev.Seq < conv.StartSeq || ev.Type != event.Dossier {
			continue
		}
		var d event.DossierData
		if ev.Source.File == "" || ev.Decode(&d) != nil || d.TaskID != "" || !(d.CoversSeq > 0 || d.CoversNone) || !strings.Contains(d.Text, factsHeader) {
			return "", 0, 0, false
		}
		return ev.Source.File, ev.Source.Line, d.CoversSeq, true
	}
	return "", 0, 0, false
}

// fileTableChained is fileTable with the files before the one the
// conversation's briefing sits in given as one range, left to that
// briefing, which lists them (chainLink). Loop goroutine only.
func (r *Runtime) fileTableChained(opens string) string {
	full := r.fileTable(opens)
	file, line, _, ok := r.chainLink()
	if !ok {
		return full
	}
	files := r.recordFiles()
	cut := 0
	for cut < len(files) && files[cut].name != file {
		cut++
	}
	rows := strings.Split(full, "\n")
	if cut == 0 || cut == len(files) || len(rows) != len(files) {
		return full
	}
	head := fmt.Sprintf("- %s to %s, %d files: the record before the briefing at %s:%d, which lists each of them (or names the briefing before it that does); session_read opens any of their lines", files[0].name, files[cut-1].name, cut, file, line)
	return head + "\n" + strings.Join(rows[cut:], "\n")
}

// recordPlace is a line of a record file.
type recordPlace struct {
	file string
	n    int
}

// byPlace lists places by file alone: "- WHAT, F lines 3, 9", or with
// ranges "- WHAT, F lines 3-5, 9" for runs of consecutive lines.
func byPlace(b *strings.Builder, what string, places []recordPlace, ranges bool) {
	var file string
	for i := 0; i < len(places); i++ {
		p := places[i]
		end := i
		for ranges && end+1 < len(places) && places[end+1].file == p.file && places[end+1].n == places[end].n+1 {
			end++
		}
		at := fmt.Sprint(p.n)
		if end > i {
			at = fmt.Sprintf("%d-%d", p.n, places[end].n)
		}
		i = end
		if p.file != file {
			if file != "" {
				b.WriteString("\n")
			}
			file = p.file
			fmt.Fprintf(b, "- %s, %s lines %s", what, file, at)
			continue
		}
		fmt.Fprintf(b, ", %s", at)
	}
	if len(places) > 0 {
		b.WriteString("\n")
	}
}

// dossierFactsWith is dossierFacts within lim. Loop goroutine only.
func (r *Runtime) dossierFactsWith(covers int64, opens string, lim factLimits) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\n"+factsHeader+" (exact, as of %s)\n", r.when(time.Now()))
	var since int64 // with chain: what came before is left to the briefing named here
	if lim.chain {
		if file, line, covers, ok := r.chainLink(); ok {
			since = covers
			fmt.Fprintf(&b, "\nEARLIER: the record before the briefing at %s:%d is mapped by it. It lists every file, agent and message from the user up to then, or names the briefing before it that does, and so on back to the start: read it with session_read to go further back. session_search finds the rest of what those files hold, the agents' earlier turn ends among it. Below is what came after it.\n", file, line)
		}
	}

	b.WriteString("\nTHE RECORD'S FILES, oldest first (session_read opens any file:line; session_search searches them all)\n")
	if lim.chain {
		b.WriteString(r.fileTableChained(opens) + "\n")
	} else {
		b.WriteString(r.fileTable(opens) + "\n")
	}

	b.WriteString("\nAGENTS\n")
	if len(r.st.RelayOrder) == 0 {
		b.WriteString("(none has been started)\n")
	}
	earlier := 0
	for _, name := range r.st.RelayOrder {
		rel := r.st.Relays[name]
		if since > 0 && !rel.Active() && rel.StartSeq <= since {
			earlier++ // stopped, and in the briefing named above
			continue
		}
		fmt.Fprintf(&b, "- %s: %s (profile %s) in %s; terminal %s %s", rel.Name, firstNonEmpty(rel.Label, rel.Who()), rel.Profile, rel.Dir, rel.Host, rel.Target)
		if rel.Model != "" || rel.Effort != "" {
			fmt.Fprintf(&b, "; model %s", firstNonEmpty(rel.Model, "its own default"))
			if rel.Effort != "" {
				fmt.Fprintf(&b, ", effort %s", rel.Effort)
			}
		}
		if rel.InnerID != "" {
			fmt.Fprintf(&b, "; its own session %s", rel.InnerID)
		}
		if rel.Transcript != "" {
			fmt.Fprintf(&b, "; its transcript %s", rel.Transcript)
		}
		state := rel.State
		if !rel.Active() {
			state = "stopped"
		}
		fmt.Fprintf(&b, "; %s; %d turns finished, %d messages sent to it", state, rel.Turns, rel.Sends)
		if n := len(rel.Undelivered); n > 0 {
			fmt.Fprintf(&b, ", %d not confirmed", n)
		}
		b.WriteString(".\n")
		if ap := rel.Autopilot; ap != nil {
			decide := "you answer its questions yourself"
			if !ap.Decide {
				decide = "its questions go to the user"
			}
			fmt.Fprintf(&b, "  Autopilot is on %s (%s). The standing order: %s\n", autopilotUntil(*ap), decide, clipRunes(ap.Instruction, 600))
		}
		if rel.Limit != nil {
			fmt.Fprintf(&b, "  Usage: %s\n", limitLine(rel.Who(), *rel.Limit))
		}
	}
	if earlier > 0 {
		fmt.Fprintf(&b, "(%d more, stopped, are in the briefing named above)\n", earlier)
	}

	type line struct {
		head, text, file string
		n                int
	}
	var user, turns []line
	for _, ev := range r.st.Events {
		if ev.Seq > covers {
			break
		}
		if ev.Seq <= since {
			continue
		}
		at := r.when(ev.Time)
		if src := ev.Source.String(); src != "" {
			at += ", " + src
		}
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) != nil || strings.TrimSpace(d.Text) == "" && len(d.Attachments) == 0 {
				continue
			}
			text := d.Text
			for _, a := range d.Attachments {
				text += fmt.Sprintf(" [attached %s: %s]", a.Name, a.Path)
			}
			if d.Held != "" {
				text += " (kept for you: " + d.Held + ")"
			}
			user = append(user, line{at, text, ev.Source.File, ev.Source.Line})
		case event.UserAnswer:
			var d event.UserAnswerData
			if ev.Decode(&d) != nil {
				continue
			}
			q := clipRunes(oneLine(r.st.QuestionText(d.QuestionID), 200), 200)
			user = append(user, line{at, fmt.Sprintf("(answering %q) %s", q, d.Text), ev.Source.File, ev.Source.Line})
		case event.RelayTurn:
			var d event.RelayTurnData
			if ev.Decode(&d) != nil {
				continue
			}
			who := d.Name
			if rel := r.st.RelayAt(d.Name, ev.Seq); rel != nil {
				who = rel.Who()
			}
			head := who
			if d.DurationMS > 0 {
				head += ", " + roundDur(time.Duration(d.DurationMS)*time.Millisecond)
			}
			if d.Tools > 0 {
				head += fmt.Sprintf(", %d tool calls", d.Tools)
			}
			if d.Interrupted {
				head += ", interrupted"
			}
			report := strings.TrimSpace(d.Text)
			if report == "" {
				report = "(no message)"
			}
			turns = append(turns, line{head: at, text: head + ": " + clipRunes(oneLine(report, 200), 200), file: ev.Source.File, n: ev.Source.Line})
		}
	}

	// Every message the user sent is here, so that no instruction given
	// in the middle of a long session depends on the model having kept it:
	// the first few and the latest word for word, the ones between in a
	// line each. In a session with more of those than the lines can hold,
	// the oldest of them are given by place alone.
	if len(user) > lim.head+lim.tail {
		fmt.Fprintf(&b, "\nTHE USER'S MESSAGES, oldest first (%d in all: the first %d and the latest %d word for word, the ones between in a line each)\n", len(user), lim.head, lim.tail)
	} else {
		fmt.Fprintf(&b, "\nTHE USER'S MESSAGES, word for word, oldest first (%d in all)\n", len(user))
	}
	if len(user) == 0 {
		b.WriteString("(none yet)\n")
	}
	whole := func(m line) {
		text := strings.TrimSpace(m.text)
		if len(text) > lim.chars {
			text = clipRunes(text, lim.chars) + " (cut here; the whole message is at the place cited)"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", m.head, strings.ReplaceAll(text, "\n", "\n  "))
	}
	head, tail := user, []line(nil)
	var mid []line
	if len(user) > lim.head+lim.tail {
		head, mid, tail = user[:lim.head], user[lim.head:len(user)-lim.tail], user[len(user)-lim.tail:]
	}
	for _, m := range head {
		whole(m)
	}
	if len(mid) > 0 {
		places := max(0, len(mid)-lim.midLines)
		fmt.Fprintf(&b, "(the %d between, in a line each; session_read at the place gives the whole message)\n", len(mid))
		if places > 0 {
			// By place alone, grouped by file.
			var at []recordPlace
			for _, m := range mid[:places] {
				at = append(at, recordPlace{m.file, m.n})
			}
			byPlace(&b, "by place only", at, lim.ranges)
		}
		for _, m := range mid[places:] {
			fmt.Fprintf(&b, "- [%s] %s\n", m.head, clipRunes(oneLine(m.text, lim.midChars), lim.midChars))
		}
		if len(tail) > 0 {
			fmt.Fprintf(&b, "(the latest %d, word for word)\n", len(tail))
		}
	}
	for _, m := range tail {
		whole(m)
	}

	if len(turns) > 0 {
		fmt.Fprintf(&b, "\nTHE AGENT'S TURN ENDS, oldest first (%d in all", len(turns))
		var earlier []line
		if len(turns) > lim.turns {
			fmt.Fprintf(&b, "; the latest %d", lim.turns)
			earlier, turns = turns[:len(turns)-lim.turns], turns[len(turns)-lim.turns:]
		}
		b.WriteString("). session_read at the place cited gives the whole report.\n")
		if lim.turnPlaces && len(earlier) > 0 {
			var at []recordPlace
			for _, m := range earlier {
				if m.file != "" {
					at = append(at, recordPlace{m.file, m.n})
				}
			}
			byPlace(&b, "earlier turn ends by place", at, lim.ranges)
		}
		for _, m := range turns {
			fmt.Fprintf(&b, "- [%s] %s\n", m.head, m.text)
		}
	}
	if q := r.st.Question; q != nil {
		fmt.Fprintf(&b, "\nYOUR QUESTION TO THE USER IS STILL OPEN (%s): %s\n", q.ID, q.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}
