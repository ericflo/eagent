package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// r5Working starts a session whose agent is mid-turn on a step, and waits
// until the phone shows that step and the status keeper has nothing armed.
func r5Working(t *testing.T, fp *phoneFake) (*phoneRig, string) {
	t.Helper()
	// The keeper's first look, a second in at most, comes at once.
	setFor(t, &statusFirstLook, 50*time.Millisecond)
	g := phoneStart(t, fp, "", false, phoneStopAndClose("Ran it."), nil)
	g.seat.mu.Lock()
	g.seat.hang = true
	g.seat.mu.Unlock()
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "run it", From: "web"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	g.seat.emit(agentDoes("running go test ./..."))
	const line = "Codex: running go test ./..."
	waitFor(t, "the status to name the step", func() bool { return fp.shownStatus() == line })
	g.statusIdle()
	var name string
	g.rt.sync(func() { name = g.rt.st.ActiveRelays()[0].Name })
	return g, name
}

// r5LineHolds fails unless the phone still shows line once the post
// kind has gone up, for a window of many status gaps.
func r5LineHolds(t *testing.T, fp *phoneFake, kind, line string) {
	t.Helper()
	waitFor(t, "the "+kind+" post", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool { m, _ := p["meta"].(map[string]any); return m["kind"] == kind })) == 1
	})
	deadline := time.Now().Add(10 * statusMinGap)
	for time.Now().Before(deadline) {
		if got := fp.shownStatus(); got != line {
			t.Fatalf("after the %s post the phone shows %q, want %q", kind, got, line)
		}
		time.Sleep(statusMinGap / 4)
	}
}

// What eagent types to the agent is posted as an agent message, which the
// service takes as the end of the status line unless it carries one. The
// line must stay while the agent works on.
func TestWhatEagentTypesToTheAgentKeepsThePhonesStatusLine(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	g, name := r5Working(t, fp)
	g.rt.sync(func() {
		ev := event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: name, ID: "s1", From: "eagent", Text: "Go on."})
		ev.Seq = 9999
		g.rt.phone.observe(g.rt, ev)
	})
	r5LineHolds(t, fp, "relay_send", "Codex: running go test ./...")
}

// The agent's screen posted to the thread keeps the status line too.
func TestTheAgentsScreenOnThePhoneKeepsItsStatusLine(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	g, name := r5Working(t, fp)
	g.rt.phone.postRelayScreen(name, "$ go test ./...\nok")
	r5LineHolds(t, fp, "relay_screen", "Codex: running go test ./...")
}

// A status write the service turns down (it holds a newer one) is not
// taken for shown: the keeper writes it again soon, not at its next
// refresh forty seconds on.
func TestAStatusTheServiceTurnedDownIsWrittenAgain(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	fp.mu.Lock()
	fp.rejectActivity = true
	fp.mu.Unlock()
	g := phoneStart(t, fp, "", false, phoneStopAndClose("Ran it."), nil)
	g.seat.mu.Lock()
	g.seat.hang = true
	g.seat.mu.Unlock()
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "run it", From: "web"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	g.seat.emit(agentDoes("running go test ./..."))
	const line = "Codex: running go test ./..."
	tries := func() int {
		n := 0
		for _, a := range fp.statuses() {
			if a["text"] == line {
				n++
			}
		}
		return n
	}
	waitFor(t, "a first write of the step", func() bool { return tries() >= 1 })
	fp.mu.Lock()
	fp.rejectActivity = false
	fp.mu.Unlock()
	waitFor(t, "the step on the phone", func() bool { return fp.shownStatus() == line })
}

// A picture eagent may look at because the user or an agent named it may
// also be sent to the user: the two tools hold a picture to one rule. A
// named file that is not a picture, and a picture nobody named, still stay.
func TestSendMessageAttachesAPictureThatViewImageTakes(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	run := func(name, args string) (string, bool) {
		out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc(name, args))
		return out, isErr
	}
	dir := t.TempDir()
	shot, notes, never := filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt"), filepath.Join(dir, "never.png")
	for path, data := range map[string][]byte{shot: tinyPNG, notes: []byte("secret notes"), never: tinyPNG} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.rt.sync(func() {
		s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "look at " + shot + " and " + notes, Source: "terminal"}))
	})
	s.settled()
	view, _ := json.Marshal(map[string]string{"path": shot})
	if out, isErr := run("view_image", string(view)); isErr {
		t.Fatalf("view_image: %s", out)
	}
	send := func(path string) (string, bool) {
		args, _ := json.Marshal(map[string]any{"text": "Here.", "attachments": []string{path}})
		return run("send_message", string(args))
	}
	if out, isErr := send(shot); isErr {
		t.Fatalf("send_message refused the picture view_image took: %s", out)
	}
	if out, isErr := send(notes); !isErr {
		t.Fatalf("a named file that is not a picture was sent: %s", out)
	}
	if out, isErr := send(never); !isErr {
		t.Fatalf("a picture nobody named was sent: %s", out)
	}
	var sent []event.Attachment
	for _, ev := range s.events(event.Message) {
		var d event.MessageData
		if ev.Decode(&d) == nil {
			sent = append(sent, d.Attachments...)
		}
	}
	if len(sent) != 1 || sent[0].Name != "shot.png" {
		t.Fatalf("sent %+v", sent)
	}
	if entries, _ := os.ReadDir(attachmentsDir(s.rt.sess.Path)); len(entries) != 2 {
		t.Fatalf("the session keeps %d copies, want the look and the send", len(entries))
	}
}

// A deleted thread takes its cards and their keys with it, so the card a
// resume then makes in the new thread has the plain key again. On the
// resume after that, the question still has that one open card: a second
// must not go up beside it.
func TestAQuestionAskedAgainAfterTheThreadWasDeletedKeepsOneCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	// The user deletes the thread on the phone: its questions and their
	// keys go with it.
	fp.mu.Lock()
	clear(fp.questions)
	clear(fp.qkeys)
	fp.mu.Unlock()
	g.resume(Options{})
	waitFor(t, "a card in the new thread", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the new card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 2 })
	g.interrupt()
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) >= 3 })
	waitFor(t, "the card to be watched", func() bool {
		var p *phone
		g.rt.sync(func() { p = g.rt.phone })
		if p == nil {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.open) == 1
	})
	if n := fp.pendingQuestions(); n != 1 {
		t.Fatalf("%d open cards for one question after the second resume, want 1", n)
	}
}

// The message that resumes a session is taken as the answer to its open
// question only when the question's card is known not to be dismissed. If
// the service cannot say, the person may have closed the card while the
// session was stopped: their words are a message, and the question stays.
func TestAResumeMessageIsNotTakenAsTheAnswerWhenTheCardCannotBeChecked(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	fp.mu.Lock()
	fp.questionsDown = true
	fp.mu.Unlock()
	g.resume(Options{Prompt: "use the blue one", PromptSource: "finalechat"})
	waitFor(t, "the words to be recorded", func() bool {
		for _, ev := range g.log() {
			switch ev.Type {
			case event.UserAnswer:
				t.Fatalf("taken as the answer: %s", ev.Data)
			case event.UserMessage:
				var d event.UserMessageData
				if ev.Decode(&d) == nil && d.Text == "use the blue one" {
					return true
				}
			}
		}
		return false
	})
	var open bool
	g.rt.sync(func() { open = g.rt.st.Question != nil })
	if !open {
		t.Fatal("the question was closed")
	}
}

// A card the person dismissed while the session was stopped is found
// whichever of the question's recorded cards it is.
func TestADismissalWhileAwayIsFoundOnAnOlderCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	// A later card the record knows of, gone with a deleted thread.
	qid := r5OpenQuestion(t, g)
	sess, _, err := store.Open(g.rt.sess.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.PhoneQuestion, event.ActorHarness, event.PhoneQuestionData{QuestionID: qid, CardID: "gone-card"})); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	g.resume(Options{Prompt: "use the blue one", PromptSource: "finalechat"})
	waitFor(t, "the dismissal to be recorded", func() bool { return len(g.events(event.UserDismiss)) == 1 })
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatalf("taken as the answer: %s", ev.Data)
		}
	}
}

func r5OpenQuestion(t *testing.T, g *phoneRig) string {
	t.Helper()
	st := state.Replay(g.log())
	if st.Question == nil {
		t.Fatal("no open question")
	}
	return st.Question.ID
}
