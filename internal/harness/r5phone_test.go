package harness

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r5AskBrain asks one question, stops the agent, and says "Done." once the
// question is answered and the session is closing.
func r5AskBrain() func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Done.") {
				return hold()
			}
			return say("Done.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}
}

// r5AskedAndStopped starts a batch session that asks its question on the
// phone, waits for the card to be recorded, and stops the session with the
// question still open.
func r5AskedAndStopped(t *testing.T, fp *phoneFake) *phoneRig {
	t.Helper()
	g := phoneStart(t, fp, "paint it", false, r5AskBrain(), nil)
	waitFor(t, "the first phone question", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) > 0 })
	g.interrupt()
	return g
}

// The service answers a question's client key with the question it made
// for that key, whatever its status. A question whose card timed out while
// the session was stopped must come back as a new card on resume, not as
// the dead one, which would end the session at once.
func TestResumedQuestionWhoseCardExpiredIsAskedOnANewCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	if fp.expire() != 1 {
		t.Fatal("no card to expire")
	}
	g.resume(Options{})
	waitFor(t, "a new card for the question", func() bool { return fp.pendingQuestions() == 1 })
	if n := fp.cards(); n != 2 {
		t.Fatalf("cards = %d, want the expired one and a new one", n)
	}
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if st := state.Replay(g.log()); st.EndReason != "done" || st.Question != nil {
		t.Fatalf("end=%s question=%v", st.EndReason, st.Question)
	}
}

// A card still open on the service when the session resumes is the one
// the user answers: the re-ask must not put up a second card.
func TestResumedQuestionWhoseCardIsStillOpenKeepsThatOneCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) == 2 })
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d, want the one still open", n)
	}
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d after the answer", n)
	}
}

// A card the user dismissed while the session was stopped stays
// dismissed: no new card, nothing decided and nothing sent to the agent.
func TestResumedQuestionWhoseCardWasDismissedIsNotAskedAgain(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	sent := len(g.seat.sentTexts())
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	g.resume(Options{})
	code := g.exit(30 * time.Second)
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d, want only the dismissed one", n)
	}
	if got := len(g.seat.sentTexts()); got != sent {
		t.Fatalf("the agent was sent %d more messages", got-sent)
	}
	st := state.Replay(g.log())
	if code != 2 || st.EndReason != "awaiting-input" {
		t.Fatalf("exit %d, end reason %q; want 2 and awaiting-input", code, st.EndReason)
	}
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatalf("a dismissal recorded an answer: %s", ev.Data)
		}
	}
}

// r5FileServer serves each path's bytes, with no length up front, as a
// server streaming a file does.
func r5FileServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		for len(data) > 0 {
			n := min(len(data), 1<<20)
			_, _ = w.Write(data[:n])
			w.(http.Flusher).Flush()
			data = data[n:]
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func r5Runtime(t *testing.T) *Runtime {
	t.Helper()
	sess, err := store.Create(store.Root(t.TempDir()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return &Runtime{sess: sess, ctx: context.Background()}
}

// The service's ids are UUIDv7: their first eight characters are a clock
// that moves once a minute or so. Two files of one name sent within that
// minute are two files on disk, each with its own bytes.
func TestTwoPhoneFilesOfOneNameSentTogetherAreBothKept(t *testing.T) {
	srv := r5FileServer(t, map[string][]byte{"/f/1": []byte("first"), "/f/2": []byte("second")})
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	r := r5Runtime(t)
	a, err := r.downloadAttachment(client, finalechat.Attachment{ID: "019abc12-0000-7000-8000-000000000001", Filename: "image.png", URL: "/f/1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.downloadAttachment(client, finalechat.Attachment{ID: "019abc12-0000-7000-8000-000000000002", Filename: "image.png", URL: "/f/2"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path {
		t.Fatalf("both saved at %s", a.Path)
	}
	for path, want := range map[string]string{a.Path: "first", b.Path: "second"} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != want {
			t.Fatalf("%s holds %q (%v), want %q", path, raw, err, want)
		}
	}
}

// A file over the limit is refused, never saved cut short.
func TestPhoneFileOverTheLimitIsRefusedNotSavedCutShort(t *testing.T) {
	big := bytes.Repeat([]byte("x"), finalechat.MaxAttachmentSize+5)
	srv := r5FileServer(t, map[string][]byte{"/f/big": big})
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	r := r5Runtime(t)
	// Size 0: the message did not say, so only the download can tell.
	_, err := r.downloadAttachment(client, finalechat.Attachment{ID: "019abc12-0000-7000-8000-00000000000b", Filename: "big.bin", URL: "/f/big"})
	if !errors.Is(err, finalechat.ErrTooLarge) {
		t.Fatalf("err = %v, want too large", err)
	}
	if entries, _ := os.ReadDir(attachmentsDir(r.sess.Path)); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}

// The person who sent a file too large to take is told so on the phone,
// and the words they sent with it still reach the session.
func TestPhoneIsToldWhenAFileItSentIsTooLarge(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	size := finalechat.MaxAttachmentSize + 5
	fp.files["big-1"] = phoneFakeFile{name: "clip.mov", contentType: "video/quicktime", data: bytes.Repeat([]byte("x"), size)}
	g := phoneStart(t, fp, "", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	fp.userReplyWith("here is the clip", []map[string]any{{"id": "big-1", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": size, "url": "/api/v1/attachments/big-1"}})
	waitFor(t, "the note about the file", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool {
			b, _ := p["body"].(string)
			return strings.Contains(b, "clip.mov") && strings.Contains(b, "10 MB")
		})) == 1
	})
	waitFor(t, "the words to reach the session", func() bool {
		for _, ev := range g.events(event.UserMessage) {
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.Text == "here is the clip" {
				return len(d.Attachments) == 0
			}
		}
		return false
	})
	if entries, _ := os.ReadDir(attachmentsDir(g.rt.sess.Path)); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}
