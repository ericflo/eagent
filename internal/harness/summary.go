package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// The thread's title and summary.
//
// A session is one row in the user's thread list, and the title and summary
// are all that row says about it. They used to be the project's directory
// name and the first message, word for word, unless the orchestrator
// remembered to call retitle_thread, which it mostly did not. So the harness
// writes them itself: a small model call on the narrator's route reads what
// the user asked for and what they have been told since, and returns a title
// for the session's subject and a summary of the whole session and where it
// stands. It runs soon after the user writes, when the agent finishes a turn,
// and now and then while one long turn goes on.
//
// The title names the subject and is meant to stay put: the user finds the
// thread by it. A title someone chose on purpose (the user in the app, the
// orchestrator at the user's request) is kept. The summary moves with the
// work.

// The schedule. Variables so tests can run it in milliseconds.
var (
	summaryFirstDelay = 2 * time.Second  // the first one: as soon as there is a request to name
	summaryUserDelay  = 20 * time.Second // after a later message from the user
	summaryWorkDelay  = 20 * time.Second // after the agent reports, so what the user was told is in it
	summaryTurnGap    = time.Minute      // between summaries prompted by the end of a turn
	summaryTalkGap    = 5 * time.Minute  // between summaries prompted by progress the user heard
	summaryLongGap    = 15 * time.Minute // a refresh while one long turn goes on
	summaryRetry      = 2 * time.Minute  // after a failed call, doubling up to summaryLongGap
	summaryTimeout    = 2 * time.Minute  // one call
	summaryFinalWait  = 45 * time.Second // the call a batch session waits for before it ends
	summaryAttachWait = 20 * time.Second // how long the first summary waits for the phone thread
	summaryAttachPoll = 500 * time.Millisecond
)

// summariesDisabled is the process-wide switch (EAGENT_SUMMARIES=off): no
// summary calls, titles change only through retitle_thread.
func summariesDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EAGENT_SUMMARIES"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

// summaryKeeper is the loop-owned schedule for the next summary.
type summaryKeeper struct {
	on     bool
	busy   bool
	timer  *time.Timer
	due    time.Time // when timer fires; zero when none is armed
	last   time.Time // when the latest call started
	seen   int64     // newest event a finished call covered
	worthy int64     // newest event worth a fresh summary
	fails  int
	final  bool // the last summary of a batch session has been started
	// attached says the phone thread's own title and summary have been
	// read (adoptThread), and attachBy is how long a summary waits for
	// that: the model should see the title the user sees.
	attached bool
	attachBy time.Time
}

// startSummaries arms the first summary of a run: for whatever the user
// wrote before the loop started (the prompt a session is started or resumed
// with), and for a session that has never had one (from before the harness
// did this, or whose first call failed). Loop goroutine, before the loop
// starts.
func (r *Runtime) startSummaries() {
	r.summary.on = !summariesDisabled()
	if !r.summary.on {
		return
	}
	r.summary.seen = r.st.SummarySeq
	r.summary.attachBy = time.Now().Add(summaryAttachWait)
	if r.st.LastUserSeq > r.st.SummarySeq {
		r.summary.worthy = r.st.LastSeq()
		delay := summaryUserDelay
		if r.st.SummarySeq == 0 {
			delay = summaryFirstDelay
		}
		r.armSummary(time.Now().Add(delay))
	}
}

func (r *Runtime) stopSummaries() {
	if r.summary.timer != nil {
		r.summary.timer.Stop()
		r.summary.timer = nil
	}
}

// noteSummary decides whether an event is news for the thread list and when
// to act on it. Loop goroutine only.
func (r *Runtime) noteSummary(ev event.Event) {
	k := &r.summary
	if !k.on || r.ending {
		return
	}
	now := time.Now()
	notBefore := func(gap time.Duration) time.Time {
		due := now.Add(summaryWorkDelay)
		if t := k.last.Add(gap); t.After(due) {
			due = t
		}
		return due
	}
	var due time.Time
	switch ev.Type {
	case event.UserMessage, event.UserAnswer:
		due = now.Add(summaryUserDelay)
		if r.st.SummarySeq == 0 {
			due = now.Add(summaryFirstDelay)
		}
	case event.RelayTurn:
		var d event.RelayTurnData
		if ev.Decode(&d) != nil || d.Idle {
			return // a turn that only said "still waiting" is not news
		}
		due = notBefore(summaryTurnGap)
	case event.Yield, event.RelayEnd, event.NarratorQuestion, event.UserDismiss:
		due = notBefore(summaryTurnGap)
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		if t := r.st.Tasks[d.ID]; t != nil && t.Kind == "dossier" {
			return
		}
		due = notBefore(summaryTalkGap)
	case event.NarratorMessage:
		due = notBefore(summaryTalkGap)
	case event.RelayProgress:
		due = notBefore(summaryLongGap)
	default:
		return
	}
	k.worthy = ev.Seq
	r.armSummary(due)
}

// armSummary schedules a summary for `at`, unless one is already due sooner.
func (r *Runtime) armSummary(at time.Time) {
	k := &r.summary
	if !k.on || r.ending {
		return
	}
	if k.timer != nil && !k.due.IsZero() && !at.Before(k.due) {
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	k.due = at
	k.timer = time.AfterFunc(max(time.Until(at), 0), func() {
		r.post(func() { r.summaryDue(at) })
	})
}

func (r *Runtime) summaryDue(at time.Time) {
	k := &r.summary
	if !k.due.Equal(at) {
		return // rescheduled since this timer was set
	}
	k.timer, k.due = nil, time.Time{}
	if r.ending || k.busy || k.worthy <= k.seen || r.st.LastUserSeq == 0 {
		return // a call in flight re-arms when it ends
	}
	if r.phone != nil && !k.attached && time.Now().Before(k.attachBy) {
		// The mirror is still attaching: a moment more and the summary
		// starts from the thread's own title instead of a second guess.
		r.armSummary(time.Now().Add(summaryAttachPoll))
		return
	}
	r.startSummary()
}

// finalSummary holds a batch session open for one last summary when it is
// about to end with news the thread list has not got: the row should say
// how the session ended, not that it is still working. It reports whether
// the session should wait; the call's return ticks the loop again. One
// attempt, bounded by summaryFinalWait. Loop goroutine only.
func (r *Runtime) finalSummary() bool {
	k := &r.summary
	if !k.on {
		return false
	}
	if k.busy {
		return true
	}
	if k.final || k.worthy <= k.seen || r.st.LastUserSeq == 0 {
		return false
	}
	k.final = true
	r.startSummary()
	return true
}

// startSummary makes one summary call on its own goroutine and applies the
// answer on the loop.
func (r *Runtime) startSummary() {
	k := &r.summary
	k.busy, k.last = true, time.Now()
	timeout := summaryTimeout
	if k.final {
		timeout = summaryFinalWait
	}
	project := filepath.Base(r.projectPath())
	in := summaryInput(r.st, project, time.Now())
	seen := r.st.LastSeq()
	client := r.narrClient
	system := r.prompts.Render("THREAD-SUMMARY.md", nil)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ctx, cancel := context.WithTimeout(r.ctx, timeout)
		defer cancel()
		out, err := summarize(ctx, client, system, in)
		r.post(func() {
			k.busy = false
			if r.ending {
				return
			}
			if err != nil {
				k.fails++
				if k.fails == 1 && ctx.Err() == nil {
					r.ui.Log("thread summary: %s; the title and summary keep their last values until a call succeeds", shortErr(err))
				}
				r.armSummary(time.Now().Add(min(summaryRetry<<min(k.fails-1, 3), summaryLongGap)))
				return
			}
			k.fails = 0
			k.seen = seen
			r.applySummary(project, seen, out)
			if k.worthy > k.seen {
				r.armSummary(time.Now().Add(summaryTurnGap))
			}
		})
	}()
}

// applySummary records what the call returned and sends what changed to the
// phone. Loop goroutine only.
func (r *Runtime) applySummary(project string, seen int64, out summaryResult) {
	title, description := acceptSummary(r.st, project, out)
	usage := out.Usage
	r.append(event.New(event.ThreadTitle, event.ActorHarness, event.ThreadTitleData{
		Title: title, Description: description, By: "summary", SeenSeq: seen, Usage: &usage, ElapsedMS: out.Elapsed.Milliseconds(),
	}))
	if r.phone != nil && (title != "" || description != "") {
		r.phone.RetitleThread(r, title, description)
	}
}

// acceptSummary returns the title and summary to record: "" for a side that
// stays as it is. The summary is taken whenever it differs. The title is
// held to a stricter rule, because the user finds the thread by it: a title
// someone chose stays; otherwise the model's title replaces a missing or
// generic one, may be revised once as the work comes into focus, and after
// that changes only when the user has written since it was set (the subject
// of a session moves when the user moves it).
func acceptSummary(st *state.State, project string, out summaryResult) (title, description string) {
	if d := cleanSummary(out.Summary); d != "" && d != st.Description {
		description = d
	}
	t := cleanTitle(out.Title)
	switch {
	case t == "" || genericTitle(t, project) || strings.EqualFold(t, st.Title):
	case st.TitlePinned:
	case st.Title == "" || genericTitle(st.Title, project):
		title = t
	case st.TitleRevisions < 2 || st.LastUserSeq > st.TitleSeq:
		title = t
	}
	return title, description
}

// genericTitle reports a title that would fit any session: the project
// directory's own name, or a placeholder.
func genericTitle(title, project string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" || (project != "" && t == strings.ToLower(project)) {
		return true
	}
	switch t {
	case "session", "new session", "eagent session", "eagent", "chat", "conversation", "untitled", "untitled thread", "thread", "question", "help", "task", "idle chit-chat":
		return true
	}
	return false
}

// cleanTitle makes a model's title fit the row: one line, no wrapping quotes
// or closing punctuation, at most 60 characters ending on a whole word.
func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, "\"'`“”‘’ ")
	s = strings.TrimRight(s, ".:;,!– —-")
	r := []rune(s)
	if len(r) <= 60 {
		return s
	}
	cut := string(r[:60])
	if i := strings.LastIndexByte(cut, ' '); i > 30 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ".:;,!– —-") + "…"
}

// cleanSummary makes a model's summary one paragraph of at most 320
// characters, ending on a sentence where it can.
func cleanSummary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, "\"`“” ")
	r := []rune(s)
	if len(r) <= 320 {
		return s
	}
	cut := string(r[:320])
	if i := strings.LastIndex(cut, ". "); i > 160 {
		return cut[:i+1]
	}
	if i := strings.LastIndexByte(cut, ' '); i > 160 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",;: ") + "…"
}

// summaryResult is one answer from the summary call.
type summaryResult struct {
	Title, Summary string
	Usage          event.Usage
	Elapsed        time.Duration
}

// summarize asks the model once, and once more if the answer was not the
// JSON object it was asked for.
func summarize(ctx context.Context, client *llm.Client, system, input string) (summaryResult, error) {
	req := llm.Request{System: system, Messages: []llm.Message{{Role: "user", Text: input}}}
	var out summaryResult
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := client.Complete(ctx, req, nil)
		if err != nil {
			return out, err
		}
		out.Usage.Input += resp.Usage.Input
		out.Usage.Output += resp.Usage.Output
		out.Usage.Cached += resp.Usage.Cached
		out.Usage.Reasoning += resp.Usage.Reasoning
		out.Elapsed += resp.Elapsed
		if title, summary, ok := parseSummary(resp.Text); ok {
			out.Title, out.Summary = title, summary
			return out, nil
		}
		req.Messages = append(req.Messages,
			llm.Message{Role: "assistant", Text: resp.Text, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL},
			llm.Message{Role: "user", Text: `Reply with only the JSON object: {"title": "...", "summary": "..."}`})
	}
	return out, errors.New("the model did not return a title and summary")
}

// parseSummary reads the JSON object out of a reply that may wrap it in a
// code fence or a sentence.
func parseSummary(text string) (title, summary string, ok bool) {
	i, j := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	if i < 0 || j <= i {
		return "", "", false
	}
	var v struct {
		Title       string `json:"title"`
		Summary     string `json:"summary"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(text[i:j+1]), &v); err != nil {
		return "", "", false
	}
	if v.Summary == "" {
		v.Summary = v.Description
	}
	if strings.TrimSpace(v.Title) == "" && strings.TrimSpace(v.Summary) == "" {
		return "", "", false
	}
	return v.Title, v.Summary, true
}

// summaryInput is what the summary call reads: the title and summary as they
// stand, everything the user wrote (the early and the recent in full, the
// middle counted), what they were told lately, the relayed agent's last
// report, the tasks, and the state of play.
func summaryInput(st *state.State, project string, now time.Time) string {
	var b strings.Builder
	stamp := func(t time.Time) string { return t.Local().Format("Jan 2 15:04") }

	switch {
	case st.Title == "" || genericTitle(st.Title, project):
		b.WriteString("Current title: none yet.\n")
	case st.TitlePinned:
		fmt.Fprintf(&b, "Current title (chosen on purpose; return it unchanged): %q\n", st.Title)
	default:
		fmt.Fprintf(&b, "Current title: %q\n", st.Title)
	}
	if st.Description != "" {
		fmt.Fprintf(&b, "Current summary: %q\n", clipRunes(st.Description, 600))
	} else {
		b.WriteString("Current summary: none yet.\n")
	}
	if project != "" {
		fmt.Fprintf(&b, "Project directory (not a title): %s\n", project)
	}
	if !st.Started.IsZero() {
		fmt.Fprintf(&b, "Session started %s; it is now %s.\n", stamp(st.Started), stamp(now))
	}

	type line struct {
		at   time.Time
		text string
	}
	var user, told []line
	var lastYield *event.YieldData
	for _, ev := range st.Events {
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			// A command to the relay (/stop-relay, /model …) manages the
			// session; it says nothing about the work.
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" && !controlCommand(d.Text) {
				user = append(user, line{ev.Time, d.Text})
			}
		case event.UserAnswer:
			var d event.UserAnswerData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				text := d.Text
				if q := st.QuestionText(d.QuestionID); q != "" {
					text = fmt.Sprintf("(answering %q) %s", clipRunes(oneLine(q, 200), 200), d.Text)
				}
				user = append(user, line{ev.Time, text})
			}
		case event.NarratorMessage:
			var d event.NarratorMessageData
			if ev.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				told = append(told, line{ev.Time, d.Text})
			}
		case event.Yield:
			var d event.YieldData
			if ev.Decode(&d) == nil {
				lastYield = &d
			}
		}
	}

	b.WriteString("\n## What the user wrote, oldest first\n")
	const head, tail = 4, 12
	for i, m := range user {
		if len(user) > head+tail && i == head {
			fmt.Fprintf(&b, "(%d more messages in between)\n", len(user)-head-tail)
		}
		if len(user) > head+tail && i >= head && i < len(user)-tail {
			continue
		}
		room := 700
		if i == 0 {
			room = 1500 // the first message is what the session is for
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, stamp(m.at), clipRunes(oneLine(m.text, room), room))
	}
	if len(user) == 0 {
		b.WriteString("(nothing yet)\n")
	}

	if len(told) > 0 {
		b.WriteString("\n## What the user has been told most recently, oldest first\n")
		for _, m := range told[max(0, len(told)-6):] {
			fmt.Fprintf(&b, "- [%s] %s\n", stamp(m.at), clipRunes(oneLine(m.text, 600), 600))
		}
	}

	for _, name := range st.RelayOrder {
		rel := st.Relays[name]
		if rel == nil || rel.LastTurn == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s does the work in this session. Its latest report (turn %d, %s)\n%s\n", rel.Who(), rel.Turns, stamp(rel.LastTurnAt), clipRunes(rel.LastTurn, 1800))
	}

	var tasks []string
	for _, id := range st.TaskOrder {
		if t := st.Tasks[id]; t != nil && t.Kind != "dossier" {
			tasks = append(tasks, fmt.Sprintf("- %s (%s)", clipRunes(oneLine(t.Title, 120), 120), t.Status))
		}
	}
	if len(tasks) > 0 {
		b.WriteString("\n## Tasks, oldest first\n")
		if len(tasks) > 10 {
			fmt.Fprintf(&b, "(%d earlier tasks)\n", len(tasks)-10)
			tasks = tasks[len(tasks)-10:]
		}
		b.WriteString(strings.Join(tasks, "\n") + "\n")
	}

	b.WriteString("\n## Right now\n")
	working := false
	for _, rel := range st.ActiveRelays() {
		switch rel.State {
		case "working":
			working = true
			fmt.Fprintf(&b, "%s is in the middle of its work (for %s so far).\n", rel.Who(), roundDur(now.Sub(rel.StateSince)))
		case "blocked":
			fmt.Fprintf(&b, "%s is stuck on a prompt on its screen.\n", rel.Who())
		case "idle":
			fmt.Fprintf(&b, "%s last reported %s ago and is not working at the moment. If its report says it left something running in the background, that may still be going.\n", rel.Who(), roundDur(now.Sub(rel.StateSince)))
		}
	}
	if n := len(st.RunningTasks()); n > 0 {
		working = true
		fmt.Fprintf(&b, "%d task(s) running.\n", n)
	}
	reason := ""
	if lastYield != nil {
		reason = clipRunes(oneLine(lastYield.Reason, 300), 300)
	}
	switch {
	case st.Question != nil:
		fmt.Fprintf(&b, "The agent asked the user a question and is waiting for the answer: %s\n", clipRunes(oneLine(st.Question.Text, 300), 300))
	case st.Ended:
		fmt.Fprintf(&b, "The session has ended (%s).\n", st.EndReason)
	case working:
	case lastYield == nil:
		b.WriteString("The agent is working.\n")
	case st.RelayMode() && len(st.ActiveRelays()) == 0:
		// The user stopped the relayed agent. Said outright, that becomes
		// the summary's second sentence ("…; relay stopped"), which tells
		// the reader nothing about the work.
		b.WriteString("Nothing is in progress. The second sentence says where the work was left: what is finished and what is not.\n")
	case st.RelayMode():
		// The note is eagent's own, about a session another agent works in.
		if reason != "" {
			fmt.Fprintf(&b, "Latest status note: %s\n", reason)
		}
	case lastYield.Done:
		fmt.Fprintf(&b, "The agent says the work is finished: %s\n", reason)
	default:
		fmt.Fprintf(&b, "Nothing is running. The agent's closing note: %s\n", reason)
	}
	return b.String()
}

// controlCommand reports whether a user message is a slash command that
// manages the session's relay rather than something the user said about
// the work.
func controlCommand(text string) bool {
	fields := strings.Fields(text)
	return len(fields) > 0 && relayCommands[fields[0]]
}

// SessionSummary is a stored session's fresh title and summary.
type SessionSummary struct {
	Session            string
	Title, Description string // as the model wrote them, cleaned
	OldTitle, OldDesc  string // what the thread showed before ("" when there is no thread)
	Thread             bool   // the session has a FinaleChat thread
	Sent               bool   // the thread was updated
	Skipped            string // why nothing was written, when nothing was
	Usage              event.Usage
	// Input is what the model read, and ModelTitle and ModelSummary what it
	// answered before the rules in acceptSummary, for work on the prompt.
	Input, ModelTitle, ModelSummary string
}

// SummarizeSession writes a fresh title and summary for a stored session
// from its log and, unless dryRun, sends them to the session's FinaleChat
// thread. It never touches the log, so it is safe on a session that another
// process is running; that session takes the thread's title as its own the
// next time it attaches (see adoptThread). client may be nil: then the
// result is only returned. A title the user pinned in the app stays, by the
// service's own rule.
func SummarizeSession(ctx context.Context, model *llm.Client, set *prompts.Set, client *finalechat.Client, project string, info store.Info, dryRun bool) (SessionSummary, error) {
	res := SessionSummary{Session: info.ID}
	events, err := store.Read(info.Path)
	if err != nil {
		return res, err
	}
	st := state.Replay(events)
	if st.LastUserSeq == 0 {
		res.Skipped = "no user message"
		return res, nil
	}
	base := filepath.Base(project)
	ref := finalechat.Ref("eagent:" + info.ID)
	if client != nil {
		if th, err := client.Thread(ctx, ref); err == nil {
			res.Thread, res.OldTitle, res.OldDesc = true, th.Title, th.Description
			// What the app shows is the current title, as it will be for
			// the session itself when it next attaches.
			if t := strings.TrimSpace(th.Title); t != "" && !genericTitle(t, base) {
				st.Title, st.TitlePinned = t, th.TitlePinned
			}
			st.Description = strings.TrimSpace(th.Description)
		} else {
			var fe *finalechat.Error
			if !errors.As(err, &fe) || fe.Status != 404 {
				return res, err
			}
		}
	}
	// A stored session's summary is a fresh look: the title is the model's
	// to set unless someone pinned it.
	st.TitleRevisions = 0
	res.Input = summaryInput(st, base, time.Now())
	out, err := summarize(ctx, model, set.Render("THREAD-SUMMARY.md", nil), res.Input)
	res.Usage = out.Usage
	if err != nil {
		return res, err
	}
	res.ModelTitle, res.ModelSummary = out.Title, out.Summary
	// The thread is what gets compared: a title the log has and the thread
	// never received (an update that was lost, a thread made later) is sent
	// as much as a new one is.
	title, desc := acceptSummary(st, base, out)
	if title == "" {
		title = st.Title
	}
	if desc == "" {
		desc = st.Description
	}
	if genericTitle(title, base) || title == res.OldTitle {
		title = ""
	}
	if desc == res.OldDesc {
		desc = ""
	}
	res.Title, res.Description = title, desc
	if title == "" && desc == "" {
		res.Skipped = "already current"
		return res, nil
	}
	if dryRun || client == nil || !res.Thread {
		if !res.Thread {
			res.Skipped = "no FinaleChat thread"
		}
		return res, nil
	}
	if _, err := client.UpdateThreadDescription(ctx, ref, title, desc); err != nil {
		return res, err
	}
	res.Sent = true
	return res, nil
}
