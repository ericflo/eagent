package integration

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// k6Host hosts every session. Once armed, its next look (a sweep's) says so
// on entered and waits for release.
type k6Host struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (h *k6Host) Hosted(string) bool {
	if h.armed.Swap(false) {
		close(h.entered)
		<-h.release
	}
	return true
}

func (h *k6Host) Resume(string, PhoneMessage) error { return nil }

// The replies a watcher keeps are written down when it stops, for the next
// one to take up. A sweep still under way when the stop wrote them down
// could go round once more, take the file up (and remove it) into a
// watcher that is gone, and the next serve would know nothing of them. The
// stop waits for the sweep under way, and no sweep starts after it.
func TestK6AStopWaitsForTheSweepUnderWayBeforeItWritesDown(t *testing.T) {
	saved := sweepEvery
	sweepEvery = time.Millisecond
	t.Cleanup(func() { sweepEvery = saved })
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	at := r6Info(t, project, sid).Modified.Add(time.Minute)
	f.say(sid, "m1", "one more thing", at, nil)
	thread := finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}
	t.Setenv("FINALECHAT_URL", k1Stream(t, f, thread, map[string]any{"id": "m1", "sender": "user", "origin": "session", "body": "one more thing", "created_at": at}))
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &k6Host{entered: make(chan struct{}), release: make(chan struct{})}
	left := make(chan struct{}, 1)
	logf := func(format string, args ...any) {
		if strings.Contains(format, "left to its running mirror") {
			select {
			case left <- struct{}{}:
			default:
			}
		}
	}
	stop := StartResumeWatcher(context.Background(), project, host, logf)
	stopped := make(chan struct{})
	halt := func() {
		go func() { stop(); close(stopped) }()
	}
	select {
	case <-left:
	case <-time.After(2 * time.Second):
		close(host.release)
		halt()
		t.Fatal("the reply never reached the watcher")
	}
	// The stream brings nothing more: the next look is a sweep's.
	host.armed.Store(true)
	select {
	case <-host.entered:
	case <-time.After(2 * time.Second):
		close(host.release)
		halt()
		t.Fatal("no sweep came")
	}
	halt()
	select {
	case <-stopped:
		close(host.release)
		t.Fatal("the stop wrote the kept replies down and returned while a sweep was still under way")
	case <-time.After(50 * time.Millisecond):
	}
	close(host.release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the stop never returned")
	}
}
