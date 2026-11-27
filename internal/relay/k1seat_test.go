package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// k1Paused is an agent whose seat's watcher waits for the test after each
// read of the transcript (its poll runs right after the read): a line the
// test appends meanwhile is read at the next read, at the moment the test
// chooses (readNow). handOver, when set, is its after-turn hand-over.
type k1Paused struct {
	Agent
	read, more chan struct{}
	handOver   func(s *Seat, id, text string) error
}

func k1Pause(a Agent) *k1Paused {
	return &k1Paused{Agent: a, read: make(chan struct{}), more: make(chan struct{})}
}

func (a *k1Paused) poll(s *Seat, _ time.Time) {
	select {
	case a.read <- struct{}{}:
	case <-s.stop:
		return
	}
	select {
	case <-a.more:
	case <-s.stop:
	}
}

func (a *k1Paused) afterTurn(s *Seat, id, text string) error {
	if a.handOver != nil {
		return a.handOver(s, id, text)
	}
	return a.Agent.afterTurn(s, id, text)
}

// readNow has the waiting watcher read the transcript once more, and
// returns once it has folded what it read.
func (a *k1Paused) readNow(t *testing.T) {
	t.Helper()
	select {
	case a.more <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher is not waiting")
	}
	select {
	case <-a.read:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not read")
	}
}

// k1Watched starts watching a seat for a, working, on a transcript that
// holds first, and returns once its watcher has read it and waits.
func k1Watched(t *testing.T, spec Spec, a *k1Paused, h Host, first string) (*Seat, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeLines(t, path, first)
	s := newSeat(spec, a, h, "k1-"+filepath.Base(filepath.Dir(path)), "thread-1")
	s.transcript, s.state, s.poll = path, "working", time.Millisecond
	go s.watch()
	t.Cleanup(s.Close)
	select {
	case <-a.read:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher never read the transcript")
	}
	return s, path
}

// k1CodexUser is a Codex rollout line recording a user message.
func k1CodexUser(text string) string {
	raw, _ := json.Marshal(text)
	return fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":%s}]}}}`, stamp(time.Now()), raw)
}

func k1CodexMeta() string {
	return fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"thread-1","cwd":"/w","timestamp":"%s"}}`, stamp(time.Now()), stamp(time.Now()))
}

func k1Typed(h *fakeHost) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.typed)
}

// The tailer says where each line it returns starts in the file: across a
// line split between two reads, and when it starts inside a line.
func TestK1TailerSaysWhereEachLineStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n\n  {\"b\":"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &tailer{path: path}
	if lines, starts, _ := tl.next(); len(lines) != 1 || !slices.Equal(starts, []int64{0}) {
		t.Fatalf("first read %q at %v", lines, starts)
	}
	appendLines(t, path, "2}", `{"c":3}`)
	if lines, starts, _ := tl.next(); len(lines) != 2 || !slices.Equal(starts, []int64{9, 19}) {
		t.Fatalf("second read %q at %v", lines, starts)
	}
	mid := &tailer{path: path, offset: 3, skipPartial: true}
	if lines, starts, _ := mid.next(); len(lines) != 2 || !slices.Equal(starts, []int64{9, 19}) {
		t.Fatalf("mid-file read %q at %v", lines, starts)
	}
}

// A14: an after-turn send is tracked before its hand-over, and an entry the
// watcher reads during the hand-over that the agent wrote before the send
// was tracked (an earlier message holding the same words) is not its
// arrival: when the hand-over then fails, the send is typed once, and
// nothing is confirmed. One the agent writes after it is: a hand-over that
// went through and then reported failure is not typed again.
func TestK1AnEntryWrittenBeforeAnAfterTurnSendIsNotItsArrival(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const earlier, text = "fix the build and run the tests", "run the tests"
	for _, c := range []struct {
		name   string
		agent  Agent
		spec   Spec
		first  string
		user   func(string) string
		hand   error // what the hand-over returns; nil: the agent's own
		arrive bool  // the agent records the send during the hand-over
	}{
		{"codex_queue_fails", codexAgent{}, Spec{}, k1CodexMeta(), k1CodexUser, errors.New("codex queue: exit status 1"), false},
		{"claude_without_its_hook", claudeAgent{}, Spec{Spool: filepath.Join(t.TempDir(), "after_turn")}, claudeUser(time.Now(), "sort the shelves"), func(s string) string { return claudeUser(time.Now(), s) }, nil, false},
		{"codex_queue_took_it_then_failed", codexAgent{}, Spec{}, k1CodexMeta(), k1CodexUser, errors.New("codex queue: signal: killed"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := k1Pause(c.agent)
			h := &fakeHost{alive: true}
			s, path := k1Watched(t, c.spec, a, h, c.first)
			// Written before the send; the watcher reads it during the
			// hand-over.
			appendLines(t, path, c.user(earlier))
			a.handOver = func(s *Seat, id, text string) error {
				if c.arrive {
					appendLines(t, path, c.user(text))
				}
				a.readNow(t)
				if c.hand != nil {
					return c.hand
				}
				return a.Agent.afterTurn(s, id, text)
			}
			if _, err := s.Send("r2", text, "after_turn"); err != nil {
				t.Fatal(err)
			}
			ok, typed := okDeliveries(drain(s.events)), k1Typed(h)
			if c.arrive {
				if !slices.Equal(ok, []string{"r2"}) || len(typed) != 0 {
					t.Fatalf("confirmed %v, typed %q; want r2 confirmed by its arrival and nothing typed", ok, typed)
				}
				return
			}
			if len(ok) != 0 || !slices.Equal(typed, []string{text}) {
				t.Fatalf("confirmed %v, typed %q; want nothing confirmed and the message typed once", ok, typed)
			}
		})
	}
	t.Run("handed_over_then_arrives", func(t *testing.T) {
		a := k1Pause(codexAgent{})
		a.handOver = func(*Seat, string, string) error { return nil }
		s, path := k1Watched(t, Spec{}, a, &fakeHost{alive: true}, k1CodexMeta())
		appendLines(t, path, k1CodexUser(earlier))
		if _, err := s.Send("r2", text, "after_turn"); err != nil {
			t.Fatal(err)
		}
		a.readNow(t)
		if ok := okDeliveries(drain(s.events)); len(ok) != 0 {
			t.Fatalf("the earlier entry confirmed %v", ok)
		}
		appendLines(t, path, k1CodexUser(text))
		a.readNow(t)
		if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r2"}) {
			t.Fatalf("its arrival confirmed %v; want r2", ok)
		}
	})
}
