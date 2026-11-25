package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// newFreshPiSeat watches a pi seat as Launch leaves a fresh one: since is
// zero, the seat expects the session id it chose ("launched"), and pi has
// not written that session's file yet (it does so at the first message),
// though its status file already names it.
func newFreshPiSeat(t *testing.T) *piSwitchSeat {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	dir := t.TempDir()
	w := &piSwitchSeat{t: t, dir: dir, status: filepath.Join(dir, "pi-status.json"), first: filepath.Join(dir, "launched.jsonl")}
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: w.first, SessionID: "launched", SessionEntries: entries(0)})
	w.s = newSeat(Spec{StatusFile: w.status, Dir: dir}, piAgent{label: "Pi"}, &fakeHost{alive: true}, "pi-fresh-"+filepath.Base(dir), "launched")
	w.s.state = "idle"
	w.s.launched = time.Now()
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
			w := newFreshPiSeat(t)
			time.Sleep(1500 * time.Millisecond) // a few looks at the empty start
			older := olderSession(t, w.dir, tc.oldAt)
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: older, SessionID: "older", SessionEntries: tc.entries})
			got := w.until(5*time.Second, innerIs(older))
			got = append(got, w.quiet(2*time.Second)...)
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
			if again := w.quiet(1500 * time.Millisecond); len(turnsIn(again)) != 0 {
				t.Fatalf("reported again: %+v", again)
			}
		})
	}
}

// The resumed session's first new turn can be on disk before the seat
// first looks at it. It is news, and the history before it is not.
func TestFreshPiResumedReportsATurnWrittenBeforeTheFirstLook(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries *int
	}{
		{"extension records the start", entries(7)},
		{"older extension without the count", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newFreshPiSeat(t)
			time.Sleep(1500 * time.Millisecond)
			older := olderSession(t, w.dir, time.Now().Add(-3*time.Hour))
			appendLines(t, older, piTurnLines("n1", "right away", "Fast answer.", time.Now())...)
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: older, SessionID: "older", SessionEntries: tc.entries, Settled: 1})
			got := w.until(5*time.Second, isTurn)
			if turns := turnsIn(got); len(turns) != 1 || turns[0] != "Fast answer." {
				t.Fatalf("turns %q", turns)
			}
			if again := w.quiet(2 * time.Second); len(turnsIn(again)) != 0 {
				t.Fatalf("reported more: %q", turnsIn(again))
			}
		})
	}
}

// The usual fresh launch: pi writes the session it was started on at its
// first message, and that turn is reported.
func TestFreshPiLaunchReportsItsFirstTurn(t *testing.T) {
	w := newFreshPiSeat(t)
	time.Sleep(1500 * time.Millisecond)
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
// waited for the old one; a restart of the same conversation does.
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
	if got := left(); len(got) != 0 {
		seat.Kill()
		t.Fatalf("a fresh agent inherits %v", got)
	}
	inner := seat.Snapshot().InnerID
	if err := seat.Kill(); err != nil {
		t.Fatal(err)
	}
	seed()
	before := left()
	seat, _, err = Launch(spec, inner, true)
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Kill()
	if got := left(); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("a resume lost what waited for it: %v, had %v", got, before)
	}
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

// runPiExtension starts the extension with a spool holding one message
// and returns the spool, a function that lists what it holds, and one that
// waits for the run to end and returns the hand-overs.
func runPiExtension(t *testing.T, accept bool, run time.Duration, env ...string) (func() []string, func() []piHandOver) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	ext, driver, spool := filepath.Join(dir, "eagent-relay.ts"), filepath.Join(dir, "driver.mjs"), filepath.Join(dir, "after_turn")
	if err := os.WriteFile(ext, []byte(piExtension), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driver, []byte(piExtensionDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := spoolMessage(spool, "a1", "the held message"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, driver, ext)
	cmd.Env = append(os.Environ(), "EAGENT_PI_STATUS="+filepath.Join(dir, "pi-status.json"), "EAGENT_PI_SPOOL="+spool, fmt.Sprintf("RUN_MS=%d", run.Milliseconds()))
	if accept {
		cmd.Env = append(cmd.Env, "ACCEPT=1")
	}
	cmd.Env = append(cmd.Env, env...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	list := func() []string {
		names, _ := filepath.Glob(filepath.Join(spool, "*"))
		for i := range names {
			names[i] = filepath.Base(names[i])
		}
		return names
	}
	wait := func() []piHandOver {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("extension driver: %v\n%s", err, out.String())
		}
		var got []piHandOver
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var h piHandOver
			if line == "" {
				continue
			}
			if json.Unmarshal([]byte(line), &h) != nil {
				t.Fatalf("driver printed %q", line)
			}
			got = append(got, h)
		}
		return got
	}
	return list, wait
}

// pi refuses a hand-over it cannot take (a prompt while it compacts, no
// model): the message is not gone, and goes again later.
func TestPiExtensionKeepsAMessagePiRefused(t *testing.T) {
	t.Parallel()
	list, wait := runPiExtension(t, false, 9*time.Second)
	time.Sleep(2500 * time.Millisecond) // the extension looks every second
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
	list, wait := runPiExtension(t, true, 9*time.Second)
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
