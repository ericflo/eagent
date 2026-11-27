package harness

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// k1Recreating is the fake service with one twist: just before it takes
// the first post that match picks, the user deletes the thread in the app,
// so that post creates it again under the id t2.
func k1Recreating(t *testing.T, fp *phoneFake, match func(body map[string]any) bool) *httptest.Server {
	svc := newR13Service(t, fp)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if json.Unmarshal(raw, &body) == nil && match(body) {
				once.Do(func() { svc.deleteThread("t2") })
			}
		}
		svc.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// k1ThreadAfter waits for the post match picks, then for the phone's
// worker to finish with it, and fails unless the session now knows the
// thread by the id that post named.
func k1ThreadAfter(t *testing.T, g *phoneRig, match func(body map[string]any) bool) {
	t.Helper()
	waitFor(t, "the post", func() bool { return len(g.fp.postsWhere(match)) > 0 })
	r14Drained(t, g)
	var p *phone
	g.rt.sync(func() { p = g.rt.phone })
	p.mu.Lock()
	id := p.threadID
	p.mu.Unlock()
	if id != "t2" {
		t.Fatalf("the post recreated the thread as t2; the session still knows it as %q", id)
	}
}

func k1Kind(kind string) func(map[string]any) bool {
	return func(b map[string]any) bool {
		meta, _ := b["meta"].(map[string]any)
		return meta["kind"] == kind
	}
}

func k1UserSaid(text string) func(map[string]any) bool {
	return func(b map[string]any) bool { return b["sender"] == "user" && b["body"] == text }
}

// B16: every post of eagent's that the service answers with a thread
// takes that thread's id, so a thread the user deleted and the post
// recreated is known by its new id (as a narrator message or a question
// already is).
func TestK1EveryPostTakesTheThreadItNames(t *testing.T) {
	held := func(string, []map[string]any) reply { return hold() }
	t.Run("words typed at the terminal", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1UserSaid("hello from the terminal")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
		g.ui.input <- "hello from the terminal"
		k1ThreadAfter(t, g, match)
	})
	t.Run("an answer given at the terminal", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("answer")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "paint it", true, r14AskBrain(), func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 1 })
		g.ui.input <- "blue"
		k1ThreadAfter(t, g, match)
	})
	t.Run("what eagent typed to the agent", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("relay_send")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "paint it", true, func(_ string, msgs []map[string]any) reply {
			if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "paint the second coat") {
				return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"paint the second coat"}`)}}
			}
			return hold()
		}, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		k1ThreadAfter(t, g, match)
	})
	t.Run("a file refused", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("attachment_refused")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
		big := map[string]any{"id": "big-9", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/big-9"}
		r10Says(fp, r10Msg("and this", big))
		k1ThreadAfter(t, g, match)
	})
	t.Run("the words a session was resumed with", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1UserSaid("blue, and carry on")
		srv := k1Recreating(t, fp, match)
		g := r5AskedAndStopped(t, fp) // its agent stopped
		g.cfg.Finalechat.BaseURL = srv.URL
		g.resume(Options{Interactive: true, Prompt: "blue, and carry on"})
		k1ThreadAfter(t, g, match)
	})
}

// B13, on a service that keeps client keys: the resumed session asks its
// open question again, and the service answers with the earlier card
// itself. While that ask is in flight the person closes the card and
// writes; their words are not the answer to the question they dismissed.
func TestK1DismissedCardThenWordsWhileTheKeyedAskIsInFlight(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	var first event.PhoneQuestionData
	_ = r13Logged(g, event.PhoneQuestion)[0].Decode(&first)
	var once sync.Once
	var words string
	fp.mu.Lock()
	fp.onAsk = func() {
		once.Do(func() {
			fp.mu.Lock()
			q := fp.questions[first.CardID]
			q["status"], q["answered_at"] = "dismissed", time.Now().UTC().Format(time.RFC3339Nano)
			fp.cond.Broadcast()
			fp.mu.Unlock()
			words = r10Says(fp, r10Msg("never mind, check the tests first"))[0]
			r14Until(func() bool {
				for _, ev := range r13Logged(g, event.UserAnswer) {
					var d event.UserAnswerData
					if ev.Decode(&d) == nil && d.PhoneID == words {
						return true
					}
				}
				return r14Taken(g, words) > 0
			})
		})
	}
	fp.mu.Unlock()
	g.resume(Options{Interactive: true})
	waitFor(t, "the card to be recorded again", func() bool { return len(r13Logged(g, event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	g.rt.sync(func() {})
	for _, ev := range r13Logged(g, event.UserAnswer) {
		var d event.UserAnswerData
		if ev.Decode(&d) == nil && d.PhoneID == words {
			t.Fatalf("the person dismissed the card and then wrote %q; it was taken as the answer (dismissals %d)", d.Text, len(r13Logged(g, event.UserDismiss)))
		}
	}
}
