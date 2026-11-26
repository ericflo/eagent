package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/integration"
)

// r10Msg is a message the person writes in the app: words, files.
func r10Msg(text string, atts ...map[string]any) map[string]any {
	m := map[string]any{"thread_id": "t1", "sender": "user", "origin": "session", "body": text, "format": "text", "importance": "normal", "meta": map[string]any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if len(atts) > 0 {
		m["attachments"] = atts
	}
	return m
}

// r10Says is the person sending messages from the phone, all at once: the
// poll reads them in one page. It returns their ids.
func r10Says(fp *phoneFake, msgs ...map[string]any) []string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	var ids []string
	for _, m := range msgs {
		m["id"] = fp.nextID()
		fp.messages = append(fp.messages, m)
		ids = append(ids, m["id"].(string))
	}
	fp.cond.Broadcast()
	return ids
}

// r10Untaken checks that the ending session took nothing of the message
// id: nothing in the record names it, the ledger leaves it for the next run
// to take, no receipt was set under it, and the thread's last word is the
// session's closing note.
func r10Untaken(t *testing.T, g *phoneRig, id, text string) {
	t.Helper()
	evs := g.log()
	for _, ev := range evs {
		var d struct {
			PhoneID   string `json:"phone_id"`
			MessageID string `json:"message_id"`
		}
		if ev.Decode(&d) == nil && (d.PhoneID == id || d.MessageID == id) {
			t.Errorf("the ending session recorded the message: %s %s", ev.Type, ev.Data)
		}
	}
	l := integration.ReadPhoneLedger(evs)
	if !l.Keeps || l.Taken[id] || !l.Takeable(finalechat.Message{ID: id, Sender: "user", Origin: "session", Body: text}) {
		t.Errorf("the ledger says the message was taken: keeps %v, taken %v, cursor %q", l.Keeps, l.Taken, l.Cursor)
	}
	for _, m := range g.fp.metaPatches() {
		if rr, ok := m["relay_receipts"].(map[string]any); ok && rr[id] != nil {
			t.Errorf("a receipt under the message: %v", rr)
		}
	}
	g.fp.mu.Lock()
	last := g.fp.messages[len(g.fp.messages)-1]
	g.fp.mu.Unlock()
	if meta, _ := last["meta"].(map[string]any); last["sender"] != "system" || meta["kind"] != "session_end" {
		t.Fatalf("the thread's last message is %v", last)
	}
}

// Finding 13: a message written from the phone while the session is ending
// (here its closing word is still being written, which the shutdown waits
// for) is not taken by the ending session: it is left for the next run.
func TestR10APhoneMessageWhileTheSessionEndsIsLeftForTheNextRun(t *testing.T) {
	setFor(t, &finalReportGrace, 300*time.Millisecond)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	writing := make(chan struct{})
	var once sync.Once
	never := make(chan struct{})
	g := phoneStart(t, fp, "merge the PRs", false, func(_ string, msgs []map[string]any) reply {
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			once.Do(func() { close(writing) })
			r := say("Merged both.")
			r.block = never
			return r
		case strings.Contains(lastUserText(msgs), "finished a turn"):
			return stopsWhenDone()
		}
		return hold()
	}, nil)
	t.Cleanup(func() { close(never) }) // before the model server is closed
	select {
	case <-writing:
	case <-time.After(15 * time.Second):
		t.Fatal("no closing word")
	}
	g.cancel() // as Ctrl-C: the session ends, and waits for the closing word
	waitFor(t, "the session to be ending", func() bool {
		ending := false
		g.rt.sync(func() { ending = g.rt.ending })
		return ending
	})
	id := r10Says(fp, r10Msg("one more thing: bump the version too"))[0]
	g.exit(10 * time.Second)
	r10Untaken(t, g, id, "one more thing: bump the version too")
}

// r10BehindAQuit starts an interactive session with an agent and the
// phone, and has the person send /quit and then the messages texts in one
// page; the loop is held while the poll reads the page, so the poll has
// handed all of it over before the /quit is acted on. It returns once the
// session has ended, with the messages' ids, the /quit's first.
func r10BehindAQuit(t *testing.T, fp *phoneFake, msgs ...map[string]any) (*phoneRig, []string) {
	t.Helper()
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the agent", func() bool {
		n := 0
		g.rt.sync(func() { n = len(g.rt.st.ActiveRelays()) })
		return n == 1
	})
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	blocked, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	go g.rt.sync(func() { close(blocked); <-release })
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not run")
	}
	ids := r10Says(fp, append([]map[string]any{r10Msg("/quit")}, msgs...)...)
	waitFor(t, "the poll to read the page", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.after == ids[len(ids)-1]
	})
	released.Do(func() { close(release) })
	if code := g.exit(10 * time.Second); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[0]] || l.Cursor != ids[0] {
		t.Fatalf("the /quit was not the last message taken: taken %v, cursor %q", l.Taken, l.Cursor)
	}
	return g, ids
}

// Finding 13: a message read in the same page as a /quit before it is not
// taken by the session the /quit ends: no false receipt, nothing typed into
// the agent, left for the next run. The /quit itself is taken.
func TestR10AMessageBehindAQuitIsNotTakenByTheEndingSession(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g, ids := r10BehindAQuit(t, fp, r10Msg("one more thing"))
	for _, s := range g.seat.sentTexts() {
		if strings.Contains(s, "one more thing") {
			t.Fatalf("typed into the agent: %q", g.seat.sentTexts())
		}
	}
	r10Untaken(t, g, ids[1], "one more thing")
}

// Finding 13: nothing at all is recorded once the session is ending, not
// even the read of a message that leaves nothing to take in (its only
// file refused): recorded, it would move the cursor past the words before
// it, which the next run would then never read.
func TestR10NothingIsReadPastAMessageLeftForTheNextRun(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	gone := map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}
	g, ids := r10BehindAQuit(t, fp, r10Msg("one more thing"), r10Msg("", gone))
	r10Untaken(t, g, ids[1], "one more thing")
}
