package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r10Session runs a session with its loop, as eagent does, whose first
// agent (codex) is seats[0]; later starts wait on the gate.
func r10Session(t *testing.T, seats ...relaySeat) (*Runtime, *gatedLaunches, <-chan int) {
	t.Helper()
	g := useGatedLaunches(t, seats, nil)
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 16)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	rt.sync(func() { rt.st.Relays["codex"].State = "idle" })
	return rt, g, done
}

// r10Quit ends the session as /quit does, lets the launches under way go
// once the loop has closed (so none of them is recorded), and returns the
// record.
func r10Quit(t *testing.T, rt *Runtime, g *gatedLaunches, launches int, done <-chan int) []event.Event {
	t.Helper()
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	for range launches {
		g.release <- struct{}{}
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// r10NotSentBeforeEnd checks a send has one outcome on the record,
// not_sent, written before session.end.
func r10NotSentBeforeEnd(t *testing.T, evs []event.Event, id string) {
	t.Helper()
	n, ended := 0, false
	for _, ev := range evs {
		var d event.RelayDeliveredData
		switch {
		case ev.Type == event.SessionEnd:
			ended = true
		case ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id:
			n++
			if ended || d.OK || d.Outcome != event.DeliveryNotSent {
				t.Errorf("%s: %+v (after session.end: %v)", id, d, ended)
			}
		}
	}
	if o := state.Replay(evs).SendOutcome(id); n != 1 || o != state.SendNotSent {
		t.Errorf("%s has %d outcomes on the record and reads %q; want one, not_sent", id, n, o)
	}
}

// r10Send sends text to codex as eagent's relay_send does, from its own
// goroutine (a send typed into the agent waits for its typing).
func r10Send(rt *Runtime, text string) <-chan string {
	out := make(chan string, 1)
	go func() {
		args := map[string]any{"relay": "codex", "text": text}
		got, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
		out <- got
	}()
	return out
}

// Finding 5, a restart. A send held for a restart that the session's end
// gives up (abandonLaunch) was never typed: it is not_sent on the record.
func TestR10ASendHeldForARestartCutShortByAQuitIsNotSent(t *testing.T) {
	rt, g, done := r10Session(t, newFakeSeat("one"), newFakeSeat("two"))
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	if out := recv(t, r10Send(rt, "also sweep the floor"), "relay_send"); !strings.Contains(out, "is starting") {
		t.Fatalf("send during the restart: %q", out)
	}
	id := lastSendID(rt)
	r10NotSentBeforeEnd(t, r10Quit(t, rt, g, 1, done), id)
}

// Finding 5, a fresh start under a stopped name: the same, and nothing is
// left waiting on the stopped relay.
func TestR10ASendHeldForAFreshStartCutShortByAQuitIsNotSent(t *testing.T) {
	rt, g, done := r10Session(t, newFakeSeat("one"), newFakeSeat("two"))
	tool := func(name string, args map[string]any) (string, bool) {
		return rt.relayTool(name, argStr(args), argNum(args), argList(args), false)
	}
	if out, isErr := tool("relay_control", map[string]any{"relay": "codex", "action": "stop"}); isErr {
		t.Fatalf("stop: %s", out)
	}
	started := make(chan string, 1)
	go func() {
		out, _ := tool("relay_start", map[string]any{"profile": "codex"})
		started <- out
	}()
	g.waitEntered(t)
	if out := recv(t, r10Send(rt, "also sweep the floor"), "relay_send"); !strings.Contains(out, "is starting") {
		t.Fatalf("send during the start: %q", out)
	}
	id := lastSendID(rt)
	evs := r10Quit(t, rt, g, 1, done)
	recv(t, started, "relay_start")
	r10NotSentBeforeEnd(t, evs, id)
	if rel := state.Replay(evs).Relays["codex"]; len(rel.Undelivered) != 0 {
		t.Errorf("the stopped relay still waits on %v", rel.Undelivered)
	}
}

// Finding 5, the send queue. One send is being typed when the session
// quits and another waits behind it: the one waiting was never typed and
// is not_sent on the record; the one being typed keeps what its typing
// gives (nothing on the record in this run: it is still awaited).
func TestR10ASendQueuedBehindOneBeingTypedAtAQuitIsNotSent(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, _, done := r10Session(t, seat)
	r10Send(rt, "check the version")
	var typing string
	select {
	case typing = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send was never typed")
	}
	r10Send(rt, "then run the tests")
	waitFor(t, "the second send queued", func() bool { return lastSendID(rt) != typing })
	queued := lastSendID(rt)
	evs := r10Quit(t, rt, nil, 0, done)
	seat.result <- nil
	r10NotSentBeforeEnd(t, evs, queued)
	if o := state.Replay(evs).SendOutcome(typing); o != state.SendWaiting {
		t.Errorf("the send being typed reads %q, want it still awaited", o)
	}
	// The outcome leaves where a reattach reads the agent's transcript
	// from as it was: what the agent did after the loop closed is not on
	// the record, and a resume must take it as new.
	var without []event.Event
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type != event.RelayDelivered || ev.Decode(&d) != nil || d.ID != queued {
			without = append(without, ev)
		}
	}
	if a, b := state.Replay(evs).Relays["codex"].LastEventAt, state.Replay(without).Relays["codex"].LastEventAt; !a.Equal(b) {
		t.Errorf("the outcome moved the relay's last news from %v to %v", b, a)
	}
}

// Finding 5, the send queue, the other order. The send being typed ends
// after the loop has closed and before the session's end reads the queue
// (a start of another agent under way holds it): the queue takes nothing
// more, so the send behind it is still there, and is not_sent.
func TestR10ASendQueuedWhenTheOneAheadEndsAfterAQuitIsNotSent(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, g, done := r10Session(t, seat, newFakeSeat("two"))
	r10Send(rt, "check the version")
	var typing string
	select {
	case typing = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send was never typed")
	}
	r10Send(rt, "then run the tests")
	waitFor(t, "the second send queued", func() bool { return lastSendID(rt) != typing })
	queued := lastSendID(rt)
	q := typingQueue(t, rt, typing)
	go rt.relayTool("relay_start", argStr(map[string]any{"profile": "claude"}), argNum(nil), argList(nil), false)
	g.waitEntered(t)
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.result <- nil
	waitIdle(t, q)
	g.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	r10NotSentBeforeEnd(t, evs, queued)
}

// r10moved is a send that waited for a switch from prev and went to name.
func r10moved(rec *relayRecord, name, prev, id string, seq int64) {
	rec.add(event.RelayMoved, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: name, ID: id, From: "user", Seq: seq, Mode: "queue", Text: "check the version"}, Prev: prev})
}

// r10switch records a switch from one relay to another, as finishLaunch
// does: the old relay's end, then the new one's start.
func r10switch(rec *relayRecord, from, to string) {
	rec.add(event.RelayEnd, event.RelayEndData{Name: from, Reason: "handed over to " + to})
	rec.start(to, to)
}

// Finding 6. A send that waited through a switch from Codex to Claude, and
// through the switch back, is Codex's again, listed once. When it then
// certainly did not go, the user's message may be forwarded to Codex again,
// and nothing claims it went to Claude or to the Codex that was stopped.
func TestR10ASendMovedBackToItsFirstRelayIsThatRelaysAgain(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if id, why := forwardedAs(st, "codex", seq, ""); id != "" {
		t.Errorf("forwarding again to codex refused as %s: %q", id, why)
	}
	if sent := userSends(st, "codex", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("codex's list: %q, want r1 once, as not sent", sent)
	}
	if sent := userSends(st, "claude", 8); len(sent) != 1 || !strings.Contains(sent[0], "went to Codex") {
		t.Errorf("claude's list: %q, want r1 once, as gone to Codex", sent)
	}
}

// Finding 6, the claim. A send that went on to Claude and certainly did
// not go there says nothing of the Codex that held the name before: the
// refusal says where it went, not that the stopped Codex had it.
func TestR10ASendThatDidNotGoIsNotClaimedForTheEarlierHolder(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	rec.failed("claude", "r1", event.DeliveryNotSent)
	r10switch(rec, "claude", "codex")
	id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, "")
	if id != "r1" || strings.Contains(why, "held the name") || !strings.Contains(why, "went to Claude instead") {
		t.Errorf("refused as %s: %q, want r1 as gone to Claude", id, why)
	}
}

const r10Earlier = "that message went as written (r1) to the agent that held the name codex before, which was stopped; Codex, running now, was not sent it as its own message (its hand-over brief may quote it); to give it to this one, send the words with relay_send"

// Finding 9. A send typed into the Codex that was stopped, whose arrival
// that Codex never confirmed (still waiting, or given up on), was that
// Codex's: the refusal says so, not that it is on its way to the Codex
// started since, which was never sent it.
func TestR10ASendOfAStoppedHolderIsNotOnItsWayToTheNewOne(t *testing.T) {
	for _, outcome := range []string{"", event.DeliveryUnconfirmed} {
		rec := &relayRecord{t: time.Now()}
		rec.start("codex", "codex")
		seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
		rec.send("codex", "r1", seq)
		if outcome != "" {
			rec.failed("codex", "r1", outcome)
		}
		rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "stopped"})
		rec.start("codex", "codex")
		st := state.Replay(rec.evs)
		if n := st.Relays["codex"].Awaited(); n != 0 {
			t.Fatalf("the new Codex awaits %d sends", n)
		}
		if id, why := forwardedAs(st, "codex", seq, ""); id != "r1" || why != r10Earlier {
			t.Errorf("outcome %q: refused as %s:\n %q\nwant r1:\n %q", outcome, id, why, r10Earlier)
		}
	}
}

// Finding 7. Codex is stopped, a fresh Codex is started, and the user's
// instruction is forwarded while it starts: the new Codex is typed it and
// confirms it. A switch to Claude then hands that instruction over in the
// brief, as the new Codex's own.
func TestR10AnInstructionHeldForAFreshStartIsInTheNextBrief(t *testing.T) {
	const text = "build the payment webhook handler"
	fresh, claude := newFakeSeat("two"), newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), fresh, claude}, nil)
	rt := newLaunchRuntime(t)
	tool := func(name string, args map[string]any) (string, bool) {
		return rt.relayTool(name, argStr(args), argNum(args), argList(args), false)
	}
	if out, isErr := tool("relay_control", map[string]any{"relay": "codex", "action": "stop"}); isErr {
		t.Fatalf("stop: %s", out)
	}
	started := make(chan string, 1)
	go func() {
		out, _ := tool("relay_start", map[string]any{"profile": "codex"})
		started <- out
	}()
	g.waitEntered(t)
	var seq int64
	rt.sync(func() {
		seq = rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text})).Seq
	})
	if out, isErr := tool("relay_forward", map[string]any{"relay": "codex", "seq": int(seq)}); isErr || !strings.Contains(out, "is starting") {
		t.Fatalf("forward while the fresh Codex starts: %q", out)
	}
	id := lastSendID(rt)
	g.release <- struct{}{}
	if out := recv(t, started, "relay_start"); !strings.Contains(out, "Started.") {
		t.Fatalf("relay_start: %q", out)
	}
	waitFor(t, "the new Codex to confirm the instruction", func() bool {
		var o state.SendOutcome
		rt.sync(func() { o = rt.st.SendOutcome(id) })
		return o == state.SendArrived
	})
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	if out := recv(t, switched, "switch"); !strings.Contains(out, "Handed over") {
		t.Fatalf("switch: %q", out)
	}
	if sent := claude.sentTexts(); len(sent) == 0 || !strings.Contains(sent[0], text) {
		t.Fatalf("Claude's brief leaves out the instruction:\n%q", sent)
	}
}

// Finding 9, its bound. A send passed back to the name after the earlier
// holder was stopped is the agent's that holds it now: on its way to it.
func TestR10ASendMovedOntoTheNewHolderIsItsOwn(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	if id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, ""); id != "r1" || !strings.Contains(why, "already on its way to Codex") {
		t.Errorf("refused as %s: %q, want r1 as on its way", id, why)
	}
}
