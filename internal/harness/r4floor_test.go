package harness

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// r4SmallRuntime is a runtime on a 16,384-token route (2,048 for the
// reply), not running: the tests drive its restarts by hand.
func r4SmallRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 16384, 2048
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// r4Restarts writes stretches of a long record on rt, each ten short user
// messages and a turn end, then a restart whose dossier is written as the
// session writes one: the model's briefing (citing three places) and the
// harness's facts. The first stretch's first message is r4First.
func r4Restarts(t *testing.T, rt *Runtime, stretches int, at *time.Time) {
	t.Helper()
	add := func(ev event.Event) {
		*at = at.Add(time.Minute)
		ev.Time = *at
		rt.append(ev)
	}
	for f := 0; f < stretches; f++ {
		for i := 0; i < 10; i++ {
			text := fmt.Sprintf("message %d in stretch %d: check sensor %d", i, len(rt.st.Subsessions), i)
			if len(rt.st.Subsessions) == 1 && i == 0 {
				text = r4First
			}
			add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text}))
		}
		add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "done", DurationMS: 1000}))
		cur := rt.sess.Current()
		job := dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines()}
		rt.finishDossier(job, dossierResult{ok: true, text: briefing([]string{cur + ":3", cur + ":4", cur + ":5"})})
	}
}

// r4First is the first thing the user said, in the session's first file.
const r4First = "the first instruction: keep every reading in kelvin"

// r4Newest is the text of the newest dossier in rt's record.
func r4Newest(rt *Runtime) string {
	var d event.DossierData
	for _, ev := range rt.st.Events {
		if ev.Type == event.Dossier {
			_ = ev.Decode(&d)
		}
	}
	return d.Text
}

// r4CheckFloor checks the newest dossier against its share of a small
// route, and that it leaves room for the conversation.
func r4CheckFloor(t *testing.T, rt *Runtime) string {
	t.Helper()
	text := r4Newest(rt)
	_, share, _ := rt.smallBudget()
	n := llm.EstimateTokens(text)
	t.Logf("after %d files, the dossier is %d tokens (by the estimate) of the %d it may take", len(rt.st.Subsessions), n, rt.estimate(share))
	if n > int(rt.estimate(share)) {
		t.Fatalf("the dossier takes %d tokens of the %d it may:\n%s", n, rt.estimate(share), text)
	}
	return text
}

// r4Grows is how much the newest dossier grew over was, a dossier written
// earlier in the same record, by the estimate. Each stretch r4Restarts
// writes adds about 70 tokens to a dossier that lists every file and
// message again; one held to a conversation's worth of places grows by a
// digit or two.
func r4Grows(rt *Runtime, was string) int {
	return llm.EstimateTokens(r4Newest(rt)) - llm.EstimateTokens(was)
}

// r4GrowthSlack is how much a narrowest dossier may grow between two
// lengths of record: its counts gain digits, nothing more.
const r4GrowthSlack = 20

// On a small route the narrowest dossier does not grow with the record: it
// leaves what came before the conversation's own briefing to that
// briefing, which maps it, and holds one conversation's worth of places.
// After 8 restarts (80 messages), when the chain has begun, and after 22
// (220 messages) it is within its share (a third of what the conversation
// may fill past the system prompt and tools), and it is no longer at 22
// than at 8. A dossier that listed every file and message again would
// grow by about 70 tokens a restart and pass its share by the 22nd.
func TestSmallRouteDossierFitsALongRecord(t *testing.T) {
	rt := r4SmallRuntime(t)
	defer rt.sess.Close()
	at := time.Now().Add(-1000 * time.Hour)
	r4Restarts(t, rt, 8, &at)
	was := r4CheckFloor(t, rt)
	r4Restarts(t, rt, 14, &at)
	r4CheckFloor(t, rt)
	if g := r4Grows(rt, was); g > r4GrowthSlack {
		t.Fatalf("the dossier grew by %d tokens from 8 restarts to 22: it grows with the record", g)
	}
	if r4LoggedRT(rt, "to the briefing there") == 0 {
		t.Fatal("the session log does not say a dossier left the earlier record to the briefing before it")
	}
	if r4LoggedRT(rt, "and still") != 0 {
		t.Fatal("a dossier at its narrowest did not fit")
	}
}

// r4LoggedRT counts the log lines of a runtime's fake UI that contain what.
func r4LoggedRT(rt *Runtime, what string) int {
	n := 0
	for _, l := range rt.ui.(*fakeUI).logged() {
		if strings.Contains(l, what) {
			n++
		}
	}
	return n
}

// The same with the session stopped and resumed half-way: the chain of
// briefings is read from the record, so the run that picks it up writes
// dossiers as narrow as the one before (8 restarts, then 8 more after the
// resume; a run that lost the chain would list all 16 files again).
func TestSmallRouteDossierStaysNarrowAcrossAResume(t *testing.T) {
	rt := r4SmallRuntime(t)
	at := time.Now().Add(-1000 * time.Hour)
	r4Restarts(t, rt, 8, &at)
	was := r4CheckFloor(t, rt)
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close()
	cfg := rt.cfg
	rt2, err := Resume(cfg, Options{Project: project}, &fakeUI{input: make(chan string)}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	r4Restarts(t, rt2, 8, &at)
	text := r4CheckFloor(t, rt2)
	if g := r4Grows(rt2, was); g > r4GrowthSlack {
		t.Fatalf("the dossier grew by %d tokens across the resume: the run that picked the session up lost the chain", g)
	}
	r4Reach(t, rt2, text, r4First)
}

// r4Pointer finds where a dossier leaves the earlier record.
var r4Pointer = regexp.MustCompile(`the record before the briefing at (\d{10,}\.jsonl):(\d+) is mapped by it`)

// r4Reach starts from a dossier's text alone and follows its pointers, as
// a model would with session_read, until a briefing names the place of
// the user message that says want; then reads that place and checks it.
func r4Reach(t *testing.T, rt *Runtime, text, want string) {
	t.Helper()
	var target event.Event
	for _, ev := range rt.st.Events {
		if ev.Type == event.UserMessage && strings.Contains(string(ev.Data), want) {
			target = ev
			break
		}
	}
	if target.Source.File == "" {
		t.Fatalf("no message %q in the record", want)
	}
	hops, ok := r4Follow(t, rt, text, target.Source.File, target.Source.Line)
	if !ok {
		t.Fatalf("the newest briefing does not lead to %s", target.Source)
	}
	if got := r4Read(t, rt, target.Source.File, target.Source.Line); !strings.Contains(got, want) {
		t.Fatalf("the place a briefing names for the message does not hold it: %s", got)
	}
	t.Logf("reached %s from the newest briefing in %d hops", target.Source, hops)
}

// r4Read reads one line whole with session_read's offsets, piece by piece.
func r4Read(t *testing.T, rt *Runtime, file string, line int) string {
	t.Helper()
	var b strings.Builder
	for off := 0; ; {
		out, err := rt.archive.Read(file, line, line, false, off, dossierReadChars)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(out)
		m := regexp.MustCompile(`offset=(\d+) for the rest`).FindStringSubmatch(out)
		if m == nil {
			// session_read marks the line's own line breaks with ⏎.
			return strings.ReplaceAll(b.String(), "⏎ ", "\n")
		}
		off, _ = strconv.Atoi(m[1])
	}
}

// r4Follow follows a dossier's pointer to the briefing before it, and on,
// until one names file:line. It returns the hops taken.
func r4Follow(t *testing.T, rt *Runtime, text, file string, line int) (int, bool) {
	t.Helper()
	for hops := 0; hops < 400; hops++ {
		if r4Points(text, file, line) {
			return hops, true
		}
		m := r4Pointer.FindStringSubmatch(text)
		if m == nil {
			return hops, false
		}
		n, _ := strconv.Atoi(m[2])
		text = r4Read(t, rt, m[1], n)
	}
	return 400, false
}

// From the newest dossier alone, by following file:line places with the
// session tools, the first thing the user said in the first conversation
// of a long record on a small route is reached. The chain begins at the
// fifth restart; after 14 the way back runs through ten briefings.
func TestNewestDossierReachesTheFirstConversation(t *testing.T) {
	rt := r4SmallRuntime(t)
	defer rt.sess.Close()
	at := time.Now().Add(-1000 * time.Hour)
	r4Restarts(t, rt, 14, &at)
	r4Reach(t, rt, r4Newest(rt), r4First)
}

// The chain also runs through a briefing the harness had to write (the
// model failed) and through the takeover of a session of the earlier
// design: each carries the facts and what it covers.
func TestChainOfBriefingsRunsThroughHarnessAndUpgradeDossiers(t *testing.T) {
	project, sess, _ := r4OldSession(t)
	cfg := testConfig("http://127.0.0.1:1")
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 16384, 2048
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := Resume(cfg, Options{Project: project}, &fakeUI{input: make(chan string)}, sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	if !rt.turns.adopt {
		t.Fatal("the old session is not being taken over")
	}
	// The takeover's dossier, by the harness (the model could not write it).
	rt.finishDossier(dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines(), adopt: true}, dossierResult{})
	at := time.Now().Add(-500 * time.Hour)
	r4Restarts(t, rt, 1, &at)
	// One by the harness in the middle.
	rt.finishDossier(dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines()}, dossierResult{})
	r4Restarts(t, rt, 10, &at)
	text := r4CheckFloor(t, rt)
	r4Reach(t, rt, text, "write the release notes for v2")
}

// When only the narrowest facts leave room for the briefing, the dossier
// is written at its narrowest and the session log says so, once for that
// dossier; when even they do not, it says that instead.
func TestNarrowestDossierIsSaidInTheLog(t *testing.T) {
	rt := r4SmallRuntime(t)
	defer rt.sess.Close()
	at := time.Now().Add(-1000 * time.Hour)
	r4Restarts(t, rt, 5, &at)
	for i := 0; i < 30; i++ {
		at = at.Add(time.Minute)
		ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: r4Text(i, 600)})
		ev.Time = at
		rt.append(ev)
	}
	covers := rt.st.LastSeq()
	next, err := rt.openSubsession("context full", "rollover", 12000)
	if err != nil {
		t.Fatal(err)
	}
	_, share, _ := rt.smallBudget()
	limit := int(rt.estimate(share))
	floor := llm.EstimateTokens(rt.dossierFactsWith(covers, next, factLevels[len(factLevels)-1]))
	above := llm.EstimateTokens(rt.dossierFactsWith(covers, next, factLevels[len(factLevels)-2]))
	if floor >= above || above-floor < 20 {
		t.Fatalf("the narrowest facts (%d tokens) are not narrower than the next (%d)", floor, above)
	}
	// A briefing that leaves room for the narrowest facts and not the next.
	text := strings.Repeat("x", (limit-floor-(above-floor)/2)*7/2)
	job := dossierJob{covers: covers, lines: rt.recordLines()}
	body := rt.dossierBody(text, job, dossierResult{ok: true, text: text}, "eagent", next)
	if n := llm.EstimateTokens(body); n > limit {
		t.Fatalf("the dossier is %d tokens of %d", n, limit)
	}
	if n := r4LoggedRT(rt, "written at its narrowest ("); n != 1 {
		t.Fatalf("the log says %d times that the dossier was written at its narrowest", n)
	}
	rt.dossierBody(strings.Repeat("x", limit*4), job, dossierResult{ok: true}, "eagent", next)
	if n := r4LoggedRT(rt, "written at its narrowest and still"); n != 1 {
		t.Fatalf("the log says %d times that even the narrowest dossier did not fit", n)
	}
}

var _ = config.AutoRollover // the route is set in r4SmallRuntime
