package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// r14AskedAsMessage reports whether the question went to the thread as a
// message (the service refused it as a card).
func r14AskedAsMessage(fp *phoneFake, question string) bool {
	return len(fp.postsWhere(func(p map[string]any) bool {
		body, _ := p["body"].(string)
		return strings.Contains(body, question) && p["sender"] != "user"
	})) == 1
}

// r14Until waits, outside a test goroutine, until cond holds or ten
// seconds pass (the fake's hooks cannot fail the test themselves).
func r14Until(cond func() bool) {
	deadline := time.Now().Add(10 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// Finding 16 of the fourteenth review: a question the service refuses as a
// card goes to the thread as a message, and the session waits for the
// reply. That message carries the waiting line as the card would: a post
// without a line clears it, and the keeper, believing the line shown,
// would leave the thread looking idle for minutes.
func TestR14AQuestionPostedAsAMessageKeepsTheWaitingLine(t *testing.T) {
	quickKeeper(t)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.onAsk = func() {
		// The keeper has shown the waiting line by the time the service
		// refuses the card.
		r14Until(func() bool { return fp.shownStatus() == "Waiting for your answer" })
	}
	g := r8AskSession(t, fp, "Which branch?", r8Choices(21))
	waitFor(t, "the question in the thread", func() bool { return r14AskedAsMessage(fp, "Which branch?") })
	g.statusIdle()
	if got := fp.shownStatus(); got != "Waiting for your answer" {
		t.Fatalf("the thread shows %q while the session waits for the answer", got)
	}
}

// Finding 16, the thread: the message that stands in for the card can
// recreate a thread the user deleted, and the session then knows the
// thread by its new id, as after any other post of its own.
func TestR14AQuestionPostedAsAMessageTakesARecreatedThread(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	svc := newR13Service(t, fp)
	fp.onAsk = func() { svc.deleteThread("t2") }
	choices := r8Choices(21)
	g := phoneStart(t, fp, "pick the branch to ship", false, func(_ string, msgs []map[string]any) reply {
		if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "Which branch?") {
			return phoneAskAndStop("Which branch?", choices...)
		}
		return hold()
	}, func(c *config.Config) { c.Finalechat.BaseURL = svc.srv.URL })
	waitFor(t, "the question in the thread", func() bool { return r14AskedAsMessage(fp, "Which branch?") })
	waitFor(t, "the session to know the thread by its new id", func() bool {
		var p *phone
		g.rt.sync(func() { p = g.rt.phone })
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.threadID == "t2"
	})
}

// r14Drained waits until the phone's outbound worker has run everything
// queued so far: it runs one call at a time, in order.
func r14Drained(t *testing.T, g *phoneRig) {
	t.Helper()
	var p *phone
	g.rt.sync(func() { p = g.rt.phone })
	ran := make(chan struct{})
	if !p.enqueue(func(context.Context) { close(ran) }) {
		t.Fatal("the phone's queue is full")
	}
	select {
	case <-ran:
	case <-time.After(15 * time.Second):
		t.Fatal("the phone's worker did not get to the end of its queue")
	}
}

// r14AskBrain asks "Which colour?" once the agent has finished a turn and
// leaves the agent running, so what the person writes with no question
// open goes to the agent.
func r14AskBrain() func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "Which colour?") {
			args, _ := json.Marshal(map[string]any{"text": "Which colour?", "options": []string{"red", "blue"}})
			return reply{calls: []event.ToolCall{tc("ask_user", string(args))}}
		}
		return hold()
	}
}

// r14Answers are the live session's recorded answers.
func r14Answers(g *phoneRig) []event.UserAnswerData {
	var out []event.UserAnswerData
	for _, ev := range g.events(event.UserAnswer) {
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		out = append(out, d)
	}
	return out
}

// Finding 13 of the fourteenth review: the person answers the open
// question by typing in the phone thread rather than on its card. The
// card is withdrawn, as it is for an answer given at the terminal or on
// the web, so it cannot be tapped and the answer typed to the agent a
// second time.
func TestR14AnAnswerTypedInThePhoneThreadWithdrawsTheCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "paint it", true, r14AskBrain(), nil)
	waitFor(t, "the card", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 1 })
	fp.userReply("blue")
	waitFor(t, "the typed words to answer the question", func() bool { return len(r14Answers(g)) == 1 })
	if a := r14Answers(g)[0]; a.Text != "blue" || a.Source != "finalechat" || a.CardID != "" {
		t.Fatalf("answer = %+v", a)
	}
	r14Drained(t, g)
	if fp.pendingQuestions() == 0 {
		return
	}
	// The card is still up: tapping it is what the person can do now.
	fp.answer("blue", "")
	g.phoneWeighed(fp.lastMessageID())
	waitFor(t, "the agent to be sent the tap", func() bool { return strings.Count(strings.Join(g.seat.sentTexts(), "\n"), "blue") > 0 })
	t.Fatalf("the card stayed open after the typed answer; tapping it sent the agent %q", g.seat.sentTexts())
}

// Finding 15 of the fourteenth review: a phone message carries a file the
// session cannot take, then one whose fetch is still running when the
// session stops. The stop cuts that fetch off and the message is left for
// the next run, so the ending session tells the sender nothing about the
// first file: the next run may well take it.
func TestR14NoFileIsRefusedBeforeEveryFetchHasFinished(t *testing.T) {
	for _, tc := range []struct {
		name string
		file map[string]any
	}{
		{"fetch fails", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}},
		{"too large", map[string]any{"id": "big-9", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/big-9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			held := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/attachments/slow-1" {
					// A fetch still running when the session stops.
					select {
					case held <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					return
				}
				fp.handle(w, r)
			}))
			t.Cleanup(srv.Close)
			g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
			waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
			slow := map[string]any{"id": "slow-1", "kind": "image", "content_type": "image/png", "filename": "shot.png", "size": 3, "url": "/api/v1/attachments/slow-1"}
			id := r10Says(fp, r10Msg("and these", tc.file, slow))[0]
			select {
			case <-held:
			case <-time.After(15 * time.Second):
				t.Fatal("the second file was never fetched")
			}
			g.interrupt()
			if refused := r11Refusals(fp); len(refused) > 0 {
				t.Fatalf("the ending session told the phone a file was refused: %v", refused)
			}
			r10Untaken(t, g, id, "and these")
		})
	}
}

// r14LostAnchor is the fake Finalechat with reads that never wait (each
// read sees the thread as it is then), answering a read after a message
// the thread no longer has as the service does: the anchor is unknown, and
// the page lists the user's messages from the thread's first.
func r14LostAnchor(t *testing.T, fp *phoneFake) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodGet {
			q := r.URL.Query()
			after := q.Get("after")
			fp.mu.Lock()
			known := after == ""
			page := []map[string]any{}
			for _, m := range fp.messages {
				known = known || m["id"] == after
				if m["sender"] == "user" {
					page = append(page, phoneCopy(m))
				}
			}
			fp.mu.Unlock()
			if !known {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"messages": page, "has_more": false, "timed_out": false, "anchor_unknown": true})
				return
			}
			q.Set("wait", "0")
			r.URL.RawQuery = q.Encode()
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// r14TakeBack is the person taking a message back: the thread no longer
// has it.
func r14TakeBack(fp *phoneFake, id string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for i, m := range fp.messages {
		if m["id"] == id {
			fp.messages = append(fp.messages[:i:i], fp.messages[i+1:]...)
			return
		}
	}
}

// r14Taken is how many times the session's record took app message id.
func r14Taken(g *phoneRig, id string) int {
	n := 0
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == id {
			n++
		}
	}
	return n
}

// r14Session is an interactive session with an agent, whose phone reads
// go through srv.
func r14Session(t *testing.T, fp *phoneFake, srv *httptest.Server) *phoneRig {
	t.Helper()
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	return g
}

// Finding 12 of the fourteenth review: the person sends two messages,
// takes the second back and writes a third. The service no longer knows
// the message the poll reads on from and lists the thread from its first
// message; the first message, taken already, is not taken a second time.
func TestR14AMessageTakenBeforeALostAnchorIsNotTakenAgain(t *testing.T) {
	setFor(t, &phonePollFloor, 5*time.Millisecond)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r14Session(t, fp, r14LostAnchor(t, fp))
	ids := r10Says(fp, r10Msg("run the migration"), r10Msg("actually wait"))
	g.phoneWeighed(ids[1])
	r14TakeBack(fp, ids[1])
	later := r10Says(fp, r10Msg("check status"))[0]
	waitFor(t, "the later message to be taken", func() bool { return r14Taken(g, later) == 1 })
	g.rt.sync(func() {})
	if n := r14Taken(g, ids[0]); n != 1 {
		t.Fatalf("the first message was taken %d times; the agent was sent %q", n, g.seat.sentTexts())
	}
}

// Finding 12, across runs: what an earlier run took is not taken again by
// a later one when the service loses the anchor of the later one's read.
func TestR14AMessageAnEarlierRunTookIsNotTakenAgainAfterALostAnchor(t *testing.T) {
	setFor(t, &phonePollFloor, 5*time.Millisecond)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r14Session(t, fp, r14LostAnchor(t, fp))
	before := r10Says(fp, r10Msg("run the migration"), r10Msg("then the seed"))
	waitFor(t, "the first run to take both", func() bool { return r14Taken(g, before[1]) == 1 })
	g.interrupt()
	reads := fp.getCount()
	g.resume(Options{Interactive: true})
	waitFor(t, "the resumed run's poll", func() bool { return fp.getCount() > reads })
	second := r10Says(fp, r10Msg("actually wait"))[0]
	g.phoneWeighed(second)
	r14TakeBack(fp, second)
	later := r10Says(fp, r10Msg("check status"))[0]
	waitFor(t, "the later message to be taken", func() bool { return r14Taken(g, later) == 1 })
	g.rt.sync(func() {})
	for _, id := range before {
		if n := r14Taken(g, id); n != 1 {
			t.Errorf("message %s of the first run was taken %d times", id, n)
		}
	}
}
