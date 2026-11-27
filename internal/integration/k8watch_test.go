package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// sr9Host is a resume host whose own configuration (a serve's bundle)
// names a phone account of its own (PhoneConfig).
type sr9Host struct {
	*k1Host
	fc config.Finalechat
}

func (h *sr9Host) PhoneConfig() (config.Finalechat, error) { return h.fc, nil }

// sr9Remote stands in front of an r6Thread fixture as a remote service: its
// thread list takes latency to answer; cut counts the lists the client gave
// up on before they were answered.
func sr9Remote(t *testing.T, f *r6Thread, latency time.Duration, cut *atomic.Int32) string {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/threads" {
			select {
			case <-r.Context().Done():
				cut.Add(1)
				return
			case <-time.After(latency):
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The project's file names service A (the phone the user writes on); the
// serve runs a bundle whose phone is a self-hosted server B that is down
// (connection refused). A reply waits on A for a stopped session. main
// watched A alone and resumed the session; here B's connection failing at
// once cuts A's connection with it, every five seconds, before A's sweep
// has read its threads: the session is never resumed for the reply.
func TestSR9ADownServeAccountStopsTheProjectsWatch(t *testing.T) {
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
	var cut atomic.Int32
	urlA := sr9Remote(t, f, 150*time.Millisecond, &cut)
	down := httptest.NewServer(http.NotFoundHandler())
	urlB := down.URL
	down.Close()
	t.Setenv("FINALECHAT_URL", urlA)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	t.Setenv("SR9_B_TOKEN", "fc_b")
	host := &sr9Host{k1Host: &k1Host{ResumeHost: &fakeResumeHost{hosted: map[string]bool{}}, next: make(chan PhoneMessage, 4)}, fc: config.Finalechat{BaseURL: urlB, TokenEnv: "SR9_B_TOKEN"}}
	ctx, cancel := context.WithCancel(context.Background())
	stopWatch := StartResumeWatcher(ctx, project, host, t.Logf)
	defer func() { cancel(); stopWatch() }()
	select {
	case m := <-host.next:
		if m.ID != "m1" {
			t.Fatalf("resumed with %#v, want m1", m)
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("no resume for the reply on A in 12s (two of the watcher's restarts); A's thread list was cut %d times by B's connection failing", cut.Load())
	}
}

// Control for TestSR9ADownServeAccountStopsTheProjectsWatch: the same
// remote A, the serve's own phone being A too (one connection, as main's
// only one): the reply resumes its session.
func TestSR9ControlOneAccountResumes(t *testing.T) {
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
	var cut atomic.Int32
	urlA := sr9Remote(t, f, 150*time.Millisecond, &cut)
	t.Setenv("FINALECHAT_URL", urlA)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &sr9Host{k1Host: &k1Host{ResumeHost: &fakeResumeHost{hosted: map[string]bool{}}, next: make(chan PhoneMessage, 4)}, fc: config.Finalechat{BaseURL: urlA}}
	ctx, cancel := context.WithCancel(context.Background())
	stopWatch := StartResumeWatcher(ctx, project, host, t.Logf)
	defer func() { cancel(); stopWatch() }()
	select {
	case m := <-host.next:
		if m.ID != "m1" {
			t.Fatalf("resumed with %#v, want m1", m)
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("control: no resume in 12s; cut %d", cut.Load())
	}
}
