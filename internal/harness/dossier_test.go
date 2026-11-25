package harness

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

const dossierAsk = "Your conversation has grown long and is about to be restarted from a briefing"

// dossierRequest finds the request for a dossier in a prompt: "" when the
// call is an ordinary one.
func dossierRequest(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if s, _ := msgs[i]["content"].(string); msgs[i]["role"] == "user" && strings.Contains(s, dossierAsk) {
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
// file and a line-by-line index, may read and search any of it before
// writing, and must cite places; the harness adds the table of files and
// the user's own words with their places. A later dossier carries the
// earlier map forward and can reach the earlier dossier itself.
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
		dumpLogs(t, s)
		mu.Lock()
		t.Log(asks[0])
		mu.Unlock()
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
	for _, want := range []string{"- " + files[0] + ": lines 1-", "the file being written now", "the user wrote: " + brief[:40], "Codex finished a turn: Merged #42", "you told the user: Codex merged #42"} {
		if !strings.Contains(ask, want) {
			t.Fatalf("the dossier request lacks %q:\n%s", want, ask)
		}
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

// A session written by the three-actor eagent resumes into a new
// subsession whose dossier the harness assembles from the old record; the
// old files stay as they are and stay reachable.
func TestOldSessionResumesFromAHarnessDossier(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old := []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Models: map[string]string{"orchestrator": "glm-5", "task": "glm-5", "narrator": "glm-5"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: sess.Current(), Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "write the release notes for v2"}),
		event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{Text: "drafting", Usage: event.Usage{Input: 900, Output: 20}}),
		event.New(event.Message, event.LegacyNarrator, event.MessageData{Text: "The release notes are drafted in NOTES.md; the changelog section is still empty."}),
		event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: false, Reason: "waiting for the user to confirm the changelog"}),
	}
	for _, ev := range old {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	oldFile := sess.Current()
	sess.Close()

	seat := newFakeSeat("Filled in the changelog.")
	launched, _ := useFakeRelays(t, seat)
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if strings.Contains(tail, "No agent is running: if they want one, start it") {
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`), tc("relay_forward", `{}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
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
	s.waitFor("the message to reach a new agent", func() bool { return len(seat.sentTexts()) == 1 })
	s.settled()
	if got := seat.sentTexts()[0]; got != "fill in the changelog too" {
		t.Fatalf("the agent was sent %q", got)
	}
	if len(*launched) != 1 {
		t.Fatalf("launched %d agents", len(*launched))
	}
	var subs []state.Subsession
	s.read(func(st *state.State) {
		subs = append(subs, st.Subsessions...)
	})
	if len(subs) != 2 || subs[1].Reason != "upgrade" || subs[0].File != oldFile {
		t.Fatalf("subsessions = %+v", subs)
	}
	d := s.dossiers()
	if len(d) != 1 || d[0].By != "harness" {
		t.Fatalf("dossiers = %+v", d)
	}
	for _, want := range []string{
		"started by an earlier eagent", "WHAT THE USER WAS LAST TOLD", "The release notes are drafted in NOTES.md",
		"HOW THE OLD SESSION LEFT THINGS", "it stopped to wait: waiting for the user to confirm the changelog",
		"- " + oldFile + ": lines 1-6", "from the earlier eagent that did the work itself",
		oldFile + ":3] write the release notes for v2",
	} {
		if !strings.Contains(d[0].Text, want) {
			t.Fatalf("the dossier lacks %q:\n%s", want, d[0].Text)
		}
	}
	// The old events were never weighed by this loop and are not owed a look.
	for _, q := range model.requests(testModel) {
		for _, m := range q.Messages[1:] {
			if strings.Contains(string(m), `"drafting"`) {
				t.Fatalf("an old actor's reply is in the new conversation: %s", m)
			}
		}
	}
}
