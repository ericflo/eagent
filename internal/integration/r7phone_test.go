package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// Finding 16: a reply kept for retry after a failed resume stays kept when
// the retry cannot read the thread; only a read that finds nothing to take
// lets it go.
func TestARetriedReplyIsKeptWhenTheThreadCannotBeRead(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := &resumeWatcher{project: project, host: host, client: &finalechat.Client{BaseURL: "http://127.0.0.1:1", Token: "fc_fixture"}, logf: t.Logf,
		retries: &replyRetries{replies: map[string]pendingReply{}}, handled: map[string]time.Time{}}
	thread := finalechat.Thread{ID: "t1", ExternalID: "eagent:" + sid}
	m1 := finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "go on", CreatedAt: time.Now()}
	w.retries.replies[m1.ID] = pendingReply{thread: thread, msg: m1, since: time.Now(), told: true}
	w.retryPending(context.Background())
	if host.calls() != 0 {
		t.Fatalf("resumed %d times without reading the thread", host.calls())
	}
	if _, kept := w.retries.replies[m1.ID]; !kept {
		t.Fatal("the reply waiting for a retry was dropped after one failed read of the thread")
	}
}
