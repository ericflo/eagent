package relay

import (
	"strings"
	"testing"
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
