package integration

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

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
