package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// r6Account points the project's own configuration at a fake service, so
// the reply watcher's entry point (integration.PendingReply, which the
// hosted restore and the sweep share) reads the thread the session wrote.
func r6Account(t *testing.T, project, url string) {
	t.Helper()
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"finalechat": map[string]any{"enabled": true, "base_url": url, "token_env": "EAGENT_TEST_FC", "artifacts": false}})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// r6Pending is what the reply watcher would resume a record with: the
// record cut to evs, in a session folder of its own.
func r6Pending(t *testing.T, project, sid string, evs []event.Event) (string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, ev := range evs {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(append(buf, raw...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "0001.jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
	msg, ok, err := integration.PendingReply(context.Background(), project, store.Info{ID: sid, Path: dir, Modified: time.Now()})
	if !ok {
		return "", err
	}
	return msg.ID, err
}

// r6Names reports the app message ids and cards an event says were taken.
func r6Names(ev event.Event) (ids, cards []string) {
	var d struct {
		PhoneID   string `json:"phone_id"`
		MessageID string `json:"message_id"`
		CardID    string `json:"card_id"`
	}
	switch ev.Type {
	case event.UserMessage, event.UserAnswer, event.PhoneRead, event.PhoneAnswerTaken:
		if ev.Decode(&d) == nil {
			for _, id := range []string{d.PhoneID, d.MessageID} {
				if id != "" {
					ids = append(ids, id)
				}
			}
			if d.CardID != "" {
				cards = append(cards, d.CardID)
			}
		}
	}
	return ids, cards
}

// A session that takes in every kind of app message (a plain message, an
// answer, a card's answer with no question open, a command, an empty
// message), stops, waits for two more, and is resumed from the phone with
// the first while its poll takes the second. Its real record is then cut
// after every event from the first on, as a hard stop would leave it, and
// each cut is handed to the reply watcher. Each time it must name the
// first message the cut record had not taken in: none taken twice, none
// skipped.
func TestEveryCutOfARealRecordGoesOnFromTheRightMessage(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "paint it", true, r5AskBrain(), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	recorded := func(id string) func() bool {
		return func() bool {
			for _, ev := range g.log() {
				ids, _ := r6Names(ev)
				for _, x := range ids {
					if x == id {
						return true
					}
				}
			}
			return false
		}
	}
	say := func(text string) string {
		fp.userReply(text)
		id := fp.lastMessageID()
		waitFor(t, "the poll to take "+text, recorded(id))
		return id
	}
	answer := say("red") // the answer to the open question
	g.rt.sync(func() {})
	fp.injectQuestion("old-card", "q-old")
	fp.answerQuestion("old-card", "blue") // a card's answer, with no question of it open
	cardAnswer := fp.lastMessageID()
	waitFor(t, "the old card's answer", recorded(cardAnswer))
	command := say("/status")
	fp.userReplyWith("", nil)
	empty := fp.lastMessageID()
	waitFor(t, "the empty message", recorded(empty))
	plain := say("thanks")
	g.interrupt()
	fp.userReply("the checklist is A, B, C")
	w1 := fp.lastMessageID()
	fp.userReply("start with A")
	w2 := fp.lastMessageID()
	g.resume(Options{Prompt: "the checklist is A, B, C", PromptSource: "finalechat", PromptPhoneID: w1, Interactive: true})
	waitFor(t, "the second waiting message", recorded(w2))
	g.interrupt()

	thread := []string{answer, cardAnswer, command, empty, plain, w1, w2}
	takeable := map[string]bool{answer: true, cardAnswer: true, command: true, plain: true, w1: true, w2: true}
	cardOf := map[string]string{cardAnswer: "old-card"}
	evs := g.log()
	takenAt := map[string]int{}
	for i, ev := range evs {
		ids, cards := r6Names(ev)
		for _, id := range ids {
			if _, ok := takenAt[id]; !ok {
				takenAt[id] = i
			}
		}
		for _, c := range cards {
			for m, mc := range cardOf {
				if mc == c {
					if _, ok := takenAt[m]; !ok {
						takenAt[m] = i
					}
				}
			}
		}
	}
	for _, id := range thread {
		if _, ok := takenAt[id]; !ok && takeable[id] {
			t.Fatalf("the full record never took %s", id)
		}
	}
	r6Account(t, g.project, fp.srv.URL)
	sid := filepath.Base(g.rt.sess.Path)
	var failures []string
	for cut := 1; cut <= len(evs); cut++ {
		want := ""
		for _, id := range thread {
			if at, ok := takenAt[id]; takeable[id] && (!ok || at >= cut) {
				want = id
				break
			}
		}
		got, err := r6Pending(t, g.project, sid, evs[:cut])
		if err != nil || got != want {
			failures = append(failures, fmt.Sprintf("cut after %s (event %d of %d): resumed with %q, want %q (err %v)", evs[cut-1].Type, cut, len(evs), got, want, err))
		}
	}
	for _, f := range failures {
		t.Error(f)
	}
}

// A run with the phone switched off after a run that kept the ledger: it
// took nothing from the app, so the ledger and its cursor are as the run
// before left them, and a message sent before it is still found, by the
// reply watcher and by a later terminal resume.
func TestARunWithThePhoneOffLeavesTheLedgerAsItWas(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprintf("terminal=%v", terminal), func(t *testing.T) {
			cfg, project, path, fp := stoppedRelaySession(t)
			waiting := fp.userSays("are you still there?", nil)
			before, _ := store.Read(path)
			lb := integration.ReadPhoneLedger(before)
			t.Setenv("EAGENT_FINALECHAT", "off")
			useFakeRelays(t, newFakeSeat("Done."))
			rt2, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
			if err != nil {
				t.Fatal(err)
			}
			r6RunAndStop(t, rt2)
			after, _ := store.Read(path)
			if la := integration.ReadPhoneLedger(after); !la.Keeps || la.Cursor != lb.Cursor {
				t.Fatalf("after the run: keeps=%v cursor=%q, before cursor=%q", la.Keeps, la.Cursor, lb.Cursor)
			}
			t.Setenv("EAGENT_FINALECHAT", "on")
			r6Account(t, project, fp.srv.URL)
			if !terminal {
				msg, ok, err := integration.PendingReply(context.Background(), project, r6sInfo(t, project, path))
				if err != nil || !ok || msg.ID != waiting {
					t.Fatalf("pending reply %q ok=%v err=%v, want %q", msg.ID, ok, err, waiting)
				}
				return
			}
			useFakeRelays(t, newFakeSeat("Done."))
			rt3, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
			if err != nil {
				t.Fatal(err)
			}
			relayRunLive(t, rt3)
			waitFor(t, "the message that waited", func() bool {
				for _, id := range r6PhoneIDs(path) {
					if id == waiting {
						return true
					}
				}
				return false
			})
		})
	}
}

// A run during which the service cannot be reached at all: the same.
func TestARunWithTheServiceUnreachableLeavesTheLedgerAsItWas(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	// The closing note waits behind a service that cannot be reached.
	setFor(t, &phoneCloseWait, 50*time.Millisecond)
	waiting := fp.userSays("are you still there?", nil)
	before, _ := store.Read(path)
	lb := integration.ReadPhoneLedger(before)
	down := cfg
	down.Finalechat.BaseURL = "http://127.0.0.1:1"
	useFakeRelays(t, newFakeSeat("Done."))
	rt2, err := Resume(down, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	r6RunAndStop(t, rt2)
	after, _ := store.Read(path)
	if la := integration.ReadPhoneLedger(after); !la.Keeps || la.Cursor != lb.Cursor {
		t.Fatalf("after the run: keeps=%v cursor=%q, before cursor=%q", la.Keeps, la.Cursor, lb.Cursor)
	}
	r6Account(t, project, fp.srv.URL)
	msg, ok, err := integration.PendingReply(context.Background(), project, r6sInfo(t, project, path))
	if err != nil || !ok || msg.ID != waiting {
		t.Fatalf("pending reply %q ok=%v err=%v, want %q", msg.ID, ok, err, waiting)
	}
}

// r6RunAndStop runs a resumed session until its loop has turned once,
// then stops it.
func r6RunAndStop(t *testing.T, rt *Runtime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	rt.sync(func() {})
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not stop")
	}
}

func r6sInfo(t *testing.T, project, path string) store.Info {
	t.Helper()
	info, err := store.Resolve(store.Root(project), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// r6CardOn is the card_id recorded on the first event of type ty whose
// text is text.
func r6CardOn(g *phoneRig, ty, text string) (string, bool) {
	for _, ev := range g.log() {
		var d struct {
			Text   string `json:"text"`
			CardID string `json:"card_id"`
		}
		if ev.Type == ty && ev.Decode(&d) == nil && strings.TrimSpace(d.Text) == text {
			return d.CardID, true
		}
	}
	return "", false
}

// A card's answer names its card on the answer it became, from every place
// that writes one: the poll (as the answer to the open question, and as a
// message when the card's question is not open) and the resume (rule 2:
// the card answered while the session was stopped).
func TestACardsAnswerNamesItsCardWhereverItIsRecorded(t *testing.T) {
	t.Run("poll, the question open", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		g := phoneStart(t, fp, "paint it", true, r5AskBrain(), nil)
		waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
		waitFor(t, "the card recorded", func() bool { return len(g.events(event.PhoneQuestion)) > 0 })
		card := r5CardOf(t, g)
		fp.answer("blue", "")
		waitFor(t, "the answer", func() bool { _, ok := r6CardOn(g, event.UserAnswer, "blue"); return ok })
		if got, _ := r6CardOn(g, event.UserAnswer, "blue"); got != card {
			t.Fatalf("card_id %q, want %q", got, card)
		}
	})
	t.Run("poll, no question of it open", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		g := phoneStart(t, fp, "", true, func(string, []map[string]any) reply { return hold() }, nil)
		waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
		fp.injectQuestion("old-card", "q-old")
		fp.answerQuestion("old-card", "blue")
		waitFor(t, "the message", func() bool { _, ok := r6CardOn(g, event.UserMessage, "blue"); return ok })
		if got, _ := r6CardOn(g, event.UserMessage, "blue"); got != "old-card" {
			t.Fatalf("card_id %q, want old-card", got)
		}
	})
	t.Run("resume, answered while stopped", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		g := r5AskedAndStopped(t, fp)
		card := r5CardOf(t, g)
		fp.answer("blue", "")
		g.resume(Options{Prompt: "are you there?", PromptSource: "finalechat"})
		waitFor(t, "the card's answer", func() bool { _, ok := r6CardOn(g, event.UserAnswer, "blue"); return ok })
		if got, _ := r6CardOn(g, event.UserAnswer, "blue"); got != card {
			t.Fatalf("card_id %q, want %q", got, card)
		}
	})
}
