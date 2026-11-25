package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// partMarker is the line a part of a long report ends with.
var partMarker = regexp.MustCompile(`\n\n_\(part (\d+) of (\d+)\)_$`)

// unsplit takes the parts of a report back to the report: the marker each
// ends with goes, and so do the fence lines added where a code block was
// cut (a closing line at the end of a part, the same opening line at the
// start of the next). It reads a part's fences before taking its opening
// line away. The code blocks it is given are cut at line ends, where the
// closing fence is added after the part's own last line end.
func unsplit(t *testing.T, parts []string) string {
	t.Helper()
	var b strings.Builder
	for i, p := range parts {
		if len(parts) > 1 {
			loc := partMarker.FindStringSubmatchIndex(p)
			if loc == nil {
				t.Fatalf("part %d does not end with its marker: ...%q", i+1, p[max(0, len(p)-40):])
			}
			p = p[:loc[0]]
		}
		if i < len(parts)-1 {
			if fence := openFenceAtEnd(p); fence != "" {
				if !strings.HasSuffix(p, "\n"+fence) {
					t.Fatalf("part %d leaves a %q block open", i+1, fence)
				}
				p = strings.TrimSuffix(p, fence)
			}
		}
		if i > 0 {
			if open := reopened(parts[i-1]); open != "" {
				if !strings.HasPrefix(p, open+"\n") {
					t.Fatalf("part %d does not reopen the code block with %q: %q", i+1, open, p[:min(len(p), 40)])
				}
				p = strings.TrimPrefix(p, open+"\n")
			}
		}
		b.WriteString(p)
	}
	return b.String()
}

// openFenceAtEnd is the fence of the block still open when the text ends,
// counting the closing line the splitter adds as a close: it reads the
// text without its last line.
func openFenceAtEnd(p string) string {
	i := strings.LastIndexByte(p, '\n')
	if i < 0 {
		return ""
	}
	fence, _ := scanFences(p[:i+1], "", "")
	return fence
}

// reopened is the opening line the next part must begin with, after part
// p (marker and all) when it cut a code block, else "".
func reopened(p string) string {
	if loc := partMarker.FindStringIndex(p); loc != nil {
		p = p[:loc[0]]
	}
	i := strings.LastIndexByte(p, '\n')
	if i < 0 {
		return ""
	}
	_, open := scanFences(p[:i+1], "", "")
	return open
}

// scanFences follows code blocks through text, as CommonMark reads them,
// and returns the fence of the block left open and its opening line.
func scanFences(text, fence, open string) (string, string) {
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimLeft(line, " ")
		if len(line)-len(s) > 3 || s == "" {
			continue
		}
		c := s[0]
		if c != '`' && c != '~' {
			continue
		}
		n := 0
		for n < len(s) && s[n] == c {
			n++
		}
		if n < 3 {
			continue
		}
		if fence == "" {
			if c == '`' && strings.ContainsRune(s[n:], '`') {
				continue
			}
			fence, open = s[:n], strings.TrimSpace(line)
		} else if c == fence[0] && n >= len(fence) && strings.TrimSpace(s[n:]) == "" {
			fence, open = "", ""
		}
	}
	return fence, open
}

// Each part starts with the report's own words and ends with its marker,
// and the parts go back to the report once the markers and the fence lines
// added at the cuts are taken away.
func TestReportPartsStartWithTheReportAndEndWithTheirMarker(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	report := "First line of the report.\n" + strings.Repeat(line, 3000) + "END"
	parts := splitReport(report, phoneReportPart)
	if len(parts) < 2 {
		t.Fatalf("%d parts", len(parts))
	}
	if !strings.HasPrefix(parts[0], "First line of the report.") {
		t.Errorf("part 1 starts %q", parts[0][:30])
	}
	for i, p := range parts {
		if strings.HasPrefix(p, "_(") {
			t.Errorf("part %d starts with its marker", i+1)
		}
		m := partMarker.FindStringSubmatch(p)
		if m == nil || m[1] != itoa(i+1) || m[2] != itoa(len(parts)) {
			t.Errorf("part %d ends %q", i+1, p[len(p)-30:])
		}
		if len(p) > phoneReportPart {
			t.Errorf("part %d is %d bytes", i+1, len(p))
		}
	}
	if got := unsplit(t, parts); got != report {
		t.Fatalf("the parts rejoin to %d bytes, want %d", len(got), len(report))
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// A code block cut between parts is closed with its own fence and opened
// again with the same fence and language, for backtick fences of any
// length and for tilde fences; a fence inside a longer one does not close
// it.
func TestSplitReportReopensCodeBlocksWithTheirFenceAndLanguage(t *testing.T) {
	body := strings.Repeat("+ a line of code\n", 40)
	cases := []struct{ name, open, close string }{
		{"go", "```go", "```"},
		{"four backticks", "````diff", "````"},
		{"tildes", "~~~ python", "~~~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner := ""
			if c.close == "````" {
				inner = "```\nnested, not a close\n```\n"
			}
			report := "Here is the change:\n" + c.open + "\n" + inner + body + c.close + "\nDone."
			parts := splitReport(report, 400)
			if len(parts) < 2 {
				t.Fatalf("%d parts", len(parts))
			}
			for i, p := range parts {
				if len(p) > 400 {
					t.Errorf("part %d is %d bytes", i+1, len(p))
				}
				body := partMarker.ReplaceAllString(p, "")
				if fence, _ := scanFences(body, "", ""); fence != "" {
					t.Errorf("part %d leaves a %q block open:\n%s", i+1, fence, p)
				}
				if i > 0 && reopened(parts[i-1]) != "" && !strings.HasPrefix(p, c.open+"\n") {
					t.Errorf("part %d reopens with %q, want %q", i+1, strings.SplitN(p, "\n", 2)[0], c.open)
				}
			}
			if got := unsplit(t, parts); got != report {
				t.Fatalf("the parts rejoin to\n%q\nwant\n%q", got, report)
			}
		})
	}
	long := strings.Repeat("é", 1000) // no line breaks, two bytes a rune
	parts := splitReport(long, 301)
	for i, p := range parts {
		body := partMarker.ReplaceAllString(p, "")
		if len(p) > 301 || !utf8.ValidString(body) || !strings.HasSuffix(body, "é") {
			t.Errorf("part %d: %d bytes, ends %q", i+1, len(p), body[len(body)-2:])
		}
	}
	if got := unsplit(t, parts); got != long {
		t.Fatalf("the runes rejoin to %d bytes, want %d", len(got), len(long))
	}
}

// The figures about the turn ride on the last part only, every part keeps
// what identifies it as this agent's report, and every part carries the
// status line as a single report post does.
func TestReportPartsCarryTheTurnFiguresOnTheLastPartOnly(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	report := strings.Repeat(line, 3000) + "END"
	_, fp := relayReportSession(t, report, 20000)
	waitFor(t, "the report's parts", func() bool { return len(relayReports(fp)) == 3 })
	for i, p := range relayReports(fp) {
		meta, _ := p["meta"].(map[string]any)
		for _, k := range []string{"eagent", "kind", "relay", "agent", "seq", "source_anchor"} {
			if meta[k] == nil {
				t.Errorf("part %d has no %s", i+1, k)
			}
		}
		last := i == 2
		for _, k := range []string{"duration_ms", "tools", "interrupted", "images"} {
			if _, has := meta[k]; has != last {
				t.Errorf("part %d: %s present %v", i+1, k, has)
			}
		}
		if p["activity"] == nil {
			t.Errorf("part %d carries no status line, so it clears the thread's", i+1)
		}
	}
}

// keyPhone is a phone whose server has not been asked what it supports
// yet: its posts are queued and recorded, not sent.
func keyPhone(t *testing.T) (*phone, *[]finalechat.PostRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []finalechat.PostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req finalechat.PostRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"message":{"id":"m1"},"thread":{"id":"t1"}}`))
	}))
	t.Cleanup(srv.Close)
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	p := &phone{client: client, ref: "ext:eagent:x", sid: "x", features: map[string]bool{}, queue: make(chan func(context.Context), 64), posted: map[string]bool{}, ctx: context.Background()}
	return p, &got
}

// A report recorded before the phone connection knows what the server
// supports still goes out with its idempotency key: the key is made when
// the post is sent, as every other post's is.
func TestReportPostedBeforeTheServerIsKnownKeepsItsKey(t *testing.T) {
	p, got := keyPhone(t)
	r := &Runtime{st: state.New(), relayNotifyDone: map[string]bool{}}
	ev := event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "Fixed it."})
	ev.Seq = 7
	p.observeRelay(r, ev)
	// The first call learns the server's features; it runs first on the
	// worker, before the report.
	p.mu.Lock()
	p.features = featureSet([]string{"idempotency", "activity"})
	p.mu.Unlock()
	for len(p.queue) > 0 {
		(<-p.queue)(context.Background())
	}
	if len(*got) != 1 {
		t.Fatalf("%d posts", len(*got))
	}
	if (*got)[0].ClientKey == "" {
		t.Fatal("the report went out with no client key")
	}
}

// stoppedRelaySession runs a relay session mirrored to the phone until its
// reply poll is up, then stops it, as a session the user later writes to
// from the phone.
func stoppedRelaySession(t *testing.T) (cfg config.Config, project, path string, fp *relayPhone) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("Fixed the flaky login test."))
	fp = newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project = t.TempDir()
	cfg = relayPhoneConfig(t, project, model.srv.URL, fp)
	rt, err := New(cfg, Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not stop")
	}
	return cfg, project, rt.sess.Path, fp
}

// userSays is the user writing in the app, with the composer's meta.
func (f *relayPhone) userSays(text string, meta map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if meta == nil {
		meta = map[string]any{}
	}
	id := f.nextID()
	f.messages = append(f.messages, map[string]any{"id": id, "thread_id": "t1", "sender": "user", "origin": "session", "body": text, "format": "text", "importance": "normal", "meta": meta, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	f.cond.Broadcast()
	return id
}

// resumeFromPhone resumes the session with a message from the phone, as
// the reply watcher hands it over, and runs it until the test ends.
func resumeFromPhone(t *testing.T, cfg config.Config, project, path, id, text string, meta map[string]any) (*Runtime, *fakeSeat) {
	t.Helper()
	seat := newFakeSeat("Done.")
	useFakeRelays(t, seat)
	route, delivery := routeFromMeta(meta)
	rt, err := Resume(cfg, Options{Project: project, Interactive: true, Prompt: text, PromptSource: "finalechat", PromptClient: capsFromMeta(meta), PromptPhoneID: id, PromptRoute: route, PromptDelivery: delivery}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	return rt, seat
}

// Tapping Screen (or typing any of eagent's commands) on the thread of a
// stopped session resumes it and runs the command, as it does while the
// session runs; the words are never typed into the agent.
func TestSlashCommandThatResumesASessionRunsAsACommand(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	id := fp.userSays("/screen", nil)
	_, seat := resumeFromPhone(t, cfg, project, path, id, "/screen", nil)
	waitFor(t, "the agent's screen on the phone", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool {
			meta, _ := p["meta"].(map[string]any)
			return meta["kind"] == "relay_screen"
		})) > 0
	})
	for _, s := range seat.sentTexts() {
		if strings.Contains(s, "/screen") {
			t.Fatalf("/screen was typed into the agent: %q", seat.sentTexts())
		}
	}
}

// The message that resumes a session keeps what the app said about it:
// its id (for its receipt), its route, its delivery and the client's
// capabilities.
func TestPhoneMessageThatResumesASessionKeepsItsIDRouteAndDelivery(t *testing.T) {
	t.Run("to eagent", func(t *testing.T) {
		cfg, project, path, fp := stoppedRelaySession(t)
		meta := map[string]any{"eagent.route": "eagent", "eagent.client": map[string]any{"timezone": "Europe/Paris"}}
		id := fp.userSays("what is it doing?", meta)
		rt, seat := resumeFromPhone(t, cfg, project, path, id, "what is it doing?", meta)
		var d event.UserMessageData
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				if ev.Type == event.UserMessage {
					_ = ev.Decode(&d)
				}
			}
		})
		if d.PhoneID != id || d.Held == "" || d.Client == nil || d.Client.Timezone != "Europe/Paris" {
			t.Fatalf("recorded %+v", d)
		}
		waitFor(t, "the eagent receipt", func() bool {
			for _, m := range fp.metaPatches() {
				if rr, ok := m["relay_receipts"].(map[string]any); ok && rr[id] == "eagent" {
					return true
				}
			}
			return false
		})
		if got := seat.sentTexts(); len(got) != 0 {
			t.Fatalf("a message for eagent was typed into the agent: %q", got)
		}
	})
	t.Run("after the turn", func(t *testing.T) {
		cfg, project, path, fp := stoppedRelaySession(t)
		meta := map[string]any{"eagent.route": "agent", "eagent.delivery": "after_turn"}
		id := fp.userSays("then update the docs", meta)
		_, seat := resumeFromPhone(t, cfg, project, path, id, "then update the docs", meta)
		waitFor(t, "the send", func() bool { return len(seat.sentModes()) > 0 })
		if got := seat.sentModes(); got[0] != "after_turn" {
			t.Fatalf("sent with %q", got)
		}
	})
}

// A second message the user sends while the first is resuming the session
// is taken by the resumed session (the reply watcher leaves it to the
// running mirror), and the first is not taken twice.
func TestPhoneMessagesSentWhileASessionResumesAreEachTakenOnce(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	first := fp.userSays("Fix the flaky login test", nil)
	fp.userSays("and the signup one", nil)
	_, seat := resumeFromPhone(t, cfg, project, path, first, "Fix the flaky login test", nil)
	waitFor(t, "the second message", func() bool {
		for _, s := range seat.sentTexts() {
			if s == "and the signup one" {
				return true
			}
		}
		return false
	})
	time.Sleep(500 * time.Millisecond)
	count := map[string]int{}
	for _, s := range seat.sentTexts() {
		count[s]++
	}
	if count["Fix the flaky login test"] != 1 || count["and the signup one"] != 1 {
		t.Fatalf("sent %q", seat.sentTexts())
	}
}

// A file the user sends to a running session is saved under the session's
// attachments folder whatever its id and name say: "../" and "/" in
// either never reach the path.
func TestPhoneAttachmentStaysInTheSessionsFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	sess, err := store.Create(store.Root(t.TempDir()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	r := &Runtime{sess: sess, ctx: context.Background()}
	dir := attachmentsDir(sess.Path)
	for _, a := range []finalechat.Attachment{
		{ID: "../../x", Filename: "../../etc/passwd", URL: srv.URL + "/f/1"},
		{ID: "a/../../b", Filename: "/abs/path.txt", URL: srv.URL + "/f/2"},
		{ID: "..", Filename: "..", URL: srv.URL + "/f/3"},
	} {
		saved, err := r.downloadAttachment(client, a)
		if err != nil {
			t.Fatalf("%q: %v", a.ID, err)
		}
		if filepath.Dir(saved.Path) != dir || strings.Contains(filepath.Base(saved.Path), "..") {
			t.Fatalf("id %q name %q saved at %s, not a file of %s", a.ID, a.Filename, saved.Path, dir)
		}
	}
}

// lastMeta is the newest value the thread was given under key, and
// whether it was given one at all.
func (f *relayPhone) lastMeta(key string) (any, bool) {
	var got any
	found := false
	for _, m := range f.metaPatches() {
		if v, ok := m[key]; ok {
			got, found = v, true
		}
	}
	return got, found
}

func (f *relayPhone) receipts() map[string]any {
	v, _ := f.lastMeta("relay_receipts")
	rr, _ := v.(map[string]any)
	return rr
}

// The thread shows the agent from the moment the phone connection is up,
// not only once the agent first changes state.
func TestTheAgentIsOnTheThreadFromTheStart(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("Done."))
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	rt, err := New(relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "the agent on the thread", func() bool {
		v, _ := fp.lastMeta("relay")
		m, _ := v.(map[string]any)
		return m["name"] == "codex" && m["state"] != nil
	})
}

// An agent that exited while eagent was stopped shows as exited once the
// session is resumed, not as still working.
func TestAnAgentThatExitedWhileStoppedShowsAsExitedOnResume(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	oldAttach := attachRelay
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		return nil, errors.New("no such session")
	}
	t.Cleanup(func() { attachRelay = oldAttach })
	patches := len(fp.metaPatches())
	rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "the exited agent on the thread", func() bool {
		for _, m := range fp.metaPatches()[min(patches, len(fp.metaPatches())):] {
			if r, ok := m["relay"].(map[string]any); ok && r["state"] == "exited" {
				return true
			}
		}
		return false
	})
}

// The opening prompt of a session, when it is held for eagent, has its
// receipt under it on the phone, as any later message would.
func TestHeldOpeningPromptHasItsReceipt(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("Done."))
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	rt, err := New(relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Relay: "codex", Interactive: true, Prompt: "@e keep an eye on it"}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "the opening post", func() bool { return fp.postCount() > 0 })
	first := ""
	fp.mu.Lock()
	first, _ = fp.messages[0]["id"].(string)
	fp.mu.Unlock()
	waitFor(t, "its receipt", func() bool { return fp.receipts()[first] == "eagent" })
}

// The receipt under a message typed at the terminal (mirrored to the
// thread) is still there after the session is resumed and a new receipt
// is set: every patch replaces the thread's whole set.
func TestTerminalMessageReceiptSurvivesAResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("Fixed it."))
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	cfg := relayPhoneConfig(t, project, model.srv.URL, fp)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := New(cfg, Options{Project: project, Relay: "codex", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ui.input <- "Fix the flaky login test"
	mirrored := ""
	waitFor(t, "the mirrored message", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		for _, m := range fp.messages {
			if m["body"] == "Fix the flaky login test" {
				mirrored, _ = m["id"].(string)
			}
		}
		return mirrored != ""
	})
	waitFor(t, "its receipt", func() bool { return fp.receipts()[mirrored] == "delivered" })
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not stop")
	}
	useFakeRelays(t, newFakeSeat("Updated the docs."))
	gets := fp.getCount()
	rt2, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt2)
	waitFor(t, "the resumed phone poll", func() bool { return fp.getCount() > gets })
	second := fp.userSays("Now update the docs", nil)
	waitFor(t, "the new receipt", func() bool { return fp.receipts()[second] == "delivered" })
	if got := fp.receipts(); got[mirrored] != "delivered" {
		t.Fatalf("the terminal message's receipt was dropped: %v", got)
	}
}
