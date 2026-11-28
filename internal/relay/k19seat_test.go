package relay

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Claude works, with a message in its own queue (its enqueue line read).
// The agent's own command sent to interrupt it is refused, and nothing is
// pressed or typed: the interrupt's Escape puts the queued words back into
// the input box (popAll, as Claude Code 2.1.332 does), the command would
// be pasted after them, and Enter would send one message, "also update the
// changelog/compact", that runs no command, while the seat said the
// command was typed and delivered. A plain message sent to interrupt goes
// as before, and so does the command once the queue is empty.
func TestK19ACommandIsNotTypedBehindWhatAnInterruptPutsBackInTheBox(t *testing.T) {
	savedSettle, savedPause, savedInt := typeSettle, dialogPause, interruptPause
	typeSettle, dialogPause, interruptPause = 0, 0, 0
	t.Cleanup(func() { typeSettle, dialogPause, interruptPause = savedSettle, savedPause, savedInt })
	const text = "also update the changelog"
	working := "✻ Slithering… (2s · ↓ 118 tokens)\n" + strings.Join(claudePromptLines, "\n") + " · esc to interrupt\n"
	pressed := func(h *fakeHost) (string, []string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Join(h.keys, " "), append([]string{}, h.typed...)
	}

	s, h, feed := k17Seat(t, text)
	h.mu.Lock()
	h.screen = working
	h.mu.Unlock()
	feed(k1Queue("enqueue", text))
	drain(s.Events())
	_, err := s.Send("c1", "/compact", "interrupt")
	if keys, typed := pressed(h); err == nil || !strings.Contains(err.Error(), "queue holds a message") || keys != "" || len(typed) != 0 {
		t.Fatalf("send err %v, keys %q, typed %q; want the command refused for the queued message, nothing pressed or typed", err, keys, typed)
	}
	for _, ev := range drain(s.Events()) {
		if ev.Kind == KindDelivered && ev.SendID == "c1" {
			t.Fatalf("the refused command has an outcome of its own: %+v", ev)
		}
	}
	// A plain message interrupts as before: both go as one message.
	if _, err := s.Send("r2", "stop, and fix the build first", "interrupt"); err != nil {
		t.Fatalf("a plain message to interrupt was refused: %v", err)
	}
	if keys, typed := pressed(h); keys != "Escape Enter" || len(typed) != 1 {
		t.Fatalf("keys %q, typed %q; want the interrupt and the message", keys, typed)
	}

	// Nothing in the queue: the command interrupts and is typed.
	s, h, _ = k17Seat(t, text)
	h.mu.Lock()
	h.screen = working
	h.mu.Unlock()
	s.untrack("r1")
	if got, err := s.Send("c1", "/compact", "interrupt"); err != nil || !strings.Contains(got, "typed the command") {
		t.Fatalf("with an empty queue the command returned %q, %v; want it typed", got, err)
	}
	if keys, typed := pressed(h); keys != "Escape Enter" || len(typed) != 1 || typed[0] != "/compact" {
		t.Fatalf("keys %q, typed %q; want the interrupt and the command", keys, typed)
	}
}

// The queue has given its message up already (an Escape from the terminal
// or relay_control: popAll), and its words sit in the box of an idle
// Claude until the seat presses Enter on them. The agent's own command
// sent then, to interrupt or to queue, was pasted after those words and
// sent as one message; main types it the same way. It is refused until the
// message has gone.
func TestK19ACommandIsNotTypedBehindAMessageTheQueueGaveUp(t *testing.T) {
	k19Fast(t, 0)
	const text = "also update the changelog"
	for _, mode := range []string{"interrupt", "queue", "after_turn"} {
		s, h, feed := k17Seat(t, text)
		feed(k1Queue("enqueue", text))
		feed(k1Queue("popAll", ""), k17Prompt("[Request interrupted by user]"))
		k17Idle(s, h, fmt.Sprintf(k17Box, text))
		drain(s.Events())
		_, err := s.Send("c1", "/compact", mode)
		h.mu.Lock()
		keys, typed := strings.Join(h.keys, " "), append([]string{}, h.typed...)
		h.mu.Unlock()
		if err == nil || !strings.Contains(err.Error(), "sits in its input box") || keys != "" || len(typed) != 0 {
			t.Errorf("%s: send err %v, keys %q, typed %q; want the command refused for the words in the box, nothing pressed or typed", mode, err, keys, typed)
		}
		// A plain message still goes: it joins the words in the box.
		if _, err := s.Send("r2", "and the README", mode); err != nil {
			t.Errorf("%s: a plain message was refused: %v", mode, err)
		}
	}
}

// k19Fast drops the seat's pauses, and gives a command wait for a message
// typed before it to show as read.
func k19Fast(t *testing.T, wait time.Duration) {
	t.Helper()
	savedSettle, savedPause, savedInt, savedWait, savedLook := typeSettle, dialogPause, interruptPause, boxWait, boxLook
	typeSettle, dialogPause, interruptPause, boxWait, boxLook = 0, 0, 0, wait, 2*time.Millisecond
	t.Cleanup(func() {
		typeSettle, dialogPause, interruptPause, boxWait, boxLook = savedSettle, savedPause, savedInt, savedWait, savedLook
	})
}

func k19Typed(h *fakeHost) (string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.keys, " "), append([]string{}, h.typed...)
}

// Claude's queue threw its message away at a cancel (remove, with the
// reason cleared_on_cancel): nothing went back into the input box. The
// seat watches such a send as it does one put back in the box, and the
// agent's own command sent to the idle Claude was refused for the 45
// seconds until its report, with the words that the message sits in the
// input box. The box is empty, and the command is typed.
func TestK19ACommandIsTypedOnceTheQueueThrewItsMessageAway(t *testing.T) {
	k19Fast(t, 0)
	const text = "also update the changelog"
	s, h, feed := k17Seat(t, text)
	feed(k17Line("enqueue", text, ""))
	feed(k17Line("remove", text, "cleared_on_cancel"), k17Prompt("[Request interrupted by user]"))
	k17Idle(s, h, fmt.Sprintf(k17Box, ""))
	drain(s.Events())
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatalf("send: %v; want /compact typed into the empty box (the queue threw the message away)", err)
	}
	if _, typed := k19Typed(h); len(typed) != 1 || typed[0] != "/compact" {
		t.Fatalf("typed %q, want the command", typed)
	}
}

// The seat typed a message into an idle Claude and its Enter was lost: the
// words sit in the input box, where the check of the screen reads them
// before it presses Enter again. The agent's own command sent then was
// pasted behind them, sent as the end of that message and not run, and
// reported as typed (main does the same). It is refused while they show.
func TestK19ACommandIsNotTypedBehindAMessageWhoseEnterWasLost(t *testing.T) {
	k19Fast(t, 0)
	const text = "also update the changelog"
	for _, mode := range []string{"interrupt", "queue", "after_turn"} {
		h := &fakeHost{alive: true}
		s := newSeat(Spec{}, claudeAgent{}, h, "k19", "")
		s.track("r1", text, "queue")
		k17Idle(s, h, fmt.Sprintf(k17Box, text))
		_, err := s.Send("c1", "/compact", mode)
		if keys, typed := k19Typed(h); err == nil || !strings.Contains(err.Error(), "message r1") || !strings.Contains(err.Error(), "sits in its input box") || keys != "" || len(typed) != 0 {
			t.Errorf("%s: send err %v, keys %q, typed %q; want the command refused for r1's words in the box, nothing pressed or typed", mode, err, keys, typed)
		}
	}
}

// A message just entered is drawn in Claude's output a moment before its
// transcript shows it: for that moment it reads as the one above does. The
// command sent right after it waits for the transcript, and is typed.
func TestK19ACommandWaitsForTheMessageBeforeItToShowAsRead(t *testing.T) {
	k19Fast(t, 5*time.Second)
	const text = "also update the changelog"
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, claudeAgent{}, h, "k19", "")
	s.track("r1", text, "queue")
	k17Idle(s, h, "❯ "+text+"\n\n✻ Flowing…\n\n"+fmt.Sprintf(k17Box, ""))
	p := &claudeParser{}
	go func() {
		time.Sleep(20 * time.Millisecond)
		for _, o := range p.line([]byte(k17Prompt(text))) {
			s.fold(o)
		}
	}()
	start := time.Now()
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatalf("send: %v; want the command typed once r1 showed as read", err)
	}
	if _, typed := k19Typed(h); len(typed) != 1 || typed[0] != "/compact" {
		t.Fatalf("typed %q, want the command", typed)
	}
	if took := time.Since(start); took < 15*time.Millisecond || took > 2*time.Second {
		t.Fatalf("the command was typed after %v; want it to wait for the transcript and no longer", took)
	}
}

// Antigravity's queue is not one the seat follows: a message typed while it
// works stands in that queue, on its screen, until its turn, and a command
// typed meanwhile is an entry of its own. It is typed, as on main.
func TestK19ACommandForAWorkingAgentWhoseQueueIsNotFollowedIsTyped(t *testing.T) {
	k19Fast(t, 0)
	const text = "also update the changelog"
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, antigravityAgent{}, h, "k19", "")
	s.track("r1", text, "queue")
	h.mu.Lock()
	h.screen = strings.Join([]string{"⣽  Working...", "▸ " + text + " (alt+m to send immediately)", realRule, ">", realRule, "Antigravity relay · working · Gemini 4 Neon (XHigh)"}, "\n")
	h.mu.Unlock()
	s.mu.Lock()
	s.state, s.stateAt = "working", time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatalf("send: %v; want the command typed (the words on screen stand in Antigravity's own queue)", err)
	}
}
