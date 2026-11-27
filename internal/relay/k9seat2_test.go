package relay

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// SR7-3, a reattach's half: a block a reattach kept or made (hung, not yet
// judged) that the first screen check finds stuck is said, as one that
// turns stuck later is.
func TestK9AReattachedBlockFoundStuckIsSaid(t *testing.T) {
	h := &fakeHost{alive: true, screen: "$ "}
	s := newSeat(Spec{}, codexAgent{}, h, "k9-reattach-stuck", "thread-1")
	s.state, s.hung = "blocked", true // as attachSeat leaves a quiet turn
	s.checkScreen(s.stateAt)
	if !s.Snapshot().Stuck {
		t.Fatal("not stuck at a shell prompt")
	}
	evs := drain(s.events)
	if len(evs) != 1 || evs[0].Kind != KindState || evs[0].State != "blocked" || !evs[0].Stuck {
		t.Fatalf("events %+v; want blocked said again, stuck", evs)
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

// SR7-2, the seat's half: pi's compaction holds quiet while it runs, and
// once it ends (and the command's own hold is over) the seat is quiet.
func TestK9PiIsQuietOnceItsCompactionEnds(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	now := time.Now().Add(-time.Minute)
	lines := append([]string{piHeaderLine("first", now)}, piTurnLines("t1", "sort the shelves", "Sorted.", now.Add(time.Second))...)
	w := newPiSwitchSeat(t, time.Time{}, lines, entries(0))
	s := w.s
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	writePiStatus(t, w.status, piStatus{State: "idle", Compacting: true, SessionFile: w.first, SessionID: "first", SessionEntries: entries(0)})
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	if got := quietOrder(t, s, looks(2*quietMoveLooks)); slices.Contains(got, "quiet") {
		t.Fatalf("events %q: quiet while pi compacts", got)
	}
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: w.first, SessionID: "first", SessionEntries: entries(0)})
	if got := quietOrder(t, s, looks(8)); got[len(got)-1] != "quiet" {
		t.Fatalf("events %q; want quiet once pi's compaction is over", got)
	}
}
