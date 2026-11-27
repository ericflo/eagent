package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// A serve restart restores a session with a reply whose two files are too
// large (both notices go out, so eagent's is the thread's latest message).
// `connector run` holds the project's watch: its sweep takes up the
// restore's entry, marked restored, and leaves the session to the restore.
// The restore then cannot bring the session back and gives the reply back
// (ReleaseReply). The connector's next sweep should resume the session with
// the reply; the hold must not outlive the restore that gave it up.
func TestSR2ReleaseAfterAWatcherTookUpTheRestoresEntryIsNotDropped(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	t.Setenv("EAGENT_FINALECHAT", "")
	project, sid, f, _, stopped, connector := r11Restore(t, big2)
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("PendingReply %#v ok=%v err=%v, want m1", msg, ok, err)
	}
	if p := r10Preview(f, sid); p != "system" {
		t.Fatalf("the thread's latest message is from %q, want the notice", p)
	}
	// The connector's watcher sweeps while the restore is still starting
	// the session: it takes up the restore's entry and holds off.
	connector.sweep(context.Background())
	if n := stopped.calls(); n != 0 {
		t.Fatalf("resumed %d times while the restore held the reply", n)
	}
	// The restore fails, and gives the reply back.
	if err := ReleaseReply(project, r6Info(t, project, sid), msg); err != nil {
		t.Fatal(err)
	}
	connector.sweep(context.Background())
	connector.sweep(context.Background())
	if stopped.calls() != 1 || stopped.resumed[0].ID != "m1" {
		t.Fatalf("resumed %#v after the restore gave the reply back, want m1 once (the release was dropped while the watcher held the restore's entry)", stopped.resumed)
	}
}

// The same release, written while the watcher holding the restore's entry
// stops: what it writes down does not put the restore's mark back over the
// copy given back.
func TestK2AWatcherStoppingAfterAReleaseWritesItReleased(t *testing.T) {
	project := t.TempDir()
	thread := finalechat.Thread{ExternalID: "eagent:s1"}
	r := &replyRetries{replies: map[string]pendingReply{"m1": {thread: thread, msg: finalechat.Message{ID: "m1"}, since: time.Now(), restored: true}}}
	if err := ReleaseReply(project, store.Info{ID: "s1"}, PhoneMessage{ID: "m1"}); err != nil {
		t.Fatal(err)
	}
	_ = r.writeDown(project, t.Logf)
	next := &replyRetries{replies: map[string]pendingReply{}}
	next.takeUp(project)
	if p, ok := next.replies["m1"]; !ok || p.restored {
		t.Fatalf("took up %#v, want m1 given back", next.replies)
	}
}

// k2FailingHost never resumes the session.
type k2FailingHost struct{ fakeResumeHost }

func (h *k2FailingHost) Resume(string, PhoneMessage) error {
	return errors.New("config.json is being edited")
}

// A reply cannot resume its session, and the note that says so cannot be
// posted either (the service refuses it). The thread is not counted as
// told: the next try that fails posts the note.
func TestK2ANoteThatCouldNotBePostedIsPostedByTheNextTry(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	f.say(sid, "m1", "please deploy the fix", stop.Add(time.Minute), nil)
	fr := newR10Front(t, f)
	fr.failPosts(http.StatusForbidden)
	w := f.watcher(project, &k2FailingHost{fakeResumeHost{hosted: map[string]bool{}}}, t)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if n := len(fr.posted()); n != 0 {
		t.Fatalf("posted %q although the service refused it", fr.posted())
	}
	w.sweep(context.Background())
	if n := r11Told(fr, "could not resume"); n != 1 {
		t.Fatalf("told %d times: %q", n, fr.posted())
	}
}
