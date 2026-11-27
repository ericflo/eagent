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

// k1Host runs paste in place of the paste, when set, and enter as Enter
// goes: what the agent writes to its transcript then, and the watcher reads
// before Send returns.
type k1Host struct {
	*fakeHost
	paste, enter func() error
}

func (h *k1Host) Paste(target, text string) error {
	if h.paste != nil {
		return h.paste()
	}
	return h.fakeHost.Paste(target, text)
}

func (h *k1Host) Keys(target string, keys ...string) error {
	err := h.fakeHost.Keys(target, keys...)
	if slices.Contains(keys, "Enter") && h.enter != nil {
		return h.enter()
	}
	return err
}

// A15: a typed send is tracked before it is typed, so its arrival, read
// while its Enter goes, confirms it (also when the Enter's command then
// reports a failure: the message arrived). An entry written before the send
// was tracked, read then or during a paste that fails, confirms nothing: a
// failed paste goes back to the caller, untracked.
func TestK1AnArrivalReadWhileEnterGoesIsConfirmed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const text = "count the chairs"
	user := func(s string) string { return claudeUser(time.Now(), s) }
	start := func(t *testing.T) (*k1Paused, *k1Host, *Seat, string) {
		a := k1Pause(claudeAgent{})
		h := &k1Host{fakeHost: &fakeHost{alive: true}}
		s, path := k1Watched(t, Spec{}, a, h, user("sort the shelves"))
		return a, h, s, path
	}
	for _, c := range []struct {
		name string
		err  error // what Enter's command returns
	}{
		{"enter_returns", nil},
		{"enter_goes_then_fails", errors.New("tmux: lost server")},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, h, s, path := start(t)
			h.enter = func() error {
				appendLines(t, path, user(text))
				a.readNow(t)
				return c.err
			}
			if _, err := s.Send("r1", text, "queue"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if ok, p := okDeliveries(drain(s.events)), s.Snapshot().Pending; !slices.Equal(ok, []string{"r1"}) || len(p) != 0 {
				t.Fatalf("confirmed %v, still pending %v; want r1 confirmed", ok, p)
			}
		})
	}
	t.Run("earlier_entry_read_while_enter_goes", func(t *testing.T) {
		a, h, s, path := start(t)
		appendLines(t, path, user("then "+text))
		h.enter = func() error { a.readNow(t); return nil }
		if _, err := s.Send("r1", text, "queue"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if ok, p := okDeliveries(drain(s.events)), s.Snapshot().Pending; len(ok) != 0 || len(p) != 1 {
			t.Fatalf("confirmed %v, pending %v; want r1 pending", ok, p)
		}
		appendLines(t, path, user(text))
		a.readNow(t)
		if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r1"}) {
			t.Fatalf("its arrival confirmed %v; want r1", ok)
		}
	})
	t.Run("earlier_entry_read_while_the_paste_fails", func(t *testing.T) {
		a, h, s, path := start(t)
		appendLines(t, path, user("then "+text))
		h.paste = func() error { a.readNow(t); return errors.New("no such pane") }
		if _, err := s.Send("r1", text, "queue"); !errors.As(err, new(*TypingError)) {
			t.Fatalf("Send returned %v; the paste failed", err)
		}
		if ok, p := okDeliveries(drain(s.events)), s.Snapshot().Pending; len(ok) != 0 || len(p) != 0 {
			t.Fatalf("confirmed %v, pending %v; want neither", ok, p)
		}
	})
}

// k1Queue is Claude's record of its queue: op is enqueue, dequeue or remove.
func k1Queue(op, content string) string {
	ts := stamp(time.Now())
	if content == "" {
		return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s"}`, op, ts)
	}
	return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s","content":%q}`, op, ts, content)
}

func k1TurnEnd() string {
	return fmt.Sprintf(`{"type":"system","subtype":"turn_duration","timestamp":"%s","durationMs":1000}`, stamp(time.Now()))
}

// k1Case feeds Claude transcript lines to a working seat: sends tracked
// first, then lines, then later sends, then the last line.
type k1Case struct {
	name  string
	sends [][2]string // id, text
	lines []string
	later [][2]string
	last  string
	want  []string // the sends confirmed
}

func (c k1Case) run(t *testing.T) {
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	for _, p := range c.sends {
		s.track(p[0], p[1], "queue")
	}
	p := &claudeParser{}
	feed := func(l string) {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	for _, l := range c.lines {
		feed(l)
	}
	for _, p := range c.later {
		s.track(p[0], p[1], "queue")
	}
	feed(c.last)
	if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, c.want) {
		t.Fatalf("confirmed %v; want %v", ok, c.want)
	}
}

// A16: Claude records a message typed while it works twice, as it queues
// it (an enqueue line) and as the prompt it runs once it takes it off the
// queue (the user line right after a dequeue). That is one arrival: with
// two waiting sends of the same words, it confirms one, live or in the
// history a reattached seat re-reads; and two queued messages run one
// after the other confirm their own two sends and not a third.
func TestK1ClaudesQueuedMessageConfirmsOneSend(t *testing.T) {
	for _, c := range []k1Case{
		{"one_queued_two_waiting",
			[][2]string{{"r1", "continue"}, {"r2", "continue"}}, []string{k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", "")},
			nil, claudeUser(time.Now(), "continue"), []string{"r1"}},
		{"two_queued_run_one_by_one",
			[][2]string{{"r1", "continue"}, {"r2", "continue"}}, []string{k1Queue("enqueue", "continue"), k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", ""), claudeUser(time.Now(), "continue"), k1TurnEnd(), k1Queue("dequeue", "")},
			[][2]string{{"r3", "continue"}}, claudeUser(time.Now(), "continue"), []string{"r1", "r2"}},
		{"two_queued_run_joined",
			[][2]string{{"r1", "continue"}, {"r2", "continue"}, {"r3", "check the logs"}}, []string{k1Queue("enqueue", "continue"), k1Queue("enqueue", "check the logs"), k1TurnEnd(), k1Queue("dequeue", "")},
			nil, claudeUser(time.Now(), "continue\n\ncheck the logs"), []string{"r1", "r3"}},
	} {
		t.Run(c.name, c.run)
	}
	t.Run("history", func(t *testing.T) {
		since := time.Now()
		sent := since.Add(-time.Minute)
		s := attachSeat(Spec{Restore: []Restored{{ID: "r1", Text: "continue", Mode: "queue", At: sent}, {ID: "r2", Text: "continue", Mode: "queue", At: sent}}}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", since)
		at := sent.Add(time.Second)
		history := []string{
			fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"%s","content":"continue"}`, stamp(at)),
			fmt.Sprintf(`{"type":"queue-operation","operation":"dequeue","timestamp":"%s"}`, stamp(at)),
			claudeUser(at, "continue"),
		}
		if ok := claudeLines(s, history...); !slices.Equal(ok, []string{"r1"}) {
			t.Fatalf("confirmed %v; want r1 only", ok)
		}
	})
}

// What stays an arrival: a prompt typed again after its queued copy was
// taken into the running turn (no dequeue before it), a prompt run off the
// queue whose enqueue line showed other words (so the prompt is the first
// record of the send), and a prompt one line further than the dequeue.
func TestK1ClaudePromptsThatStayArrivals(t *testing.T) {
	long := "check the migration plan\nagainst the schema"
	for _, c := range []k1Case{
		{"typed_again_after_its_copy_ran_in_the_turn",
			[][2]string{{"r1", "continue"}}, []string{k1Queue("enqueue", "continue"), k1Queue("remove", "continue"), k1TurnEnd()},
			[][2]string{{"r2", "continue"}}, claudeUser(time.Now(), "continue"), []string{"r1", "r2"}},
		{"queued_as_other_words",
			[][2]string{{"r1", long}}, []string{k1Queue("enqueue", "[Pasted text #1 +1 lines]"), k1TurnEnd(), k1Queue("dequeue", "")},
			nil, claudeUser(time.Now(), long), []string{"r1"}},
		{"a_line_after_the_dequeue",
			[][2]string{{"r1", "continue"}}, []string{k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", ""), `{"type":"user","isMeta":true,"timestamp":"` + stamp(time.Now()) + `","message":{"content":"continue"}}`},
			[][2]string{{"r2", "continue"}}, claudeUser(time.Now(), "continue"), []string{"r1", "r2"}},
	} {
		t.Run(c.name, c.run)
	}
}

// A2: an after-turn send handed to the agent's own queue (`codex queue`,
// Antigravity's native queue) waits for the end of a turn. Once the
// transcript showed a turn over after it and the agent stayed idle the
// grace a held message gets, nothing will run it (the agent dropped its
// queue): it is watched on the timer from then, never typed again, and
// reported unconfirmed when it never arrives; its arrival still confirms
// it. A turn that starts within the grace (the queue runs) loses nothing.
func TestK1AnAfterTurnSendTheAgentsQueueDroppedIsReported(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const text = "then commit the fix"
	for _, c := range []struct {
		name  string
		agent Agent
	}{
		{"codex", handOverWorks{}},
		{"antigravity", antigravityAgent{}},
	} {
		start := func(t *testing.T) (*Seat, *fakeHost) {
			h := &fakeHost{alive: true}
			s := newSeat(Spec{}, c.agent, h, "k1q-"+c.name, "thread-1")
			s.state = "working"
			if _, err := s.Send("r1", text, "after_turn"); err != nil {
				t.Fatal(err)
			}
			s.fold(obs{kind: "turn", text: "Done."})
			return s, h
		}
		poll := func(s *Seat, now time.Time) {
			if pl, ok := s.agent.(poller); ok {
				pl.poll(s, now)
			}
		}
		t.Run(c.name+"/dropped", func(t *testing.T) {
			s, h := start(t)
			typed := len(k1Typed(h))
			poll(s, time.Now().Add(heldGrace))
			s.mu.Lock()
			s.stateAt = time.Now().Add(-time.Hour)
			s.mu.Unlock()
			later := time.Now().Add(time.Hour)
			s.checkScreen(later)
			var got []string
			for _, ev := range deliveries(drain(s.events)) {
				got = append(got, ev.SendID+":"+ev.Outcome)
			}
			if !slices.Equal(got, []string{"r1:" + OutcomeUnconfirmed}) || len(k1Typed(h)) != typed {
				t.Fatalf("reported %v, typed %q; want r1 unconfirmed and nothing typed again (pending %v)", got, k1Typed(h), s.Snapshot().Pending)
			}
		})
		t.Run(c.name+"/dropped_then_arrives", func(t *testing.T) {
			s, _ := start(t)
			poll(s, time.Now().Add(heldGrace))
			s.fold(obs{kind: "user", text: text})
			if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r1"}) {
				t.Fatalf("confirmed %v; want r1", ok)
			}
		})
		t.Run(c.name+"/the_queue_runs", func(t *testing.T) {
			s, _ := start(t)
			s.fold(obs{kind: "working"})
			poll(s, time.Now().Add(time.Hour))
			s.mu.Lock()
			lost := len(s.pending) == 1 && s.pending[0].lost
			s.mu.Unlock()
			if lost {
				t.Fatal("a send the next turn may run was taken as dropped")
			}
		})
	}
}

// k1ClosedDuringHandOver is an agent whose seat is closed (a restart, a
// switch, a stop) while its after-turn hand-over runs: after it held the
// message, and, with take, after the agent's hook claimed it too.
type k1ClosedDuringHandOver struct {
	Agent
	take bool
}

func (a k1ClosedDuringHandOver) afterTurn(s *Seat, id, text string) error {
	err := a.Agent.afterTurn(s, id, text)
	if a.take {
		takeSpool(s.spec.Spool)
	}
	s.Close()
	return err
}

// A8: a seat closed during the after-turn hand-over holds nothing, as one
// closed before it does (5a66cbd): a message the hand-over left in the
// spool, which no hook has claimed, is taken back and Send says ErrClosed,
// so the harness hands it to the agent that took over. One a hook claimed
// meanwhile may be in the agent: it is left to it, and Send says held, as
// it does for a message handed to Codex's own queue (nothing to take back).
func TestK1AnAfterTurnSendToASeatClosedDuringItsHandOverHoldsNothing(t *testing.T) {
	pi := piAgent{name: "pi", label: "Pi", binary: "pi"}
	for _, c := range []struct {
		name  string
		agent Agent
		spool bool
		take  bool
		want  error // nil: held
	}{
		{"claude", claudeAgent{}, true, false, ErrClosed},
		{"pi", pi, true, false, ErrClosed},
		{"claude_hook_claimed_it", claudeAgent{}, true, true, nil},
		{"codex_queue", handOverWorks{}, false, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := Spec{Name: "x"}
			if c.spool {
				spec.Spool, spec.Hook = filepath.Join(t.TempDir(), "after_turn"), "eagent relay-hook"
			}
			h := &fakeHost{alive: true}
			s := newSeat(spec, k1ClosedDuringHandOver{Agent: c.agent, take: c.take}, h, "k1c-"+c.name, "thread-1")
			s.state = "working"
			close(s.done)
			msg, err := s.Send("r1", "then run the tests", "after_turn")
			if !errors.Is(err, c.want) || c.want == nil && err != nil {
				t.Fatalf("Send returned %q, %v; want %v", msg, err, c.want)
			}
			names := spoolNames(spec.Spool)
			switch {
			case c.want != nil && (len(names) != 0 || len(s.Snapshot().Pending) != 0):
				t.Fatalf("spool %v, pending %v; want the message taken back", names, s.Snapshot().Pending)
			case c.take && len(names) != 1:
				t.Fatalf("spool %v; want the hook's claimed file left to it", names)
			}
			if typed := k1Typed(h); len(typed) != 0 {
				t.Fatalf("typed %q", typed)
			}
		})
	}
}
