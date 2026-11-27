package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// A serve restart finds a reply with two files too large to take, in a
// thread whose latest message is eagent's note. PendingReply hands the
// words to the restore, the files refused, writing the reply down, marked
// restored, for the watcher. The restore then cannot bring the
// session back (restoreSession fails, so nothing resumes it), and gives the
// reply back (ReleaseReply). The watcher started after it should resume
// the session with the words; a sweep's look at the thread passes it over
// behind the notice.
func TestRV15RestoreThatNeverResumesLeavesTheReplyToTheWatcher(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, f, _, stopped, _ := r11Restore(t, big2)
	host := &k1Host{ResumeHost: stopped, next: make(chan PhoneMessage, 4)}
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("PendingReply %#v ok=%v err=%v, want m1", msg, ok, err)
	}
	k1Hide(f, sid) // eagent's note is the thread's latest: the sweep passes it over
	// The restore fails here: the session is not resumed with msg.
	if err := ReleaseReply(project, r6Info(t, project, sid), msg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, t.Logf)
	defer stop()
	defer cancel()
	m, ok := k1Next(t, host)
	if !ok || m.ID != "m1" {
		t.Fatalf("resumed %#v (%v): the reply the failed restore held was never resumed", m, ok)
	}
}

// The same, when the restore did start a run but the run ended without
// taking the reply in (its record was written after the hand-over, and
// holds no trace of the message): the restore is over, and the watcher
// resumes the session with the words.
func TestK1ARestoredRunThatEndsWithoutTheReplyLeavesItToTheWatcher(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, _, _, stopped, w := r11Restore(t, big2)
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("PendingReply %#v ok=%v err=%v, want m1", msg, ok, err)
	}
	r6Record(t, project, sid, r6Run()) // the restore's run, which stopped at once
	w.sweep(context.Background())
	if stopped.calls() != 1 || stopped.resumed[0].ID != "m1" {
		t.Fatalf("resumed %#v, want the reply the run did not take", stopped.resumed)
	}
}

// A watcher holds a restore's entry (the restore has not resumed the
// session yet) and then takes up another entry for the same thread, not
// the restore's (an earlier run's message left for the next one). The
// restore's hold stays: the thread keeps one entry, still the restore's.
func TestK1ATakingUpAnotherEntryKeepsTheRestoresHold(t *testing.T) {
	project := t.TempDir()
	thread := finalechat.Thread{ExternalID: "eagent:s1"}
	r := &replyRetries{replies: map[string]pendingReply{"m1": {thread: thread, msg: finalechat.Message{ID: "m1"}, since: time.Now(), restored: true}}}
	LeftForNextRun(project, "s1", true, t.Logf)
	r.takeUp(project)
	if p, ok := r.replies["m1"]; !ok || !p.restored || len(r.replies) != 1 {
		t.Fatalf("kept %#v, want the restore's entry alone", r.replies)
	}
}

// k1Hide makes eagent's note the thread's latest message, as a closing
// note does, so the sweep's own pass passes the thread over.
func k1Hide(f *r6Thread, sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "note-" + sid, "sender": "system", "origin": "token", "body": "eagent session ended (shutdown).", "created_at": time.Now(), "meta": map[string]any{}})
}

// A restore finds a reply with two files too large to take, while the
// service refuses a post. It posts nothing itself: the reply comes back
// with both files refused, for the resumed session to record and its
// mirror to tell the sender, once each, as for the watcher's resume.
func TestK1ARestorePassesItsTooLargeFilesRefused(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, f, fr, _, _ := r11Restore(t, big2)
	fr.failPosts(0, 403)
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" || msg.Text != "please look at both" || len(msg.Attachments) != 0 {
		t.Fatalf("PendingReply %#v ok=%v err=%v, want the words", msg, ok, err)
	}
	if !k3Refused(msg, "clip.mov") || !k3Refused(msg, "clip2.mov") || len(msg.Refused) != 2 {
		t.Fatalf("refused %#v, want both files", msg.Refused)
	}
	if posts := fr.posted(); len(posts) != 0 || r10Preview(f, sid) != "user" {
		t.Fatalf("the restore posted %q", posts)
	}
}
