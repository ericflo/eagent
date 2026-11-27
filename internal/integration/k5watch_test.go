package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
)

// sr8Promised sets up a reply whose resume failed, and whose thread was
// told eagent keeps trying every minute.
func sr8Promised(t *testing.T) (project string, fr *r10Front, w *resumeWatcher) {
	t.Helper()
	project = t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	f.say(sid, "m1", "please deploy the fix", stop.Add(time.Minute), nil)
	fr = newR10Front(t, f)
	host := &flakyResumeHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, failures: 1}
	w = f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if n := r11Told(fr, "keeps trying every minute"); n != 1 {
		t.Fatalf("setup: told %d times: %q", n, fr.posted())
	}
	t.Setenv("FINALECHAT_URL", fr.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	return project, fr, w
}

func sr8PhoneOff(t *testing.T, project string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": false}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func k5Resumed(w *resumeWatcher) int {
	return w.host.(*flakyResumeHost).calls()
}

// The thread was told eagent keeps trying every minute (the resume failed
// on a config.json being edited). At the next sweep the file still does
// not load (the edit is half done): that is no phone turned off, and the
// watcher keeps its word: the reply is not given up, the thread not told
// it stopped.
func TestSR8ABrokenConfigIsNotThePhoneTurnedOff(t *testing.T) {
	project, fr, w := sr8Promised(t)
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"enabled": tr`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(project, ""); err == nil {
		t.Fatal("setup: the half-written file loads")
	}
	w.sweep(context.Background())
	w.retries.mu.Lock()
	_, kept := w.retries.replies["m1"]
	w.retries.mu.Unlock()
	if n := r11Told(fr, "phone was turned off") + r11Told(fr, "stopped trying"); n != 0 || !kept && k5Resumed(w) != 1 {
		t.Fatalf("with config.json not loading: told %q; kept %v, resumed %d", fr.posted(), kept, k5Resumed(w))
	}
}

// The thread was told eagent keeps trying every minute. The phone is then
// turned off in the project's file, and the event stream drops (or the
// serve restarts): the watcher connects again, as main does, whatever the
// file says, and keeps trying: the reply resumes its session, and the
// thread is never told eagent stopped.
func TestSR8WatcherReconnectingWithThePhoneOffSaysNothing(t *testing.T) {
	project, fr, w := sr8Promised(t)
	sr8PhoneOff(t, project)
	_ = watchResumes(context.Background(), project, w.host, w.retries, t.Logf)
	if n := r11Told(fr, "stopped trying"); n != 0 || k5Resumed(w) != 1 {
		t.Fatalf("after the reconnection with the phone off: told %q, resumed %d; want the reply resumed and nothing more said", fr.posted(), k5Resumed(w))
	}
}

// The same across a restart of the serve: the stopping watcher writes the
// promised reply down (the kept file), and the next serve starts with the
// phone off in the project's file. Its watcher still watches, takes the
// file up and resumes the session with the reply.
func TestSR8WatcherStartingWithThePhoneOffSaysNothing(t *testing.T) {
	project, fr, w := sr8Promised(t)
	if err := w.retries.writeDown(project, t.Logf); err != nil {
		t.Fatal(err)
	}
	sr8PhoneOff(t, project)
	host := &k1Host{ResumeHost: &fakeResumeHost{hosted: map[string]bool{}}, next: make(chan PhoneMessage, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, t.Logf)
	m, ok := k1Next(t, host)
	cancel()
	stop()
	if !ok || m.ID != "m1" || r11Told(fr, "stopped trying") != 0 {
		t.Fatalf("the next serve, with the phone off in the file: resumed %#v (%v), posted %q", m, ok, fr.posted())
	}
}

// k5Watch is a stopped session whose reply carries shot.png, which cannot
// be fetched (404), and a watcher for it on a service that records posts.
func k5Watch(t *testing.T, failures int) (project string, fr *r10Front, host *flakyResumeHost, w *resumeWatcher) {
	t.Helper()
	project, sid, f, _, _, w := r9Stopped(t, "see the screenshot", r9Shot())
	_ = sid
	fr = newR10Front(t, f)
	fr.files.Store(http.StatusNotFound)
	host = &flakyResumeHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, failures: failures}
	w.host = host
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	return project, fr, host, w
}

// The session's own configuration turns its phone off. A resume for a
// reply from the phone runs with the mirror on the thread's account all
// the same (the serve's Resume), so the refusal goes along with the words
// for that run's mirror to pay, and the watcher posts nothing itself.
func TestK5AWatcherPassesTheRefusalWhatever(t *testing.T) {
	project, fr, host, w := k5Watch(t, 0)
	sr8PhoneOff(t, project)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || !k3Refused(host.resumed[0], "shot.png") || len(fr.posted()) != 0 {
		t.Fatalf("resumed %#v, posted %q; want the words with shot.png refused and nothing posted", host.resumed, fr.posted())
	}
}

// With the session's phone on, its mirror tells the sender: the refusal
// goes along with the words, and the watcher posts nothing.
func TestK5AWatcherPassesTheRefusalWhenTheSessionHasAMirror(t *testing.T) {
	_, fr, host, w := k5Watch(t, 0)
	w.sweep(context.Background())
	if host.calls() != 1 || !k3Refused(host.resumed[0], "shot.png") || len(fr.posted()) != 0 {
		t.Fatalf("resumed %#v, posted %q", host.resumed, fr.posted())
	}
}

// The project's file names one account and the serve's own configuration
// another: a session mirrored to each has a reply waiting. The watcher
// watches both accounts, and resumes each session once, with its reply.
func TestK5AWatcherWatchesBothAccounts(t *testing.T) {
	project := t.TempDir()
	a := finishedSession(t, project, time.Now().Add(-time.Hour))
	b := finishedSession(t, project, time.Now().Add(-time.Hour))
	fa, fb := newR6Thread(t), newR6Thread(t)
	fa.say(a, "ma", "on the file's account", r6Info(t, project, a).Modified.Add(time.Minute), nil)
	fb.say(b, "mb", "on the serve's account", r6Info(t, project, b).Modified.Add(time.Minute), nil)
	t.Setenv("FINALECHAT_URL", fa.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	t.Setenv("K5_PHONE_TOKEN", "fc_fixture")
	inner := &k4AccountHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, fc: config.Finalechat{BaseURL: fb.srv.URL, TokenEnv: "K5_PHONE_TOKEN"}}
	host := &k5Both{k4AccountHost: inner, next: make(chan PhoneMessage, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, t.Logf)
	defer stop()
	defer cancel()
	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case m := <-host.next:
			got[m.ID] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("resumed %v, want ma and mb", got)
		}
	}
	if !got["ma"] || !got["mb"] || inner.calls() != 2 {
		t.Fatalf("resumed %v (%d calls)", got, inner.calls())
	}
}

// k5Both is a k4AccountHost that hands over every message it resumes with.
type k5Both struct {
	*k4AccountHost
	next chan PhoneMessage
}

func (h *k5Both) Resume(id string, msg PhoneMessage) error {
	err := h.k4AccountHost.Resume(id, msg)
	h.next <- msg
	return err
}
