package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// As TestK1AWatcherElsewhereLeavesARestoredReplyToTheRestore, but the
// /quit reached the session while it ran under the serve, whose watcher
// kept it quietly (keepUntilStopped) and wrote it down as it stopped for
// the restart. The restore's hand-over (PendingReply) then writes its hold
// on the same message, and writeDown keeps the older quiet entry in its
// place (the rule for a hold given back by ReleaseReply): no hold is on
// the file. `connector run`'s watcher, sweeping before the restore starts
// the session, resumes it with the /quit, and the restore resumes it with
// the /quit again.
func TestSR5WatchARestoreHoldOverAQuietEntryIsKept(t *testing.T) {
	sid, f, quit, _ := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	f.mu.Lock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "note", "sender": "system", "origin": "token", "body": "eagent session ended (shutdown).", "created_at": time.Now(), "meta": map[string]any{}})
	f.mu.Unlock()
	info := r6Info(t, quit.project, sid)
	// Written down by the serve's watcher as it stopped: the /quit, kept
	// quietly while the session ran.
	f.mu.Lock()
	m1 := finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "/quit"}
	f.mu.Unlock()
	stopping := &replyRetries{replies: map[string]pendingReply{"m1": {thread: finalechat.Thread{ExternalID: "eagent:" + sid}, msg: m1, since: time.Now()}}}
	if err := stopping.writeDown(quit.project, t.Logf); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), quit.project, info)
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the /quit", msg, ok, err)
	}
	connector := f.watcher(quit.project, quit, t)
	connector.sweep(context.Background())
	if err := quit.Resume(sid, msg); err != nil {
		t.Fatal(err)
	}
	connector.sweep(context.Background())
	if got := r13Resumed(quit); len(got) < 1 || got[0] != "m1" || len(got) > 1 && got[1] == "m1" {
		t.Fatalf("resumed %v: the /quit reached the session twice", got)
	}
}
