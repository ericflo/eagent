package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// r6PhoneIDs is the phone message ids on the record's user messages, in
// order.
func r6PhoneIDs(path string) []string {
	evs, _ := store.Read(path)
	var out []string
	for _, ev := range evs {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID != "" {
			out = append(out, d.PhoneID)
		}
	}
	return out
}

// Three messages sent while the session was stopped: it is resumed with
// the first, as the sweep now picks it, and its poll takes the other two
// after it, in order, each once, each with its route and delivery. The
// record's ledger then holds all three.
func TestThreeMessagesSentWhileStoppedAreEachTakenOnceInOrder(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	first := fp.userSays("the checklist is A, B, C", map[string]any{"eagent.route": "agent", "eagent.delivery": "after_turn"})
	second := fp.userSays("start with A", nil)
	third := fp.userSays("are you still there?", map[string]any{"eagent.route": "eagent"})
	rt, seat := resumeFromPhone(t, cfg, project, path, first, "the checklist is A, B, C", map[string]any{"eagent.route": "agent", "eagent.delivery": "after_turn"})
	waitFor(t, "all three in the record", func() bool { return len(r6PhoneIDs(path)) >= 3 })
	rt.sync(func() {})
	if got := r6PhoneIDs(path); len(got) != 3 || got[0] != first || got[1] != second || got[2] != third {
		t.Fatalf("recorded %v, want %v", got, []string{first, second, third})
	}
	waitFor(t, "the two for the agent", func() bool { return len(seat.sentTexts()) >= 2 })
	if got := seat.sentTexts(); len(got) != 2 || got[0] != "the checklist is A, B, C" || got[1] != "start with A" {
		t.Fatalf("typed into the agent: %q", got)
	}
	if got := seat.sentModes(); got[0] != "after_turn" {
		t.Fatalf("the first went %q", got[0])
	}
	evs, _ := store.Read(path)
	if l := integration.ReadPhoneLedger(evs); !l.Keeps || l.Cursor != third {
		t.Fatalf("ledger keeps=%v cursor=%q, want %q", l.Keeps, l.Cursor, third)
	}
}

// A session resumed from the terminal while messages from the app wait:
// its poll reads on after the last message the session took in, so they
// are taken, in order, instead of being left behind its own first post.
func TestASessionResumedAtTheTerminalTakesTheMessagesThatWaited(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	first := fp.userSays("the checklist is A, B, C", nil)
	second := fp.userSays("are you still there?", nil)
	seat := newFakeSeat("Done.")
	useFakeRelays(t, seat)
	rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "both in the record", func() bool { return len(r6PhoneIDs(path)) >= 2 })
	rt.sync(func() {})
	if got := r6PhoneIDs(path); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("recorded %v", got)
	}
}

// With a question open and two messages waiting, the first is judged as
// the words that resume the session (here the answer: the card is open
// and known), and the second arrives while it runs (a message, the
// question being answered).
func TestTwoMessagesWaitingOnAnOpenQuestionAreTheAnswerThenAMessage(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	fp.userReply("red")
	firstID := fp.lastMessageID()
	fp.userReply("and make it bold")
	g.resume(Options{Prompt: "red", PromptSource: "finalechat", PromptPhoneID: firstID})
	waitFor(t, "the answer and the message", func() bool {
		a, _ := r5takenTimes(g, "red")
		_, m := r5takenTimes(g, "and make it bold")
		return a == 1 && m == 1
	})
	g.rt.sync(func() {})
	if a, m := r5takenTimes(g, "red"); a != 1 || m != 0 {
		r5takenDump(g)
		t.Fatalf("\"red\": %d answers, %d messages", a, m)
	}
	if a, m := r5takenTimes(g, "and make it bold"); a != 0 || m != 1 {
		r5takenDump(g)
		t.Fatalf("\"and make it bold\": %d answers, %d messages", a, m)
	}
}
