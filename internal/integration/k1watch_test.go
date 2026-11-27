package integration

import (
	"context"
	"testing"
	"time"
)

// k1Host is r13QuitHost that hands over every message it resumes with.
type k1Host struct {
	*r13QuitHost
	next chan PhoneMessage
}

func (h *k1Host) Resume(id string, msg PhoneMessage) error {
	err := h.r13QuitHost.Resume(id, msg)
	h.next <- msg
	return err
}

// k1Next waits for the next resume, for at most two seconds.
func k1Next(t *testing.T, h *k1Host) (PhoneMessage, bool) {
	t.Helper()
	select {
	case m := <-h.next:
		return m, true
	case <-time.After(2 * time.Second):
		return PhoneMessage{}, false
	}
}

// B14: a serve restart brings back a session (PendingReply) whose earliest
// message waiting is /quit, with a message after it. The session resumed
// with the /quit stops at once, its closing note the thread's latest
// message. The reply watcher, started after the restore as the serve starts
// it, resumes the session with the message after the /quit.
func TestK1ARestoreWithAQuitFirstLeavesNothingBehind(t *testing.T) {
	sid, f, quit, _ := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	host := &k1Host{r13QuitHost: quit, next: make(chan PhoneMessage, 4)}
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), quit.project, r6Info(t, quit.project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the /quit", msg, ok, err)
	}
	if err := host.Resume(sid, msg); err != nil {
		t.Fatal(err)
	}
	<-host.next
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, quit.project, host, t.Logf)
	defer stop()
	defer cancel()
	m, ok := k1Next(t, host)
	if !ok || m.ID != "m2" || m.Text != "actually fix the login bug" {
		t.Fatalf("after the restore's /quit: resumed %v, want the message after it", r13Resumed(quit))
	}
}
