package relay

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// piPromptPath is a closer stand-in for pi 1.6.1's prompt path than the
// one in r3pi_test.go, for the orderings an after-turn hand-over can meet:
//   - a prompt made while agent_settled is emitted is deferred until the
//     settle is over (agent-session.js prompt);
//   - an extension's message can be refused before the input hooks (pi is
//     compacting) or after them (no model, no key): it shows only as an
//     extension error;
//   - the input hooks see streamingBehavior only when a run is going;
//   - after the hooks (and PREFLIGHT_MS, the login check), a run going
//     means the message is queued (followUp) and delivered inside that run
//     with a message_start and no agent_start of its own; otherwise it
//     starts a run: agent_start, then message_start for the user message;
//   - typed messages take the same path with source "interactive";
//   - an interrupt puts queued messages back in the editor, ahead of
//     what is in it, before the run settles aborted; the editor keeps
//     line ends as "\n" and tabs as four spaces; an extension can read
//     the editor's text.
//
// STEPS is a JSON list of [ms, action, text]: "type" a message, or "spool"
// one for the extension. Every observation is a JSON line.
const piPromptPath = `import { pathToFileURL } from "node:url";
import * as fs from "node:fs";
import * as path from "node:path";
const handlers = {};
let running = false, settling = false;
const deferred = [], queue = [];
let refuseBefore = Number(process.env.REFUSE_BEFORE || 0);
let refuseAfter = Number(process.env.REFUSE_AFTER || 0);
let typedBeforeDeferred = process.env.TYPED_BEFORE_DEFERRED || "";
let abortQueued = process.env.ABORT_QUEUED === "1";
const preflight = Number(process.env.PREFLIGHT_MS || 10);
const runMs = Number(process.env.RUN_EACH_MS || 300);
const log = (o) => console.log(JSON.stringify(o));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let editor = "";
const ctx = {
	isIdle: () => !running,
	hasPendingMessages: () => queue.length > 0,
	ui: { getEditorText: () => editor },
	sessionManager: { getEntries: () => [], getSessionFile: () => "", getSessionId: () => "x" },
};
async function emit(name, ev) {
	if (handlers[name]) await handlers[name](ev, ctx);
}
const userMsg = (text) => ({ role: "user", content: [{ type: "text", text }], timestamp: Date.now() });
async function startRun(text, source) {
	running = true;
	await emit("agent_start", { type: "agent_start" });
	await emit("message_start", { type: "message_start", message: userMsg(text) });
	log({ ran: text, source });
	await sleep(runMs);
	let aborted = false;
	while (queue.length) {
		if (abortQueued) {
			abortQueued = false;
			const back = queue.splice(0);
			editor = [back.map((q) => q.text).join("\n\n"), editor].filter((t) => t.trim()).join("\n\n")
				.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(/\t/g, "    ");
			log({ editor: back.map((q) => q.text) });
			aborted = true;
			break;
		}
		const q = queue.shift();
		await emit("message_start", { type: "message_start", message: userMsg(q.text) });
		log({ ran: q.text, source: q.source, queued: true });
		await sleep(runMs);
	}
	running = false;
	settling = true;
	await emit("agent_settled", { type: "agent_settled", aborted });
	settling = false;
	if (typedBeforeDeferred && deferred.length) {
		const t = typedBeforeDeferred;
		typedBeforeDeferred = "";
		prompt(t, "interactive");
	}
	for (const f of deferred.splice(0)) await f();
}
async function prompt(text, source, deliverAs) {
	if (settling) {
		log({ deferred: text });
		deferred.push(() => prompt(text, source, deliverAs));
		return;
	}
	if (source === "extension" && refuseBefore > 0) {
		refuseBefore--;
		log({ refused: text, where: "before the hooks" });
		return;
	}
	const behavior = running ? (source === "interactive" ? "steer" : deliverAs) : undefined;
	await emit("input", { type: "input", text, source, streamingBehavior: behavior });
	await sleep(source === "extension" ? preflight : 5);
	if (source === "extension" && refuseAfter > 0) {
		refuseAfter--;
		log({ refused: text, where: "after the hooks" });
		return;
	}
	if (running) {
		const how = source === "interactive" ? "steer" : deliverAs;
		if (!how) {
			log({ lost: text });
			return;
		}
		queue.push({ text, source });
		log({ queued: text });
		return;
	}
	await startRun(text, source);
}
const pi = {
	on(name, fn) { handlers[name] = fn; },
	appendEntry() {},
	sendUserMessage(text, options) {
		log({ sent: text, deliverAs: (options && options.deliverAs) || "" });
		prompt(text, "extension", options && options.deliverAs);
	},
};
const ext = await import(pathToFileURL(process.argv[2]).href);
ext.default(pi);
handlers.session_start({}, ctx);
for (const [ms, action, text] of JSON.parse(process.env.STEPS || "[]")) {
	setTimeout(() => {
		if (action === "type") prompt(text, "interactive");
		if (action === "spool") {
			const name = String(Date.now() * 1000000) + "-x.txt";
			fs.writeFileSync(path.join(process.env.EAGENT_PI_SPOOL, "." + name), text);
			fs.renameSync(path.join(process.env.EAGENT_PI_SPOOL, "." + name), path.join(process.env.EAGENT_PI_SPOOL, name));
		}
	}, ms);
}
setTimeout(() => process.exit(0), Number(process.env.RUN_MS));
`

// piPathRun is what a stand-in run showed.
type piPathRun struct {
	lines []map[string]any
	out   string
}

func (r piPathRun) count(key, text string) int {
	n := 0
	for _, l := range r.lines {
		if v, ok := l[key].(string); ok && v == text {
			n++
		}
	}
	return n
}

// runPiPromptPath runs the embedded extension (timers divided by speed)
// against piPromptPath with a spool holding "the held message". It returns a
// spool lister, a wait for the end of the run, and the run. The run's
// length, steps, PREFLIGHT_MS and RUN_EACH_MS are given in the extension's
// real units and divided by speed too.
func runPiPromptPath(t *testing.T, speed int, run time.Duration, steps string, env ...string) (func() []string, func() piPathRun, *piDriverRun) {
	t.Helper()
	return runPiPromptPathWith(t, speed, held, run, steps, env...)
}

// runPiPromptPathWith is runPiPromptPath with another held message.
func runPiPromptPathWith(t *testing.T, speed int, text string, run time.Duration, steps string, env ...string) (func() []string, func() piPathRun, *piDriverRun) {
	t.Helper()
	env = append(env, "STEPS="+piScaleSteps(t, steps, speed))
	// A run lasts 300 ms unless a test says otherwise, divided as a test's
	// own would be. The default 10 ms preflight is left as it is: it only
	// orders a hand-over's run after a typed message's (5 ms) when both go
	// through the hooks together.
	r := startPiDriver(t, speed, piPromptPath, text, run, map[string]int{"RUN_EACH_MS": 300}, env...)
	list := func() []string {
		names, _ := filepath.Glob(filepath.Join(r.spool, "*"))
		return names
	}
	wait := func() piPathRun {
		lines := r.lines()
		out := piPathRun{out: r.out.String()}
		for _, line := range lines {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				t.Fatalf("stand-in printed %q", line)
			}
			out.lines = append(out.lines, m)
		}
		return out
	}
	return list, wait, r
}

const held = "the held message"

// The reviewer's sequence: pi refuses the hand-over before its input hooks
// (it was compacting), then a typed message starts a run of its own before
// the retry. That run is not the hand-over's: the message stays in the
// spool, goes again, and arrives once.
func TestPiHandOverRefusedThenAnUnrelatedRunStaysRetryable(t *testing.T) {
	t.Parallel()
	// The extension hands it over at its first look (1 s) and pi refuses
	// it; the typed run goes at 2 s, a second clear of both that look and
	// the retry (5 s after the refusal).
	list, wait, run := runPiPromptPath(t, piFast, 10*time.Second, `[[2000, "type", "a typed message"]]`, "REFUSE_BEFORE=1")
	run.waitFor(`"ran":"a typed message"`)
	time.Sleep(800 * time.Millisecond / piFast) // the typed run (300 ms) has settled
	if got := list(); len(got) != 1 {
		t.Fatalf("after an unrelated run the spool holds %v", got)
	}
	r := wait()
	if r.count("ran", held) != 1 || r.count("ran", "a typed message") != 1 || len(list()) != 0 {
		t.Fatalf("the held message ran %d times, spool %v:\n%s", r.count("ran", held), list(), r.out)
	}
}

// (a) Idle: one hand-over, one run, the spool empties.
func TestPiHandOverWhileIdleGoesOnce(t *testing.T) {
	t.Parallel()
	list, wait, _ := runPiPromptPath(t, piFast, 8*time.Second, `[]`)
	r := wait()
	if r.count("sent", held) != 1 || r.count("ran", held) != 1 || len(list()) != 0 {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}

// (b) The usual after-turn path: pi works when the message is held, the
// hand-over is made in agent_settled and deferred, then starts a run.
func TestPiHandOverDeferredInTheSettleGoesOnce(t *testing.T) {
	t.Parallel()
	// The typed run covers the extension's first look (1 s) by a second.
	list, wait, _ := runPiPromptPath(t, piFast, 9*time.Second, `[[100, "type", "a long run"]]`, "RUN_EACH_MS=2000")
	r := wait()
	if r.count("deferred", held) != 1 || r.count("sent", held) != 1 || r.count("ran", held) != 1 || len(list()) != 0 {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}

// (c) The hooks see the hand-over while pi is idle, its login check takes
// a while, and a typed message starts a run meanwhile: pi queues the
// hand-over into that run. It goes once, without a run of its own.
func TestPiHandOverQueuedBehindATypedRunGoesOnce(t *testing.T) {
	t.Parallel()
	// The hooks see it at the first look (1 s), its login check ends at 3 s;
	// the typed run starts at 2 s and lasts to 4.5 s: a second or more
	// between each.
	list, wait, _ := runPiPromptPath(t, piFast, 9*time.Second, `[[2000, "type", "a typed message"]]`, "PREFLIGHT_MS=2000", "RUN_EACH_MS=2500")
	r := wait()
	if r.count("queued", held) != 1 || r.count("sent", held) != 1 || r.count("ran", held) != 1 || len(list()) != 0 {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}

// (d) The hand-over is deferred in agent_settled, a typed prompt starts a
// run first, and the hooks then see the hand-over with that run going
// (streamingBehavior followUp): it is queued, no agent_start comes for it,
// and it goes once.
func TestPiHandOverDeferredBehindATypedRunGoesOnce(t *testing.T) {
	t.Parallel()
	// The first run covers the extension's first look (1 s) by a second;
	// all is settled by about 6 s, and the run lasts past a second hand-over
	// 5 s after the first.
	list, wait, _ := runPiPromptPath(t, piFast, 10*time.Second, `[[100, "type", "a long run"]]`, "RUN_EACH_MS=2000", "TYPED_BEFORE_DEFERRED=typed first")
	r := wait()
	if r.count("deferred", held) != 1 || r.count("queued", held) != 1 || r.count("sent", held) != 1 || r.count("ran", held) != 1 || len(list()) != 0 {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}

// (f) Refused after the hooks (no model, no key): an unrelated typed run
// does not count as the hand-over's; the message stays in the spool for
// the long retry and is not sent again meanwhile.
func TestPiHandOverRefusedAfterTheHooksSurvivesAnUnrelatedRun(t *testing.T) {
	t.Parallel()
	// The hooks see it at the first look (1 s); the typed run goes a second
	// later; a short give-back (5 s) would hand it over again before the end.
	list, wait, _ := runPiPromptPath(t, piFast, 8*time.Second, `[[2000, "type", "a typed message"]]`, "REFUSE_AFTER=1")
	r := wait()
	if r.count("sent", held) != 1 || r.count("ran", held) != 0 || r.count("ran", "a typed message") != 1 || len(list()) != 1 {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}

// An interrupt while the hand-over is queued: pi puts it in its editor, as
// it does with every queued message. It is not handed over a second time.
func TestPiHandOverPutBackInTheEditorIsNotSentAgain(t *testing.T) {
	t.Parallel()
	list, wait, _ := runPiPromptPath(t, piFast, 10*time.Second, `[[100, "type", "a long run"]]`, "RUN_EACH_MS=2000", "TYPED_BEFORE_DEFERRED=typed first", "ABORT_QUEUED=1")
	r := wait()
	if r.count("sent", held) != 1 || r.count("ran", held) != 0 || len(list()) != 0 || !strings.Contains(r.out, `"editor":["`+held+`"]`) {
		t.Fatalf("spool %v:\n%s", list(), r.out)
	}
}
