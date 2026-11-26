package relay

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// newFreshPiSeat watches a pi seat as Launch leaves a fresh one: since is
// zero, the seat expects the session id it chose ("launched"), and pi has
// not written that session's file yet (it does so at the first message),
// though its status file already names it.
// The caller's top-level test has called isolatePi.
func newFreshPiSeat(t *testing.T) *piSwitchSeat {
	t.Helper()
	dir := t.TempDir()
	w := &piSwitchSeat{t: t, dir: dir, status: filepath.Join(dir, "pi-status.json"), first: filepath.Join(dir, "launched.jsonl")}
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: w.first, SessionID: "launched", SessionEntries: entries(0)})
	w.s = newSeat(Spec{StatusFile: w.status, Dir: dir}, piAgent{label: "Pi"}, &fakeHost{alive: true}, "pi-fresh-"+filepath.Base(dir), "launched")
	w.s.state = "idle"
	w.s.launched = time.Now()
	w.s.poll = piLook
	go w.s.watch()
	t.Cleanup(w.s.Close)
	return w
}

// olderSession writes a session file holding two finished turns stamped
// at (7 entries after the header) and returns its path.
func olderSession(t *testing.T, dir string, at time.Time) string {
	t.Helper()
	older := filepath.Join(dir, "older.jsonl")
	lines := []string{piHeaderLine("older", at), piModelLine(at)}
	lines = append(lines, piTurnLines("o1", "old question", "Old answer one.", at.Add(time.Millisecond))...)
	lines = append(lines, piTurnLines("o2", "another", "Old answer two.", at.Add(10*time.Millisecond))...)
	writeLines(t, older, lines...)
	return older
}

// A fresh pi told to /resume another conversation before its first
// message: the seat has bound no transcript yet, and the first one it
// binds is the resumed one. Its history stays quiet; what follows is
// reported once.
func TestFreshPiResumedBeforeItsFirstMessageKeepsTheHistoryQuiet(t *testing.T) {
	isolatePi(t)
	for _, tc := range []struct {
		name    string
		entries *int
		oldAt   time.Time
	}{
		{"extension records the start", entries(7), time.Now().Add(-3 * time.Hour)},
		// pi can open a session whose turns are only seconds old (a fork,
		// or a conversation just left in another terminal).
		{"recent history, extension records the start", entries(7), time.Now().Add(-2 * time.Second)},
		{"older extension without the count", nil, time.Now().Add(-3 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newFreshPiSeat(t)
			time.Sleep(looks(8)) // a few looks at the empty start
			older := olderSession(t, w.dir, tc.oldAt)
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: older, SessionID: "older", SessionEntries: tc.entries})
			got := w.until(5*time.Second, innerIs(older))
			got = append(got, w.quiet(looks(quietLooks))...)
			if turns := turnsIn(got); len(turns) != 0 {
				t.Fatalf("history reported as new: %q", turns)
			}
			if !innerSeen(got, older, "older") {
				t.Fatalf("the resumed session was not announced: %+v", got)
			}
			appendLines(t, older, piTurnLines("n1", "new question", "New answer.", time.Now())...)
			got = w.until(5*time.Second, isTurn)
			if turns := turnsIn(got); len(turns) != 1 || turns[0] != "New answer." {
				t.Fatalf("turns %q", turns)
			}
			if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 {
				t.Fatalf("reported again: %+v", again)
			}
		})
	}
}

// The resumed session's first new turn can be on disk before the seat
// first looks at it. It is news, and the history before it is not.
func TestFreshPiResumedReportsATurnWrittenBeforeTheFirstLook(t *testing.T) {
	isolatePi(t)
	for _, tc := range []struct {
		name    string
		entries *int
	}{
		{"extension records the start", entries(7)},
		{"older extension without the count", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newFreshPiSeat(t)
			time.Sleep(looks(8))
			older := olderSession(t, w.dir, time.Now().Add(-3*time.Hour))
			appendLines(t, older, piTurnLines("n1", "right away", "Fast answer.", time.Now().Add(stampAhead))...)
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: older, SessionID: "older", SessionEntries: tc.entries, Settled: 1})
			got := w.until(5*time.Second, isTurn)
			if turns := turnsIn(got); len(turns) != 1 || turns[0] != "Fast answer." {
				t.Fatalf("turns %q", turns)
			}
			if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 {
				t.Fatalf("reported more: %q", turnsIn(again))
			}
		})
	}
}

// The usual fresh launch: pi writes the session it was started on at its
// first message, and that turn is reported.
func TestFreshPiLaunchReportsItsFirstTurn(t *testing.T) {
	isolatePi(t)
	w := newFreshPiSeat(t)
	time.Sleep(looks(8))
	now := time.Now()
	writeLines(t, w.first, append([]string{piHeaderLine("launched", now), piModelLine(now)}, piTurnLines("f", "hello", "First reply.", now)...)...)
	got := w.until(5*time.Second, isTurn)
	if turns := turnsIn(got); len(turns) != 1 || turns[0] != "First reply." || !innerSeen(got, w.first, "launched") {
		t.Fatalf("events %+v", got)
	}
}

// ---- readiness ------------------------------------------------------------------

// pi 1.6.1's footer: "0.4%/1.0M (auto)" with auto-compaction on, no
// "(auto)" with it off (padding, then the model), "?" for the percentage
// after a compaction until the next reply.
func TestPiFooterRenderingsLookReady(t *testing.T) {
	for _, screen := range []string{
		"~/work (main)\n↑1.2k ↓300 $0.004 0.4%/1.0M (auto)            (anthropic) claude-haiku-5-5 • low\n",
		"~/work\n↑1.2k ↓300 0.4%/1.0M                                   claude-haiku-5-5 • low\n",
		"~/work\n↑12k ↓3.0k ?/200k (auto)                                       gpt-5.5 • high\n",
		"~/work\n?/200k                                                         gpt-5.5\n",
		"~/work\n0.0%/100k",
	} {
		if !(piAgent{}).ready(screen) {
			t.Errorf("not ready: %q", screen)
		}
	}
	for _, screen := range []string{"Working...", "50%/100 done", "compiled 12%/4k?", ""} {
		if (piAgent{}).ready(screen) {
			t.Errorf("ready: %q", screen)
		}
	}
}

// piReadySeat is a pi seat checked by hand (no watcher): its screen shows
// pi's footer, as pi's always does, whatever pi is doing.
func piReadySeat(t *testing.T, screen string, st *piStatus) (*Seat, string) {
	t.Helper()
	dir := t.TempDir()
	status := filepath.Join(dir, "pi-status.json")
	if st != nil {
		writePiStatus(t, status, *st)
	}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{label: "Pi"}, &fakeHost{alive: true, screen: screen}, "pi-ready", "id")
	return s, status
}

const piFooterScreen = "Working...\n\n~/work\n↑1.2k ↓300 0.4%/1.0M (auto)            (local) m\n"

// A pi hung for ten minutes in a turn is marked blocked. Its footer is on
// screen all the same; while the status file says pi is working, the mark
// stays.
func TestPiHangMarkHoldsWhileStatusSaysWorking(t *testing.T) {
	s, status := piReadySeat(t, piFooterScreen, &piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
	now := time.Now()
	s.state, s.stateAt, s.lastLine = "working", now.Add(-11*time.Minute), now.Add(-11*time.Minute)
	if !s.wantsDialogCheck(now) {
		t.Fatal("a ten-minute silence is not checked")
	}
	s.checkScreen(now)
	if s.Snapshot().State != "blocked" {
		t.Fatalf("not marked: %s", s.Snapshot().State)
	}
	for i := 1; i <= 3; i++ {
		s.checkScreen(now.Add(time.Duration(i) * 1500 * time.Millisecond))
	}
	if st := s.Snapshot().State; st != "blocked" {
		t.Fatalf("the footer cleared the mark while pi works: %s", st)
	}
	// pi settles: the status file says so, and the mark goes.
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: "/x.jsonl", SessionID: "id", Settled: 1})
	s.checkScreen(now.Add(6 * time.Second))
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("still %s after pi went idle", st)
	}
}

// Without a status file (the extension did not load), the screen decides,
// as before.
func TestPiHangMarkWithoutStatusFallsBackToTheScreen(t *testing.T) {
	s, _ := piReadySeat(t, piFooterScreen, nil)
	now := time.Now()
	s.state, s.stateAt = "blocked", now.Add(-time.Minute)
	s.checkScreen(now)
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("state %s", st)
	}
}

// pi with quietStartup (no key hints) and a footer this seat cannot read:
// the status file saying idle, no dialog, is enough; pi is not marked
// "still not ready" after 25 seconds.
func TestPiStartupIsReadyFromTheStatusFile(t *testing.T) {
	s, _ := piReadySeat(t, "\n\n\n> \n", &piStatus{State: "idle", SessionFile: "/x.jsonl", SessionID: "id"})
	now := time.Now()
	s.stateAt = now.Add(-30 * time.Second)
	s.checkScreen(now)
	s.checkScreen(now.Add(1500 * time.Millisecond))
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("state %s", st)
	}
}

// The status file saying a dialog is open is not ready, whatever the
// screen shows.
func TestPiStartupWithADialogIsNotReady(t *testing.T) {
	s, _ := piReadySeat(t, piFooterScreen, &piStatus{State: "idle", Prompt: "Pick one", SessionFile: "/x.jsonl", SessionID: "id"})
	now := time.Now()
	s.checkScreen(now)
	s.checkScreen(now.Add(1500 * time.Millisecond))
	if st := s.Snapshot().State; st != "starting" {
		t.Fatalf("state %s", st)
	}
}

// ---- the spool --------------------------------------------------------------------

// A fresh start under a name used before does not hand the new agent what
// waited for the old one.
func TestFreshLaunchDropsAnEarlierAgentsSpool(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	old := Socket
	Socket = fmt.Sprintf("eagent-r3spool-%d", os.Getpid())
	defer func() {
		KillSocket(Socket)
		Socket = old
	}()
	// A stand-in pi that only waits: nothing real is started.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	work, state := t.TempDir(), t.TempDir()
	profile := config.BuiltinRelayProfiles()["pi"]
	profile.Host = "tmux"
	spec := Spec{Session: fmt.Sprintf("r3spool%d", os.Getpid()), Name: "pi", Profile: profile, Dir: work}
	if err := Prepare(&spec, state); err != nil {
		t.Fatal(err)
	}
	left := func() []string {
		names, _ := filepath.Glob(filepath.Join(spec.Spool, "*"))
		hidden, _ := filepath.Glob(filepath.Join(spec.Spool, ".*"))
		return append(names, hidden...)
	}
	seed := func() {
		if err := spoolMessage(spec.Spool, "s1", "an old agent's held message"); err != nil {
			t.Fatal(err)
		}
		// One a hook claimed and never finished with.
		if err := os.WriteFile(filepath.Join(spec.Spool, "1-s0.txt.taken"), []byte("older still"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	seat, _, err := Launch(spec, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Kill()
	if got := left(); len(got) != 0 {
		t.Fatalf("a fresh agent inherits %v", got)
	}
	// A restart drops it too: TestR7ARestartDropsTheSpool.
}

// ---- the extension's hand-over ------------------------------------------------------

// piExtensionDriver runs the embedded extension under node against a
// stand-in for pi's extension API. pi.sendUserMessage returns nothing and
// reports a refusal only as an extension error, so the stand-in either
// takes the message (the input hook, then after PREFLIGHT_MS a run:
// agent_start, agent_settled) or does nothing, as pi does when it throws
// because it is compacting. pi reports idle until the run starts. Each
// call is printed as a JSON line.
const piExtensionDriver = `import { pathToFileURL } from "node:url";
const handlers = {};
const accept = process.env.ACCEPT === "1";
const pi = {
	on(name, fn) { handlers[name] = fn; },
	appendEntry() {},
	sendUserMessage(text, options) {
		console.log(JSON.stringify({ text, deliverAs: (options && options.deliverAs) || "" }));
		if (!accept) return;
		handlers.input && handlers.input({ type: "input", text, source: "extension" }, ctx);
		setTimeout(() => {
			handlers.agent_start && handlers.agent_start({});
			setTimeout(() => handlers.agent_settled && handlers.agent_settled({ aborted: false }), 50);
		}, Number(process.env.PREFLIGHT_MS || 20));
	},
};
const ext = await import(pathToFileURL(process.argv[2]).href);
ext.default(pi);
const ctx = {
	isIdle: () => true,
	hasPendingMessages: () => false,
	sessionManager: { getEntries: () => [], getSessionFile: () => "", getSessionId: () => "x" },
};
handlers.session_start({}, ctx);
setTimeout(() => process.exit(0), Number(process.env.RUN_MS));
`

type piHandOver struct {
	Text, DeliverAs string
}

// The hand-over tests run on the extension's own timers: a look every
// second while pi is idle, a hand-over given back after 5 s (refused before
// the input hook) or 120 s (after it). Run as shipped, a scenario takes most
// of ten seconds, so the tests run a copy of the extension whose timers (the
// constants at its top, and only those lines) are divided by piFast, and
// divide every delay of their own by the same factor: the run's length, the
// stand-in's PREFLIGHT_MS and RUN_EACH_MS, its steps and the tests' sleeps.
// The tests are written in the extension's real units, so every ratio a
// scenario rests on is the one it has with the real timers. piReal runs the
// extension as shipped.
const (
	piReal = 1
	piFast = 5
)

// piTimers are the extension's timer constants, as shipped.
var piTimers = map[string]int{"TICK_MS": 1000, "GIVE_BACK_MS": 5000, "GIVE_BACK_SEEN_MS": 120000, "RETRY_MS": 5000, "RETRY_MAX_MS": 60000, "AFTER_ABORT_MS": 5000}

var piTimerLine = regexp.MustCompile(`(?m)^const ([A-Z_]+_MS) = (\d+);`)

// piExtensionAt is the extension with its timers divided by speed. It
// fails the test if the extension does not declare exactly piTimers, each
// once, so a timer added or renamed cannot stay unscaled unnoticed.
func piExtensionAt(t *testing.T, speed int) string {
	t.Helper()
	found := map[string]int{}
	for _, m := range piTimerLine.FindAllStringSubmatch(piExtension, -1) {
		ms, _ := strconv.Atoi(m[2])
		if _, dup := found[m[1]]; dup {
			t.Fatalf("the extension declares %s twice", m[1])
		}
		found[m[1]] = ms
	}
	if !maps.Equal(found, piTimers) {
		t.Fatalf("the extension's timers are %v, the tests know %v: scale the new ones too", found, piTimers)
	}
	src := piExtension
	for name, ms := range piTimers {
		line := fmt.Sprintf("const %s = %d;", name, ms)
		src = strings.Replace(src, line, fmt.Sprintf("const %s = %d;", name, ms/speed), 1)
	}
	return src
}

// piScaleSteps divides the times of a stand-in's STEPS by speed.
func piScaleSteps(t *testing.T, steps string, speed int) string {
	t.Helper()
	var list [][]any
	if err := json.Unmarshal([]byte(steps), &list); err != nil {
		t.Fatalf("steps %s: %v", steps, err)
	}
	for _, step := range list {
		step[0] = int(step[0].(float64)) / speed
	}
	raw, _ := json.Marshal(list)
	return string(raw)
}

// lockedOutput is a stand-in's output, readable while it runs.
type lockedOutput struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// piDriverRun is a stand-in for pi running the extension under node.
type piDriverRun struct {
	t     *testing.T
	cmd   *exec.Cmd
	out   *lockedOutput
	spool string
}

// piScaledEnv are the stand-ins' delays a test sets, divided by speed.
var piScaledEnv = map[string]bool{"PREFLIGHT_MS": true, "RUN_EACH_MS": true}

// startPiDriver writes the extension (timers divided by speed) and driver,
// a spool holding text, and starts node. run and the env values in
// piScaledEnv are divided by speed; defaults are such values the stand-in
// assumes when they are absent, passed divided as well.
func startPiDriver(t *testing.T, speed int, driver, text string, run time.Duration, defaults map[string]int, env ...string) *piDriverRun {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	ext, script, spool := filepath.Join(dir, "eagent-relay.ts"), filepath.Join(dir, "driver.mjs"), filepath.Join(dir, "after_turn")
	if err := os.WriteFile(ext, []byte(piExtensionAt(t, speed)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := spoolMessage(spool, "a1", text); err != nil {
		t.Fatal(err)
	}
	given := map[string]bool{}
	var scaled []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if piScaledEnv[k] {
			ms, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("%s: %v", kv, err)
			}
			given[k] = true
			kv = fmt.Sprintf("%s=%d", k, ms/speed)
		}
		scaled = append(scaled, kv)
	}
	for k, ms := range defaults {
		if !given[k] {
			scaled = append(scaled, fmt.Sprintf("%s=%d", k, ms/speed))
		}
	}
	cmd := exec.Command(node, script, ext)
	cmd.Env = append(os.Environ(), "EAGENT_PI_STATUS="+filepath.Join(dir, "pi-status.json"), "EAGENT_PI_SPOOL="+spool,
		fmt.Sprintf("RUN_MS=%d", (run/time.Duration(speed)).Milliseconds()))
	cmd.Env = append(cmd.Env, scaled...)
	out := &lockedOutput{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return &piDriverRun{t: t, cmd: cmd, out: out, spool: spool}
}

// waitFor waits until the stand-in has printed s.
func (r *piDriverRun) waitFor(s string) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(r.out.String(), s) {
		if time.Now().After(deadline) {
			r.t.Fatalf("the stand-in never printed %s:\n%s", s, r.out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lines waits for the stand-in to exit and returns what it printed, a JSON
// value per line.
func (r *piDriverRun) lines() []string {
	r.t.Helper()
	if err := r.cmd.Wait(); err != nil {
		r.t.Fatalf("stand-in: %v\n%s", err, r.out.String())
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(r.out.String()), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// runPiExtension starts the extension against piExtensionDriver with a
// spool holding one message. It returns a function that lists what the
// spool holds, one that waits for the run to end and returns the
// hand-overs, and the run.
func runPiExtension(t *testing.T, speed int, accept bool, run time.Duration, env ...string) (func() []string, func() []piHandOver, *piDriverRun) {
	t.Helper()
	if accept {
		env = append(env, "ACCEPT=1")
	}
	// The driver's own 20 ms preflight and 50 ms run are not scenario times
	// and stay as they are.
	r := startPiDriver(t, speed, piExtensionDriver, "the held message", run, nil, env...)
	list := func() []string {
		names, _ := filepath.Glob(filepath.Join(r.spool, "*"))
		for i := range names {
			names[i] = filepath.Base(names[i])
		}
		return names
	}
	wait := func() []piHandOver {
		var got []piHandOver
		for _, line := range r.lines() {
			var h piHandOver
			if json.Unmarshal([]byte(line), &h) != nil {
				t.Fatalf("driver printed %q", line)
			}
			got = append(got, h)
		}
		return got
	}
	return list, wait, r
}

// pi refuses a hand-over it cannot take (a prompt while it compacts, no
// model): the message is not gone, and goes again later.
func TestPiExtensionKeepsAMessagePiRefused(t *testing.T) {
	t.Parallel()
	list, wait, run := runPiExtension(t, piFast, false, 9*time.Second)
	// pi was handed it at the extension's first look and refused it; the
	// extension looks again a second later.
	run.waitFor(`"text":"the held message"`)
	time.Sleep(1500 * time.Millisecond / piFast)
	if got := list(); len(got) != 1 {
		t.Fatalf("after a refused hand-over the spool holds %v", got)
	}
	sent := wait()
	if len(sent) < 2 || sent[0].Text != "the held message" || sent[1].Text != "the held message" {
		t.Fatalf("hand-overs %+v", sent)
	}
	for _, h := range sent {
		if h.DeliverAs != "followUp" {
			t.Fatalf("handed over as %q: pi throws on a plain message while a run starts", h.DeliverAs)
		}
	}
	if got := list(); len(got) != 1 {
		t.Fatalf("spool %v", got)
	}
}

// pi takes it: the message goes once and leaves the spool.
func TestPiExtensionHandsAMessageOverOnce(t *testing.T) {
	t.Parallel()
	list, wait, _ := runPiExtension(t, piFast, true, 9*time.Second)
	sent := wait()
	if len(sent) != 1 || sent[0].Text != "the held message" || sent[0].DeliverAs != "followUp" {
		t.Fatalf("hand-overs %+v", sent)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("spool %v", got)
	}
}

// pi runs a message sent while it works inside the run that is going, so
// one run can answer two messages. The reply to the first is not the
// run's report (that is the last one), and it must not vanish: it is
// reported as a step.
func TestPiReplyBeforeAMessageTakenMidRunIsNotLost(t *testing.T) {
	p := &piParser{exact: true}
	var got []obs
	for _, line := range []string{
		`{"type":"message","id":"1","timestamp":"2026-11-25T10:00:00Z","message":{"role":"user","content":"first"}}`,
		`{"type":"message","id":"2","timestamp":"2026-11-25T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Reply to the first."}],"stopReason":"stop"}}`,
		`{"type":"message","id":"3","timestamp":"2026-11-25T10:00:06Z","message":{"role":"user","content":"second"}}`,
		`{"type":"message","id":"4","timestamp":"2026-11-25T10:00:09Z","message":{"role":"assistant","content":[{"type":"text","text":"Reply to the second."}],"stopReason":"stop"}}`,
		`{"type":"custom","id":"5","timestamp":"2026-11-25T10:00:10Z","customType":"` + piSettledEntry + `","data":{"aborted":false}}`,
	} {
		got = append(got, p.line([]byte(line))...)
	}
	var steps, turns []string
	for _, o := range got {
		switch o.kind {
		case "progress":
			steps = append(steps, o.text)
		case "turn":
			turns = append(turns, o.text)
		}
	}
	if len(steps) != 1 || steps[0] != "Reply to the first." {
		t.Fatalf("the reply to the first message was reported as %q, want it once as a step", steps)
	}
	if len(turns) != 1 || turns[0] != "Reply to the second." {
		t.Fatalf("the run was reported as %q, want one turn ending on the last reply", turns)
	}
}

// Between two ordinary runs nothing is held, and a new message brings no
// step with it.
func TestPiMessageAfterASettledRunBringsNoStep(t *testing.T) {
	p := &piParser{exact: true}
	var got []obs
	for _, line := range []string{
		`{"type":"message","id":"1","timestamp":"2026-11-25T10:00:00Z","message":{"role":"user","content":"first"}}`,
		`{"type":"message","id":"2","timestamp":"2026-11-25T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Reply to the first."}],"stopReason":"stop"}}`,
		`{"type":"custom","id":"3","timestamp":"2026-11-25T10:00:06Z","customType":"` + piSettledEntry + `","data":{"aborted":false}}`,
		`{"type":"message","id":"4","timestamp":"2026-11-25T10:00:09Z","message":{"role":"user","content":"second"}}`,
	} {
		got = append(got, p.line([]byte(line))...)
	}
	for _, o := range got {
		if o.kind == "progress" {
			t.Fatalf("a step %q was reported between two settled runs", o.text)
		}
	}
}
