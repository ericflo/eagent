package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// summaryMarker is how a scripted model tells the harness's summary call
// from a narrator turn: both go to the narrator's model.
const summaryMarker = "You write the title and summary of one work session"

// quickSummaries turns the summary call on for one test and runs its
// schedule in milliseconds. first is the delay before a session's first
// summary; a long one leaves that to the end of a batch session.
func quickSummaries(t *testing.T, first time.Duration) {
	t.Helper()
	t.Setenv("EAGENT_SUMMARIES", "on")
	old := []time.Duration{summaryFirstDelay, summaryUserDelay, summaryWorkDelay, summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry}
	ms := time.Millisecond
	summaryFirstDelay, summaryUserDelay, summaryWorkDelay = first, 40*ms, 40*ms
	summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry = 80*ms, 80*ms, 200*ms, 100*ms
	t.Cleanup(func() {
		summaryFirstDelay, summaryUserDelay, summaryWorkDelay = old[0], old[1], old[2]
		summaryTurnGap, summaryTalkGap, summaryLongGap, summaryRetry = old[3], old[4], old[5], old[6]
	})
}

func titleEvents(t *testing.T, path string) []event.ThreadTitleData {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []event.ThreadTitleData
	for _, ev := range evs {
		if ev.Type != event.ThreadTitle {
			continue
		}
		var d event.ThreadTitleData
		if err := ev.Decode(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func TestParseSummary(t *testing.T) {
	cases := []struct {
		in, title, summary string
		ok                 bool
	}{
		{`{"title": "Fence repainting", "summary": "Two coats on."}`, "Fence repainting", "Two coats on.", true},
		{"```json\n{\"title\":\"A\",\"summary\":\"B\"}\n```", "A", "B", true},
		{`Here it is: {"title":"A","description":"B"} Hope that helps.`, "A", "B", true},
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

func TestCleanTitleAndSummary(t *testing.T) {
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

func TestGenericTitle(t *testing.T) {
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

// The title is what the user finds the thread by, so it moves far less
// readily than the summary does.
func TestAcceptSummaryTitleRules(t *testing.T) {
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

// What the summary call reads: the title as it stands, the user's words
// (early and recent in full, the middle counted), what they were told, and
// the state of play.
func TestSummaryInput(t *testing.T) {
	st := state.New()
	seq := int64(0)
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	add := func(ev event.Event) {
		seq++
		ev.Seq, ev.Time = seq, at.Add(time.Duration(seq)*time.Minute)
		st.Apply(ev)
	}
	add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: "/home/u/Development"}))
	add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Repaint the back fence blue.\nTwo coats."}))
	for i := 0; i < 20; i++ {
		add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "note " + string(rune('a'+i))}))
	}
	add(event.New(event.NarratorMessage, event.ActorNarrator, event.NarratorMessageData{Text: "First coat is on."}))
	add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: true, Reason: "both coats are dry"}))

	in := summaryInput(st, "Development", at.Add(time.Hour))
	for _, want := range []string{
		"Current title: none yet.",
		"Current summary: none yet.",
		"Project directory (not a title): Development",
		"1. [", "Repaint the back fence blue. Two coats.",
		"(5 more messages in between)",
		"21. [", "note t",
		"First coat is on.",
		"The agent says the work is finished: both coats are dry",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("input lacks %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "note e\n") || strings.Contains(in, "note h\n") {
		t.Errorf("the middle of a long conversation should be counted, not quoted:\n%s", in)
	}

	// A title someone chose is passed on as one to keep.
	add(event.New(event.ThreadTitle, event.ActorOrchestrator, event.ThreadTitleData{Title: "My fence job", Description: "Painting.", Pin: true}))
	in = summaryInput(st, "Development", at.Add(time.Hour))
	if !strings.Contains(in, `Current title (chosen on purpose; return it unchanged): "My fence job"`) || !strings.Contains(in, `Current summary: "Painting."`) {
		t.Errorf("a pinned title should be marked:\n%s", in)
	}
}

// thread.title folds into state by who wrote it: the harness's own summaries
// count as revisions and never unpin, and their model call is the
// narrator's usage.
func TestThreadTitleState(t *testing.T) {
	st := state.New()
	seq := int64(0)
	add := func(actor string, d event.ThreadTitleData) {
		seq++
		ev := event.New(event.ThreadTitle, actor, d)
		ev.Seq = seq
		st.Apply(ev)
	}
	add(event.ActorHarness, event.ThreadTitleData{Title: "Fence", Description: "Started.", By: "summary", SeenSeq: 4, Usage: &event.Usage{Input: 900, Output: 40}})
	add(event.ActorHarness, event.ThreadTitleData{Title: "Fence repainting", By: "summary", SeenSeq: 9, Usage: &event.Usage{Input: 1000, Output: 50}})
	if st.Title != "Fence repainting" || st.Description != "Started." || st.TitleRevisions != 2 || st.TitleSeq != 2 || st.SummarySeq != 9 || st.TitlePinned {
		t.Fatalf("after two summaries: %+v", []any{st.Title, st.Description, st.TitleRevisions, st.TitleSeq, st.SummarySeq, st.TitlePinned})
	}
	if u := st.Totals[event.ActorNarrator]; u.Input != 1900 || u.Output != 90 {
		t.Fatalf("summary usage should count as the narrator's: %+v", u)
	}
	// The orchestrator names it at the user's request: pinned.
	add(event.ActorOrchestrator, event.ThreadTitleData{Title: "My fence job", Pin: true})
	// A later summary with no title leaves the pin alone and moves the summary.
	add(event.ActorHarness, event.ThreadTitleData{Description: "Two coats on.", By: "summary", SeenSeq: 15})
	if st.Title != "My fence job" || !st.TitlePinned || st.Description != "Two coats on." || st.TitleRevisions != 2 {
		t.Fatalf("after a pin: %q pinned=%v desc=%q revisions=%d", st.Title, st.TitlePinned, st.Description, st.TitleRevisions)
	}
	// The user cleared their title in the app and the agent's came back.
	add(event.ActorHarness, event.ThreadTitleData{Title: "Fence repainting", By: "finalechat"})
	if st.Title != "Fence repainting" || st.TitlePinned {
		t.Fatalf("after the app unpinned: %q pinned=%v", st.Title, st.TitlePinned)
	}
}

// A batch session that ends before the schedule comes round still leaves
// the thread list a title and a summary of how it ended.
func TestSummaryBeforeABatchSessionEnds(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	quickSummaries(t, time.Hour)
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case model == "orch":
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"both coats are on"}`)}}
		case strings.Contains(all, summaryMarker):
			if !strings.Contains(all, "paint the fence blue") || !strings.Contains(all, "both coats are on") {
				return reply{text: `{"title":"Wrong input","summary":"The summary call did not get the session."}`}
			}
			return reply{text: "```json\n{\"title\": \"Fence repainting\", \"summary\": \"Back fence painted blue. Both coats are on.\"}\n```"}
		default:
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	cfg := fakePhoneConfig(t, project, s.srv.URL, fp)
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "paint the fence blue"}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if title, desc := fp.threadTitle(); title != "Fence repainting" || desc != "Back fence painted blue. Both coats are on." {
		t.Fatalf("thread shows %q / %q", title, desc)
	}
	// The request itself was never sent as the summary.
	for _, p := range fp.postBodies() {
		if p["description"] != nil || p["summary"] != nil {
			t.Fatalf("a post carried a description: %v", p)
		}
	}
	// One call names the thread. A second may follow the narrator's last
	// word and find nothing to change; it is recorded for its usage alone.
	got := titleEvents(t, rt.sess.Path)
	named, lastSeen := 0, int64(0)
	for _, d := range got {
		if d.By != "summary" || d.Pin || d.Usage == nil || d.Usage.Input == 0 || d.SeenSeq <= lastSeen {
			t.Fatalf("summary event = %+v", d)
		}
		lastSeen = d.SeenSeq
		if d.Title == "Fence repainting" && d.Description != "" {
			named++
		} else if d.Title != "" || d.Description != "" {
			t.Fatalf("unexpected summary event = %+v", d)
		}
	}
	if named != 1 {
		t.Fatalf("thread.title events = %+v", got)
	}
	if rt.st.EndReason != "done" || rt.st.Title != "Fence repainting" || rt.st.SummarySeq != lastSeen {
		t.Fatalf("end %q title %q seen %d", rt.st.EndReason, rt.st.Title, rt.st.SummarySeq)
	}
	// Every summary call is counted with the narrator's usage.
	calls := 0
	for _, ev := range mustRead(t, rt.sess.Path) {
		if ev.Type == event.Assistant && ev.Actor == event.ActorNarrator {
			calls++
		}
	}
	if want := (calls + len(got)) * 1000; rt.st.Totals[event.ActorNarrator].Input != want {
		t.Fatalf("narrator input tokens = %d, want %d (%d turns + %d summaries)", rt.st.Totals[event.ActorNarrator].Input, want, calls, len(got))
	}
}

// In a session that stays open the summary follows the conversation, and a
// title the user typed in the app wins over the session's own from then on.
func TestSummaryFollowsTheSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	quickSummaries(t, 20*time.Millisecond)
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case model == "orch":
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for the user"}`)}}
		case strings.Contains(all, summaryMarker):
			switch {
			case strings.Contains(all, "and the shed"):
				return reply{text: `{"title":"Fence, gate and shed","summary":"Repainting the fence, the gate and the shed. Waiting on a colour."}`}
			case strings.Contains(all, "also the gate"):
				return reply{text: `{"title":"Fence repainting","summary":"Repainting the fence and the gate. Waiting on a colour."}`}
			}
			return reply{text: `{"title":"Fence repainting","summary":"Repainting the back fence blue. Just started."}`}
		default:
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	cfg := fakePhoneConfig(t, project, s.srv.URL, fp)
	rt, err := New(cfg, Options{Project: project, Interactive: true, Prompt: "paint the fence blue"}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	shows := func(title, desc string) func() bool {
		return func() bool {
			gotTitle, gotDesc := fp.threadTitle()
			return gotTitle == title && gotDesc == desc
		}
	}
	waitFor(t, "the first title and summary", shows("Fence repainting", "Repainting the back fence blue. Just started."))

	fp.userReply("also the gate")
	waitFor(t, "the summary to follow the second message", shows("Fence repainting", "Repainting the fence and the gate. Waiting on a colour."))
	// The title did not change, so it was not sent again.
	titles := 0
	for _, p := range fp.retitlePatches() {
		if p["title"] == "Fence repainting" {
			titles++
		}
	}
	if titles != 1 {
		t.Fatalf("the unchanged title was sent %d times", titles)
	}

	// The user renames the thread in the app, then writes again.
	fp.rename("My fence job")
	fp.userReply("and the shed")
	waitFor(t, "the summary to follow the third message", shows("My fence job", "Repainting the fence, the gate and the shed. Waiting on a colour."))
	waitFor(t, "the session to take the user's title", func() bool {
		ch := make(chan bool, 1)
		rt.post(func() { ch <- rt.st.Title == "My fence job" && rt.st.TitlePinned })
		select {
		case ok := <-ch:
			return ok
		case <-time.After(time.Second):
			return false
		}
	})
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("session did not stop")
	}
	var adopted bool
	for _, d := range titleEvents(t, rt.sess.Path) {
		if d.By == "finalechat" && d.Title == "My fence job" && d.Pin {
			adopted = true
		}
	}
	if !adopted {
		t.Fatalf("no thread.title event recorded the user's title: %+v", titleEvents(t, rt.sess.Path))
	}
	st := state.Replay(mustRead(t, rt.sess.Path))
	if st.Title != "My fence job" || !st.TitlePinned || st.Description != "Repainting the fence, the gate and the shed. Waiting on a colour." {
		t.Fatalf("replayed title %q pinned=%v summary %q", st.Title, st.TitlePinned, st.Description)
	}
}

// With the switch off (as every other test in this package runs) the
// narrator's model gets no summary call and nothing is written.
func TestSummariesOff(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	asked := make(chan struct{}, 8)
	brain := func(model string, msgs []map[string]any) reply {
		if strings.Contains(allText(msgs), summaryMarker) {
			asked <- struct{}{}
		}
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"done"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Interactive: false, Prompt: "paint the fence"}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	rt.Run(ctx)
	if len(asked) != 0 || len(titleEvents(t, rt.sess.Path)) != 0 {
		t.Fatalf("summaries ran with EAGENT_SUMMARIES=off: %d calls, %+v", len(asked), titleEvents(t, rt.sess.Path))
	}
}

// eagent summarize: a stored session's thread gets a fresh title and
// summary from its log, the log itself is left alone, and a dry run sends
// nothing.
func TestSummarizeStoredSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: project}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "paint the fence blue"}),
		event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: true, Reason: "both coats are on"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	id, path := sess.ID, sess.Path
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	before := len(mustRead(t, path))

	s := newScripted(func(model string, msgs []map[string]any) reply {
		return reply{text: `{"title":"Fence repainting","summary":"Back fence painted blue. Both coats are on."}`}
	})
	defer s.srv.Close()
	routes, err := testConfig(s.srv.URL).Narrator.Routes()
	if err != nil {
		t.Fatal(err)
	}
	model := llm.NewClient(routes[0])
	set, err := prompts.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	fp := newFakePhone(false)
	defer fp.srv.Close()
	client := &finalechat.Client{BaseURL: fp.srv.URL, Token: "fc_test"}
	info, err := store.Resolve(store.Root(project), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// No thread yet: the result is returned, nothing is sent.
	res, err := SummarizeSession(ctx, model, set, client, project, info, false)
	if err != nil || res.Sent || res.Thread || res.Title != "Fence repainting" || res.Skipped == "" {
		t.Fatalf("without a thread: %+v, %v", res, err)
	}

	// The thread as old sessions left it: the project's name, the request.
	fp.mu.Lock()
	fp.title, fp.desc = "x", "paint the fence blue"
	fp.mu.Unlock()
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, true)
	if err != nil || res.Sent || res.Title != "Fence repainting" || res.OldTitle != "x" {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	if title, _ := fp.threadTitle(); title != "x" {
		t.Fatalf("a dry run changed the thread to %q", title)
	}
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || !res.Sent {
		t.Fatalf("summarize: %+v, %v", res, err)
	}
	if title, desc := fp.threadTitle(); title != "Fence repainting" || desc != "Back fence painted blue. Both coats are on." {
		t.Fatalf("thread shows %q / %q", title, desc)
	}
	// Again: nothing to do.
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || res.Sent || res.Skipped != "already current" {
		t.Fatalf("second run: %+v, %v", res, err)
	}
	// A title the user chose stays; only the summary is offered.
	fp.rename("My fence job")
	fp.mu.Lock()
	fp.desc = "old"
	fp.mu.Unlock()
	res, err = SummarizeSession(ctx, model, set, client, "/somewhere/x", info, false)
	if err != nil || !res.Sent || res.Title != "" || res.Description == "" {
		t.Fatalf("pinned title: %+v, %v", res, err)
	}
	if title, _ := fp.threadTitle(); title != "My fence job" {
		t.Fatalf("a pinned title became %q", title)
	}
	if after := len(mustRead(t, path)); after != before {
		t.Fatalf("summarize wrote to the session log: %d events, was %d", after, before)
	}
}

func mustRead(t *testing.T, path string) []event.Event {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}
