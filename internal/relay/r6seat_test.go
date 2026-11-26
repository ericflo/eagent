package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// One user entry in the transcript is one arrival: two pending sends of
// the same words are not both confirmed by it, and a send whose words are
// part of another's is not confirmed by the other's arrival.
func TestOneTranscriptEntryConfirmsOneSend(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		pending [][2]string // id, text
		entry   string
		want    []string
	}{
		{"two identical", [][2]string{{"r1", "continue"}, {"r2", "continue"}}, "continue", []string{"r1"}},
		{"negation first", [][2]string{{"r1", "do not deploy"}, {"r2", "deploy"}}, "do not deploy", []string{"r1"}},
		{"negation second", [][2]string{{"r1", "deploy"}, {"r2", "do not deploy"}}, "do not deploy", []string{"r2"}},
	} {
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		for _, p := range c.pending {
			s.track(p[0], p[1], "queue")
		}
		if got := s.matchPending(c.entry); !slices.Equal(got, c.want) {
			t.Errorf("%s: the entry %q confirmed %v, want %v", c.name, c.entry, got, c.want)
		}
	}
}

// view is a pending send for the table: typed (t), held and still waiting
// in the spool (w), or held and taken by the hook (h).
func view(kind, text string) sendView {
	return sendView{text: text, waiting: kind == "w", handed: kind == "h"}
}

// The rule arrivals' comment states, row by row. Each row is a pending
// list (oldest first) and the user entries the transcript shows, in order;
// what an entry confirms leaves the list before the next entry is matched.
func TestArrivalsTable(t *testing.T) {
	t.Parallel()
	intro := strings.Repeat("CI failed on main. Read the log below, find the cause and fix it. ", 3)
	longA := intro + "\nFAIL internal/relay TestOne"
	longB := intro + "\nFAIL internal/harness TestTwo"
	long := strings.Repeat("Read the log and fix the failing test. ", 8) + "The end."
	type send struct{ id, kind, text string }
	for _, c := range []struct {
		name    string
		pending []send
		entries []string
		want    [][]string // per entry
	}{
		{"two identical sends, one entry: the older",
			[]send{{"r1", "t", "continue"}, {"r2", "t", "continue"}}, []string{"continue"}, [][]string{{"r1"}}},
		{"two identical sends, two entries: one each",
			[]send{{"r1", "t", "continue"}, {"r2", "t", "continue"}}, []string{"continue", "continue"}, [][]string{{"r1"}, {"r2"}}},
		{"do not deploy, then deploy: the entry do not deploy",
			[]send{{"r1", "t", "do not deploy"}, {"r2", "t", "deploy"}}, []string{"do not deploy", "deploy"}, [][]string{{"r1"}, {"r2"}}},
		{"deploy, then do not deploy: the entry do not deploy",
			[]send{{"r1", "t", "deploy"}, {"r2", "t", "do not deploy"}}, []string{"do not deploy", "deploy"}, [][]string{{"r2"}, {"r1"}}},
		{"a send that contains another's text",
			[]send{{"r1", "t", "run the tests"}, {"r2", "t", "run the tests and then push"}}, []string{"run the tests and then push", "run the tests"}, [][]string{{"r2"}, {"r1"}}},
		{"a hook joins two held messages",
			[]send{{"r1", "h", "first"}, {"r2", "h", "second"}}, []string{"first\n\nsecond"}, [][]string{{"r1", "r2"}}},
		{"a hook joins two identical held messages",
			[]send{{"r1", "h", "continue"}, {"r2", "h", "continue"}}, []string{"continue\n\ncontinue"}, [][]string{{"r1", "r2"}}},
		{"a hook joins a held message with one the seat does not know (held before a restart)",
			[]send{{"r1", "h", "then run the tests"}}, []string{"an older held message\n\nthen run the tests"}, [][]string{{"r1"}}},
		{"held messages with a common long start, joined in either order",
			[]send{{"r1", "h", longA}, {"r2", "h", longB}}, []string{longB + "\n\n" + longA}, [][]string{{"r1", "r2"}}},
		{"typed messages joined at a line break (pi puts queued ones back in its editor)",
			[]send{{"r1", "t", "first"}, {"r2", "t", "second"}}, []string{"first\nsecond"}, [][]string{{"r1", "r2"}}},
		{"a long message the transcript cuts short",
			[]send{{"r1", "t", long}}, []string{long[:200] + "…"}, [][]string{{"r1"}}},
		{"a long message the transcript cuts short without a mark",
			[]send{{"r1", "t", long}}, []string{long[:200]}, [][]string{{"r1"}}},
		{"a reflowed message",
			[]send{{"r1", "t", long}}, []string{strings.ReplaceAll(long, ". ", ".\n  ")}, [][]string{{"r1"}}},
		{"two long messages with a common first 160 bytes",
			[]send{{"r1", "t", longA}, {"r2", "t", longB}}, []string{longB, longA}, [][]string{{"r2"}, {"r1"}}},
		{"two long messages with a common first 160 bytes, cut after they differ",
			[]send{{"r1", "t", longA}, {"r2", "t", longB}}, []string{longB[:len(intro)+20] + "…"}, [][]string{{"r2"}}},
		{"an entry that is not a send (the user typed at the terminal)",
			[]send{{"r1", "t", "continue"}}, []string{"what are you doing?"}, [][]string{nil}},
		{"a short entry that is the start of a send is not it",
			[]send{{"r1", "t", "deploy to staging and tell me"}}, []string{"deploy"}, [][]string{nil}},
		{"an entry that quotes a typed send inside other text",
			[]send{{"r1", "t", "deploy"}}, []string{"Earlier you said:\n\ndeploy\n\nShould I still?"}, [][]string{nil}},
		{"an entry that quotes a typed send on one line",
			[]send{{"r1", "t", "fix the login test"}}, []string{"The user said fix the login test, so I did."}, [][]string{nil}},
		{"a typed send and other text joined at a line break",
			[]send{{"r1", "t", "first"}}, []string{"first\nsecond"}, [][]string{nil}},
		{"a held message still waiting in the spool, quoted",
			[]send{{"r1", "w", "then run the tests"}}, []string{"an older message\n\nthen run the tests"}, [][]string{nil}},
		{"a held message still waiting in the spool, typed by a person word for word",
			[]send{{"r1", "w", "continue"}}, []string{"continue"}, [][]string{nil}},
		{"a waiting held message does not take a typed one's arrival",
			[]send{{"r1", "w", "continue"}, {"r2", "t", "continue"}}, []string{"continue"}, [][]string{{"r2"}}},
		{"a hand-over takes held messages only, never a typed one quoted beside them",
			[]send{{"r1", "t", "deploy"}, {"r2", "h", "then run the tests"}}, []string{"deploy now?\n\nthen run the tests"}, [][]string{{"r2"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			list := slices.Clone(c.pending)
			for n, entry := range c.entries {
				views := make([]sendView, len(list))
				for i, p := range list {
					views[i] = view(p.kind, p.text)
				}
				var got []string
				idx := arrivals(views, entry)
				for _, i := range idx {
					got = append(got, list[i].id)
				}
				if !slices.Equal(got, c.want[n]) {
					t.Fatalf("entry %d %q confirmed %v, want %v", n+1, oneLine(entry, 60), got, c.want[n])
				}
				var keep []send
				for i, p := range list {
					if !slices.Contains(idx, i) {
						keep = append(keep, p)
					}
				}
				list = keep
			}
		})
	}
}

// Claude records a message typed while it works twice: as it queues it
// (an "enqueue" line) and, once it takes it off the queue to run it, as a
// user line. That is one arrival: with a second send of the same words
// still on its way, the user line does not confirm it.
func TestClaudesQueuedMessageIsOneArrivalNotTwo(t *testing.T) {
	t.Parallel()
	ts := `"timestamp":"2026-11-25T10:00:00Z"`
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	s.track("r1", "continue", "queue")
	s.track("r2", "continue", "queue")
	p := claudeAgent{}.newParser(Spec{})
	var delivered []string
	for _, l := range []string{
		`{"type":"queue-operation","operation":"enqueue",` + ts + `,"content":"continue"}`,
		`{"type":"system","subtype":"turn_duration",` + ts + `,"durationMs":1000}`,
		`{"type":"queue-operation","operation":"dequeue",` + ts + `}`,
		`{"type":"user",` + ts + `,"message":{"content":"continue"}}`,
	} {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			delivered = append(delivered, ev.SendID)
		}
	}
	if !slices.Equal(delivered, []string{"r1"}) {
		t.Fatalf("confirmed %v, want r1 alone", delivered)
	}
	if st := s.Snapshot().State; st != "working" {
		t.Fatalf("the dequeued prompt's line left the seat %s, want working", st)
	}
}

// The seat tells a held message the hook took from one still waiting by
// the spool itself: a quote of a waiting one confirms nothing, and the
// hook's hand-over of it, once taken, confirms it.
func TestAHeldMessageArrivesOnlyOnceTheHookHasTakenIt(t *testing.T) {
	t.Parallel()
	spool := filepath.Join(t.TempDir(), "after_turn")
	if err := spoolMessage(spool, "r1", "then run the tests"); err != nil {
		t.Fatal(err)
	}
	s := newSeat(Spec{Spool: spool}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.trackHeld("r1", "then run the tests", "after_turn", true)
	if ids := s.matchPending("an older message\n\nthen run the tests"); len(ids) != 0 {
		t.Fatalf("a quote of a message still in the spool confirmed %v", ids)
	}
	taken, _ := takeSpool(spool)
	for _, p := range taken {
		_ = os.Remove(p)
	}
	if ids := s.matchPending("an older message\n\nthen run the tests"); !slices.Equal(ids, []string{"r1"}) {
		t.Fatalf("the hook's hand-over confirmed %v", ids)
	}
}
