package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A session a terminal runs (another process), with an agent. The user
// presses Stop on the page: it goes to the inbox, the run takes it, and
// stopSession records the agent's relay.end at once and begins the end.
// The user writes to the session from the page in the run's last seconds,
// past its last look: the words are "queued". The run lets go.
// (Adapted from the review's test, which wanted the server to resume the
// session for them: the server no longer resumes a session another
// process runs for words it left there. The words wait in the inbox, and
// the answer says so, with no promise that anything resumes for them.)
func TestSR8WebWordsAfterAStopPastTheRunsLastLookAreNeverResumedFor(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	sess, _, err := store.Open(info.Path, time.Now()) // the terminal's run
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project}),
		// The user's Stop, taken: stopSession ends the agent at once.
		event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "the user stopped the session"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	// Past the run's last look: the user writes from the page.
	words := "one more thing: add the retry test"
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": words}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"queued"`) {
		_ = sess.Close()
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "resumed for them") || !strings.Contains(w.Body.String(), "next resumed") {
		_ = sess.Close()
		t.Fatalf("the answer promises what nobody does, or not what is true: %s", w.Body)
	}
	end := event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "quit"})
	end.Time = time.Now().UTC().Add(2 * time.Second)
	if _, err := sess.Append(end); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close() // the terminal's run lets go
	if sr7WatcherGone(t, s, info.ID) {
		t.Fatal("the server resumed a session another process ran, for words it left")
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 1 {
		t.Fatalf("the inbox holds %d file(s), want the words, waiting", len(left))
	}
}

// The other order. A session a terminal runs, with an agent. The user
// writes to it from the page ("queued"), and before the run's next look
// at its inbox stops the session from the phone (or types /quit at the
// terminal). The run takes the stop: stopSession records the agent's
// relay.end, and the run ends. Its last look at the inbox finds the words
// and keeps them on the record for the next run (held "the session was
// ending"), removing their file. The user stopped the session after
// writing the words; the server resumed the session the user stopped.
func TestSR8WebAStopAfterWordsAnEndingRunKeptResumesTheSession(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	sess, _, err := store.Open(info.Path, time.Now()) // the terminal's run
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project})); err != nil {
		t.Fatal(err)
	}
	words := "and add the retry test"
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": words}, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"queued"`) {
		_ = sess.Close()
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	files, _ := os.ReadDir(harness.InboxDir(info.Path))
	if len(files) != 1 {
		_ = sess.Close()
		t.Fatalf("setup: the inbox holds %d file(s)", len(files))
	}
	id := strings.TrimSuffix(files[0].Name(), ".json")
	// The user's stop from the phone, taken before the run's next look.
	if _, err := sess.Append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "the user stopped the session"})); err != nil {
		t.Fatal(err)
	}
	// The ending run's last look: the words kept for the next run.
	if _, err := sess.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: words, Source: "web", Inbox: id, Held: harness.HeldWhileEnding})); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(harness.InboxDir(info.Path), files[0].Name())); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "quit"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close() // the terminal's run lets go
	if sr7WatcherGone(t, s, info.ID) {
		t.Fatal("the user stopped the session after writing the words (from the phone, before the run's next look at its inbox), and the server resumed the session the user stopped, for them")
	}
}

// A session a terminal runs (another process). The user sends "/kill" from
// the page (eagent's own command, which a run takes wherever it is typed):
// it is "queued", and the page shows it as words that wait. The run takes
// it at its next look and carries it out: the record shows inbox.taken,
// and no user.message or user.answer with the words, so the page's
// waiting copy was never taken away. The watcher, finding the file gone
// and no turn on the record, kept watching, and when the terminal's run
// ended (Ctrl-C), it resumed the session here for words that were carried
// out long ago.
func TestSR8WebACommandFromThePageIsShownWaitingForEverAndResumesTheSession(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	info := r11Session(t, project)
	ui := &sr7Held{webUI: newWebUI(func(string, ...any) {}), in: make(chan string, 1), held: make(chan struct{}), hold: make(chan struct{})}
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan struct{})
	go func() { rt.Run(ctx); close(runDone) }()
	var after int64
	if evs, err := store.Read(info.Path); err == nil && len(evs) > 0 {
		after = evs[len(evs)-1].Seq
	}
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "/kill"}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"queued"`) {
		cancel()
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	var answer map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	// The run takes it at its next look.
	var later []event.Event
	for deadline := time.Now().Add(10 * time.Second); ; runtime.Gosched() {
		evs, _ := store.Read(info.Path)
		later = later[:0]
		taken := false
		for _, ev := range evs {
			if ev.Seq > after {
				later = append(later, ev)
				taken = taken || ev.Type == "inbox.taken"
			}
		}
		if left, _ := os.ReadDir(harness.InboxDir(info.Path)); taken && len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("setup: the run did not take the command")
		}
	}
	// What the page does with the record's events since it sent them.
	raw, _ := json.Marshal(later)
	msg, _ := json.Marshal(answer)
	got := sr8Page(t, "/kill", string(msg), `for (const ev of `+string(raw)+`) takeQueued(ev); console.log(JSON.stringify([S.queued.length, shown()]));`)
	cancel() // Ctrl-C at the terminal
	<-runDone
	resumed := sr7WatcherGone(t, s, info.ID)
	var faults []string
	if !strings.HasPrefix(got, "[0,") {
		faults = append(faults, "the page still shows the command as waiting, with "+got+", after the run carried it out (the record shows inbox.taken, never a user.message with the words)")
	}
	if resumed {
		faults = append(faults, "the terminal's run ended (interrupted) and the server resumed the session for the command it carried out")
	}
	if len(faults) > 0 {
		t.Fatal(strings.Join(faults, "; "))
	}
}

// A session mirrored to the phone (its thread is on the record) was
// resumed from the page on a saved configuration with the phone off
// ("nophone"), and stopped. The user replies in its thread on the phone;
// the reply watcher, which acts because the server's own configuration
// has the phone on, resumes the session with the reply (Resume). On main
// that resume ran on the server's own configuration: the phone on, so the
// run mirrored and the reply's answer went to the thread. Here host takes
// the session's own (runConfig: the page's pick, phone off): the run takes
// the reply from the phone and tells the phone nothing, ever: no answer,
// no closing note; the watcher counts the reply handled.
func TestSR8WebAPhoneReplyResumesTheSessionWithThePhoneOff(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	cfg := r11Model(t)
	cfg.Finalechat.Enabled = nil // the project's file: the phone on
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "nophone"), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	if own, err := s.loadConfig("", ""); err != nil || !own.Finalechat.Wanted() {
		t.Fatalf("setup: the server's phone is off (%v): the reply watcher would not act", err)
	}
	info := r11Session(t, project)
	k16Mirrored(t, info) // an earlier run mirrored: its thread is on the record
	stop := func(what string) {
		t.Helper()
		s.mu.Lock()
		h := s.running[info.ID]
		s.mu.Unlock()
		if h == nil {
			t.Fatalf("setup: %s: the session is not hosted", what)
		}
		h.cancel()
		<-h.done
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{"config": "nophone"}, nil); w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	stop("the page's resume")
	notes := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.notes)
	}
	before := notes()
	// The user's reply in the thread: the reply watcher resumes it.
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "reply-1", Text: "what is the status?"}); err != nil {
		t.Fatal(err)
	}
	stop("the phone's resume")
	if n := k1Recorded(t, info, "what is the status?"); n != 1 {
		t.Fatalf("setup: the reply is on the record %d time(s)", n)
	}
	got := notes() - before
	// Control: the same resume again, on the server's own configuration
	// (as on main), tells the phone.
	mark := notes()
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "reply-2", Text: "and now?"}); err != nil {
		t.Fatal(err)
	}
	stop("the control's resume")
	if notes() == mark {
		t.Fatal("setup: a run on the server's own configuration told the phone nothing either")
	}
	if got == 0 {
		t.Fatal("the session was resumed for a reply from the phone, on the configuration the page picked last (the phone off): the run took the reply and told the phone nothing (no closing note; its answers would not go either); on main the phone's resume ran on the server's own configuration, the phone on")
	}
}

// sr8Page is k14Send for text.
func sr8Page(t *testing.T, text, answer, after string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	var funcs []string
	for _, name := range []string{"send", "showQueued", "queuedNode", "showsWords", "takeQueued"} {
		f := regexp.MustCompile(`(?s)\nfunction ` + name + `\(.*?\n\}\n|\nasync function ` + name + `\(.*?\n\}\n`).FindString(string(src))
		funcs = append(funcs, f)
	}
	quoted, _ := json.Marshal(text)
	script := `
const all = [];
const node = (tag, attrs, kids) => { const n = {tag, attrs: attrs || {}, kids, removed: false, remove() { this.removed = true; }}; all.push(n); return n; };
const h = (tag, attrs, ...kids) => node(tag, attrs, kids.flat());
const text = n => !n ? '' : typeof n === 'string' ? n : (n.attrs.html || '') + (n.kids || []).map(text).join(' ');
const col = node('div', {class: 'col'}, []);
col.append = n => col.kids.push(n);
col.insertBefore = (n, ref) => col.kids.splice(col.kids.indexOf(ref), 0, n);
const working = node('div', {id: 'working'}, []);
col.kids.push(working);
const ta = {value: ` + string(quoted) + `, style: {}};
const $ = sel => sel === '#input' ? ta : sel === '.chat .col' ? col : sel === '#working' ? working : sel === '.chat .scroll' ? {scrollHeight: 0} : null;
const document = {querySelector: sel => all.find(n => !n.removed && sel === '[data-queued="' + n.attrs['data-queued'] + '"]') || null};
const md = s => s;
const ARCHIVE = null;
const S = {sid: 's1', tab: 'chat', detail: {alive: true, agents: []}, events: [], lastSeq: 0, queued: [], queuedSeq: 0, drafts: {}};
const clientCaps = () => ({});
const showScreen = () => {};
const told = [];
const toast = m => told.push(m);
const api = async () => (` + answer + `);
` + strings.Join(funcs, "\n") + `
const shown = () => col.kids.filter(n => !n.removed && n !== working).map(text).join(' | ');
Promise.resolve(send()).then(() => { ` + after + ` });
`
	out, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// The chat shows words a run kept for the next run (held "the session was
// ending") as kept, as the record says.
func TestK17TheChatShowsWordsKeptForTheNextRun(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	chat := regexp.MustCompile(`(?s)\nfunction chatNode\(.*?\n\}\n`).FindString(string(src))
	if chat == "" {
		t.Fatal("app.js has no chatNode()")
	}
	script := `
const h = (tag, attrs, ...kids) => ({tag, attrs: attrs || {}, kids: kids.flat()});
const text = n => !n ? '' : typeof n === 'string' ? n : (n.attrs.html || '') + (n.kids || []).map(text).join(' ');
const srcLabel = () => '', fmtTime = () => 'now', md = s => s, attachmentsNode = () => null;
` + chat + `
const kept = chatNode({type: 'user.message', data: {text: 'add the retry test', inbox: 'i1', held: '` + harness.HeldWhileEnding + `'}});
const plain = chatNode({type: 'user.message', data: {text: 'add the retry test', held: 'no agent is running'}});
console.log(JSON.stringify([text(kept).includes('kept for the next run'), text(plain).includes('kept for the next run')]));
`
	out, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	if got := strings.TrimSpace(string(out)); got != "[true,false]" {
		t.Fatalf("kept words shown kept, other held words not: got %s, want [true,false]", got)
	}
}
