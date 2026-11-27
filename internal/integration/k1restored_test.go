package integration

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

// A serve restart finds a reply with two files too large to take.
// PendingReply posts both notices (eagent's is now the thread's latest
// message) and hands the words to the restore, writing the reply down,
// marked restored, for the watcher. The restore then cannot bring the
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
	if p := r10Preview(f, sid); p != "system" {
		t.Fatalf("the thread's latest message is from %q, want the notice", p)
	}
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
