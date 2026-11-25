package harness

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/tools"
)

// The dossier.
//
// The conversation grows with everything that happens. When its prompt
// reaches rollover_tokens it is restarted, and the next subsession opens
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
// holds and the room it needs to write, within fixed bounds. Loop
// goroutine only.
func (r *Runtime) dossierBudget(promptTokens int, fresh bool) int {
	window := r.cfg.Model.ContextTokens
	if window <= 0 {
		window = 200_000
	}
	limit := dossierReadMax
	if fresh {
		limit, promptTokens = dossierFreshMax, 8000
	}
	free := window - promptTokens - r.cfg.Model.MaxTokens - 8000
	// About three characters of the record to a token, with a fifth held back.
	return max(dossierReadMin, min(limit, free*3*4/5))
}

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
	limit := r.cfg.RolloverTokens
	if !t.overflowed && (limit <= 0 || r.contextTokens() < limit) {
		return false
	}
	if cur := r.st.Current(); cur != nil && cur.Reason == "rollover" && ((limit > 0 && t.firstPrompt >= limit) || (t.overflowed && t.subCalls == 0)) {
		// The conversation was this long on its first call after the last
		// restart: the dossier and the standing prompt alone are too much.
		// Restarting again would only rebuild the same prompt.
		if !t.futileLogged {
			t.futileLogged = true
			r.ui.Log("the conversation is too long right after a restart, so restarting again cannot shrink it; raise rollover_tokens or shorten .agents/eagent/INSTRUCTIONS.md")
			// The user is told, once: it is theirs to fix, and nothing
			// else will say why eagent grows slower and dearer.
			r.recordMessage(event.MessageData{Auto: true, Important: true, Text: "My conversation is full again right after I restarted it from my notes: the notes and this project's standing instructions alone reach the limit, so another restart cannot make room. Raise `rollover_tokens` in .agents/eagent/config.json or shorten .agents/eagent/INSTRUCTIONS.md. Until then I carry on with a conversation longer than it should be."})
		}
		return false
	}
	return true
}

// dossierJob is what one dossier pass works from, gathered on the loop.
type dossierJob struct {
	msgs   []llm.Message  // the conversation, ending with the request
	lines  map[string]int // lines in each record file, to check citations
	budget int            // characters of lookups the pass may read back
	covers int64
	tokens int
	adopt  bool // the record is the three-actor design's (resume.go)
}

// dossierResult is what a pass came back with.
type dossierResult struct {
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
	t.busy, t.compacting, t.callAt = true, true, time.Now()
	r.flushSilent()
	job := dossierJob{covers: r.st.LastSeq(), tokens: r.contextTokens(), lines: r.recordLines(), adopt: t.adopt}
	data := prompts.DossierData{Files: r.fileTable(""), Index: r.dossierIndex()}
	var msgs []llm.Message
	switch {
	case t.adopt:
		// A session written by the three-actor design. Its conversation
		// was the orchestrator's and has no rendering here, so the model
		// writes from the files, as a writer with no memory would.
		data.Fresh, data.Legacy = true, true
		r.ui.Log("this session was written by an earlier eagent; writing a dossier from its record to continue from")
	case t.overflowed:
		// The provider refused the conversation as too long, so it cannot
		// be the prompt. The model works from the files instead, starting
		// from the dossier this subsession opened with (the first message
		// of the conversation).
		data.Fresh, data.Reason = true, "the model refused the conversation as too long"
		if cur := r.st.Current(); cur != nil && cur.Dossier != "" {
			if all := r.st.View(&r.view); len(all) > 0 {
				msgs = all[:1:1]
			}
		}
		r.ui.Log("the model refused the conversation as too long; restarting it from a dossier")
	default:
		msgs = r.st.View(&r.view)
		r.ui.Log("the conversation reached %dk tokens; restarting it from a dossier", job.tokens/1000)
	}
	job.budget = r.dossierBudget(job.tokens, data.Fresh)
	job.msgs = state.WithTail(msgs, r.prompts.Render("DOSSIER.md", data))
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		res := r.writeDossier(job)
		r.post(func() { r.finishDossier(job, res) })
	}()
}

// citation matches a place in the record: file:line or file:line-line.
var citation = regexp.MustCompile(`(\d{10,}\.jsonl):(\d+)(?:-(\d+))?`)

// checkCitations counts the places a text cites and lists those that do
// not exist: a file the session does not have, or a line past its end.
func checkCitations(text string, lines map[string]int) (n int, bad []string) {
	seen := map[string]bool{}
	for _, m := range citation.FindAllStringSubmatch(text, -1) {
		n++
		from, _ := strconv.Atoi(m[2])
		to := from
		if m[3] != "" {
			to, _ = strconv.Atoi(m[3])
		}
		if max, ok := lines[m[1]]; ok && from >= 1 && to >= from && to <= max {
			continue
		}
		if !seen[m[0]] {
			seen[m[0]] = true
			bad = append(bad, m[0])
		}
	}
	return n, bad
}

// writeDossier runs the pass: the model looks up what it needs in the
// record and then writes. A reply that is not a dossier (too short, no
// map, places that do not exist) is sent back once with what was wrong.
// The lookups and the exchange are part of the writing and are not
// recorded. It runs on its own goroutine.
func (r *Runtime) writeDossier(job dossierJob) (res dossierResult) {
	msgs := job.msgs[:len(job.msgs):len(job.msgs)]
	read, rejected := 0, false
	for res.calls < dossierMaxCalls {
		ctx, cancel := context.WithTimeout(r.ctx, dossierTimeout)
		resp, err := r.complete(ctx, event.ActorEagent, llm.Request{System: r.system, Messages: msgs, Tools: r.toolset, CacheKey: r.sess.ID}, nil)
		cancel()
		if err != nil {
			if r.ctx.Err() == nil {
				r.ui.Log("dossier: model call failed: %s", shortErr(err))
			}
			return res
		}
		res.calls++
		res.usage.Input += resp.Usage.Input
		res.usage.Output += resp.Usage.Output
		res.usage.Cached += resp.Usage.Cached
		res.usage.Reasoning += resp.Usage.Reasoning
		res.elapsed += resp.Elapsed
		msgs = append(msgs, llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol, NativeHost: resp.BaseURL})
		if len(resp.ToolCalls) > 0 {
			// Looking things up before writing.
			results := llm.Message{Role: "tool"}
			for _, c := range resp.ToolCalls {
				out, isErr := r.dossierLookup(c, &read, job.budget, &res)
				results.Results = append(results.Results, llm.ToolResult{CallID: c.ID, Name: c.Name, Output: out, IsError: isErr})
			}
			msgs = append(msgs, results)
			if res.calls == dossierMaxCalls-1 {
				msgs = append(msgs, llm.Message{Role: "user", Text: "[eagent] That was the last lookup. Write the briefing now, as your whole reply, with no tool call."})
			}
			continue
		}
		text := strings.TrimSpace(resp.Text)
		cites, bad := checkCitations(text, job.lines)
		var why string
		switch {
		case len(text) < dossierMinChars:
			why = fmt.Sprintf("it is too short to be a briefing (%d characters)", len(text))
		case cites < dossierMinCites:
			why = fmt.Sprintf("its MAP cites %d places in the record; a map names the place of every topic that matters, as file:line", cites)
		case len(bad) > 0:
			why = "these places do not exist in the record: " + strings.Join(bad[:min(len(bad), 12)], ", ") + ". Cite only places you were shown, and check with session_read if unsure"
		}
		if len(text) > len(res.text) || why == "" {
			res.text, res.bad = text, bad
		}
		if why == "" {
			res.ok = true
			return res
		}
		if rejected {
			// Twice wrong. A text with a thin or partly wrong map still
			// beats none: it is kept when it reads as a briefing, with the
			// wrong places named.
			res.ok = len(res.text) >= dossierMinChars
			return res
		}
		rejected = true
		r.ui.Log("dossier: sent back once: %s", why)
		msgs = append(msgs, llm.Message{Role: "user", Text: "[eagent] That cannot be used: " + why + ". Write the whole briefing again, all six parts, as plain text."})
	}
	res.ok = len(res.text) >= dossierMinChars
	return res
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
		res.consulted = append(res.consulted, fmt.Sprintf("read %s:%d-%d", str("file"), num("from", 1), num("to", 0)))
		out, err = r.archive.Read(str("file"), num("from", 1), num("to", 0), raw, dossierReadChars)
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
	case len(res.bad) > 0:
		text += "\n\n(eagent checked the places cited above. These do not exist in the record, so disregard them: " + strings.Join(res.bad, ", ") + ")"
	}
	endReason, startReason := "context full", "rollover"
	if job.adopt {
		endReason, startReason = "resumed by an eagent with one loop", "upgrade"
	}
	next, err := r.openSubsession(endReason, startReason, job.tokens)
	if err != nil {
		r.ui.Log("dossier: %v; the conversation continues as it is for now", err)
		t.dossierFailedAt = time.Now()
		return
	}
	// The facts are written now that the next file is open, so the table
	// of files shows the closed one whole and says where this dossier sits.
	body := strings.TrimSpace(text) + "\n\n" + r.dossierFacts(job.covers, next)
	d := event.DossierData{Text: body, CoversSeq: job.covers, By: by, ElapsedMS: res.elapsed.Milliseconds(), Calls: res.calls, Consulted: res.consulted}
	if res.usage != (event.Usage{}) {
		d.Usage = &res.usage
	}
	r.append(event.New(event.Dossier, event.ActorHarness, d))
	t.promptTokens, t.firstPrompt, t.subCalls, t.overflowed, t.adopt = 0, 0, 0, false, false
	cites, _ := checkCitations(res.text, job.lines)
	r.ui.Log("subsession %d begins with a dossier written by %s (%d characters, %d places cited, %d lookups)", len(r.st.Subsessions), by, len(body), cites, len(res.consulted))
}

// openSubsession ends the current subsession file and starts the next.
// Loop goroutine (or before the loop starts).
func (r *Runtime) openSubsession(endReason, startReason string, tokens int) (string, error) {
	next, err := r.sess.NewSubsessionName(time.Now())
	if err != nil {
		return "", err
	}
	r.append(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: endReason, NextFile: next, InputTokens: tokens}))
	if _, err := r.sess.OpenSubsession(next); err != nil {
		return "", err
	}
	r.append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: len(r.st.Subsessions), Reason: startReason}))
	return next, nil
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
	var from int64
	if cur := r.st.Current(); cur != nil {
		from = cur.StartSeq
		if cur.CoversSeq > 0 {
			from = cur.CoversSeq + 1
		}
	}
	who := func(name string) string {
		if rel := r.st.Relays[name]; rel != nil {
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
		for drop := len(older) - dossierOlderLines; drop > 0; {
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
	if skipped := len(recent) - dossierIndexLines; skipped > 0 {
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
	var b strings.Builder
	b.WriteString("(Your model could not write this briefing, so eagent assembled it from its records. It has no judgement in it and no MAP by topic: what the user wants and where the work stands are in their messages and the agent's reports below, each with its place in the record, and the table of files shows where every earlier briefing sits. Read those places with session_read before you rely on anything here.)\n")
	var told []event.Event
	for _, ev := range r.st.Events {
		if ev.Type == event.Message {
			told = append(told, ev)
		}
	}
	if len(told) > 0 {
		b.WriteString("\nWHAT THE USER LAST HEARD, oldest first:\n")
		for _, ev := range told[max(0, len(told)-5):] {
			var d event.MessageData
			_ = ev.Decode(&d)
			fmt.Fprintf(&b, "- [%s] %s\n", r.when(ev.Time), clipRunes(oneLine(d.Text, 600), 600))
		}
	}
	for _, name := range r.st.RelayOrder {
		if rel := r.st.Relays[name]; rel != nil && rel.LastTurn != "" {
			fmt.Fprintf(&b, "\n%s'S LAST REPORT (%s):\n%s\n", strings.ToUpper(rel.Who()), r.when(rel.LastTurnAt), clipRunes(rel.LastTurn, 3000))
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
)

// dossierFacts is the part of a dossier the harness writes from the record,
// so it holds no matter what the model's part kept: the agents and how to
// reach them, the user's own words, and where each of the agent's turns
// ended. It covers events up to covers; later ones are in the conversation
// itself. Loop goroutine only.
func (r *Runtime) dossierFacts(covers int64, opens string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nFROM EAGENT'S OWN RECORDS (exact, as of %s)\n", r.when(time.Now()))

	b.WriteString("\nTHE RECORD'S FILES, oldest first (session_read opens any file:line; session_search searches them all)\n")
	b.WriteString(r.fileTable(opens) + "\n")

	b.WriteString("\nAGENTS\n")
	if len(r.st.RelayOrder) == 0 {
		b.WriteString("(none has been started)\n")
	}
	for _, name := range r.st.RelayOrder {
		rel := r.st.Relays[name]
		fmt.Fprintf(&b, "- %s: %s (profile %s) in %s; terminal %s %s", rel.Name, firstNonEmpty(rel.Label, rel.Who()), rel.Profile, rel.Dir, rel.Host, rel.Target)
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

	type line struct{ head, text string }
	var user, turns []line
	for _, ev := range r.st.Events {
		if ev.Seq > covers {
			break
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
			user = append(user, line{at, text})
		case event.UserAnswer:
			var d event.UserAnswerData
			if ev.Decode(&d) != nil {
				continue
			}
			q := clipRunes(oneLine(r.st.QuestionText(d.QuestionID), 200), 200)
			user = append(user, line{at, fmt.Sprintf("(answering %q) %s", q, d.Text)})
		case event.RelayTurn:
			var d event.RelayTurnData
			if ev.Decode(&d) != nil {
				continue
			}
			who := d.Name
			if rel := r.st.Relays[d.Name]; rel != nil {
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
			turns = append(turns, line{at, head + ": " + clipRunes(oneLine(report, 200), 200)})
		}
	}

	fmt.Fprintf(&b, "\nTHE USER'S MESSAGES, word for word, oldest first (%d in all)\n", len(user))
	if len(user) == 0 {
		b.WriteString("(none yet)\n")
	}
	for i, m := range user {
		if len(user) > dossierUserHead+dossierUserTail {
			if i == dossierUserHead {
				fmt.Fprintf(&b, "(%d more in between; session_search finds them)\n", len(user)-dossierUserHead-dossierUserTail)
			}
			if i >= dossierUserHead && i < len(user)-dossierUserTail {
				continue
			}
		}
		text := strings.TrimSpace(m.text)
		if len(text) > dossierUserChars {
			text = clipRunes(text, dossierUserChars) + " (cut here; the whole message is at the place cited)"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", m.head, strings.ReplaceAll(text, "\n", "\n  "))
	}

	if len(turns) > 0 {
		fmt.Fprintf(&b, "\nTHE AGENT'S TURN ENDS, oldest first (%d in all", len(turns))
		if len(turns) > dossierTurns {
			fmt.Fprintf(&b, "; the latest %d", dossierTurns)
			turns = turns[len(turns)-dossierTurns:]
		}
		b.WriteString("). session_read at the place cited gives the whole report.\n")
		for _, m := range turns {
			fmt.Fprintf(&b, "- [%s] %s\n", m.head, m.text)
		}
	}
	if q := r.st.Question; q != nil {
		fmt.Fprintf(&b, "\nYOUR QUESTION TO THE USER IS STILL OPEN (%s): %s\n", q.ID, q.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}
