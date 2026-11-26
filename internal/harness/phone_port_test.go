package harness

// The phone mirror's tests, ported from the three-actor design's
// phone_test.go. What the narrator used to say is the loop's send_message,
// what the orchestrator asked is its ask_user; a session with no terminal
// ends once its agent is stopped, so the scripts stop the agent where the
// old orchestrator yielded. Each test names what the old one protected.

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// tinyPNG is a valid 1x1 PNG.
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}

// phoneStopAndClose is the brain of a session that has nothing to ask: it
// stops the agent when its turn ends and closes with one line.
func phoneStopAndClose(closing string) func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(allText(msgs), closing) {
				return hold()
			}
			return say(closing)
		case strings.Contains(lastUserText(msgs), "finished a turn"):
			return stopsWhenDone()
		}
		return hold()
	}
}

// Old TestPhoneAnswersQuestionInBatchSession: a batch session with the
// mirror on asks on the phone and waits instead of ending; the user taps an
// answer there; the work finishes and the final report is posted as
// important; the phone saw the prompt (as the user, naming the thread), the
// one question, the report, and the closing note.
func TestPhoneAnswersQuestionInBatchSession(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Blue it is") {
				return hold()
			}
			return say("Blue it is; done.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}, nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	// With the agent stopped nothing but the question is left: a session
	// that did not wait would end on the tick that saw it, or the next.
	g.agentStopped()
	g.running(10*loopPoll, "while waiting on the phone")
	if !fp.answer("blue", "") {
		t.Fatal("no pending question to answer")
	}
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	evs := g.log()
	st := state.Replay(evs)
	if st.EndReason != "done" {
		t.Fatalf("end reason = %s", st.EndReason)
	}
	if st.Phone == nil || st.Phone.ThreadID != "t1" || !st.Phone.RemoteMode {
		t.Fatalf("phone thread not recorded: %+v", st.Phone)
	}
	var answer event.UserAnswerData
	var final event.MessageData
	for _, ev := range evs {
		switch ev.Type {
		case event.UserAnswer:
			_ = ev.Decode(&answer)
		case event.Message:
			_ = ev.Decode(&final)
		}
	}
	if answer.Text != "blue" || answer.Source != "finalechat" || answer.QuestionID != "q1" {
		t.Fatalf("answer = %+v", answer)
	}
	if !final.Important {
		t.Fatalf("final report should be important: %+v", final)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "user" }); len(got) != 1 || got[0]["body"] != "paint it" || got[0]["title"] == "" {
		t.Fatalf("prompt mirror = %v", got)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["importance"] == "important" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "Blue it is") {
		t.Fatalf("important posts = %v", got)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "system" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "finished") {
		t.Fatalf("end note = %v", got)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.asks) != 1 || fp.asks[0]["prompt"] != "Which colour?" {
		t.Fatalf("asks = %v", fp.asks)
	}
}

// Old TestPhoneReplyAndTerminalAnswerMirror: in an interactive session a
// reply typed on the phone becomes a user message (source finalechat), a
// question goes to the phone too, an answer typed in the terminal withdraws
// the phone's card and is mirrored to the thread as the user's, and the
// report reaches the user once.
func TestPhoneReplyAndTerminalAnswerMirror(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "hello", true, func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case strings.Contains(tail, "The user answered your question"):
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done.","important":true}`)}}
		case strings.Contains(tail, "finished a turn") && strings.Contains(all, "this comes from the phone") && !strings.Contains(all, "Which colour?"):
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
		}
		return hold()
	}, nil)
	waitFor(t, "the thread to exist", func() bool { return fp.postCount() >= 1 })
	fp.userReply("this comes from the phone")
	waitFor(t, "the question", func() bool { return g.ui.askedCount() == 1 })
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	g.ui.input <- "1" // answered in the terminal
	withdrawn := func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.cancelled) >= 1 }
	for deadline := time.Now().Add(15 * time.Second); !withdrawn(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the phone question to be withdrawn\n%s", phoneQuestionTrace(g.rt, fp, g.ui))
		}
	}
	waitFor(t, "the final message", func() bool { return g.ui.messageCount() >= 1 })
	waitFor(t, "the answer mirrored to the phone", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "user" && p["body"] == "red" })) == 1
	})
	waitFor(t, "the report on the phone", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool { return p["importance"] == "important" })) == 1
	})
	fp.mu.Lock()
	cancelled := append([]string{}, fp.cancelled...)
	fp.mu.Unlock()
	if len(cancelled) != 1 {
		t.Fatalf("withdrew %v, want the one question\n%s", cancelled, phoneQuestionTrace(g.rt, fp, g.ui))
	}
	g.ui.input <- "/quit"
	g.exit(15 * time.Second)
	if n := g.ui.messageCount(); n != 1 {
		t.Fatalf("the report was delivered %d times: %q", n, g.ui.said())
	}
	fromPhone := false
	for _, ev := range g.log() {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Source == "finalechat" && d.Text == "this comes from the phone" {
				fromPhone = true
			}
		}
	}
	if !fromPhone {
		t.Fatal("phone reply was not recorded as a user message with source finalechat")
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["importance"] == "important" }); len(got) != 1 {
		t.Fatalf("important posts = %v", got)
	}
}

// Old TestPhoneDisabledInConfig: with enabled: false nothing touches the
// phone, token or not.
func TestPhoneDisabledInConfig(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "hi", false, phoneStopAndClose("Nothing to do."), func(cfg *config.Config) {
		off := false
		cfg.Finalechat.Enabled = &off
	})
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if fp.postCount() != 0 {
		t.Fatalf("phone was contacted while disabled: %v", fp.posts)
	}
	if state.Replay(g.log()).Phone != nil {
		t.Fatal("phone thread recorded while disabled")
	}
}

// events reads the live session's events of one type.
func (g *phoneRig) events(ty string) []event.Event {
	var out []event.Event
	g.rt.sync(func() {
		for _, ev := range g.rt.st.Events {
			if ev.Type == ty {
				out = append(out, ev)
			}
		}
	})
	return out
}

// Old TestPhoneAttachmentsBothWays: a screenshot the user sends from the
// phone with no words is saved under the session and reaches the model with
// its path and as an image (and now the agent, with its path); a file
// eagent attaches to a message goes up to the phone as multipart.
func TestPhoneAttachmentsBothWays(t *testing.T) {
	var mu sync.Mutex
	var sawImagePart, sawAttachmentNote bool
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	fp.files["att-1"] = phoneFakeFile{name: "IMG_1.png", contentType: "image/png", data: tinyPNG}
	g := phoneStart(t, fp, "look at what I send you", false, func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		for _, m := range msgs {
			if parts, ok := m["content"].([]any); ok && m["role"] == "user" {
				for _, p := range parts {
					if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
						mu.Lock()
						sawImagePart = true
						mu.Unlock()
					}
				}
			}
		}
		switch {
		case phoneLastIsTool(msgs), phoneEnding(msgs):
			return hold()
		case strings.Contains(lastUserText(msgs), "finished a turn") && strings.Contains(all, "The user attached a file") && strings.Contains(all, "in-att-1-") && !strings.Contains(all, "delivered with 1 attachment"):
			mu.Lock()
			sawAttachmentNote = true
			mu.Unlock()
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Here is the shot.","attachments":["shot.png"]}`),
				tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
			}}
		}
		return hold()
	}, nil)
	if err := os.WriteFile(filepath.Join(g.project, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	fp.userReplyWith("", []map[string]any{{"id": "att-1", "kind": "image", "content_type": "image/png", "filename": "IMG_1.png", "size": len(tinyPNG), "width": 1, "height": 1, "url": "/api/v1/attachments/att-1"}})
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawAttachmentNote {
		t.Fatal("the model was not told about the attached file")
	}
	if !sawImagePart {
		t.Fatal("the model did not receive the image as an image part")
	}
	var in, out []event.Attachment
	for _, ev := range g.log() {
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if d.Source == "finalechat" {
				in = d.Attachments
			}
		case event.Message:
			var d event.MessageData
			_ = ev.Decode(&d)
			if len(d.Attachments) > 0 {
				out = d.Attachments
			}
		}
	}
	if len(in) != 1 || in[0].ContentType != "image/png" || in[0].Size != int64(len(tinyPNG)) || !strings.HasPrefix(filepath.Base(in[0].Path), "in-att-1-") {
		t.Fatalf("inbound attachment = %+v", in)
	}
	if _, err := os.Stat(in[0].Path); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	toAgent := false
	for _, s := range g.seat.sentTexts() {
		toAgent = toAgent || strings.Contains(s, in[0].Path)
	}
	if !toAgent {
		t.Fatalf("the agent was not given the picture's path: %q", g.seat.sentTexts())
	}
	if len(out) != 1 || out[0].Name != "shot.png" || out[0].Kind != "image" || !strings.HasPrefix(out[0].Path, g.rt.sess.Path) {
		t.Fatalf("outbound attachment = %+v", out)
	}
	ups := fp.uploads()
	if len(ups) != 1 || ups[0].name != "shot.png" || ups[0].contentType != "image/png" || len(ups[0].data) != len(tinyPNG) {
		t.Fatalf("%d uploads", len(ups))
	}
}

// Old TestViewImageShowsPictureAndFindsFilesByName, the part kept_test.go's
// TestViewImageShowsThePictureToTheModel does not hold: a picture named in
// the wrong directory is still found in the project by its name.
func TestViewImageFindsAFileNamedInTheWrongPlace(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		switch {
		case phoneAfterLook(msgs):
			return say("Seen.")
		case strings.Contains(lastUserText(msgs), "kept for you"):
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"/tmp/wrong/place/render.png"}`)}}
		}
		return hold()
	}, nil)
	if err := os.MkdirAll(filepath.Join(s.rt.opts.Project, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.rt.opts.Project, "out", "render.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at the render")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	var res event.ToolResultData
	_ = s.events(event.ToolResult)[0].Decode(&res)
	if res.Name != "view_image" || res.IsError || len(res.Images) != 1 || res.Images[0].Width != 1 || !strings.HasPrefix(res.Images[0].Path, s.rt.sess.Path) {
		t.Fatalf("result = %+v", res)
	}
}

// Old TestWorkerAndNarratorRoutesFallBack, for the one model there is now:
// a dead primary route (bad key) is abandoned for the next one, once, and
// the switch is recorded so the log says which model really answered.
func TestModelRouteFallsBackOnceAndIsRecorded(t *testing.T) {
	t.Setenv("EAGENT_DEAD_KEY", "dead")
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer dead.Close()
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && !phoneLastIsTool(msgs) {
			return say("Here.")
		}
		return hold()
	}, func(cfg *config.Config) {
		fb := cfg.Model
		cfg.Model.BaseURL, cfg.Model.APIKeyEnv = dead.URL, "EAGENT_DEAD_KEY"
		cfg.Model.Fallback = &fb
	})
	s.type_("@e are you there?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	s.type_("@e and now?")
	s.waitFor("the second answer", func() bool { return s.ui.messageCount() == 2 })
	s.settled()
	routes := s.events(event.Route)
	if len(routes) != 1 {
		t.Fatalf("%d route events, want one switch", len(routes))
	}
	var d event.RouteData
	_ = routes[0].Decode(&d)
	if d.Actor != event.ActorEagent || d.BaseURL != s.model.srv.URL {
		t.Fatalf("route = %+v", d)
	}
	s.read(func(st *state.State) {
		if st.Hosts[event.ActorEagent] != s.model.srv.URL {
			t.Errorf("host after fallback = %v", st.Hosts)
		}
	})
}

// Old TestNarratorSteerCadence: the rules of the narrator's cadence, now in
// the tail of every look. An early look when the user has heard nothing
// asks for an orientation; a look past the quiet limit while work goes on
// asks for a progress line now; one soon after the last message only says
// how long ago it was; with the limit off nothing pushes; a look with
// nothing new asks to name what is still running. (The acknowledgement of
// a passed message is TestAPassedMessageIsAcknowledgedInOneLine, the steps
// in flight TestProgressLooksFollowTheWork.)
func TestTailKeepsTheNarratorsCadence(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	tail := func(quiet int, said time.Duration, moved bool) string {
		var out string
		s.rt.sync(func() {
			s.rt.activeSettings.QuietSeconds = quiet
			if said == 0 {
				s.rt.turns.saidAt = time.Time{}
				s.rt.st.Started = time.Now().Add(-2 * time.Minute)
			} else {
				s.rt.turns.saidAt = time.Now().Add(-said)
			}
			out = s.rt.tail(wake{look: true, moved: moved}, time.Now())
		})
		return out
	}
	if got := tail(180, 0, true); !strings.Contains(got, "The user has heard nothing from you in the 2m since this began: tell them now") {
		t.Fatalf("no orientation ask:\n%s", got)
	}
	if got := tail(180, 4*time.Minute, true); !strings.Contains(got, "The user has heard nothing for 4m while the work kept going: send a progress line now") {
		t.Fatalf("no progress ask:\n%s", got)
	}
	if got := tail(180, 40*time.Second, true); !strings.Contains(got, "The user last heard from you 40s ago") || strings.Contains(got, "progress line now") {
		t.Fatalf("a push soon after the last message:\n%s", got)
	}
	if got := tail(0, 10*time.Minute, true); strings.Contains(got, "progress line now") {
		t.Fatalf("the cadence should be off:\n%s", got)
	}
	if got := tail(180, 4*time.Minute, false); !strings.Contains(got, "Nothing new has shown since you last spoke, 4m ago") || !strings.Contains(got, "naming the command or step") {
		t.Fatalf("a quiet stretch is not named:\n%s", got)
	}
}

// Old TestPhoneStatusLineFollowsWorkAndPostsAreIdempotent: while the agent
// runs a command the phone shows a working status naming it, secrets
// blanked, with the busy TTL and a seq; every post carries an idempotency
// key scoped to the session; the thread meta says where the session runs
// and on which model, with the keys earlier builds set cleared; the status
// is cleared at the end.
func TestPhoneStatusLineFollowsWorkAndPostsAreIdempotent(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "", false, phoneStopAndClose("Ran it."), nil)
	g.seat.mu.Lock()
	g.seat.hang = true
	g.seat.mu.Unlock()
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "run it", From: "web"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	g.seat.emit(agentDoes("running curl -H 'Authorization: Bearer fc_AbCdEf0123456789xyz' https://x.test"))
	// The status is recomputed when an event is recorded; the step alone
	// does not do it (see TestGapPhoneStatusFollowsTheAgentsStep).
	g.seat.emit(relay.Event{Kind: relay.KindProgress, Text: "Checking the endpoint."})
	var tool map[string]any
	waitFor(t, "the working status", func() bool {
		for _, a := range fp.statuses() {
			if a["kind"] == "working" && strings.Contains(a["text"].(string), "curl") {
				tool = a
				return true
			}
		}
		return false
	})
	if tool["text"] != "Codex: running curl -H 'Authorization: bearer …' https://x.test" || tool["ttl_seconds"] != float64(90) || tool["seq"] == nil {
		t.Fatalf("tool status = %v", tool)
	}
	g.seat.emit(relay.Event{Kind: relay.KindTurn, Text: "Ran it.", DurationMS: 3000}, relay.Event{Kind: relay.KindState, State: "idle"})
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if fp.clearedCount() < 1 {
		t.Fatal("the status was not cleared at the end")
	}
	prefix := "eagent:" + g.rt.sess.ID + ":"
	fp.mu.Lock()
	posts := append([]map[string]any(nil), fp.posts...)
	fp.mu.Unlock()
	if len(posts) < 3 {
		t.Fatalf("posts = %d", len(posts))
	}
	for _, p := range posts {
		key, _ := p["client_key"].(string)
		if !strings.HasPrefix(key, prefix) {
			t.Fatalf("post without a session-scoped key: %v", p)
		}
	}
	if last, _ := posts[len(posts)-1]["client_key"].(string); !strings.HasSuffix(last, ":end:done:0") {
		t.Fatalf("end note key = %s", last)
	}
	metas := fp.metaPatches()
	if len(metas) == 0 || metas[0]["cwd"] != g.project || metas[0]["host"] == "" || metas[0]["model"] != testModel {
		t.Fatalf("thread meta = %v", metas)
	}
	if _, has := metas[0]["branch"]; has {
		t.Fatalf("a temp dir is not a git checkout; meta = %v", metas[0])
	}
	for _, k := range []string{"models", "hosts", "efforts", "config"} {
		if v, has := metas[0][k]; !has || v != nil {
			t.Fatalf("meta %s = %v, want cleared", k, v)
		}
	}
}

// Old TestGitBranch.
func TestGitBranch(t *testing.T) {
	dir := t.TempDir()
	if gitBranch(context.Background(), dir) != "" {
		t.Fatal("a plain directory has no branch")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for _, args := range [][]string{{"init", "-q", "-b", "feature/x"}, {"-c", "user.email=t@x", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got := gitBranch(context.Background(), dir); got != "feature/x" {
		t.Fatalf("branch = %q", got)
	}
}

// Old TestPhoneIgnoresTokenOriginAndDismissalSendsNothing: a "user" message
// posted with a token (another agent's mirror) is not the user speaking. A
// question dismissed on the phone is closed and nothing else: no answer is
// recorded, the model is not called, and the batch session keeps listening
// and says so on the phone. What the user writes next is an ordinary
// message, and the model reads it knowing nothing was decided.
func TestPhoneIgnoresTokenOriginAndDismissalSendsNothing(t *testing.T) {
	var mu sync.Mutex
	sawNotice := false
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			return hold()
		case strings.Contains(all, "make it green") && !strings.Contains(all, "Painted it green"):
			mu.Lock()
			sawNotice = strings.Contains(all, "dismissed question q1 without answering") && strings.Contains(all, "nothing was decided")
			mu.Unlock()
			return say("Painted it green.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}, nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	fp.tokenUserMessage("red")
	g.phoneWeighed(fp.lastMessageID())
	g.running(10*loopPoll, "on a token-origin message")
	g.rt.sync(func() {}) // the token message has been weighed
	before := g.model.count(testModel)
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	waitFor(t, "the dismissal to be recorded", func() bool { return len(g.events(event.UserDismiss)) == 1 })
	// The turn and the end are decided on the tick that recorded it; ten
	// more ticks for anything that waits on the clock.
	g.running(10*loopPoll, "on the dismissal")
	if got := g.model.count(testModel); got != before {
		t.Fatalf("the dismissal called the model: %d calls before, %d after", before, got)
	}
	if n := fp.pendingQuestions(); n != 0 {
		t.Fatalf("%d question(s) open after the dismissal", n)
	}
	waitFor(t, "the phone to say the session is waiting for a message", func() bool {
		for _, a := range fp.statuses() {
			if a["text"] == "Waiting for your next message" {
				return true
			}
		}
		return false
	})
	fp.userReply("actually, make it green")
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	evs := g.log()
	if st := state.Replay(evs); st.EndReason != "done" {
		t.Fatalf("end reason = %s", st.EndReason)
	}
	var dismissals []event.UserDismissData
	var green []event.UserMessageData
	for _, ev := range evs {
		switch ev.Type {
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if strings.Contains(d.Text, "red") {
				t.Fatalf("a token-origin message became user input: %+v", d)
			}
			if strings.Contains(d.Text, "green") {
				green = append(green, d)
			}
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			t.Fatalf("an answer was recorded for a dismissed question: %+v", d)
		case event.UserDismiss:
			var d event.UserDismissData
			_ = ev.Decode(&d)
			dismissals = append(dismissals, d)
		}
	}
	if len(dismissals) != 1 || dismissals[0].QuestionID != "q1" || dismissals[0].Source != "finalechat" {
		t.Fatalf("dismissals = %+v", dismissals)
	}
	if len(green) != 1 || green[0].Source != "finalechat" {
		t.Fatalf("the message after the dismissal = %+v", green)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawNotice {
		t.Fatal("the model was not told the question was dismissed with nothing decided")
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["body"] == "red" }); len(got) != 0 {
		t.Fatalf("the token message was mirrored back: %v", got)
	}
}

// Old TestPhoneDismissedQuestionIsNotAskedAgain: after a dismissal the
// question cannot be put back: ask_user is refused until the user writes or
// the agent moves. When the time the question had runs out, the batch
// session ends, having asked once and posted one card. (How it ends, which
// the old test also held, is in gaps_phone_test.go.)
func TestPhoneDismissedQuestionIsNotAskedAgain(t *testing.T) {
	// The question's four seconds, at 100ms each: the session listens for
	// 400ms after the question, many times what the dismissal takes to arrive.
	setFor(t, &questionSecond, 100*time.Millisecond)
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, phoneDismissedBrain(), func(cfg *config.Config) { cfg.Finalechat.QuestionTimeoutSeconds = 4 })
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	g.exit(30 * time.Second)
	evs := g.log()
	asked, refused := 0, false
	for _, ev := range evs {
		switch ev.Type {
		case event.Question:
			asked++
		case event.UserAnswer:
			t.Fatal("an answer was recorded for a dismissed question")
		case event.ToolResult:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			if d.Name == "ask_user" && d.IsError && strings.Contains(d.Output, "not asked:") {
				refused = true
			}
		}
	}
	if asked != 1 || !refused {
		t.Fatalf("asked %d time(s), second ask refused = %v", asked, refused)
	}
	fp.mu.Lock()
	cards := len(fp.questions)
	fp.mu.Unlock()
	if cards != 1 {
		t.Fatalf("%d cards were posted to the phone, want 1", cards)
	}
	if st := state.Replay(evs); st.Question != nil {
		t.Fatalf("the dismissed question is still pending: %+v", st.Question)
	}
}

// phoneDismissedBrain asks, and on the closing look tries to ask again, as
// a model that will not take the dismissal for an answer.
func phoneDismissedBrain() func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case strings.Contains(all, "not asked:"):
			return hold()
		case phoneEnding(msgs):
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Red or blue, then?","options":["red","blue"]}`)}}
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}
}

// phoneColourBrain is the old e2e colourBrain: ask which colour, and paint
// it green when the user says so.
func phoneColourBrain(stop bool) func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs), phoneEnding(msgs):
			return hold()
		case strings.Contains(all, "make it green") && !strings.Contains(all, "Painted it green"):
			if strings.Contains(lastUserText(msgs), "typed into") && !strings.Contains(lastUserText(msgs), "finished a turn") {
				return hold() // the agent has it; its turn's end is the moment to report
			}
			return say("Painted it green.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			if stop {
				return phoneAskAndStop("Which colour?", "red", "blue")
			}
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
		}
		return hold()
	}
}

// Old TestPhoneDismissedWhileSessionWasDown: a card dismissed while the
// session was not running. The message that resumes the session is a
// message, not the answer to the question the user closed, and the
// question is not put back on the phone.
func TestPhoneDismissedWhileSessionWasDown(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, phoneColourBrain(true), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 1 })
	g.interrupt() // the process goes away with the question still open
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	g.resume(Options{Prompt: "actually, make it green", PromptSource: "finalechat"})
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("resumed exit %d logs=%v", code, g.ui.logged())
	}
	dismissals, messages, asked := 0, 0, 0
	for _, ev := range g.log() {
		switch ev.Type {
		case event.Question:
			asked++
		case event.UserAnswer:
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			t.Fatalf("the resuming message was taken for the answer to a dismissed question: %+v", d)
		case event.UserDismiss:
			dismissals++
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if strings.Contains(d.Text, "green") {
				messages++
			}
		}
	}
	if dismissals != 1 || messages != 1 || asked != 1 {
		t.Fatalf("dismissals = %d, messages = %d, questions asked = %d", dismissals, messages, asked)
	}
	fp.mu.Lock()
	cards := len(fp.questions)
	fp.mu.Unlock()
	if cards != 1 || fp.pendingQuestions() != 0 {
		t.Fatalf("%d card(s) on the phone, %d pending; want the one dismissed card", cards, fp.pendingQuestions())
	}
}

// Old TestSlashDismissClosesQuestionAndPhoneCard: /dismiss in the terminal
// closes the open question the same way, without calling the model, and the
// card on the phone closes as dismissed (not withdrawn) with it. The next
// line typed is a message, not the answer.
func TestSlashDismissClosesQuestionAndPhoneCard(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", true, phoneColourBrain(false), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the loop to settle", func() bool {
		busy := true
		g.rt.sync(func() { busy = g.rt.turns.busy })
		return !busy
	})
	before := g.model.count(testModel)
	g.ui.input <- "/dismiss"
	waitFor(t, "the card to close as dismissed", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return len(fp.dismissedByToken) == 1
	})
	// /dismiss ran on the loop before the card was closed; ten more ticks
	// for anything it would have set off.
	time.Sleep(10 * loopPoll)
	g.rt.sync(func() {})
	if got := g.model.count(testModel); got != before {
		t.Fatalf("/dismiss called the model: %d calls before, %d after", before, got)
	}
	g.ui.input <- "actually, make it green"
	waitFor(t, "the report", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool { return p["body"] == "Painted it green." })) == 1
	})
	g.ui.input <- "/quit"
	g.exit(20 * time.Second)
	dismissals, messages := 0, 0
	for _, ev := range g.log() {
		switch ev.Type {
		case event.UserAnswer:
			t.Fatal("the line typed after /dismiss was taken for the answer")
		case event.UserDismiss:
			dismissals++
		case event.UserMessage:
			var d event.UserMessageData
			_ = ev.Decode(&d)
			if strings.Contains(d.Text, "green") {
				messages++
			}
		}
	}
	if dismissals != 1 || messages != 1 {
		t.Fatalf("dismissals = %d, messages after = %d", dismissals, messages)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.cancelled) != 0 {
		t.Fatalf("the card was withdrawn instead of dismissed: %v", fp.cancelled)
	}
}

// Old TestPhoneWithoutFeaturesSendsNoStatusOrKeys: an older deployment that
// lists no features gets plain posts: no status calls, no idempotency keys,
// and everything else still works.
func TestPhoneWithoutFeaturesSendsNoStatusOrKeys(t *testing.T) {
	fp := newPhoneFake(false)
	fp.features = []string{}
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "idle", false, phoneStopAndClose("Nothing to do."), nil)
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if n := len(fp.statuses()); n != 0 || fp.clearedCount() != 0 {
		t.Fatalf("status calls against a server without the feature: %d set, %d cleared", n, fp.clearedCount())
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.posts) < 2 {
		t.Fatalf("posts = %d", len(fp.posts))
	}
	for _, p := range fp.posts {
		if _, has := p["client_key"]; has {
			t.Fatalf("client_key sent to a server without idempotency: %v", p)
		}
	}
}

// Old TestRedactSecrets.
func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		"curl -H 'Authorization: Bearer fc_AbCdEf0123456789xyz' https://x.test": "curl -H 'Authorization: bearer …' https://x.test",
		"OPENAI_API_KEY=sk-live-0123456789abcdef go test ./...":                 "OPENAI_API_KEY=… go test ./...",
		"psql postgres://eric:hunter2@db.example/app -c 'select 1'":             "psql postgres://…@db.example/app -c 'select 1'",
		"tool --api-key abc123 --password 'p w' run":                            "tool --api-key … --password … run",
		"echo token: xyz && export DB_PASSWORD=\"s3cret\"":                      "echo token: … && export DB_PASSWORD=…",
		"git push origin main":                          "git push origin main",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345 in a log": "… in a log",
	}
	for in, want := range cases {
		if got := redactSecrets(in); got != want {
			t.Errorf("redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := oneLine("  a\n\n b   c ", 3); got != "a …" {
		t.Errorf("oneLine = %q", got)
	}
}

// Old TestPhoneClosingNoteSurvivesSlowNetwork: on a real network the
// closing note takes two round trips; shutting the mirror down waits for it
// rather than cancelling it mid-flight.
func TestPhoneClosingNoteSurvivesSlowNetwork(t *testing.T) {
	fp := newPhoneFake(false)
	fp.latency = 50 * time.Millisecond // the note's two round trips take 100ms
	t.Cleanup(fp.srv.Close)            // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "idle", false, phoneStopAndClose("Nothing to do."), nil)
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "system" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "finished") {
		t.Fatalf("the closing note was lost: system posts = %v", got)
	}
	if fp.clearedCount() != 1 {
		t.Fatalf("status cleared %d times", fp.clearedCount())
	}
}

// Old TestPhoneStatusOfFreshInteractiveSessionIsWaiting: a fresh
// interactive session that has not been given a prompt is waiting for the
// user, and the phone says so rather than "Working".
func TestPhoneStatusOfFreshInteractiveSessionIsWaiting(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the first status", func() bool { return len(fp.statuses()) > 0 })
	for _, a := range fp.statuses() {
		if a["kind"] != "waiting" || a["text"] != "Waiting for your next message" {
			t.Fatalf("a fresh session reported %v", a)
		}
	}
	var got finalechatActivity
	g.rt.sync(func() { a := g.rt.currentActivity(); got = finalechatActivity{a.Kind, a.TTLSeconds} })
	if got.kind != "waiting" || got.ttl != statusWaitTTL {
		t.Fatalf("currentActivity = %+v", got)
	}
}

type finalechatActivity struct {
	kind string
	ttl  int
}

// Old TestClipLabelIsRuneSafe.
func TestClipLabelIsRuneSafe(t *testing.T) {
	long := strings.Repeat("é", 250)
	got := clipLabel(long, 200)
	if r := []rune(got); len(r) != 200 || r[199] != '…' || !strings.HasPrefix(got, "éé") {
		t.Fatalf("clip = %d runes, ends %q", len(r), string(r[len(r)-1]))
	}
	if clipLabel("  short  ", 200) != "short" {
		t.Fatal("short labels are only trimmed")
	}
}

// Old TestPhoneSessionKindsAndSequencedClear: the opening and closing system
// posts carry the kinds the app reads to show "this session has ended", and
// a message posted with no status in flight clears the line with a
// watermark. (Only eagent's own messages carry a status, as on main: the
// agent's turn reports never did.)
func TestPhoneSessionKindsAndSequencedClear(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "idle", false, phoneStopAndClose("Nothing to do."), nil)
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	var kinds []string
	messages := 0
	for _, p := range fp.posts {
		meta, _ := p["meta"].(map[string]any)
		if k, ok := meta["kind"].(string); ok {
			kinds = append(kinds, k)
		}
		if meta["eagent"] == "message" {
			messages++
			act, ok := p["activity"].(map[string]any)
			if !ok {
				t.Fatalf("a message without an activity object: %v", p)
			}
			if act["seq"] == nil {
				t.Fatalf("an activity without a seq: %v", act)
			}
		}
	}
	if messages == 0 {
		t.Fatal("no message was posted")
	}
	if len(kinds) < 1 || kinds[0] != "session_start" || kinds[len(kinds)-1] != "session_end" {
		t.Fatalf("kinds = %v", kinds)
	}
}

// Old TestPhoneMirrorsTheResumePrompt: the message a session is resumed
// with was recorded before the mirror existed; it still reaches the phone,
// as the user, right after the resume note.
func TestPhoneMirrorsTheResumePrompt(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	brain := phoneStopAndClose("Nothing to do.")
	g := phoneStart(t, fp, "first ask", false, func(m string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && !phoneLastIsTool(msgs) {
			return say("Noted; no agent is running for it.")
		}
		return brain(m, msgs)
	}, nil)
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("first run exit %d", code)
	}
	g.resume(Options{Prompt: "and now this"})
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("resumed run exit %d", code)
	}
	users := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "user" })
	if len(users) != 2 || users[0]["body"] != "first ask" || users[1]["body"] != "and now this" {
		t.Fatalf("user posts on the phone = %v", users)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	resumedAt, promptAt := -1, -1
	for i, p := range fp.posts {
		if b, _ := p["body"].(string); strings.Contains(b, "resumed here") {
			resumedAt = i
		}
		if p["body"] == "and now this" {
			promptAt = i
		}
	}
	if resumedAt < 0 || promptAt != resumedAt+1 {
		t.Fatalf("the resume prompt must follow the resume note: note at %d, prompt at %d", resumedAt, promptAt)
	}
}

// Old TestPhoneUnknownQuestionAnswersAreNotMisbound: an answer to a phone
// card the mirror does not know is bound to the question named in the
// card's own meta, and to none when that is another question: it never
// lands on whatever question happens to be open.
func TestPhoneUnknownQuestionAnswersAreNotMisbound(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "clean up", false, func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Done.") {
				return hold()
			}
			return say("Done.")
		case strings.Contains(tail, "kept for you"):
			return say("That tap was on an old card; I am still waiting on whether to wipe the branch.")
		case strings.Contains(tail, "finished a turn") && !strings.Contains(all, "Wipe the branch?"):
			return phoneAskAndStop("Wipe the branch?", "yes", "no")
		}
		return hold()
	}, nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	g.agentStopped()
	// A stale card from another run, about an unrelated question: the tap
	// must not become "yes" to wiping the branch.
	fp.injectQuestion("old-card", "q-from-another-run")
	fp.answerQuestion("old-card", "blue")
	g.phoneWeighed(fp.lastMessageID())
	g.running(10*loopPoll, "on a stale card's answer")
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatalf("a stale card's answer was bound to the open question: %s", ev.Data)
		}
	}
	// A card the mirror does not know but whose meta names the open question
	// (an ask whose reply was lost) is applied to it.
	fp.injectQuestion("lost-reply-card", "q1")
	fp.answerQuestion("lost-reply-card", "no")
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	var answers []event.UserAnswerData
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answers = append(answers, d)
		}
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].Text != "no" {
		t.Fatalf("answers = %+v", answers)
	}
}

// Old TestPhonePollPacesItselfWhenTheServerDoesNotHold: a server that
// answers the long poll at once with nothing (an unknown anchor) is not
// asked again at once, and the recreated thread is logged once.
func TestPhonePollPacesItselfWhenTheServerDoesNotHold(t *testing.T) {
	// The pause starts at 50ms and doubles: polls at 0, 50, 150 and 350ms
	// make four in the window of three first pauses, as 0, 1 and 3 seconds
	// did in three seconds. With no pacing it would be hundreds.
	setFor(t, &phonePollFloor, 50*time.Millisecond)
	fp := newPhoneFake(false)
	fp.anchorUnknown = true
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the first poll", func() bool { return fp.getCount() >= 1 })
	time.Sleep(3 * phonePollFloor)
	n := fp.getCount()
	g.interrupt()
	if n > 5 {
		t.Fatalf("%d polls in three first pauses against a server that does not hold requests", n)
	}
	said := 0
	for _, l := range g.ui.logged() {
		if strings.Contains(l, "recreated") {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("the recreated-thread note was logged %d times", said)
	}
}

// Old TestResumePromptAnswersThePendingQuestion: a batch session that
// stopped on a question ends awaiting input; resumed with a prompt, the
// prompt is the answer ("2" is the second option); an --answer with no
// question open is kept as a message instead of vanishing.
func TestResumePromptAnswersThePendingQuestion(t *testing.T) {
	g := phoneStart(t, nil, "paint it", false, func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Done.") {
				return hold()
			}
			return say("Done.")
		case strings.Contains(tail, "kept for you"):
			return say("Noted the extra.")
		case strings.Contains(tail, "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}, nil)
	if code := g.exit(20 * time.Second); code != 2 {
		t.Fatalf("a batch session with a question open should stop awaiting input; exit %d", code)
	}
	g.resume(Options{Prompt: "2"})
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("resumed exit %d logs=%v", code, g.ui.logged())
	}
	evs := g.log()
	var answers []event.UserAnswerData
	for _, ev := range evs {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answers = append(answers, d)
		}
	}
	if st := state.Replay(evs); len(answers) != 1 || answers[0].Text != "blue" || st.EndReason != "done" {
		t.Fatalf("answers = %+v end=%s", answers, st.EndReason)
	}
	g.resume(Options{Answer: "and also this"})
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("third run exit %d", code)
	}
	found := false
	for _, ev := range g.log() {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			found = found || d.Text == "and also this"
		}
	}
	if !found {
		t.Fatal("an --answer with no question open was dropped")
	}
}

// Old TestStaleStopDoesNotQuitTheNextResume: a stop left in the inbox by a
// run that has already ended does not quit the next resume.
func TestStaleStopDoesNotQuitTheNextResume(t *testing.T) {
	brain := phoneStopAndClose("Nothing to do.")
	g := phoneStart(t, nil, "first", false, func(m string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && !phoneLastIsTool(msgs) {
			return say("Here is the report: nothing is left to do.")
		}
		return brain(m, msgs)
	}, nil)
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("first run exit %d", code)
	}
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	g.resume(Options{Prompt: "please write the report"})
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("resumed exit %d", code)
	}
	if st := state.Replay(g.log()); st.EndReason != "done" {
		t.Fatalf("a stale stop ended the resume as %q", st.EndReason)
	}
}

// Old TestScheduleDoesNotHideThePrompt: an interactive session with a timer
// set still shows its prompt.
func TestScheduleDoesNotHideThePrompt(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && !phoneLastIsTool(msgs) {
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"every 45s","note":"check the build"}`)}}
		}
		return hold()
	}, nil)
	s.seat.emit(relay.Event{Kind: relay.KindState, State: "idle"}) // the agent is up and between turns
	s.type_("@e check the build every 45 seconds")
	s.waitFor("the timer", func() bool { return len(s.events(event.ScheduleCreate)) == 1 })
	s.settled()
	before := s.ui.idleCount()
	s.rt.sync(func() {}) // a tick
	s.waitFor("the prompt with a timer set", func() bool { return s.ui.idleCount() > before })
}

// Old TestCarriedOverIncludesMessagesAndTaskEnds, for what restarts the
// conversation now: the dossier's facts restate the user's messages word
// for word, with where their files landed. (Task ends went with the task
// workers; the old rule of leaving out messages already read is replaced by
// listing the latest messages always.)
func TestDossierFactsRestateTheUsersMessagesWithTheirFiles(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	var facts string
	s.rt.sync(func() {
		s.rt.userMessage(event.UserMessageData{Text: "please also add tests", Attachments: []event.Attachment{{Name: "spec.md", Path: "/p/spec.md"}}})
		facts = s.rt.dossierFacts(s.rt.st.LastSeq(), "")
	})
	for _, want := range []string{"THE USER'S MESSAGES, word for word", "please also add tests [attached spec.md: /p/spec.md]"} {
		if !strings.Contains(facts, want) {
			t.Fatalf("the facts lack %q:\n%s", want, facts)
		}
	}
}

// Old TestRetitleThreadToolPatchesThread, through update_thread (which
// replaced retitle_thread): a title the user asked for, set with pin,
// PATCHes the phone thread's title, description and the summary alias, and
// is recorded in the log so replay and the web view see it.
func TestUpdateThreadPatchesThePhoneThread(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "", true, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && !phoneLastIsTool(msgs) {
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Paint the fence","summary":"Repainting the fence blue and covering it.","pin":true}`)}}
		}
		return hold()
	}, nil)
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	g.ui.input <- "@e call this thread Paint the fence"
	var got map[string]any
	waitFor(t, "the retitle PATCH", func() bool {
		for _, p := range fp.retitlePatches() {
			if p["title"] == "Paint the fence" {
				got = p
				return true
			}
		}
		return false
	})
	if got["description"] != "Repainting the fence blue and covering it." || got["summary"] != "Repainting the fence blue and covering it." {
		t.Fatalf("patch = %v", got)
	}
	var td event.ThreadTitleData
	for _, ev := range g.events(event.ThreadTitle) {
		_ = ev.Decode(&td)
	}
	if td.Title != "Paint the fence" || td.Description != "Repainting the fence blue and covering it." || !td.Pin {
		t.Fatalf("thread.title = %+v", td)
	}
	var title string
	g.rt.sync(func() { title = g.rt.st.Title })
	if title != "Paint the fence" {
		t.Fatalf("state title = %q", title)
	}
}

// Old TestRetitleThreadToolWithoutPhone, through update_thread: without the
// mirror the title is still recorded in the log (the web view reads it
// there), and an empty title and summary are refused so nothing is wiped.
func TestUpdateThreadWithoutPhone(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	if out, isErr, _ := s.rt.updateThread("Paint the fence", "Repainting the fence.", true); isErr {
		t.Fatalf("update = %q", out)
	}
	var td event.ThreadTitleData
	_ = s.events(event.ThreadTitle)[0].Decode(&td)
	if td.Title != "Paint the fence" || td.Description != "Repainting the fence." {
		t.Fatalf("thread.title = %+v", td)
	}
	s.read(func(st *state.State) {
		if st.Title != "Paint the fence" || st.Description != "Repainting the fence." {
			t.Errorf("state title = %q desc = %q", st.Title, st.Description)
		}
	})
	// An update with neither field sets nothing: it is how the model says
	// both still fit when it is asked about them (the old rename tool
	// refused it; that tool was only ever called to change something).
	if out, isErr, _ := s.rt.updateThread("", "", false); isErr || out != "kept as they are" {
		t.Fatalf("an empty update = %q (error %v)", out, isErr)
	}
	if n := len(s.events(event.ThreadTitle)); n > 2 {
		t.Fatalf("an empty update changed the record: %d thread.title events", n)
	}
}

// Old TestRouteMetaIsTheModelInUse: the thread's model chip is the model
// eagent runs on, and the per-actor keys are cleared.
func TestRouteMetaIsTheModelInUse(t *testing.T) {
	st := state.New()
	st.Models = map[string]string{event.ActorEagent: "reflection/beam-1.0-501b-a23b", "task": "z-ai/glm-5.3"}
	meta := routeMeta(&Runtime{st: st})
	if meta["model"] != "reflection/beam-1.0-501b-a23b" {
		t.Fatalf("model = %v", meta["model"])
	}
	if len(meta) != 5 || meta["models"] != nil {
		t.Fatalf("meta = %v", meta)
	}
}

var _ = bytes.Contains
var _ = image.NewRGBA
var _ = png.Encode
var _ = json.Marshal
