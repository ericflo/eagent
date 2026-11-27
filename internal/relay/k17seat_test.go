package relay

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// k17Line is a queue line as Claude writes it: remove lines give a reason,
// and every line but a dequeue gives the message's words.
func k17Line(op, content, reason string) string {
	line := fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s","sessionId":"s1","content":%q`, op, stamp(time.Now()), content)
	if reason != "" {
		line += fmt.Sprintf(`,"reason":%q`, reason)
	}
	return line + "}"
}

const k17Box = "● Interrupted\n\n╭──────────────────────────────╮\n│ > %s    │\n╰──────────────────────────────╯\n  ? for shortcuts"

// k17Idle sets the seat idle for a minute with screen on its terminal.
func k17Idle(s *Seat, h *fakeHost, screen string) time.Time {
	h.mu.Lock()
	h.screen = screen
	h.mu.Unlock()
	now := time.Now()
	s.mu.Lock()
	s.state, s.stateAt = "idle", now.Add(-time.Minute)
	s.mu.Unlock()
	return now
}

func k17Prompt(text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, stamp(time.Now()), text)
}

// k17Seat is a working Claude seat with r1 typed and tracked, and feed,
// which folds transcript lines into it.
func k17Seat(t *testing.T, text string) (*Seat, *fakeHost, func(...string)) {
	t.Helper()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, claudeAgent{}, h, "k17", "")
	s.state = "working"
	s.track("r1", text, "queue")
	p := &claudeParser{}
	return s, h, func(lines ...string) {
		for _, l := range lines {
			for _, o := range p.line([]byte(l)) {
				s.fold(o)
			}
		}
	}
}

func k17Arrivals(s *Seat, id string) (ok, failed int) {
	for _, ev := range drain(s.Events()) {
		if ev.Kind == KindDelivered && ev.SendID == id {
			if ev.OK {
				ok++
			} else {
				failed++
			}
		}
	}
	return ok, failed
}

func k17Watched(s *Seat) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, p := range s.pending {
		ids = append(ids, p.id)
	}
	return ids
}

// A message typed while Claude works arrives at its enqueue line. An
// interrupt (relay_control's Escape) then empties Claude's queue back into
// the input box (popAll), unread. The send is watched again: once Claude
// is idle and its words sit in the box the seat presses Enter, as for any
// send left there, and the prompt that follows confirms it. (It stayed
// recorded as arrived while it sat in the box for good; main the same.)
func TestK17AQueuedSendPutBackInTheBoxByAnInterruptIsSubmitted(t *testing.T) {
	const text = "also update the changelog"
	s, h, feed := k17Seat(t, text)
	feed(k1Queue("enqueue", text))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 || len(k17Watched(s)) != 0 {
		t.Fatalf("after the enqueue line: %d arrivals, watched %v; want it arrived and not watched", ok, k17Watched(s))
	}
	feed(k1Queue("popAll", ""), k17Prompt("[Request interrupted by user]"))
	if got := k17Watched(s); !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("after the interrupt put it back into the input box: watched %v; want r1", got)
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != "idle" {
		t.Fatalf("state %q after the interrupt; want idle", state)
	}
	now := k17Idle(s, h, fmt.Sprintf(k17Box, text))
	for _, after := range []time.Duration{1, 4, 7, 10} {
		s.checkScreen(now.Add(after * time.Second))
	}
	h.mu.Lock()
	keys := append([]string(nil), h.keys...)
	h.mu.Unlock()
	if !slices.Contains(keys, "Enter") {
		t.Fatalf("keys %q; want Enter pressed on the message left in the box", keys)
	}
	feed(k17Prompt(text))
	if ok, failed := k17Arrivals(s, "r1"); ok != 1 || failed != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("after the prompt showed it: %d arrivals, %d failures, watched %v; want it confirmed", ok, failed, k17Watched(s))
	}
}

// A queued message Claude takes at the end of its turn is shown by the
// prompt after its dequeue line. It is not watched again by a later
// interrupt that finds the queue empty, nor by one that puts another
// message of the same words back (the user's own, typed in the terminal
// after it).
func TestK17AQueuedSendAPromptShowedIsNotWatchedAgain(t *testing.T) {
	const text = "also update the changelog"
	s, _, feed := k17Seat(t, text)
	feed(k1Queue("enqueue", text), k1Queue("dequeue", ""), k17Prompt(text))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 {
		t.Fatalf("arrivals %d; want one, at the enqueue line", ok)
	}
	feed(k1Queue("popAll", ""))
	feed(k1Queue("enqueue", text), k1Queue("popAll", ""))
	if ok, failed := k17Arrivals(s, "r1"); ok != 0 || failed != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("after a prompt showed it: %d more arrivals, %d failures, watched %v; want nothing more of it", ok, failed, k17Watched(s))
	}
}

// Claude writes a popAll line for each message it puts back, with its
// words. Two were queued, the relay's and one a person typed: the relay's
// is watched again once, and the person's is nobody's send.
func TestK17EachQueuedMessagePutBackHasItsOwnLine(t *testing.T) {
	const text, theirs = "also update the changelog", "and bump the version"
	s, _, feed := k17Seat(t, text)
	feed(k17Line("enqueue", text, ""), k17Line("enqueue", theirs, ""))
	drain(s.Events())
	feed(k17Line("popAll", text, ""), k17Line("popAll", theirs, ""))
	if got := k17Watched(s); !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("watched %v; want r1 once", got)
	}
	if ok, failed := k17Arrivals(s, "r1"); ok != 0 || failed != 0 {
		t.Fatalf("%d arrivals and %d failures said as the queue gave it back; want none until a prompt or the wait says", ok, failed)
	}
}

// One queued message leaves the queue unread on a line of its own: pulled
// up into the input box to edit (popOne), or thrown away with the queue
// (remove, reason cleared_on_cancel). The send is watched again. One that
// a remove line takes into the running turn (no reason, or absorbed) was
// read: it stays arrived and is not watched.
func TestK17AQueuedSendThatLeftTheQueueOnItsOwn(t *testing.T) {
	const text = "also update the changelog"
	for _, c := range []struct {
		op, reason string
		watched    bool
	}{
		{"popOne", "", true},
		{"remove", "cleared_on_cancel", true},
		{"remove", "", false},
		{"remove", "absorbed_mid_turn", false},
	} {
		s, _, feed := k17Seat(t, text)
		feed(k17Line("enqueue", text, ""))
		if ok, _ := k17Arrivals(s, "r1"); ok != 1 {
			t.Fatalf("%s %s: arrivals at the enqueue line %d; want one", c.op, c.reason, ok)
		}
		feed(k17Line(c.op, text, c.reason))
		if got := k17Watched(s); slices.Equal(got, []string{"r1"}) != c.watched || !c.watched && len(got) != 0 {
			t.Errorf("%s %s: watched %v; want watched again: %v", c.op, c.reason, got, c.watched)
		}
	}
}

// A message the queue gave up that no prompt shows (thrown away, or taken
// out of the box by a person) is reported not confirmed after the usual
// wait, once, with what happened to it; no Enter goes on a box that does
// not hold it.
func TestK17AQueuedSendGivenUpAndNeverShownIsReported(t *testing.T) {
	const text = "also update the changelog"
	s, h, feed := k17Seat(t, text)
	feed(k17Line("enqueue", text, ""))
	drain(s.Events())
	feed(k17Line("remove", text, "cleared_on_cancel"), k17Prompt("[Request interrupted by user]"))
	now := k17Idle(s, h, fmt.Sprintf(k17Box, ""))
	for _, after := range []time.Duration{1, 4, 7, 10, 44} {
		s.checkScreen(now.Add(after * time.Second))
	}
	if got := deliveries(drain(s.Events())); len(got) != 0 {
		t.Fatalf("said before the wait was over: %+v", got)
	}
	for _, after := range []time.Duration{46, 50, 120} {
		s.checkScreen(now.Add(after * time.Second))
	}
	got := deliveries(drain(s.Events()))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed || !strings.Contains(got[0].Detail, "gave it up unread") {
		t.Fatalf("said %+v; want r1 not confirmed once, saying the queue gave it up", got)
	}
	h.mu.Lock()
	keys := append([]string(nil), h.keys...)
	h.mu.Unlock()
	if len(keys) != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("pressed %q, still watched %v", keys, k17Watched(s))
	}
}

// The seat is closed (a restart, a switch, the session's end) while a
// message the queue gave back still waits for its prompt. The record has
// it arrived, and nothing watches it after the seat: the seat's last word
// on it is that it was not confirmed.
func TestK17AClosedSeatSaysAQueuedSendGivenBackWasNotConfirmed(t *testing.T) {
	const text = "also update the changelog"
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeLines(t, path, k17Prompt("start on the parser"))
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, claudeAgent{}, h, "k17", "")
	s.transcript, s.state, s.poll = path, "working", time.Millisecond
	go s.watch()
	t.Cleanup(s.Close)
	s.track("r1", text, "queue")
	appendLines(t, path, k17Line("enqueue", text, ""))
	if !eventually(func() bool { return len(k17Watched(s)) == 0 }) {
		t.Fatal("the enqueue line was not read")
	}
	appendLines(t, path, k17Line("popAll", text, ""))
	if !eventually(func() bool { return slices.Equal(k17Watched(s), []string{"r1"}) }) {
		t.Fatalf("watched %v after the queue gave it back; want r1", k17Watched(s))
	}
	s.Close()
	var said []Event
	for ev := range s.Events() {
		if ev.Kind == KindDelivered {
			said = append(said, ev)
		}
	}
	if len(said) != 2 || !said[0].OK || said[1].OK || said[1].SendID != "r1" || said[1].Outcome != OutcomeUnconfirmed || said[1].Read != 0 {
		t.Fatalf("the seat said %+v; want the arrival at the enqueue line, then not confirmed as its last word (with no read mark)", said)
	}
}

// Two queued messages of the same words, a person's and then the relay's.
// Claude takes the first at the end of its turn, and its prompt shows it:
// the relay's is still queued. Escape then puts it back into the box, and
// it is watched again. (Which message a line is about goes by its place in
// the queue, not by its words.)
func TestK17TheSameWordsQueuedTwice(t *testing.T) {
	const text = "continue"
	s, _, feed := k17Seat(t, text)
	s.mu.Lock()
	s.pending = nil // the person's message comes first: r1 is typed after it
	s.mu.Unlock()
	feed(k17Line("enqueue", text, ""))
	s.track("r1", text, "queue")
	feed(k17Line("enqueue", text, ""))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 {
		t.Fatalf("arrivals at its own enqueue line %d; want one", ok)
	}
	feed(k1Queue("dequeue", ""), k17Prompt(text))
	feed(k17Line("popAll", text, ""))
	if got := k17Watched(s); !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("watched %v after its own line was given back; want r1", got)
	}
}

// A queued message the running turn took (a remove line) is done with:
// the same words queued and given back later are another message, and a
// send confirmed long ago is not watched again.
func TestK17AQueuedSendTheTurnTookIsNotWatchedAgainByTheSameWordsLater(t *testing.T) {
	const text = "continue"
	s, _, feed := k17Seat(t, text)
	feed(k17Line("enqueue", text, ""), k17Line("remove", text, "absorbed_mid_turn"))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 {
		t.Fatalf("arrivals %d; want one", ok)
	}
	feed(k17Line("enqueue", text, ""), k17Line("popAll", text, ""))
	if ok, failed := k17Arrivals(s, "r1"); ok != 0 || failed != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("%d more arrivals, %d failures, watched %v; want nothing more of r1", ok, failed, k17Watched(s))
	}
}
