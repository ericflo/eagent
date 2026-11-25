package harness

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

const (
	dossierAsk       = "Your conversation has grown long and is about to be restarted from a briefing"
	dossierAskLegacy = "This session was started by an earlier eagent, which did the work itself"
)

// dossierRequest finds the request for a dossier in a prompt: "" when the
// call is an ordinary one.
func dossierRequest(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if s, _ := msgs[i]["content"].(string); msgs[i]["role"] == "user" && (strings.Contains(s, dossierAsk) || strings.HasPrefix(s, "[eagent] "+dossierAskLegacy)) {
			return s
		}
	}
	return ""
}

// briefing writes a dossier body that cites the given places.
func briefing(cites []string) string {
	var b strings.Builder
	b.WriteString("1. WHAT THE USER WANTS: every pending PR merged and the dependencies upgraded, on main, with no branches.\n")
	b.WriteString("2. WHERE THE WORK STANDS: Codex merged #42 and #44; main is green at c92b5bad.\n")
	b.WriteString("3. DECISIONS AND CONSTRAINTS: the work happens on main only.\n")
	b.WriteString("4. OPEN: nothing is waiting on the user.\n")
	b.WriteString("5. WHAT THE USER LAST HEARD: that the PRs are merged.\n")
	b.WriteString("6. MAP:\n")
	for i, c := range cites {
		fmt.Fprintf(&b, "- topic %d: %s\n", i+1, c)
	}
	return b.String()
}

// places returns the distinct file:line places a text names.
func places(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range citation.FindAllString(text, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// dossiers returns the session's dossier events, decoded.
func (s *session) dossiers() []event.DossierData {
	var out []event.DossierData
	for _, ev := range s.events(event.Dossier) {
		var d event.DossierData
		_ = ev.Decode(&d)
		out = append(out, d)
	}
	return out
}

// The dossier is a map over the whole record. Its writer is shown every
// file and an index of the record by place, may read and search any of it
// before writing, and must cite places; the harness adds the table of files
// and the user's own words with their places. A later dossier is rebuilt
// from the whole record: its writer is shown the earlier files in outline,
// with their places, and where the earlier dossier sits.
func TestDossierIsAMapOverTheWholeRecord(t *testing.T) {
	var big atomic.Bool
	var mu sync.Mutex
	var asks []string // each dossier request, as the model saw it
	var afterRestart []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			if msgs[len(msgs)-1]["role"] != "tool" {
				mu.Lock()
				asks = append(asks, ask)
				mu.Unlock()
				file := strings.SplitN(places(ask)[0], ":", 2)[0]
				return reply{calls: []event.ToolCall{
					tc("session_read", fmt.Sprintf(`{"file":%q,"from":1,"to":30}`, file)),
					tc("session_search", `{"query":"PR"}`),
					tc("send_message", `{"text":"this must not be sent while the briefing is written"}`),
				}}
			}
			// Carry forward the places of the present briefing's map, and
			// add the places of what happened since.
			cites := places(ask)
			if len(cites) > 6 {
				cites = cites[len(cites)-6:]
			}
			for _, m := range msgs {
				if text, _ := m["content"].(string); strings.HasPrefix(text, "[Briefing.") {
					if i := strings.Index(text, "6. MAP:"); i >= 0 {
						if j := strings.Index(text[i:], "FROM EAGENT'S OWN RECORDS"); j >= 0 {
							cites = append(places(text[i:i+j]), cites...)
						}
					}
				}
			}
			return reply{text: briefing(cites)}
		}
		tail := lastUserText(msgs)
		mu.Lock()
		if len(msgs) > 1 {
			if first, _ := msgs[1]["content"].(string); strings.HasPrefix(first, "[Briefing.") {
				afterRestart = msgs
			}
		}
		mu.Unlock()
		switch {
		case strings.Contains(tail, "kept for you"):
			r := say("Noted.")
			if big.CompareAndSwap(true, false) {
				r.prompt = 25000
			}
			return r
		case strings.Contains(tail, "finished a turn"):
			return say("Codex merged #42 and #44; main is green at c92b5bad.")
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()

	// The conversation fills up: it is restarted from a dossier.
	big.Store(true)
	s.type_("@e remember that I only want work on main")
	s.waitFor("the first dossier", func() bool { return len(s.dossiers()) == 1 })
	s.settled()
	first := s.dossiers()[0]
	var files []string
	s.read(func(st *state.State) {
		for _, sub := range st.Subsessions {
			files = append(files, sub.File)
		}
	})
	if len(files) != 2 {
		t.Fatalf("subsessions = %v", files)
	}
	if first.By != "eagent" || first.CoversSeq == 0 || first.Calls != 2 || first.Usage == nil {
		t.Fatalf("dossier = by %q covers %d calls %d usage %v", first.By, first.CoversSeq, first.Calls, first.Usage)
	}
	if want := []string{"read " + files[0] + ":1-30", "search PR"}; strings.Join(first.Consulted, "|") != strings.Join(want, "|") {
		t.Fatalf("consulted %q, want %q", first.Consulted, want)
	}
	lines := map[string]int{}
	s.rt.sync(func() { lines = s.rt.recordLines() })
	if n, bad := checkCitations(first.Text, lines); n < 6 || len(bad) != 0 {
		t.Fatalf("the dossier cites %d places, %d of them wrong (%v):\n%s", n, len(bad), bad, first.Text)
	}
	for _, want := range []string{
		"6. MAP:", "FROM EAGENT'S OWN RECORDS", "THE RECORD'S FILES, oldest first",
		"- " + files[0] + ": lines 1-", "AGENTS", "- codex: ", "THE USER'S MESSAGES, word for word",
		brief, "@e remember that I only want work on main (kept for you: it is addressed to you)",
		"THE AGENT'S TURN ENDS", "Codex, 4s, 3 tool calls: Merged #42 and #44",
	} {
		if !strings.Contains(first.Text, want) {
			t.Fatalf("the dossier lacks %q:\n%s", want, first.Text)
		}
	}
	// What the writer was given: every file, and the places of what happened.
	mu.Lock()
	ask := asks[0]
	mu.Unlock()
	for _, want := range []string{
		"- " + files[0] + ": lines 1-", "the file being written now", "the user wrote: " + brief[:40], "Codex finished a turn: Merged #42", "you told the user: Codex merged #42",
		"Build the briefing from the record every time, the whole of it, not from the briefing before",
		"A topic that has gone quiet stays on the map",
	} {
		if !strings.Contains(ask, want) {
			t.Fatalf("the dossier request lacks %q:\n%s", want, ask)
		}
	}
	if strings.Contains(ask, "Before your last briefing, in outline") {
		t.Fatalf("the first dossier request speaks of an earlier briefing:\n%s", ask)
	}
	// The lookups belong to the writing: nothing of them is in the record,
	// and nothing else ran.
	for _, ev := range s.events(event.ToolResult) {
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.Name == "session_read" || d.Name == "session_search" {
			t.Fatalf("a dossier lookup was recorded: %+v", d)
		}
	}
	for _, m := range s.ui.said() {
		if strings.Contains(m, "must not be sent") {
			t.Fatal("a message went out while the dossier was being written")
		}
	}

	// The new conversation opens with the dossier, and the model can read
	// any place it cites.
	s.type_("@e where does that stand?")
	s.waitFor("an answer after the restart", func() bool { return s.ui.messageCount() == 3 })
	s.settled()
	mu.Lock()
	opening, _ := afterRestart[1]["content"].(string)
	n := len(afterRestart)
	mu.Unlock()
	if !strings.Contains(opening, "6. MAP:") || !strings.Contains(opening, "the record is the source") || n > 6 {
		t.Fatalf("after the restart the conversation has %d messages and opens with:\n%s", n, opening)
	}

	// A second restart: the map reaches back through both files, and the
	// table says where the earlier dossier sits.
	big.Store(true)
	s.type_("@e and remember that too")
	s.waitFor("the second dossier", func() bool { return len(s.dossiers()) == 2 })
	s.settled()
	second := s.dossiers()[1]
	s.rt.sync(func() { lines = s.rt.recordLines() })
	if len(lines) != 3 {
		t.Fatalf("record files = %v", lines)
	}
	if n, bad := checkCitations(second.Text, lines); n < 8 || len(bad) != 0 {
		t.Fatalf("the second dossier cites %d places, %d wrong (%v)", n, len(bad), bad)
	}
	mapPart := second.Text[strings.Index(second.Text, "6. MAP:"):strings.Index(second.Text, "FROM EAGENT'S OWN RECORDS")]
	if !strings.Contains(mapPart, files[0]+":") || !strings.Contains(mapPart, files[1]+":") {
		t.Fatalf("the second map does not reach into both earlier files:\n%s", mapPart)
	}
	if want := "- " + files[1] + ": lines 1-"; !strings.Contains(second.Text, want) || !strings.Contains(second.Text, "opens with the briefing written then ("+files[1]+":2, by eagent)") {
		t.Fatalf("the table of files does not place the earlier dossier:\n%s", second.Text)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(asks[1], "opens with the briefing written then ("+files[1]+":2, by eagent)") {
		t.Fatalf("the second writer was not shown where the first dossier is:\n%s", asks[1])
	}
	// The second writer is shown the first file too, by place, so its map
	// is built from the record and not from the map before it.
	outline := asks[1]
	if i := strings.Index(outline, "Before your last briefing, in outline"); i < 0 {
		t.Fatalf("the second writer was not shown the earlier stretch:\n%s", asks[1])
	} else {
		outline = outline[i:strings.Index(outline, "Since your last briefing, line by line")]
	}
	for _, want := range []string{files[0] + ":", "the user wrote: " + brief[:40], "Codex finished a turn: Merged #42"} {
		if !strings.Contains(outline, want) {
			t.Fatalf("the outline of the earlier record lacks %q:\n%s", want, outline)
		}
	}
	if !strings.Contains(asks[1], "- "+files[1]+":2 ") || !strings.Contains(asks[1], "a briefing was written here (by eagent), with its own MAP") {
		t.Fatalf("the index does not place the earlier dossier:\n%s", asks[1])
	}
	// The dossier's calls are the same request as every other call, but
	// for their messages: one effort, one output limit, one tool list.
	reqs := s.model.requests(testModel)
	for i, q := range reqs {
		if q.Rest != reqs[0].Rest || string(q.Tools) != string(reqs[0].Tools) || string(q.Messages[0]) != string(reqs[0].Messages[0]) {
			t.Fatalf("call %d differs from the first outside its messages:\n%s\n%s", i, reqs[0].Rest, q.Rest)
		}
	}
}

// A dossier without a map is sent back once with the reason. If the second
// try cites places that do not exist, it is kept and the wrong places are
// named, so nothing sends the reader to a line that is not there.
func TestDossierWithoutAMapIsSentBackAndWrongPlacesAreNamed(t *testing.T) {
	var big atomic.Bool
	var mu sync.Mutex
	var sentBack string
	attempt := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			mu.Lock()
			defer mu.Unlock()
			attempt++
			if attempt == 1 {
				return reply{text: briefing(nil)}
			}
			sentBack = lastUserText(msgs)
			return reply{text: briefing(append(places(ask)[:3], "1111111111111.jsonl:5"))}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") {
			r = say("Hello.")
		}
		if big.CompareAndSwap(true, false) {
			r.prompt = 25000
		}
		return r
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e hello")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	d := s.dossiers()[0]
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(sentBack, "That cannot be used: its MAP cites 0 places in the record") {
		t.Fatalf("sent back with %q", sentBack)
	}
	if d.By != "eagent" || d.Calls != 2 || !strings.Contains(d.Text, "These do not exist in the record, so disregard them: 1111111111111.jsonl:5)") {
		t.Fatalf("dossier by %q after %d calls:\n%s", d.By, d.Calls, d.Text)
	}
}

// When the model cannot write a dossier the harness writes one from its
// records: no judgement in it, but every place is still there.
func TestHarnessWritesTheDossierWhenTheModelCannot(t *testing.T) {
	var big atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: "ok"}
		}
		tail := lastUserText(msgs)
		if strings.Contains(tail, "finished a turn") {
			return say("Codex merged #42 and #44.")
		}
		r := hold()
		if strings.Contains(tail, "kept for you") {
			r = say("Hello.")
		}
		if big.CompareAndSwap(true, false) {
			r.prompt = 25000
		}
		return r
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e hello")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	d := s.dossiers()[0]
	if d.By != "harness" {
		t.Fatalf("by %q", d.By)
	}
	for _, want := range []string{"could not write this briefing", "WHAT THE USER LAST HEARD", "Codex merged #42 and #44.", "CODEX'S LAST REPORT", "Merged #42 and #44; main is green", "THE RECORD'S FILES", "THE USER'S MESSAGES, word for word", brief} {
		if !strings.Contains(d.Text, want) {
			t.Fatalf("the dossier lacks %q:\n%s", want, d.Text)
		}
	}
	lines := map[string]int{}
	s.rt.sync(func() { lines = s.rt.recordLines() })
	if n, bad := checkCitations(d.Text, lines); n < 2 || len(bad) != 0 {
		t.Fatalf("places: %d, wrong %v", n, bad)
	}
}

// Nothing is lost at the seam: what arrives while the dossier is being
// written goes to the agent at once and is in the new conversation.
func TestWhatArrivesWhileTheDossierIsWrittenIsKept(t *testing.T) {
	var big atomic.Bool
	release := make(chan struct{})
	var mu sync.Mutex
	var after []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3]), block: release}
		}
		mu.Lock()
		if first, _ := msgs[1]["content"].(string); strings.HasPrefix(first, "[Briefing.") {
			after = msgs
		}
		mu.Unlock()
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") {
			r = say("Hello.")
		}
		if big.CompareAndSwap(true, false) {
			r.prompt = 25000
		}
		return r
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e hello")
	s.waitFor("the dossier to be asked for", func() bool {
		compacting := false
		s.rt.sync(func() { compacting = s.rt.turns.compacting })
		return compacting
	})
	s.type_("one more thing: also tag v2 when you are done")
	s.waitFor("the message to reach the agent while the dossier is written", func() bool { return len(s.seat.sentTexts()) == 2 })
	close(release)
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("a look after the restart", func() bool { mu.Lock(); defer mu.Unlock(); return after != nil })
	s.settled()
	d := s.dossiers()[0]
	var late event.Event
	for _, ev := range s.events(event.UserMessage) {
		late = ev
	}
	if late.Seq <= d.CoversSeq {
		t.Fatalf("the late message (seq %d) is inside what the dossier covers (%d)", late.Seq, d.CoversSeq)
	}
	mu.Lock()
	defer mu.Unlock()
	rest := allText(after[2:])
	for _, want := range []string{"one more thing: also tag v2 when you are done", "Passed to Codex as written", "Codex finished a turn"} {
		if !strings.Contains(rest, want) {
			t.Fatalf("the new conversation lacks %q:\n%s", want, rest)
		}
	}
}

// A conversation the provider refuses as too long cannot be the prompt for
// its own dossier. The writer then works from the files alone.
func TestRefusedConversationIsRestartedFromTheFiles(t *testing.T) {
	var tooLong atomic.Bool
	var mu sync.Mutex
	var fresh []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			if msgs[len(msgs)-1]["role"] != "tool" {
				mu.Lock()
				fresh = msgs
				mu.Unlock()
				return reply{calls: []event.ToolCall{tc("session_read", fmt.Sprintf(`{"file":%q}`, strings.SplitN(places(ask)[0], ":", 2)[0]))}}
			}
			tooLong.Store(false)
			return reply{text: briefing(places(ask)[:4])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: `{"error":{"message":"prompt is too long: 250000 tokens > 200000 maximum","type":"invalid_request_error"}}`}
		}
		if strings.Contains(lastUserText(msgs), "kept for you") {
			return say("Here.")
		}
		return hold()
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	tooLong.Store(true)
	s.type_("@e are you there?")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("the answer after the restart", func() bool { return s.ui.messageCount() == 1 })
	d := s.dossiers()[0]
	if d.By != "eagent" || len(d.Consulted) != 1 || !strings.HasPrefix(d.Consulted[0], "read ") {
		t.Fatalf("dossier by %q, consulted %v", d.By, d.Consulted)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(fresh) != 2 || !strings.Contains(lastUserText(fresh), "You no longer have the conversation in front of you") {
		t.Fatalf("the writer was given %d messages; the last:\n%s", len(fresh), lastUserText(fresh))
	}
	if len(s.events(event.Error)) != 0 {
		t.Fatal("the refusal was recorded as an outage")
	}
}

// oldSession writes a session the way the three-actor eagent did: two
// files, the second opening with the dossier a task worker wrote.
func oldSession(t *testing.T) (project string, sess *store.Session, files []string) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	project = t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	add := func(evs ...event.Event) {
		for _, ev := range evs {
			if _, err := sess.Append(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := sess.Current()
	add(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Models: map[string]string{"orchestrator": "glm-5", "task": "glm-5", "narrator": "glm-5"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: first, Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "write the release notes for v2"}),
		event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{Text: "drafting", Usage: event.Usage{Input: 900, Output: 20}}),
		event.New(event.Message, event.LegacyNarrator, event.MessageData{Text: "The release notes are drafted in NOTES.md; the changelog section is still empty."}),
	)
	next, err := sess.NewSubsessionName(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: next, InputTokens: 151000}))
	if _, err := sess.OpenSubsession(next); err != nil {
		t.Fatal(err)
	}
	add(
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: 1, Reason: "rollover"}),
		event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "1. USER'S GOAL: release notes for v2.\n5. MAP: the request " + first + ":3; the draft " + first + ":5"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "keep the tone dry, no exclamation marks"}),
		event.New(event.Message, event.LegacyNarrator, event.MessageData{Text: "Understood: dry tone. The changelog is next."}),
		event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: false, Reason: "waiting for the user to confirm the changelog"}),
	)
	sess.Close()
	return project, sess, []string{first, next}
}

// resumeOld resumes an old session with a new message from the user.
func resumeOld(t *testing.T, project string, sess *store.Session, brain func(model string, msgs []map[string]any) reply) (*session, *[]relay.Spec) {
	t.Helper()
	seat := newFakeSeat("Filled in the changelog.")
	launched, _ := useFakeRelays(t, seat)
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Interactive: true, Prompt: "fill in the changelog too"}, ui, sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s, launched
}

// startsAnAgent is what the model does with the user's message once the
// old session has been picked up: no agent is running, so it starts one
// and passes the message on.
func startsAnAgent(msgs []map[string]any) (reply, bool) {
	if strings.Contains(lastUserText(msgs), "No agent is running: if they want one, start it") && msgs[len(msgs)-1]["role"] != "tool" {
		return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`), tc("relay_forward", `{}`)}}, true
	}
	return reply{}, false
}

// A session written by the three-actor eagent is picked up with nothing
// lost: before anything else the model writes a dossier from the old
// record (every file, the old dossiers among them), with a map into it,
// and the session continues in a new subsession. The old files stay as
// they are and stay reachable.
func TestOldSessionIsContinuedFromADossierWrittenFromItsRecord(t *testing.T) {
	project, sess, files := oldSession(t)
	var mu sync.Mutex
	var ask string
	var asked []map[string]any
	s, launched := resumeOld(t, project, sess, func(_ string, msgs []map[string]any) reply {
		if a := dossierRequest(msgs); a != "" {
			if msgs[len(msgs)-1]["role"] != "tool" {
				mu.Lock()
				ask, asked = a, msgs
				mu.Unlock()
				return reply{calls: []event.ToolCall{
					tc("session_read", fmt.Sprintf(`{"file":%q,"from":1,"to":12}`, files[1])),
					tc("session_read", fmt.Sprintf(`{"file":%q,"from":1,"to":6}`, files[0])),
				}}
			}
			return reply{text: briefing([]string{files[0] + ":3", files[0] + ":5", files[1] + ":2", files[1] + ":3", files[1] + ":5"})}
		}
		if r, ok := startsAnAgent(msgs); ok {
			return r
		}
		return hold()
	})
	s.waitFor("the message to reach a new agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if got := s.seat.sentTexts()[0]; got != "fill in the changelog too" {
		t.Fatalf("the agent was sent %q", got)
	}
	if len(*launched) != 1 {
		t.Fatalf("launched %d agents", len(*launched))
	}
	var subs []state.Subsession
	s.read(func(st *state.State) { subs = append(subs, st.Subsessions...) })
	if len(subs) != 3 || subs[2].Reason != "upgrade" || subs[0].File != files[0] || subs[1].File != files[1] {
		t.Fatalf("subsessions = %+v", subs)
	}
	d := s.dossiers()
	last := d[len(d)-1]
	if len(d) != 2 || last.By != "eagent" || len(last.Consulted) != 2 {
		t.Fatalf("dossiers = %d, the last by %q after consulting %v", len(d), last.By, last.Consulted)
	}
	lines := map[string]int{}
	s.rt.sync(func() { lines = s.rt.recordLines() })
	if n, bad := checkCitations(last.Text, lines); n < 5 || len(bad) != 0 {
		t.Fatalf("the dossier cites %d places, %d of them wrong (%v)", n, len(bad), bad)
	}
	for _, want := range []string{
		"6. MAP:", "- " + files[0] + ": lines 1-6", "from the earlier eagent that did the work itself",
		"opens with the briefing written then (" + files[1] + ":2,",
		"- " + subs[2].File + ": opens with this briefing",
		files[0] + ":3] write the release notes for v2", files[1] + ":3] keep the tone dry, no exclamation marks",
	} {
		if !strings.Contains(last.Text, want) {
			t.Fatalf("the dossier lacks %q:\n%s", want, last.Text)
		}
	}
	// What the writer was given: no conversation, the whole record by
	// place, and where the old dossier sits.
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 2 {
		t.Fatalf("the writer was given %d messages, want the system prompt and the request", len(asked))
	}
	for _, want := range []string{
		"You have no conversation to work from, only the record",
		"Before your last briefing, in outline", files[0] + ":3 ", "the user wrote: write the release notes for v2",
		"a briefing was written here (by a helper of the earlier eagent), with its own MAP",
		"the user wrote: keep the tone dry", "the earlier eagent stopped to wait: waiting for the user to confirm the changelog",
		"the user was told: Understood: dry tone",
	} {
		if !strings.Contains(ask, want) {
			t.Fatalf("the request lacks %q:\n%s", want, ask)
		}
	}
	// The old actors' replies are never part of the new conversation.
	for _, q := range s.model.requests(testModel) {
		for _, m := range q.Messages[1:] {
			if strings.Contains(string(m), `"drafting"`) {
				t.Fatalf("an old actor's reply is in the new conversation: %s", m)
			}
		}
	}
}

// If the model cannot write that dossier, eagent assembles one from the
// old record itself, with the old session's last dossier inside it.
func TestOldSessionFallsBackToADossierFromTheHarness(t *testing.T) {
	project, sess, files := oldSession(t)
	s, _ := resumeOld(t, project, sess, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: "ok"}
		}
		if r, ok := startsAnAgent(msgs); ok {
			return r
		}
		return hold()
	})
	s.waitFor("the message to reach a new agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	d := s.dossiers()
	last := d[len(d)-1]
	if len(d) != 2 || last.By != "harness" {
		t.Fatalf("dossiers = %d, the last by %q", len(d), last.By)
	}
	for _, want := range []string{
		"started by an earlier eagent", "WHAT THE USER WAS LAST TOLD", "Understood: dry tone. The changelog is next.",
		"HOW THE OLD SESSION LEFT THINGS", "it stopped to wait: waiting for the user to confirm the changelog",
		"THE OLD SESSION'S LAST BRIEFING (" + files[1] + ":2", "1. USER'S GOAL: release notes for v2.",
		"- " + files[0] + ": lines 1-6", files[0] + ":3] write the release notes for v2",
	} {
		if !strings.Contains(last.Text, want) {
			t.Fatalf("the dossier lacks %q:\n%s", want, last.Text)
		}
	}
}
