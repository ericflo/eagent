package relay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Finding 13: on a reattach, an entry Claude stamped to the millisecond
// before a restored send was typed is an earlier send of the same words,
// not this one's arrival; only a transcript that stamps to the whole
// second (Antigravity) gets the second's grace.
func TestR7AnEarlierSameSecondEntryDoesNotConfirmARestoredSend(t *testing.T) {
	t.Parallel()
	sec := time.Now().Add(-time.Minute).Truncate(time.Second)
	r := Restored{ID: "r2", Text: "continue", Mode: "queue", At: sec.Add(500 * time.Millisecond)}
	s := attachSeat(Spec{Restore: []Restored{r}}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", sec.Add(800*time.Millisecond))
	if ok := claudeLines(s, claudeUser(sec.Add(100*time.Millisecond), "continue")); len(ok) != 0 {
		t.Fatalf("an entry 400ms before r2 was typed confirmed %v", ok)
	}
}

// Finding 7: a long send the transcript shows cut short keeps its entry
// against a short waiting send whose words sit inside it ("yes" inside
// "yesterday"); the short one has not arrived.
func TestR7ACutShortLongSendIsNotTakenByAShortSendInsideIt(t *testing.T) {
	t.Parallel()
	long := "Please check yesterday's deployment log for the payment service " + strings.Repeat("and list every error it shows with the time it happened ", 4)
	if len(long) <= 200 {
		t.Fatalf("long is %d bytes, want over 200", len(long))
	}
	for _, entry := range []string{long[:100], long[:200] + "…"} {
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.track("r1", "yes", "queue")
		s.track("r2", long, "queue")
		if got := s.matchPending(entry); !slices.Equal(got, []string{"r2"}) {
			t.Errorf("the entry %q confirmed %v, want r2", entry, got)
		}
	}
}

// Finding 6: an after-turn send typed straight into the prompt, because
// the agent was idle or the hand-over to the end of its turn failed, is
// held by nothing, so it is watched on the timer as any typed send is.
func TestR7AnAfterTurnSendTypedStraightInIsTimed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	for _, state := range []string{"idle", "working"} { // working: no Stop hook, so the hand-over fails
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.state = state
		if _, err := s.Send("r1", "count the chairs", "after_turn"); err != nil {
			t.Fatalf("%s: Send: %v", state, err)
		}
		s.mu.Lock()
		timed := len(s.pending) == 1 && s.pending[0].timed()
		s.mu.Unlock()
		if !timed {
			t.Errorf("%s: the typed after-turn send is not on the timer", state)
		}
	}
}

// Finding 5: a restart (Launch with resume) starts with an empty spool. The
// harness reports every send the old agent still waited on as unconfirmed
// at the restart, and eagent may send it again; a file left in the spool
// would be handed to the new agent as well (pi's extension does so on
// startup, Claude's Stop hook at its next turn end): the message twice.
func TestR7ARestartDropsTheSpool(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	old := Socket
	Socket = fmt.Sprintf("eagent-r7spool-%d", os.Getpid())
	defer func() {
		KillSocket(Socket)
		Socket = old
	}()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	work, state := t.TempDir(), t.TempDir()
	profile := config.BuiltinRelayProfiles()["pi"]
	profile.Host = "tmux"
	spec := Spec{Session: fmt.Sprintf("r7spool%d", os.Getpid()), Name: "pi", Profile: profile, Dir: work}
	if err := Prepare(&spec, state); err != nil {
		t.Fatal(err)
	}
	if err := spoolMessage(spec.Spool, "r1", "deploy to prod"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Spool, "1-r0.txt.taken"), []byte("claimed, never finished"), 0o644); err != nil {
		t.Fatal(err)
	}
	seat, _, err := Launch(spec, "inner-1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Kill()
	names, _ := filepath.Glob(filepath.Join(spec.Spool, "*"))
	if len(names) != 0 {
		t.Fatalf("the restarted agent inherits %v", names)
	}
}

// arrivingHost makes the agent record the message the moment Enter goes,
// and the watcher read it before Send returns.
type arrivingHost struct {
	*fakeHost
	seat *Seat
	text string
}

func (h *arrivingHost) Keys(target string, keys ...string) error {
	err := h.fakeHost.Keys(target, keys...)
	if slices.Contains(keys, "Enter") {
		h.seat.fold(obs{kind: "user", text: h.text})
	}
	return err
}

// Finding 10, first half: a send whose arrival the transcript shows while
// its Enter is still being pressed is confirmed, not left waiting.
func TestR7AnArrivalReadBeforeSendReturnsIsConfirmed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &arrivingHost{fakeHost: &fakeHost{alive: true}, text: "count the chairs"}
	s := newSeat(Spec{}, claudeAgent{}, h, "x", "")
	h.seat = s
	s.state = "idle"
	if _, err := s.Send("r1", h.text, "queue"); err != nil {
		t.Fatal(err)
	}
	var ok []string
	for _, ev := range deliveries(drain(s.events)) {
		ok = append(ok, fmt.Sprintf("%s:%v", ev.SendID, ev.OK))
	}
	if !slices.Equal(ok, []string{"r1:true"}) || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("reports %v, still pending %v", ok, s.Snapshot().Pending)
	}
}

type failingPasteHost struct{ *fakeHost }

func (failingPasteHost) Paste(string, string) error { return errors.New("no such pane") }

// Finding 10: a send whose typing fails is not left tracked; the caller
// reports it, and the seat reports nothing of it.
func TestR7ASendWhoseTypingFailsIsNotLeftTracked(t *testing.T) {
	t.Parallel()
	s := newSeat(Spec{}, claudeAgent{}, failingPasteHost{&fakeHost{alive: true}}, "x", "")
	s.state = "idle"
	_, err := s.Send("r1", "count the chairs", "queue")
	if !errors.As(err, new(*TypingError)) {
		t.Fatalf("Send returned %v", err)
	}
	if p := s.Snapshot().Pending; len(p) != 0 || len(drain(s.events)) != 0 {
		t.Fatalf("still pending %v", p)
	}
}

// sweepingAgent is Claude whose turn ends, and whose left-overs are swept,
// right after the held message is written to the spool.
type sweepingAgent struct{ claudeAgent }

func (a sweepingAgent) afterTurn(s *Seat, id, text string) error {
	err := a.claudeAgent.afterTurn(s, id, text)
	s.mu.Lock()
	s.state, s.turnEndAt = "idle", time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.reportLeftOvers(time.Now())
	return err
}

// Finding 10, second half: a held message swept from the spool as soon as
// it is written is reported once (not sent), and not tracked after that.
func TestR7AHeldMessageSweptAtOnceIsReportedOnce(t *testing.T) {
	t.Parallel()
	spool := filepath.Join(t.TempDir(), "after_turn")
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, sweepingAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	if _, err := s.Send("r1", "then commit", "after_turn"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range deliveries(drain(s.events)) {
		got = append(got, ev.SendID+":"+ev.Outcome)
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeNotSent}) || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("reports %v, still pending %v", got, s.Snapshot().Pending)
	}
}

// closingHost closes the seat the first time the send takes its last look
// at the screen before typing: a stop that lands after the seat said it was
// ready and before the text is pasted.
type closingHost struct {
	*fakeHost
	once sync.Once
	seat *Seat
}

func (h *closingHost) Capture(target string, lines int) (string, error) {
	h.once.Do(h.seat.Close)
	return h.fakeHost.Capture(target, lines)
}

// Finding 12: a seat closed after it was found ready, before the text is
// typed, types nothing and says so with ErrClosed (not_sent), not as a
// send that may have gone.
func TestR7ASeatClosedJustBeforeTypingTypesNothing(t *testing.T) {
	t.Parallel()
	fh := &fakeHost{alive: true}
	h := &closingHost{fakeHost: fh}
	s := newSeat(Spec{}, claudeAgent{}, h, "x", "")
	h.seat = s
	s.state = "idle"
	close(s.done)
	_, err := s.Send("r1", "count the chairs", "queue")
	if !errors.Is(err, ErrClosed) {
		t.Errorf("Send returned %v, want ErrClosed", err)
	}
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.typed) != 0 || len(fh.keys) != 0 {
		t.Errorf("typed %q, pressed %q into a closed seat", fh.typed, fh.keys)
	}
}

// Two sends the hook hands over together, while a third waiting send
// begins with both of them: the entry shows the two whole and the third
// only in part, so the two are confirmed (an independent read's case).
func TestSendsShownWholeAreNotLeftForALongerOneShownInPart(t *testing.T) {
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("a", "check the logs", "queue")
	s.track("b", "restart the server", "queue")
	s.track("c", "check the logs\n\nrestart the server and then tell me what failed", "queue")
	if got := s.matchPending("check the logs\n\nrestart the server"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("the joined entry confirmed %v, want [a b]", got)
	}
}
