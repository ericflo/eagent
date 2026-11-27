package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// r13QuitHost resumes as the serve does: the message the session is
// resumed with is recorded as taken in, and the session is hosted. A
// session resumed with /quit stops at once (during runs first, while it
// is hosted): its closing note becomes the thread's latest message, and it
// reads nothing after the /quit.
type r13QuitHost struct {
	fakeResumeHost
	t       *testing.T
	project string
	f       *r6Thread
	during  func()
}

func (h *r13QuitHost) Resume(id string, msg PhoneMessage) error {
	h.mu.Lock()
	h.hosted[id] = true
	h.mu.Unlock()
	r6Record(h.t, h.project, id, r6Run(), r6Read(msg.ID))
	_ = h.fakeResumeHost.Resume(id, msg)
	if msg.Text != "/quit" {
		return nil
	}
	if h.during != nil {
		h.during()
	}
	h.f.mu.Lock()
	h.f.threads[id] = append(h.f.threads[id], map[string]any{"id": "close-" + msg.ID, "sender": "system", "origin": "token", "body": "eagent stopped this session. A reply here resumes it while eagent serve is running.", "created_at": time.Now(), "meta": map[string]any{}})
	h.f.mu.Unlock()
	h.mu.Lock()
	h.hosted[id] = false
	h.mu.Unlock()
	return nil
}

// r13QuitWaiting is a stopped session that keeps the ledger, whose thread
// has the mirror's first post (the cursor) and then the messages said,
// sent from the app after the session stopped, a minute apart.
func r13QuitWaiting(t *testing.T, said ...string) (sid string, f *r6Thread, host *r13QuitHost, w *resumeWatcher) {
	t.Helper()
	project := t.TempDir()
	sid = finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f = newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	for i, body := range said {
		f.say(sid, "m"+string(rune('1'+i)), body, stop.Add(time.Duration(i+1)*time.Minute), nil)
	}
	host = &r13QuitHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, t: t, project: project, f: f}
	return sid, f, host, f.watcher(project, host, t)
}

func r13Resumed(h *r13QuitHost) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ids []string
	for _, m := range h.resumed {
		ids = append(ids, m.ID)
	}
	return ids
}

// Finding 17: /quit and then a message wait in a stopped session's
// thread. The sweep resumes the session with the /quit, which stops it
// again at once, its closing note the thread's latest message. The next
// sweep resumes it with the message after the /quit. Once it has stopped
// with that taken in too, nothing more is resumed or kept.
func TestR13AMessageAfterAQuitThatWaitedResumesTheSession(t *testing.T) {
	sid, _, host, w := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	w.sweep(context.Background())
	if got := r13Resumed(host); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("the first sweep resumed %v, want the /quit", got)
	}
	w.sweep(context.Background())
	if got := r13Resumed(host); len(got) != 2 || got[1] != "m2" {
		t.Fatalf("resumed %v, want the /quit and then the message after it", got)
	}
	host.mu.Lock()
	host.hosted[sid] = false
	host.mu.Unlock()
	w.sweep(context.Background())
	if got := r13Resumed(host); len(got) != 2 || len(w.retries.replies) != 0 {
		t.Fatalf("after it stopped with all taken in: resumed %v, kept %#v", got, w.retries.replies)
	}
}

// Finding 17, its other case: the message after the /quit is written while
// the session resumed with the /quit runs, so the event stream brings it
// to a running session and it is kept for when the session stops. The
// /quit's resume returning does not forget it.
func TestR13AReplyKeptWhileAQuitsResumeRunsIsNotForgotten(t *testing.T) {
	sid, f, host, w := r13QuitWaiting(t, "/quit")
	thread := finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}
	host.during = func() {
		at := time.Now()
		f.say(sid, "m2", "actually fix the login bug", at, nil)
		w.consider(context.Background(), thread, finalechat.Message{ID: "m2", Sender: "user", Origin: "session", Body: "actually fix the login bug", CreatedAt: at})
	}
	w.sweep(context.Background())
	if got := r13Resumed(host); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("the first sweep resumed %v, want the /quit", got)
	}
	w.sweep(context.Background())
	if got := r13Resumed(host); len(got) != 2 || got[1] != "m2" {
		t.Fatalf("resumed %v, want the /quit and then the message kept while it ran", got)
	}
}
