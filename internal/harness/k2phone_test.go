package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/relay"
)

// k2LoseCard makes the record lose the open question's card, as a stop
// between the ask's reply and its record does: its phone.question line
// names another card.
func k2LoseCard(t *testing.T, g *phoneRig, card string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.rt.sess.Path, "*.jsonl"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, l := range lines {
			if strings.Contains(l, `"phone.question"`) {
				lines[i] = strings.ReplaceAll(l, card, "lost-card")
			}
		}
		if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// k2HeldStart is the fake service holding the resumed session's first
// post until release is closed: the phone is still attaching, and the
// open question is not asked again until it has.
func k2HeldStart(t *testing.T, fp *phoneFake) (url string, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte("eagent session resumed here.")) {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return srv.URL, release
}

// Fifteenth review, finding 4: the resumed session's open question is
// answered or dismissed before the phone has attached and asked it
// again. The earlier run's card is withdrawn (or closed as dismissed)
// all the same: left open, a tap on it would reach the session as a
// second answer.
func TestK2AQuestionSettledBeforeItIsAskedAgainLeavesNoCardOpen(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		settled           func(g *phoneRig) bool
	}{
		{"answered at the terminal", "blue", "cancelled", func(g *phoneRig) bool { return len(g.events(event.UserAnswer)) == 1 }},
		{"dismissed at the terminal", "/dismiss", "dismissed", func(g *phoneRig) bool { return len(g.events(event.UserDismiss)) == 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g := r5AskedAndStopped(t, fp)
			card := r5CardOf(t, g)
			u, release := k2HeldStart(t, fp)
			g.cfg.Finalechat.BaseURL = u
			g.ui = &fakeUI{input: make(chan string, 16)}
			g.resume(Options{Interactive: true})
			g.ui.input <- tc.input
			waitFor(t, "the question to be settled", func() bool { return tc.settled(g) })
			release()
			waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 2 })
			r14Drained(t, g)
			if s := r14Status(fp, card); s != tc.want {
				t.Fatalf("the earlier run's card is %q, want %q", s, tc.want)
			}
		})
	}
}

// Fifteenth review, finding 3: the record lost the open question's card,
// the person answers on it while the session is stopped, and the reply
// watcher resumes the session with the answer; the service cannot be
// asked about the card during the resume. The words are taken once (here
// as a message, the card's question being unknown), and the re-ask that
// then finds the card answered does not take them a second time.
func TestK2AnAnswerOnALostCardIsTakenOnceWhenTheCardCannotBeChecked(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	k2LoseCard(t, g, card)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	fp.mu.Lock()
	fp.questionsDown = true
	fp.mu.Unlock()
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: fp.lastMessageID(), PromptAnswers: card})
	waitFor(t, "the re-ask to record the card", func() bool {
		for _, ev := range g.log() {
			var d event.PhoneQuestionData
			if ev.Type == event.PhoneQuestion && ev.Decode(&d) == nil && d.CardID == card {
				return true
			}
		}
		return false
	})
	r14Drained(t, g)
	g.rt.sync(func() {})
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		r5takenDump(g)
		t.Fatalf("one tap on the card is recorded %d times as the answer and %d times as a message", a, m)
	}
	// No card of the question is open on the phone now: a session with no
	// terminal does not wait there for ever.
	g.exit(20 * time.Second)
}

// Finding 4, in a running session: on a service without client keys the
// resume asks the question on a new card, and the service does not let
// the earlier run's card be withdrawn then. The question is answered at
// the terminal afterwards; that card is withdrawn with the new one.
func TestK2AnAnswerWithdrawsAnEarlierCardWhoseWithdrawalFailed(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.features = []string{"activity", "dismiss", "attachments"}
	g := r5AskedAndStopped(t, fp)
	first := r5CardOf(t, g)
	var refuse sync.Mutex
	refusing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refuse.Lock()
		no := refusing && r.URL.Path == "/api/v1/questions/"+first+"/cancel"
		refuse.Unlock()
		if no {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"not now"}}`))
			return
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	g.cfg.Finalechat.BaseURL = srv.URL
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Interactive: true})
	waitFor(t, "the second card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	if s := r14Status(fp, first); s != "pending" {
		t.Fatalf("the earlier card is %q; the test wants its withdrawal refused", s)
	}
	refuse.Lock()
	refusing = false
	refuse.Unlock()
	g.ui.input <- "blue"
	waitFor(t, "the answer", func() bool { return len(r14Answers(g)) == 1 })
	r14Drained(t, g)
	if s := r14Status(fp, first); s == "pending" {
		t.Fatalf("the earlier run's card is still open after the answer at the terminal")
	}
}

// k2CutAfter rewrites a stopped session's record to end at the first event
// cut picks, as a stop right after writing it leaves it.
func k2CutAfter(t *testing.T, g *phoneRig, cut func(event.Event) bool) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.rt.sess.Path, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("the record is in %d files", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev event.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatal(err)
		}
		out = append(append(out, line...), '\n')
		if cut(ev) {
			if err := os.WriteFile(files[0], out, 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no event to cut after")
}

// k2Receipt is the receipt the thread meta last showed under message id,
// in the patches after the first from.
func k2Receipt(fp *phoneFake, id string, from int) string {
	got := ""
	for _, m := range fp.metaPatches()[from:] {
		if rs, ok := m["relay_receipts"].(map[string]any); ok {
			if s, ok := rs[id].(string); ok {
				got = s
			}
		}
	}
	return got
}

// B7: a message from the phone was recorded and the session stopped
// before its send was; at the resume the agent is gone, so the message is
// kept for eagent. The phone's receipt under it says so, as it does for
// a message kept for eagent while the session runs.
func TestK2AMessageCutBeforeItsSendIsShownKeptForEagent(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	waitFor(t, "the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	id := r10Says(fp, r10Msg("now run the tests"))[0]
	waitFor(t, "its send", func() bool { return len(g.seat.sentTexts()) == 2 })
	g.interrupt()
	k2CutAfter(t, g, func(ev event.Event) bool {
		var d event.UserMessageData
		return ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == id
	})
	old := attachRelay
	attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
		return nil, errors.New("no such terminal")
	}
	t.Cleanup(func() { attachRelay = old })
	// What the first run showed went with the record's tail.
	before := len(fp.metaPatches())
	g.resume(Options{Interactive: true})
	waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 2 })
	r14Drained(t, g)
	if got := k2Receipt(fp, id, before); got != "eagent" {
		t.Fatalf("the receipt under the message is %q, want it kept for eagent", got)
	}
}

// B10: a file from the phone cannot be fetched, and the service refuses
// the note that would tell the sender. The words are taken once, as
// before; the session's log says the file was dropped and that the
// sender could not be told.
func TestK2AFileWhoseRefusalCannotBePostedIsSaidInTheLog(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"no"}}`))
				return
			}
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	gone := map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}
	id := r10Says(fp, r10Msg("and this one", gone))[0]
	waitFor(t, "the words to be taken", func() bool { return r14Taken(g, id) == 1 })
	r14Drained(t, g)
	logs := strings.Join(g.ui.logged(), "\n")
	if !strings.Contains(logs, "IMG_9.png") || !strings.Contains(logs, "could not be told") {
		t.Fatalf("the session's log does not say the sender was not told: %q", g.ui.logged())
	}
}

// B12, in the running mirror: a file from the phone whose first download
// fails as a service briefly down does (502) is tried again, as the reply
// watcher tries one, and reaches the session; nothing is refused.
func TestK2AFileWhoseDownloadFailsOnceIsTakenByTheMirror(t *testing.T) {
	setFor(t, &phoneRetryStep, 5*time.Millisecond)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.files["att-1"] = phoneFakeFile{name: "IMG_1.png", contentType: "image/png", data: tinyPNG}
	var mu sync.Mutex
	failed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		first := r.URL.Path == "/api/v1/attachments/att-1" && !failed
		if first {
			failed = true
		}
		mu.Unlock()
		if first {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_gateway","message":"a moment"}}`))
			return
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	file := map[string]any{"id": "att-1", "kind": "image", "content_type": "image/png", "filename": "IMG_1.png", "size": len(tinyPNG), "url": "/api/v1/attachments/att-1"}
	id := r10Says(fp, r10Msg("here is the shot", file))[0]
	waitFor(t, "the words to be taken", func() bool { return r14Taken(g, id) == 1 })
	r14Drained(t, g)
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == id && len(d.Attachments) != 1 {
			t.Fatalf("the message was taken with %d files, want its one", len(d.Attachments))
		}
	}
	if got := r11Refusals(fp); len(got) != 0 {
		t.Fatalf("the file was refused: %v", got)
	}
}

// B16's siblings: an agent's report and a snapshot of its terminal take
// the thread the service names, as every other post does.
func TestK2RelayPostsTakeTheThreadTheyName(t *testing.T) {
	held := func(string, []map[string]any) reply { return hold() }
	t.Run("the agent's report", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("relay_report")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		k1ThreadAfter(t, g, match)
	})
	t.Run("the agent's screen", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("relay_screen")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the agent's report", func() bool { return len(fp.postsWhere(k1Kind("relay_report"))) > 0 })
		r14Drained(t, g)
		g.ui.input <- "/screen"
		k1ThreadAfter(t, g, match)
	})
}

// k2BehindAQuit is r10BehindAQuit without its check that the /quit is the
// last message taken: the person sends /quit and then msgs in one page,
// read while the loop is held, and the session ends.
func k2BehindAQuit(t *testing.T, fp *phoneFake, msgs ...map[string]any) (*phoneRig, []string) {
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
	<-blocked
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
	return g, ids
}

// C9: a command from the phone reaches a session that is ending (a /quit
// before it in the page). It is not run, and not left for the next run
// either, which the reply watcher would start only to run it: it is taken,
// and the thread says it was not run. Words after a message left for the
// next run are left too, so the cursor never passes them.
func TestK2ACommandToAnEndingSessionIsTakenAndSaidNotRun(t *testing.T) {
	t.Run("a command", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		g, ids := k2BehindAQuit(t, fp, r10Msg("/kill"))
		l := integration.ReadPhoneLedger(g.log())
		if !l.Taken[ids[1]] || l.Cursor != ids[1] {
			t.Fatalf("the command was left for the next run: taken %v, cursor %q", l.Taken, l.Cursor)
		}
		if got := fp.postsWhere(k1Kind("command_not_run")); len(got) != 1 {
			t.Fatalf("the thread was told %d times that the command was not run", len(got))
		}
	})
	t.Run("a command after words left for the next run", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		g, ids := r10BehindAQuit(t, fp, r10Msg("one more thing"), r10Msg("/kill"))
		r10Untaken(t, g, ids[1], "one more thing")
		if l := integration.ReadPhoneLedger(g.log()); l.Taken[ids[2]] {
			t.Fatalf("the command after words left for the next run was taken: cursor %q", l.Cursor)
		}
	})
}

// k2Unconfirmed is what the thread meta last said of message id under
// relay_unconfirmed, in the patches after the first from.
func k2Unconfirmed(fp *phoneFake, id string, from int) (said, unconfirmed bool) {
	for _, m := range fp.metaPatches()[from:] {
		if u, ok := m["relay_unconfirmed"].(map[string]any); ok {
			said, unconfirmed = true, u[id] == true
		}
	}
	return said, unconfirmed
}

// B17: a message from the phone is typed into the agent and its arrival
// is never confirmed. Its receipt stays "sent" (FinaleChat's Retry must
// not type it again), and the thread meta now says which "sent" messages
// may not have arrived, so the app can show it; one still on its way is
// not among them.
func TestK2AMessageTypedAndNeverConfirmedIsSaidSoOnThePhone(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	waitFor(t, "the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	r14Drained(t, g)
	g.seat.mu.Lock()
	g.seat.sendErr = &relay.TypingError{Err: errors.New("enter failed")}
	g.seat.mu.Unlock()
	from := len(fp.metaPatches())
	id := r10Says(fp, r10Msg("now run the tests"))[0]
	waitFor(t, "its outcome", func() bool {
		for _, ev := range g.events(event.RelayDelivered) {
			var d event.RelayDeliveredData
			if ev.Decode(&d) == nil && d.Outcome == event.DeliveryUnconfirmed {
				return true
			}
		}
		return false
	})
	r14Drained(t, g)
	if got := k2Receipt(fp, id, from); got != "sent" {
		t.Fatalf("the receipt is %q, want sent", got)
	}
	if said, u := k2Unconfirmed(fp, id, from); !said || !u {
		t.Fatalf("the meta does not say the message may not have arrived (said %v)", said)
	}
}
