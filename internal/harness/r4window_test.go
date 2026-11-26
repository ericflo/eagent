package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	"github.com/ericflo/eagent/internal/tools"
)

// r4Fixed estimates what every request of a runtime carries before its
// conversation: the system prompt and the tool list.
func r4Fixed(rt *Runtime) int {
	b, _ := json.Marshal(tools.All())
	return llm.EstimateTokens(rt.system) + llm.EstimateTokens(string(b))
}

// The trigger for a restart follows what the route can take as input. On
// a small local route (16,384 tokens, 2,048 of them for the reply) it lies
// inside the window, above what the system prompt and tools take; on an
// ordinary one it is what it always was.
func TestRolloverThresholdFitsASmallWindowAndStaysOnOrdinaryOnes(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	for _, c := range []struct{ window, reply, want int }{
		{200_000, 32768, 100_000},
		{200_000, 0, 100_000},
		{1_000_000, 32768, 500_000},
		{80_000, 4096, 40_000},
		{128_000, 16384, 64_000},
		{32_768, 4096, 20_000},
	} {
		cfg := testConfig("http://127.0.0.1:1")
		cfg.RolloverTokens = 0
		cfg.Model.ContextTokens, cfg.Model.MaxTokens = c.window, c.reply
		rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
		if err != nil {
			t.Fatal(err)
		}
		if got := rt.rolloverTokens(); got != c.want {
			t.Errorf("window %d, reply %d: restarts at %d, want %d", c.window, c.reply, got, c.want)
		}
		if got := config.AutoRollover(c.window, c.reply); got != c.want {
			t.Errorf("config.AutoRollover(%d, %d) = %d, want %d", c.window, c.reply, got, c.want)
		}
		rt.sess.Close()
	}
	cfg := testConfig("http://127.0.0.1:1")
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 16384, 2048
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	fixed := r4Fixed(rt)
	if got := rt.rolloverTokens(); got >= 16384-2048 || got <= fixed {
		t.Fatalf("a 16,384-token route with a 2,048-token reply restarts at %d (the system prompt and tools take about %d)", got, fixed)
	}
}

// r4Text is a user message of about n characters.
func r4Text(i, n int) string {
	s := fmt.Sprintf("message %d: keep the archived temperature readings in celsius and recheck sensor %d. ", i, i)
	return strings.Repeat(s, n/len(s)+1)[:n]
}

// A session on a small local route: a fake provider with a 16,384-token
// window and a 2,048-token reply, which refuses as too long any request
// that would not fit (counted with the estimator eagent uses). The user
// writes 50 messages (r4WindowMessages) in rounds. Every restart is
// written by the model (its request fits), the first request after each
// restart fits, every message gets its answer, and the latest briefing
// still points at every message the user sent before it, by file and
// line, through the chain of briefings.
//
// A round can still carry a conversation from under the threshold past the
// window; that request is refused, and the restart that follows is the one
// after a refusal (the test below).
func TestSessionOnASmallWindowRestartsIntoRequestsThatFit(t *testing.T) {
	r4SmallWindow(t, 5, r4WindowMessages)
}

// The same with all the messages arriving at once: the provider refuses
// the conversation as too long, and the restart that follows (written
// from the files) still leads to requests that fit.
func TestRefusedConversationOnASmallWindowRestartsIntoRequestsThatFit(t *testing.T) {
	if refused := r4SmallWindow(t, r4WindowMessages, r4WindowMessages); refused == 0 {
		t.Fatal("the conversation was never refused as too long")
	}
}

// r4WindowMessages is how many messages the user writes in the sessions
// above: four restarts with five a round, the briefings chained from the
// second, the largest first request after a restart as large as after
// fifteen, and one round refused as too long; all at once, far past the
// window.
const r4WindowMessages = 50

// r4SmallWindow runs the session above, with total messages from the user
// arriving perRound at a time, and returns how many requests were refused
// as too long.
func r4SmallWindow(t *testing.T, perRound, total int) int {
	t.Helper()
	const window, ceiling = 16384, 2048
	b, _ := json.Marshal(tools.All())
	toolTokens := llm.EstimateTokens(string(b))
	size := func(msgs []map[string]any) int {
		raw, _ := json.Marshal(msgs)
		return llm.EstimateTokens(string(raw)) + toolTokens
	}
	var mu sync.Mutex
	var refusedPass, refusedAfter, refused, passes, firsts int
	var largest int
	pending := false // a dossier was written and no ordinary request has followed
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		n := size(msgs)
		mu.Lock()
		defer mu.Unlock()
		first, _ := msgs[1]["content"].(string)
		ask := dossierRequest(msgs)
		afterRestart := ask == "" && pending && strings.HasPrefix(first, "[Briefing.")
		if afterRestart {
			pending = false
		}
		if n > window-ceiling {
			refused++
			switch {
			case ask != "":
				refusedPass++
			case afterRestart:
				refusedAfter++
			}
			return reply{status: 400, body: `{"error":{"message":"prompt is too long: the model's context window is 16384 tokens","type":"invalid_request_error"}}`}
		}
		if ask != "" {
			passes++
			pending = true
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):]), prompt: n}
		}
		if afterRestart {
			firsts++
			largest = max(largest, n)
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Noted.")
		}
		r.prompt = n
		return r
	}, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = window, ceiling
	})
	t.Cleanup(func() {
		if t.Failed() {
			for _, l := range s.ui.logged() {
				t.Log(l)
			}
		}
	})
	r4Until(t, "the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	r4Settled(s)
	sent := 0
	for sent < total {
		s.rt.sync(func() {
			for i := 0; i < perRound && sent < total; i++ {
				s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: r4Text(sent, 400+sent%7*300)}))
				sent++
			}
		})
		before := s.ui.messageCount()
		s.type_(fmt.Sprintf("@e round %d", sent))
		r4Until(t, "the answer to round "+fmt.Sprint(sent), func() bool { return s.ui.messageCount() > before })
		r4Settled(s)
	}
	mu.Lock()
	defer mu.Unlock()
	if passes == 0 {
		t.Fatal("the conversation was never restarted")
	}
	if refusedAfter > 0 || refusedPass > 0 {
		t.Fatalf("on a 16,384-token window, %d first requests after a restart and %d dossier requests were too long", refusedAfter, refusedPass)
	}
	if largest > window-ceiling || firsts == 0 {
		t.Fatalf("the largest of %d first requests after a restart measured %d tokens", firsts, largest)
	}
	t.Logf("%d restarts; the largest first request after one measured %d tokens of %d; %d requests refused as too long in all", passes, largest, window-ceiling, refused)
	for _, d := range s.dossiers() {
		if d.By != "eagent" {
			t.Fatalf("a dossier was written by %s", d.By)
		}
	}
	// The latest briefing points at every message the user sent before it.
	var dossier event.Event
	var users []event.Event
	for _, ev := range s.events(event.Dossier) {
		dossier = ev
	}
	var d event.DossierData
	_ = dossier.Decode(&d)
	for _, ev := range s.events(event.UserMessage) {
		if ev.Seq <= d.CoversSeq {
			users = append(users, ev)
		}
	}
	if perRound < total && r4Pointer.FindStringSubmatch(d.Text) == nil {
		t.Fatal("the latest briefing points at no earlier briefing: the chain is not exercised")
	}
	for _, ev := range users {
		if _, ok := r4Follow(t, s.rt, d.Text, ev.Source.File, ev.Source.Line); !ok {
			t.Fatalf("the latest briefing does not lead to %s (of %d messages before it):\n%s", ev.Source, len(users), d.Text)
		}
	}
	return refused
}

// r4Points reports that a dossier names file:line, whole or in a list of
// places by file (one by one, or as a run "9-14").
func r4Points(text, file string, line int) bool {
	if strings.Contains(text, fmt.Sprintf("%s:%d]", file, line)) || strings.Contains(text, fmt.Sprintf("%s:%d ", file, line)) {
		return true
	}
	for _, l := range strings.Split(text, "\n") {
		if i := strings.Index(l, file+" lines "); i >= 0 {
			for _, n := range strings.Split(l[i+len(file)+7:], ", ") {
				from, to, _ := strings.Cut(strings.TrimSpace(n), "-")
				if to == "" {
					to = from
				}
				a, _ := strconv.Atoi(from)
				b, _ := strconv.Atoi(to)
				if a <= line && line <= b {
					return true
				}
			}
		}
	}
	return false
}

// r4OrdinaryRecord builds the record of a long session on an ordinary
// route (200k window, 32k reply): 210 user messages, some long, and 35
// turn ends. It returns the runtime, which has written no dossier yet,
// and a second one on a copy of the same record (each writes one dossier,
// which changes its record). The copy is read back as Resume reads a
// session, without what Resume writes on taking it up, so both records
// are the same to the line; the goldens below hold the two to that.
func r4OrdinaryRecord(t *testing.T) (*Runtime, *Runtime) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 200_000, 32768
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	add := func(ev event.Event) {
		at = at.Add(time.Minute)
		ev.Time = at
		rt.append(ev)
	}
	for i := 0; i < 210; i++ {
		add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: r4Text(i, 200+i%9*350)}))
		if i%6 == 0 {
			add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: fmt.Sprintf("Turn %d done: the readings for sensor %d are converted.", i, i), DurationMS: 61000, Tools: 3}))
		}
		if i%40 == 0 {
			add(event.New(event.Message, event.ActorEagent, event.MessageData{Text: fmt.Sprintf("Progress at %d.", i)}))
		}
	}
	copied := filepath.Join(t.TempDir(), filepath.Base(rt.sess.Path))
	if err := os.CopyFS(copied, os.DirFS(rt.sess.Path)); err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(copied)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(events)
	sess, err := store.Open(copied, st.LastSeq(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rt2, err := build(cfg, rt.opts, &fakeUI{input: make(chan string)}, sess, st)
	if err != nil {
		sess.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	return rt, rt2
}

// r4OrdinaryDossier writes rt's dossier, by the model (ok) or by the
// harness, and returns its body with file names and times made stable.
func r4OrdinaryDossier(t *testing.T, rt *Runtime, ok bool) string {
	t.Helper()
	job := dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines()}
	res := dossierResult{ok: ok, text: briefing([]string{rt.sess.Current() + ":3", rt.sess.Current() + ":4", rt.sess.Current() + ":5"})}
	rt.finishDossier(job, res)
	var d event.DossierData
	evs := rt.st.Events
	_ = evs[len(evs)-1].Decode(&d)
	if evs[len(evs)-1].Type != event.Dossier {
		t.Fatal("no dossier was written")
	}
	names := map[string]string{}
	text := regexp.MustCompile(`\d{10,}\.jsonl`).ReplaceAllStringFunc(d.Text, func(m string) string {
		if names[m] == "" {
			names[m] = fmt.Sprintf("FILE%d", len(names)+1)
		}
		return names[m]
	})
	text = strings.ReplaceAll(text, rt.opts.Project, "PROJECT")
	text = regexp.MustCompile(`eagent-relay-\d+`).ReplaceAllString(text, "eagent-relay-ID")
	return regexp.MustCompile(`[A-Z][a-z]{2} \d{1,2} \d{2}:\d{2}`).ReplaceAllString(text, "WHEN")
}

// On an ordinary route the dossier is byte for byte what it was before
// dossiers were sized to the window: the model's text and the facts, for
// a briefing by the model and for one the harness had to write (on two
// copies of one record).
func TestOrdinaryWindowDossierIsUnchanged(t *testing.T) {
	rt, copied := r4OrdinaryRecord(t)
	for _, c := range []struct {
		rt   *Runtime
		ok   bool
		file string
	}{{rt, true, "r4dossier-ordinary.golden"}, {copied, false, "r4dossier-ordinary-harness.golden"}} {
		got := r4OrdinaryDossier(t, c.rt, c.ok)
		path := filepath.Join("testdata", c.file)
		if os.Getenv("EAGENT_R4_WRITE_GOLDEN") != "" {
			_ = os.MkdirAll("testdata", 0o755)
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("the dossier on an ordinary route changed (%d bytes, was %d)", len(got), len(want))
		}
	}
}
