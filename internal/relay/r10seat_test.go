package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Finding 11: on a reattach, Claude (and pi) stamp a transcript entry to
// the millisecond, the record stamps a send to the nanosecond. An entry in
// the same millisecond as a restored send is its arrival; one a millisecond
// earlier is still not.
func TestR10AnEntryInTheSendsMillisecondConfirmsARestoredSend(t *testing.T) {
	t.Parallel()
	sec := time.Now().Add(-time.Minute).Truncate(time.Second)
	sent := sec.Add(123456789 * time.Nanosecond)
	r := Restored{ID: "r1", Text: "count the chairs", Mode: "queue", At: sent}
	s := attachSeat(Spec{Restore: []Restored{r}}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", sec.Add(800*time.Millisecond))
	if ok := claudeLines(s, claudeUser(sec.Add(122*time.Millisecond), "count the chairs")); len(ok) != 0 {
		t.Fatalf("an entry a millisecond before r1 was recorded confirmed %v", ok)
	}
	if ok := claudeLines(s, claudeUser(sec.Add(123*time.Millisecond), "count the chairs")); !slices.Equal(ok, []string{"r1"}) {
		t.Fatalf("an entry in r1's millisecond confirmed %v, want r1", ok)
	}
}

// Finding 10, agents with a queue of their own: an after-turn "/compact"
// handed to Codex's or Antigravity's queue is the agent's own command,
// which shows no arrival. It is delivered once handed over, as a command
// typed into the prompt is once typed, and is not left waiting.
func TestR10AnAfterTurnCommandHandedToAnAgentsQueueIsDelivered(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, c := range []struct {
		agent Agent
		inner string
		typed int
	}{{antigravityAgent{}, "", 1}, {codexAgent{}, "0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11", 0}} {
		h := &fakeHost{alive: true}
		s := newSeat(Spec{Name: "x"}, c.agent, h, "x", c.inner)
		s.state = "working"
		if msg, err := s.Send("c1", "/compact", "after_turn"); err != nil || !strings.Contains(msg, "held for") {
			t.Fatalf("%T: send: %q %v", c.agent, msg, err)
		}
		var got []string
		for _, ev := range deliveries(drain(s.events)) {
			got = append(got, fmt.Sprintf("%s:%v", ev.SendID, ev.OK))
		}
		if !slices.Equal(got, []string{"c1:true"}) || len(s.Snapshot().Pending) != 0 || len(h.typed) != c.typed {
			t.Errorf("%T: reports %v, still pending %v, typed %q", c.agent, got, s.Snapshot().Pending, h.typed)
		}
	}
}

// Finding 10, agents with a spool: Claude's Stop hook hands a held
// "/compact" over as text, so its arrival shows as a held message's does.
// One the hook took and never handed over is reported unconfirmed after
// the usual wait, as such a message is, once; never typed or pressed.
func TestR10AHeldCommandTheHookLostIsReportedUnconfirmed(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, h, spool := heldClaudeSeat(t, "/compact")
	killedHook(spool)
	s.fold(obs{kind: "turn", text: "Done."})
	drain(s.events)
	seen := time.Now().Add(20 * time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once", got)
	}
	if len(h.typed) != 0 || len(h.keys) != 0 || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("typed %q, pressed %q, pending %v", h.typed, h.keys, s.Snapshot().Pending)
	}
}

// Finding 10, the hand-over that works: a held "/compact" the Stop hook
// hands over is confirmed by its arrival, and nothing more is reported.
func TestR10AHeldCommandTheHookHandsOverIsConfirmedByItsArrival(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, _, spool := heldClaudeSeat(t, "/compact")
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if !strings.Contains(out.String(), `"reason":"/compact"`) {
		t.Fatalf("hook said %q", out.String())
	}
	at := time.Now()
	feedback := fmt.Sprintf(`{"type":"user","isMeta":true,"timestamp":"%s","message":{"content":"Stop hook feedback:\n/compact"}}`, stamp(at))
	if ok := claudeLines(s, feedback, fmt.Sprintf(`{"type":"system","subtype":"turn_duration","timestamp":"%s","durationMs":2}`, stamp(at.Add(time.Second)))); !slices.Equal(ok, []string{"r1"}) {
		t.Fatalf("the hand-over confirmed %v, want r1", ok)
	}
	later := time.Now().Add(20 * time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(later.Add(after * time.Second))
		s.checkScreen(later.Add(after * time.Second))
	}
	if got := deliveries(drain(s.events)); len(got) != 0 {
		t.Fatalf("reported again: %+v", got)
	}
}

// Finding 8: eagent stopped while a message waited in Claude's spool. On
// the reattach the history shows the turn it waited for over, and the Stop
// hook has claimed the file (.taken) but not yet removed it, so the message
// stays held. The hook then removes the file without handing it over: the
// message is lost from the next sweep, and reported unconfirmed after the
// usual wait, once; never typed or pressed.
func TestR10ARestoredHeldSendTheHookLostAfterTheReattachIsReported(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	since := time.Now().Add(-time.Minute)
	spool := filepath.Join(t.TempDir(), "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	taken := filepath.Join(spool, fmt.Sprintf("%d-r1.txt.taken", since.Add(-10*time.Second).UnixNano()))
	if err := os.WriteFile(taken, []byte("then run the tests"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Restored{ID: "r1", Text: "then run the tests", Mode: "after_turn", At: since.Add(-10 * time.Second)}
	h := &fakeHost{alive: true}
	s := attachSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook", Restore: []Restored{r}}, claudeAgent{}, h, "x", "", "", since)
	history := []string{
		claudeUser(since.Add(-20*time.Second), "build the parser"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"text","text":"Built."}]}}`, stamp(since.Add(-6*time.Second))),
		fmt.Sprintf(`{"type":"system","subtype":"turn_duration","timestamp":"%s","durationMs":2}`, stamp(since.Add(-5*time.Second))),
	}
	if ok := claudeLines(s, history...); len(ok) != 0 {
		t.Fatalf("the history confirmed %v", ok)
	}
	s.caughtUp(time.Now())
	if err := os.Remove(taken); err != nil { // the hook removes it, never handing it over
		t.Fatal(err)
	}
	seen := time.Now().Add(time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once (pending %v)", got, s.Snapshot().Pending)
	}
	if len(h.typed) != 0 || len(h.keys) != 0 {
		t.Fatalf("typed %q, pressed %q", h.typed, h.keys)
	}
}
