package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/integration"
)

// k1LeftHost is the serve, as the reply watcher sees it: it resumes the
// session (and hands each message over), and runs nothing.
type k1LeftHost struct {
	mu      sync.Mutex
	resumed []integration.PhoneMessage
	next    chan integration.PhoneMessage
}

func (h *k1LeftHost) Hosted(string) bool { return false }
func (h *k1LeftHost) Resume(_ string, msg integration.PhoneMessage) error {
	h.mu.Lock()
	h.resumed = append(h.resumed, msg)
	h.mu.Unlock()
	h.next <- msg
	return nil
}

// B14's other half: while a session is stopped the person sends /quit and
// then a message from the phone. The session is resumed from the browser,
// or from the terminal, with words of its own; its mirror reads on, takes
// the /quit, and leaves the message for the next run, the closing note the
// thread's latest message. The project's reply watcher resumes the session
// with the message, once.
func TestK1AMessageLeftBehindAQuitByAResumeIsResumedOnceItStops(t *testing.T) {
	for _, from := range []string{"web", ""} {
		name := map[string]string{"web": "web", "": "terminal"}[from]
		t.Run(name, func(t *testing.T) {
			fp := newPhoneFake(false)
			t.Cleanup(fp.srv.Close)
			g := phoneStart(t, fp, "first ask", false, phoneStopAndClose("Nothing to do."), nil)
			if code := g.exit(20 * time.Second); code != 0 {
				t.Fatalf("first run exit %d", code)
			}
			ids := r10Says(fp, r10Msg("/quit"), r10Msg("one more thing: bump the version"))
			g.resume(Options{Interactive: true, Prompt: "carry on", PromptSource: from})
			if code := g.exit(20 * time.Second); code != 0 {
				t.Fatalf("resumed run exit %d", code)
			}
			if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[0]] || l.Taken[ids[1]] {
				t.Fatalf("taken %v: want the /quit taken and the message left", l.Taken)
			}
			t.Setenv("FINALECHAT_URL", fp.srv.URL)
			t.Setenv("FINALECHAT_TOKEN", "fc_test")
			host := &k1LeftHost{next: make(chan integration.PhoneMessage, 4)}
			ctx, cancel := context.WithCancel(context.Background())
			stop := integration.StartResumeWatcher(ctx, g.project, host, t.Logf)
			defer stop()
			defer cancel()
			select {
			case m := <-host.next:
				if m.ID != ids[1] || m.Text != "one more thing: bump the version" {
					t.Fatalf("resumed with %#v, want the message left behind the /quit", m)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the message left behind the /quit was not resumed")
			}
			cancel()
			stop()
			host.mu.Lock()
			defer host.mu.Unlock()
			if len(host.resumed) != 1 {
				t.Fatalf("resumed %d times: %#v", len(host.resumed), host.resumed)
			}
		})
	}
}
