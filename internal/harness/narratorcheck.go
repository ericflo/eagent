package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// While work goes on (the orchestrator in a turn, task workers running, a
// relayed agent in a turn of its own) the user is told what is happening
// all the way through. That is the narrator's job, not a setting. On every
// narrator tick the harness decides whether there is something to weigh,
// and if so asks the narrator, whose default is to say what happened since
// it last spoke. It may hold when nothing is worth a sentence, but never
// past the quiet limit: then it says what got done, or, when nothing new
// has shown, what is still running and for how long.
//
// A check that ends in silence leaves no trace in the narrator's
// conversation: no steer, no model turn. Its prompt therefore grows only
// with what happens and what it tells the user, and each check reuses the
// cached prefix of the one before. A check that speaks goes on the record
// as an ordinary narrator turn, with the steer it was given.

// progressCheck is one such look.
type progressCheck struct {
	limit    time.Duration
	moved    bool // something happened since the user last heard
	steps    int  // relay steps counted when it looked
	recorded bool // it spoke: the turn is in the log
	tokens   int  // prompt size of its model call
}

// relayStep is one thing a relayed agent did (a tool it ran), as its
// transcript described it. Steps are kept in memory only: they are what a
// progress check reads between the agent's own remarks.
type relayStep struct {
	text string
	at   time.Time
	n    int // how many times in a row
}

// relayStepsKept bounds the steps remembered per relay; relayStepsShown the
// steps one progress check lists.
const (
	relayStepsKept  = 80
	relayStepsShown = 30
)

// narrateGap is the least time between a narrator message and the next
// progress check, so two messages never land on top of each other.
// checkBackoff is how long checks pause after one whose model call failed.
var (
	narrateGap   = 45 * time.Second
	checkBackoff = 2 * time.Minute
)

// narratorTrimAt is the narrator prompt size, in tokens, at which a relay
// session's narrator view is shortened; narratorKeepChars is about how much
// of its newest content stays. The view moves in rare steps, not with every
// call, so the cached prefix survives between them.
var (
	narratorTrimAt    = 40_000
	narratorKeepChars = 48_000
)

// noteStep records a step the agent took.
func (s *relaySeats) noteStep(name, text string, at time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.steps == nil {
		s.steps = map[string][]relayStep{}
	}
	list := s.steps[name]
	if n := len(list); n > 0 && list[n-1].text == text {
		list[n-1].n++
	} else {
		list = append(list, relayStep{text: text, at: at, n: 1})
	}
	if len(list) > relayStepsKept {
		list = list[len(list)-relayStepsKept:]
	}
	s.steps[name] = list
	s.stepCount++
}

// stepsSince returns the steps one relay took after a moment, oldest first.
func (s *relaySeats) stepsSince(name string, since time.Time) []relayStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []relayStep
	for _, st := range s.steps[name] {
		if st.at.After(since) {
			out = append(out, st)
		}
	}
	return out
}

// stepped reports how many steps the relays have taken in all, and whether
// any came after a moment.
func (s *relaySeats) stepped(since time.Time) (total int, after bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, list := range s.steps {
		if n := len(list); n > 0 && list[n-1].at.After(since) {
			after = true
		}
	}
	return s.stepCount, after
}

// maybeCheckProgress runs on each narrator tick while work goes on, and
// starts a progress check when there is something for the narrator to
// weigh. A tick with nothing new costs no model call. Loop goroutine.
func (r *Runtime) maybeCheckProgress() {
	if r.rolling || r.narrPending != "" || r.userWaitSeq != 0 || time.Since(r.checkFailedAt) < checkBackoff {
		return // a wake for something that happened comes first
	}
	limit, quiet := r.quietLimit(), r.quietFor()
	steps, stepped := r.relays.stepped(r.narrSaidAt)
	// moved: something happened since the user last heard.
	// fresh: since the narrator last looked.
	moved := r.narrWorthy > r.narrSaidSeen || stepped
	fresh := r.narrWorthy > max(r.narrLastSeen, r.checkSeenSeq) || steps > r.checkSeenSteps
	gap := narrateGap
	if limit > 0 {
		gap = min(gap, limit)
	}
	switch {
	case moved && limit > 0 && quiet >= limit:
		// Overdue: it speaks.
	case moved && fresh && quiet >= gap:
		// Something new to tell, and the last message has had its moment.
	case !moved && limit > 0 && quiet >= limit && time.Since(r.checkedAt) >= limit:
		// Nothing new for a while: it says what is still running.
	default:
		return
	}
	check := &progressCheck{limit: limit, moved: moved, steps: steps}
	r.narrBusy = true
	r.checkedAt = time.Now()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		endReason := r.narratorTurn(wakePeriodic, check)
		r.post(func() {
			r.narrBusy = false
			if check.recorded {
				r.append(event.New(event.TurnEnd, event.ActorNarrator, event.TurnData{Reason: endReason}))
			}
			r.maybeTrimNarrator(check.tokens)
		})
	}()
}

// speaks reports a response that says something to the user.
func speaks(resp *llm.Response) bool {
	for _, tc := range resp.ToolCalls {
		if tc.Name != "send_message" && tc.Name != "ask_user" {
			continue
		}
		if args, err := llm.ArgsObject(tc.Args); err == nil {
			if text, _ := args["text"].(string); strings.TrimSpace(text) != "" {
				return true
			}
		}
	}
	return false
}

// noteQuietCheck keeps the cost of a check that said nothing, to be put on
// the record in one line with the others. Loop goroutine.
func (r *Runtime) noteQuietCheck(resp *llm.Response) {
	r.quietChecks.Count++
	r.quietChecks.Usage.Input += resp.Usage.Input
	r.quietChecks.Usage.Output += resp.Usage.Output
	r.quietChecks.Usage.Cached += resp.Usage.Cached
	r.quietChecks.Usage.Reasoning += resp.Usage.Reasoning
	r.quietChecks.ElapsedMS += resp.Elapsed.Milliseconds()
	if r.quietChecks.Count >= 20 {
		r.flushQuietChecks()
	}
}

// flushQuietChecks records the silent checks made since the last record, so
// their tokens count toward the session's totals. Loop goroutine.
func (r *Runtime) flushQuietChecks() {
	if r.quietChecks.Count == 0 {
		return
	}
	d := r.quietChecks
	r.quietChecks = event.NarratorChecksData{}
	r.append(event.New(event.NarratorChecks, event.ActorNarrator, d))
}

// maybeTrimNarrator shortens a relay session's narrator view once its
// prompt has grown past narratorTrimAt tokens. A relay's narrator passes on
// what the agent reports as it comes and owes no report of the whole, so
// what it said long ago can go. Other sessions keep everything for their
// final report, and start afresh when the orchestrator does. Loop goroutine.
func (r *Runtime) maybeTrimNarrator(promptTokens int) {
	if r.ending || promptTokens < narratorTrimAt || !r.st.RelayMode() {
		return
	}
	from, recap, ok := r.st.NarratorTrimPoint(narratorKeepChars)
	if !ok {
		return
	}
	r.append(event.New(event.NarratorTrim, event.ActorHarness, event.NarratorTrimData{FromSeq: from, Recap: recap}))
	r.ui.Log("narrator: view shortened (its prompt reached %dk tokens)", promptTokens/1000)
}

// relayProgress is what one working relayed agent did since the user last
// heard.
type relayProgress struct {
	who   string
	steps []relayStep
}

// checkSteer is everything a progress check tells the narrator.
type checkSteer struct {
	now, started time.Time
	relays       []relayProgress
	tasks        int
	inflight     []string
	quiet, limit time.Duration
	moved        bool
	last         string // the narrator's last message
}

// steerCheck gathers a progress check's steer. Loop goroutine.
func (r *Runtime) steerCheck(now time.Time, c *progressCheck) string {
	s := checkSteer{now: now, started: r.st.Started, tasks: len(r.st.RunningTasks()), inflight: r.inflightLines(now),
		quiet: r.quietFor(), limit: c.limit, moved: c.moved, last: r.narrLastSaid}
	for _, rel := range r.st.ActiveRelays() {
		if rel.State == "working" {
			s.relays = append(s.relays, relayProgress{who: rel.Who(), steps: r.relays.stepsSince(rel.Name, r.narrSaidAt)})
		}
	}
	return s.text()
}

// text writes the steer: what happened since the user last heard, what is
// running now, and how ready to speak the narrator should be.
func (s checkSteer) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[harness %s] Progress check while work goes on.", s.now.Local().Format("15:04:05"))
	if !s.started.IsZero() {
		fmt.Fprintf(&b, " The session has been running for %s.", since(s.started, s.now))
	}
	heard := "so far"
	if s.last != "" {
		heard = "since your last message, " + roundDur(s.quiet) + " ago"
	}
	for _, rel := range s.relays {
		if len(rel.steps) == 0 {
			fmt.Fprintf(&b, "\n%s has started no new step %s.", rel.who, heard)
			continue
		}
		shown := rel.steps
		if len(shown) > relayStepsShown {
			shown = shown[len(shown)-relayStepsShown:]
		}
		fmt.Fprintf(&b, "\nWhat %s did %s, oldest first", rel.who, heard)
		if skipped := len(rel.steps) - len(shown); skipped > 0 {
			fmt.Fprintf(&b, " (%d earlier steps left out)", skipped)
		}
		b.WriteString(":")
		for _, st := range shown {
			fmt.Fprintf(&b, "\n- %s %s", st.at.Local().Format("15:04"), st.text)
			if st.n > 1 {
				fmt.Fprintf(&b, " (x%d)", st.n)
			}
		}
	}
	if len(s.relays) > 0 {
		b.WriteString("\n")
	} else {
		b.WriteString(" ")
	}
	if s.tasks > 0 {
		fmt.Fprintf(&b, "%d task(s) are still running. ", s.tasks)
	}
	if len(s.inflight) > 0 {
		fmt.Fprintf(&b, "In flight right now: %s. ", strings.Join(s.inflight, "; "))
	}
	b.WriteString("These lines and the log above them are all that is known about the work: describe only steps, checks and files that appear there, and if a call has not returned you do not know what it is doing. ")
	switch {
	case s.last == "":
		fmt.Fprintf(&b, "The user has heard nothing from you in the %s since they asked: tell them now, in two or three sentences, what you understood the job to be and how it is being done.", roundDur(s.quiet))
	case !s.moved:
		fmt.Fprintf(&b, "Nothing new has shown since you last spoke, %s ago. Tell the user in one sentence what is still running and for how long, naming the command, task or step from the lines above, so the quiet does not read as a stop. If you cannot tell what it is, say how long it has been since anything showed.", roundDur(s.quiet))
	case s.limit > 0 && s.quiet >= s.limit:
		fmt.Fprintf(&b, "The user has heard nothing for %s while work kept going: send a progress line now. Two or three sentences: what got done since you last spoke, what is happening now, anything that went wrong. Name the files, commands and numbers.", roundDur(s.quiet))
	default:
		fmt.Fprintf(&b, "The user last heard from you %s ago. Tell them what has happened since, in a sentence or two, with the files, commands and numbers that matter. Hold only if all of it is still the step you last described, or too slight to be worth a sentence yet.", roundDur(s.quiet))
	}
	if s.last != "" {
		fmt.Fprintf(&b, " Your last message to the user (do not repeat it): %q", clipTail(s.last, 400))
	}
	b.WriteString(" Respond with exactly one tool call: send_message or hold.")
	return b.String()
}
