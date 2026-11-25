package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// Ports of the old summary_test.go: the thread's title and summary.

// coreThreadAsk is how a call's tail asks about the title and summary
// (inline), and coreSummaryMarker how the dedicated summarizer's system
// prompt opens.
const (
	coreThreadAsk     = "The thread's title and summary may be out of date"
	coreSummaryMarker = "You write the title and summary of one work session"
)

// coreSummariesOn turns the title and summary on for one test, with the
// schedule in milliseconds when quick.
func coreSummariesOn(t *testing.T, quick bool) {
	t.Helper()
	t.Setenv("EAGENT_SUMMARIES", "on")
	if !quick {
		return
	}
	old := []time.Duration{summaryFirstDelay, summaryUserDelay, summaryWorkDelay, summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry}
	ms := time.Millisecond
	summaryFirstDelay, summaryUserDelay, summaryWorkDelay = 20*ms, 40*ms, 40*ms
	summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry = 80*ms, 80*ms, 200*ms, 100*ms
	t.Cleanup(func() {
		summaryFirstDelay, summaryUserDelay, summaryWorkDelay = old[0], old[1], old[2]
		summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry = old[3], old[4], old[5], old[6]
	})
}

func coreTitles(evs []event.Event) []event.ThreadTitleData {
	var out []event.ThreadTitleData
	for _, ev := range evs {
		if ev.Type == event.ThreadTitle {
			var d event.ThreadTitleData
			_ = ev.Decode(&d)
			out = append(out, d)
		}
	}
	return out
}

// Old TestParseSummary.
func TestCoreParseSummary(t *testing.T) {
	cases := []struct {
		in, title, summary string
		ok                 bool
	}{
		{`{"title": "Fence repainting", "summary": "Two coats on."}`, "Fence repainting", "Two coats on.", true},
		{"```json\n{\"title\":\"A\",\"summary\":\"B\"}\n```", "A", "B", true},
		{`Here it is: {"title":"A","description":"B"} Hope that helps.`, "A", "B", true},
		{`<think>Return {"title", "summary"} format.</think>{"title":"Local report","summary":"Checked repos. Done."}`, "Local report", "Checked repos. Done.", true},
		{`Using {"format":"json"} as requested: {"title":"Clean","summary":"Parsed last object."}`, "Clean", "Parsed last object.", true},
		{`{"title":"","summary":"only a summary"}`, "", "only a summary", true},
		{`{"title":"","summary":""}`, "", "", false},
		{`Fence repainting`, "", "", false},
		{`{"title": "unterminated`, "", "", false},
	}
	for _, c := range cases {
		title, summary, ok := parseSummary(c.in)
		if title != c.title || summary != c.summary || ok != c.ok {
			t.Errorf("parseSummary(%q) = %q, %q, %v", c.in, title, summary, ok)
		}
	}
}

// Old TestCleanTitleAndSummary.
func TestCoreCleanTitleAndSummary(t *testing.T) {
	if got := cleanTitle("  \"Fence   repainting.\"\n"); got != "Fence repainting" {
		t.Errorf("cleanTitle = %q", got)
	}
	long := cleanTitle("A very long title that goes on and on about every single thing the session ever touched")
	if n := len([]rune(long)); n > 61 || !strings.HasSuffix(long, "…") || strings.Contains(long, "  ") {
		t.Errorf("long title = %q (%d runes)", long, n)
	}
	s := cleanSummary(strings.Repeat("One sentence of the summary here. ", 20))
	if n := len([]rune(s)); n > 320 || !strings.HasSuffix(s, ".") {
		t.Errorf("long summary = %q (%d runes)", s, n)
	}
	if got := cleanSummary("  Two\ncoats   on.  "); got != "Two coats on." {
		t.Errorf("cleanSummary = %q", got)
	}
}

// Old TestGenericTitle.
func TestCoreGenericTitle(t *testing.T) {
	for _, s := range []string{"", "Development", "development", "Session", "Untitled", "eagent"} {
		if !genericTitle(s, "Development") {
			t.Errorf("%q should be generic", s)
		}
	}
	for _, s := range []string{"Development environment setup", "Fence repainting", "Chat export tool"} {
		if genericTitle(s, "Development") {
			t.Errorf("%q should not be generic", s)
		}
	}
}

// Old TestAcceptSummaryTitleRules: the title is what the user finds the
// thread by, so it moves far less readily than the summary does.
func TestCoreAcceptSummaryTitleRules(t *testing.T) {
	out := func(title, summary string) summaryResult { return summaryResult{Title: title, Summary: summary} }
	cases := []struct {
		name               string
		st                 state.State
		out                summaryResult
		wantTitle, wantSum string
	}{
		{"first title and summary", state.State{}, out("Fence repainting", "Two coats on."), "Fence repainting", "Two coats on."},
		{"the project's name is no title", state.State{}, out("Development", "Two coats on."), "", "Two coats on."},
		{"a generic title is replaced", state.State{Title: "Development"}, out("Fence repainting", ""), "Fence repainting", ""},
		{"same title, new summary", state.State{Title: "Fence repainting", Description: "Started."}, out("fence repainting", "Two coats on."), "", "Two coats on."},
		{"nothing changed", state.State{Title: "Fence repainting", Description: "Two coats on."}, out("Fence repainting", "Two coats on."), "", ""},
		{"one revision as the work comes into focus", state.State{Title: "Fence", TitleRevisions: 1, TitleSeq: 9, LastUserSeq: 3}, out("Fence repainting", ""), "Fence repainting", ""},
		{"then it stays put", state.State{Title: "Fence repainting", TitleRevisions: 2, TitleSeq: 9, LastUserSeq: 3}, out("Fence and gate", "Gate too."), "", "Gate too."},
		{"until the user moves the subject", state.State{Title: "Fence repainting", TitleRevisions: 2, TitleSeq: 9, LastUserSeq: 12}, out("Fence and gate", ""), "Fence and gate", ""},
		{"a chosen title is kept", state.State{Title: "My fence job", TitlePinned: true, LastUserSeq: 12}, out("Fence and gate", "Gate too."), "", "Gate too."},
	}
	for _, c := range cases {
		st := c.st
		title, sum := acceptSummary(&st, "Development", c.out)
		if title != c.wantTitle || sum != c.wantSum {
			t.Errorf("%s: got %q, %q; want %q, %q", c.name, title, sum, c.wantTitle, c.wantSum)
		}
	}
}

// Old TestSummaryInput: what the dedicated summary call reads: the title
// as it stands, the user's words (early and recent in full, the middle
// counted, session commands left out), what they were told, and how the
// work stands. The old "the agent says the work is finished" line came from
// the orchestrator's yield, which is gone; the agent's latest report stands
// in its place.
func TestCoreSummaryInput(t *testing.T) {
	st := state.New()
	seq := int64(0)
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	add := func(ev event.Event) {
		seq++
		ev.Seq, ev.Time = seq, at.Add(time.Duration(seq)*time.Minute)
		st.Apply(ev)
	}
	add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: "/home/u/Development"}))
	add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/home/u/Development"}))
	add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Repaint the back fence blue.\nTwo coats."}))
	for i := 0; i < 20; i++ {
		add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "note " + string(rune('a'+i))}))
	}
	add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "/stop-relay"}))
	add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "/model opus high"}))
	add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "/home/u/fence.jpg is the colour"}))
	add(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "First coat is on."}))
	add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "Both coats are dry."}))

	in := summaryInput(st, "Development", at.Add(time.Hour))
	if strings.Contains(in, "/stop-relay") || strings.Contains(in, "/model opus") {
		t.Errorf("relay commands should stay out of the input:\n%s", in)
	}
	for _, want := range []string{
		"Current title: none yet.",
		"Current summary: none yet.",
		"Project directory (not a title): Development",
		"1. [", "Repaint the back fence blue. Two coats.",
		"(6 more messages in between)",
		"21. [", "note t",
		"22. [", "/home/u/fence.jpg is the colour",
		"First coat is on.",
		"does the work in this session. Its latest report", "Both coats are dry.",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("input lacks %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "note e\n") || strings.Contains(in, "note h\n") {
		t.Errorf("the middle of a long conversation should be counted, not quoted:\n%s", in)
	}
	add(event.New(event.ThreadTitle, event.ActorEagent, event.ThreadTitleData{Title: "My fence job", Description: "Painting.", Pin: true}))
	in = summaryInput(st, "Development", at.Add(time.Hour))
	if !strings.Contains(in, `Current title (chosen on purpose; return it unchanged): "My fence job"`) || !strings.Contains(in, `Current summary: "Painting."`) {
		t.Errorf("a pinned title should be marked:\n%s", in)
	}
}

// Old TestThreadTitleState: thread.title folds into state by who wrote it.
// Running summaries count as revisions and never unpin; a dedicated
// summarizer's calls are its own usage (they were the narrator's); a title
// set on request pins; the app clearing its title unpins.
func TestCoreThreadTitleState(t *testing.T) {
	st := state.New()
	seq := int64(0)
	add := func(actor string, d event.ThreadTitleData) {
		seq++
		ev := event.New(event.ThreadTitle, actor, d)
		ev.Seq = seq
		st.Apply(ev)
	}
	add(event.ActorSummarizer, event.ThreadTitleData{Title: "Fence", Description: "Started.", By: "summary", SeenSeq: 4, Usage: &event.Usage{Input: 900, Output: 40}})
	add(event.ActorSummarizer, event.ThreadTitleData{Title: "Fence repainting", By: "summary", SeenSeq: 9, Usage: &event.Usage{Input: 1000, Output: 50}})
	if st.Title != "Fence repainting" || st.Description != "Started." || st.TitleRevisions != 2 || st.TitleSeq != 2 || st.SummarySeq != 9 || st.TitlePinned {
		t.Fatalf("after two summaries: %+v", []any{st.Title, st.Description, st.TitleRevisions, st.TitleSeq, st.SummarySeq, st.TitlePinned})
	}
	if u := st.Totals[event.ActorSummarizer]; u.Input != 1900 || u.Output != 90 {
		t.Fatalf("summary usage should count as the summarizer's: %+v", u)
	}
	add(event.ActorEagent, event.ThreadTitleData{Title: "My fence job", Pin: true})
	add(event.ActorEagent, event.ThreadTitleData{Description: "Two coats on.", By: "summary", SeenSeq: 15})
	if st.Title != "My fence job" || !st.TitlePinned || st.Description != "Two coats on." || st.TitleRevisions != 2 {
		t.Fatalf("after a pin: %q pinned=%v desc=%q revisions=%d", st.Title, st.TitlePinned, st.Description, st.TitleRevisions)
	}
	add(event.ActorHarness, event.ThreadTitleData{Title: "Fence repainting", By: "finalechat"})
	if st.Title != "Fence repainting" || st.TitlePinned {
		t.Fatalf("after the app unpinned: %q pinned=%v", st.Title, st.TitlePinned)
	}
}

// coreFenceBrain is a model that keeps the title and summary when asked,
// inline, and stops the agent at its turn's end.
func coreFenceBrain(msgs []map[string]any) reply {
	tail, all := lastUserText(msgs), allText(msgs)
	if msgs[len(msgs)-1]["role"] == "tool" {
		return hold()
	}
	var calls []event.ToolCall
	ended := strings.Contains(all, "Both coats are on") || strings.Contains(tail, "No agent is running")
	if strings.Contains(tail, "finished a turn") {
		calls = append(calls, stopsAndReports("Both coats are on; stopping it as you asked.").calls...)
	}
	if strings.Contains(tail, coreThreadAsk) {
		summary := "Painting the back fence blue. Just started."
		if ended {
			summary = "Back fence painted blue. Both coats are on."
		}
		calls = append(calls, tc("update_thread", `{"title":"Fence repainting","summary":"`+summary+`"}`))
	}
	if len(calls) == 0 {
		return hold()
	}
	return reply{calls: calls}
}

// Old TestSummaryBeforeABatchSessionEnds: a batch session that ends before
// the schedule comes round still leaves the thread a title and a summary
// of how it ended; the request itself is never sent as the summary; one
// title, set once; a dedicated summarizer's calls are counted as its own.
// (Delivery to the phone thread is the phone mirror's: here the record the
// mirror sends from.)
func TestCoreSummaryBeforeABatchSessionEnds(t *testing.T) {
	check := func(t *testing.T, rt *Runtime) []event.ThreadTitleData {
		t.Helper()
		evs := mustReadCore(t, rt.sess.Path)
		st := state.Replay(evs)
		if st.EndReason != "done" || st.Title != "Fence repainting" || st.Description != "Back fence painted blue. Both coats are on." {
			t.Fatalf("end %q title %q summary %q", st.EndReason, st.Title, st.Description)
		}
		// After the agent stopped and before the session ended, the title
		// and summary were brought up once more: the row says how it ended.
		var stopped, final int64
		for _, ev := range evs {
			switch ev.Type {
			case event.RelayEnd:
				stopped = ev.Seq
			case event.ThreadTitle:
				var d event.ThreadTitleData
				_ = ev.Decode(&d)
				if stopped > 0 && d.SeenSeq >= stopped {
					final = ev.Seq
				}
			case event.SessionEnd:
				if final == 0 || final > ev.Seq {
					t.Fatalf("no summary covered the end before the session ended (agent stopped at seq %d)", stopped)
				}
			}
		}
		titles := coreTitles(evs)
		named := 0
		for _, d := range titles {
			if strings.Contains(d.Description, "paint the fence blue") {
				t.Fatalf("the request was sent as the summary: %+v", d)
			}
			if d.Title != "" {
				named++
			}
		}
		if named != 1 {
			t.Fatalf("thread.title events = %+v", titles)
		}
		return titles
	}

	t.Run("inline", func(t *testing.T) {
		coreSummariesOn(t, false)
		rt, _, _ := batch(t, "paint the fence blue. Stop when done.", "Both coats are on.", func(_ string, msgs []map[string]any) reply { return coreFenceBrain(msgs) })
		check(t, rt)
	})

	t.Run("dedicated", func(t *testing.T) {
		coreSummariesOn(t, false)
		t.Setenv("EAGENT_TEST_KEY", "x")
		seat := newFakeSeat("Both coats are on.")
		useFakeRelays(t, seat)
		var mu sync.Mutex
		summaryCalls := 0
		model := newScripted(func(_ string, msgs []map[string]any) reply {
			all := allText(msgs)
			if strings.Contains(all, coreSummaryMarker) {
				mu.Lock()
				summaryCalls++
				mu.Unlock()
				if strings.Contains(all, "Both coats are on") {
					return reply{text: "```json\n{\"title\": \"Fence repainting\", \"summary\": \"Back fence painted blue. Both coats are on.\"}\n```"}
				}
				return reply{text: `{"title":"Fence repainting","summary":"Painting the back fence blue. Just started."}`}
			}
			if strings.Contains(lastUserText(msgs), coreThreadAsk) {
				t.Error("an inline thread ask reached the model with a dedicated summarizer")
			}
			return coreFenceBrain(msgs)
		})
		defer model.srv.Close()
		cfg := testConfig(model.srv.URL)
		cfg.Summarizer.Mode = config.SummarizerDedicated
		ui := &fakeUI{}
		rt, err := New(cfg, Options{Project: t.TempDir(), Prompt: "paint the fence blue. Stop when done."}, ui)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if code := rt.Run(ctx); code != 0 {
			t.Fatalf("exit %d; logs %v", code, ui.logged())
		}
		titles := check(t, rt)
		mu.Lock()
		n := summaryCalls
		mu.Unlock()
		for _, d := range titles {
			if d.By != "summary" || d.Usage == nil || d.Usage.Input == 0 {
				t.Fatalf("summary event = %+v", d)
			}
		}
		if got := rt.st.Totals[event.ActorSummarizer].Input; n == 0 || got != n*1000 {
			t.Fatalf("summarizer input tokens = %d after %d calls", got, n)
		}
	})
}

// Old TestSummaryFollowsTheSession: in a session that stays open the
// summary follows the conversation, an unchanged title is not set again,
// and a title the user gave the thread in the app wins from then on. (The
// app's side, reading the rename off the thread, is the phone mirror's
// adoptThread; here it is the event that records it.)
func TestCoreSummaryFollowsTheSession(t *testing.T) {
	coreSummariesOn(t, true)
	var seen tails
	s := startSession(t, "paint the fence blue", func(_ string, msgs []map[string]any) reply {
		tail, all := lastUserText(msgs), allText(msgs)
		if msgs[len(msgs)-1]["role"] == "tool" || !strings.Contains(tail, coreThreadAsk) {
			return hold()
		}
		seen.add(tail)
		switch {
		case strings.Contains(all, "and the shed"):
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Fence, gate and shed","summary":"Repainting the fence, the gate and the shed. Waiting on a colour."}`)}}
		case strings.Contains(all, "also the gate"):
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Fence repainting","summary":"Repainting the fence and the gate. Waiting on a colour."}`)}}
		}
		return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Fence repainting","summary":"Repainting the back fence blue. Just started."}`)}}
	}, nil)
	shows := func(title, desc string) func() bool {
		return func() bool {
			var ok bool
			s.read(func(st *state.State) { ok = st.Title == title && st.Description == desc })
			return ok
		}
	}
	s.waitFor("the first title and summary", shows("Fence repainting", "Repainting the back fence blue. Just started."))
	s.settled()
	s.type_("also the gate")
	s.waitFor("the summary to follow the second message", shows("Fence repainting", "Repainting the fence and the gate. Waiting on a colour."))
	s.settled()
	set := 0
	for _, d := range coreTitles(s.events(event.ThreadTitle)) {
		if d.Title == "Fence repainting" {
			set++
		}
	}
	if set != 1 {
		t.Fatalf("the unchanged title was set %d times", set)
	}
	// The user renames the thread in the app (as adoptThread records it),
	// then writes again.
	s.rt.sync(func() {
		s.rt.append(event.New(event.ThreadTitle, event.ActorHarness, event.ThreadTitleData{Title: "My fence job", Pin: true, By: "finalechat"}))
	})
	s.type_("and the shed")
	s.waitFor("the summary to follow the third message", shows("My fence job", "Repainting the fence, the gate and the shed. Waiting on a colour."))
	s.settled()
	if seen.find(`Title: "My fence job" (the user chose it: leave it)`) == "" {
		t.Fatal("the model was not told the title is the user's")
	}
	path := s.rt.sess.Path
	s.stop()
	st := state.Replay(mustReadCore(t, path))
	if st.Title != "My fence job" || !st.TitlePinned || st.Description != "Repainting the fence, the gate and the shed. Waiting on a colour." {
		t.Fatalf("replayed title %q pinned=%v summary %q", st.Title, st.TitlePinned, st.Description)
	}
}

// Old TestSummariesOff: with EAGENT_SUMMARIES=off (as every other test in
// the package runs) nothing asks about the title and summary and nothing is
// written, inline or dedicated.
func TestCoreSummariesOff(t *testing.T) {
	for _, mode := range []string{config.SummarizerInline, config.SummarizerDedicated} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("EAGENT_TEST_KEY", "x")
			useFakeRelays(t, newFakeSeat("Both coats are on."))
			var mu sync.Mutex
			asked := 0
			model := newScripted(func(_ string, msgs []map[string]any) reply {
				if strings.Contains(allText(msgs), coreSummaryMarker) || strings.Contains(lastUserText(msgs), coreThreadAsk) {
					mu.Lock()
					asked++
					mu.Unlock()
				}
				return coreFenceBrain(msgs)
			})
			defer model.srv.Close()
			cfg := testConfig(model.srv.URL)
			cfg.Summarizer.Mode = mode
			rt, err := New(cfg, Options{Project: t.TempDir(), Prompt: "paint the fence. Stop when done."}, &fakeUI{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if code := rt.Run(ctx); code != 0 {
				t.Fatalf("exit %d", code)
			}
			mu.Lock()
			defer mu.Unlock()
			if titles := coreTitles(mustReadCore(t, rt.sess.Path)); asked != 0 || len(titles) != 0 {
				t.Fatalf("summaries ran with EAGENT_SUMMARIES=off: %d asks, %+v", asked, titles)
			}
		})
	}
}

// coreThread is a FinaleChat service holding one thread, enough for eagent
// summarize: GET and PATCH of a thread, with the app's pins.
type coreThread struct {
	mu             sync.Mutex
	srv            *httptest.Server
	exists, pinned bool
	title, desc    string
	patches        int
}

func newCoreThread() *coreThread {
	c := &coreThread{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/api/v1/threads/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !c.exists {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no such thread"}`)
			return
		}
		if r.Method == http.MethodPatch {
			var p finalechat.PatchRequest
			_ = json.NewDecoder(r.Body).Decode(&p)
			c.patches++
			if p.Title != "" && !c.pinned {
				c.title = p.Title
			}
			if p.Description != "" {
				c.desc = p.Description
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": map[string]any{"id": "t1", "title": c.title, "description": c.desc, "summary": c.desc, "title_pinned": c.pinned}})
	}))
	return c
}

func (c *coreThread) shows() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.title, c.desc
}

// Old TestSummarizeStoredSession: eagent summarize gives a stored session's
// thread a fresh title and summary from its log, leaves the log alone,
// sends nothing on a dry run or when there is no thread, does nothing when
// the thread is current, and keeps a title the user chose.
func TestCoreSummarizeStoredSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: project}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "paint the fence blue"}),
		event.New(event.Message, event.ActorEagent, event.MessageData{Text: "Codex put both coats on."}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	id, path := sess.ID, sess.Path
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	before := len(mustReadCore(t, path))

	s := newScripted(func(string, []map[string]any) reply {
		return reply{text: `{"title":"Fence repainting","summary":"Back fence painted blue. Both coats are on."}`}
	})
	defer s.srv.Close()
	routes, err := testConfig(s.srv.URL).Model.Routes()
	if err != nil {
		t.Fatal(err)
	}
	model := llm.NewClient(routes[0])
	set, err := prompts.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	fp := newCoreThread()
	defer fp.srv.Close()
	client := &finalechat.Client{BaseURL: fp.srv.URL, Token: "fc_test"}
	info, err := store.Resolve(store.Root(project), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	res, err := SummarizeSession(ctx, model, set, client, project, info, false)
	if err != nil || res.Sent || res.Thread || res.Title != "Fence repainting" || res.Skipped == "" {
		t.Fatalf("without a thread: %+v, %v", res, err)
	}
	fp.mu.Lock()
	fp.exists, fp.title, fp.desc = true, "x", "paint the fence blue"
	fp.mu.Unlock()
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, true)
	if err != nil || res.Sent || res.Title != "Fence repainting" || res.OldTitle != "x" {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	if title, _ := fp.shows(); title != "x" {
		t.Fatalf("a dry run changed the thread to %q", title)
	}
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || !res.Sent {
		t.Fatalf("summarize: %+v, %v", res, err)
	}
	if title, desc := fp.shows(); title != "Fence repainting" || desc != "Back fence painted blue. Both coats are on." {
		t.Fatalf("thread shows %q / %q", title, desc)
	}
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || res.Sent || res.Skipped != "already current" {
		t.Fatalf("second run: %+v, %v", res, err)
	}
	fp.mu.Lock()
	fp.title, fp.pinned, fp.desc = "My fence job", true, "old"
	fp.mu.Unlock()
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || !res.Sent || res.Title != "" || res.Description == "" {
		t.Fatalf("pinned title: %+v, %v", res, err)
	}
	if title, _ := fp.shows(); title != "My fence job" {
		t.Fatalf("a pinned title became %q", title)
	}
	if after := len(mustReadCore(t, path)); after != before {
		t.Fatalf("summarize wrote to the session log: %d events, was %d", after, before)
	}
}
