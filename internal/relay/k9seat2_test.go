package relay

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A busy line whose words hold parentheses of their own (Claude shows the
// active task's words there, "Fixing the parser (step 2)") is still the
// busy line: what makes it one is its glyph and the closing "(… esc to
// interrupt)".
func TestK9ABusyLineWithParenthesesInItsWordsIsBusy(t *testing.T) {
	for _, c := range []struct {
		agent  busyScreen
		screen string
		busy   bool
	}{
		{claudeAgent{}, "✶ Fixing the parser (step 2)… (3m 12s · ↓ 4.1k tokens · esc to interrupt)\n> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)", true},
		{codexAgent{}, "• Running go test (./internal/...) (12m 03s • esc to interrupt)\n› ", true},
		{claudeAgent{}, "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)", false},
		{claudeAgent{}, "- press (esc to interrupt) to stop it (later)\n> ", false},
	} {
		if got := c.agent.busy(c.screen); got != c.busy {
			t.Errorf("%T busy %v on %q; want %v", c.agent, got, c.screen, c.busy)
		}
	}
}

// A Claude turn that went quiet (blocked) and whose end only the screen
// showed (its prompt back, no spinner, no turn end in the transcript, so
// no Stop hook ran) left the message held for after it in the spool:
// turnEndAt stayed zero, so leftOvers never took it back, the send stayed
// pending for good, and the seat was never quiet again.
func TestK9AHeldSendIsTakenBackAfterATurnEndSeenOnlyOnTheScreen(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = 0, 0
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: "✻ Thinking… (10m 12s · esc to interrupt)\n" + claudePromptScreen}
	s := newSeat(Spec{Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "k9-screen-end", "id")
	s.state = "working"
	if res, err := s.Send("h1", "and then the docs", "after_turn"); err != nil {
		t.Fatal(err)
	} else if !strings.HasPrefix(res, "held for") {
		t.Fatalf("send: %s; want it held for the turn's end", res)
	}
	s.mu.Lock()
	s.stateAt = time.Now().Add(-11 * time.Minute)
	s.mu.Unlock()
	s.checkScreen(time.Now()) // quiet ten minutes, busy: blocked
	h.mu.Lock()
	h.screen = claudePromptScreen
	h.mu.Unlock()
	s.checkScreen(time.Now()) // the prompt is back
	s.checkScreen(time.Now()) // and stays at the next check: idle
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("state %s; want idle once the screen shows the prompt", st)
	}
	_ = drain(s.events)
	s.reportLeftOvers(time.Now())
	evs := drain(s.events)
	if len(evs) != 1 || evs[0].Kind != KindDelivered || evs[0].SendID != "h1" || evs[0].Outcome != OutcomeNotSent {
		t.Fatalf("events %+v; want h1 taken back from the spool, not sent", evs)
	}
}

// The take-back after a turn end seen on the screen loses the race to a
// Stop hook that claimed the message first (both claim by renaming it to
// .taken; one wins): the seat reports nothing of it, and its arrival says
// delivered. One outcome, never both.
func TestK9AHookThatClaimsFirstWinsOverTheScreenTakeBack(t *testing.T) {
	heldGrace0 := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = heldGrace0 })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s := newSeat(Spec{Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "k9-hook-first", "id")
	s.trackHeld("h1", "and then the docs", "after_turn", true)
	if err := spoolMessage(spool, "h1", "and then the docs"); err != nil {
		t.Fatal(err)
	}
	s.state, s.stateAt, s.lastLine = "working", time.Now().Add(-11*time.Minute), time.Now().Add(-11*time.Minute)
	s.checkScreen(time.Now())
	// The prompt is back: the turn is over. Then the hook claims it first.
	s.checkScreen(time.Now().Add(2 * time.Second))
	if taken, _ := takeSpool(spool); len(taken) != 1 {
		t.Fatalf("the hook took %v", taken)
	}
	s.reportLeftOvers(time.Now().Add(time.Hour))
	s.fold(obs{kind: "user", text: "and then the docs", at: time.Now()}) // its hand-over arrives
	var got []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			got = append(got, fmt.Sprintf("%s ok=%v %s", ev.SendID, ev.OK, ev.Outcome))
		}
	}
	if len(got) != 1 || got[0] != "h1 ok=true " {
		t.Fatalf("outcomes %q; want h1 delivered by its arrival, and nothing else", got)
	}
}

// piCompactDriver runs the extension and calls its compaction handlers as
// pi 1.6.1 does (session_before_compact, then session_compact or, on any
// failure, a cancel by a later extension among them, session_compact_failed),
// printing the status file after each.
const piCompactDriver = `import { pathToFileURL } from "node:url";
import * as fs from "node:fs";
const handlers = {};
const pi = { on(name, fn) { handlers[name] = fn; }, appendEntry() {}, sendUserMessage() {} };
const ext = await import(pathToFileURL(process.argv[2]).href);
ext.default(pi);
const ctx = { isIdle: () => true, hasPendingMessages: () => false, sessionManager: { getEntries: () => [], getSessionFile: () => "", getSessionId: () => "x" } };
const show = (step, extra) => {
	const st = JSON.parse(fs.readFileSync(process.env.EAGENT_PI_STATUS, "utf8"));
	console.log(JSON.stringify(Object.assign({ step, state: st.state, compacting: st.compacting }, extra || {})));
};
handlers.session_start({}, ctx);
show("start");
const r = await handlers.session_before_compact({ type: "session_before_compact", reason: "manual" }, ctx);
show("before", { returned: r === undefined ? "nothing" : "something" });
await handlers.session_compact({ type: "session_compact", reason: "manual" }, ctx);
show("compacted");
await handlers.session_before_compact({ type: "session_before_compact", reason: "threshold" }, ctx);
await handlers.session_compact_failed({ type: "session_compact_failed", reason: "threshold", aborted: true }, ctx);
show("failed");
process.exit(0);
`

// SR7-2, the extension's half: it says pi compacts from the moment pi
// asks extensions before compacting until the compaction ends, either way,
// and changes nothing of the compaction itself.
func TestK9PiExtensionSaysWhilePiCompacts(t *testing.T) {
	r := startPiDriver(t, piReal, piCompactDriver, "unused", 10*time.Second, nil)
	var got []string
	for _, line := range r.lines() {
		var v struct {
			Step, State, Returned string
			Compacting            bool
		}
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("driver printed %q: %v", line, err)
		}
		got = append(got, v.Step+" "+v.State+" "+map[bool]string{true: "compacting", false: "-"}[v.Compacting]+" "+v.Returned)
	}
	want := []string{"start idle - ", "before idle compacting nothing", "compacted idle - ", "failed idle - "}
	if !slices.Equal(got, want) {
		t.Fatalf("status through a compaction %q; want %q", got, want)
	}
}
