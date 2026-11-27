package harness

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
)

// k1ResumingHost is the serve as the reply watcher drives it: it resumes
// the rig's stopped session with the reply, as the serve's Resume does.
type k1ResumingHost struct {
	g       *phoneRig
	mu      sync.Mutex
	hosted  bool
	resumed []integration.PhoneMessage
}

func (h *k1ResumingHost) Hosted(string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hosted
}

func (h *k1ResumingHost) Resume(_ string, msg integration.PhoneMessage) error {
	h.mu.Lock()
	h.hosted = true
	h.resumed = append(h.resumed, msg)
	h.mu.Unlock()
	h.g.resume(Options{Interactive: true, Prompt: msg.Text, PromptSource: "finalechat", PromptAttachments: msg.Attachments, PromptPhoneID: msg.ID, PromptRefused: msg.Refused})
	return nil
}

// A reply sent while the session was stopped carries two files, neither of
// which can be fetched; the service refuses the first refusal note about
// the second once. The reply watcher resumes the session with the words,
// once; the resumed session records both files refused, and its mirror
// tells the sender about each, once. The session and the watcher use the
// same service, as they do when both read the same configuration.
func TestK1AWatcherResumeTellsEveryRefusedFileOnce(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	// The service: the first note about two.png is refused.
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var failed atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("two.png")) && !failed.Swap(true) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"not now"}}`))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	g := phoneStart(t, fp, "first ask", false, phoneStopAndClose("Nothing to do."), func(c *config.Config) { c.Finalechat.BaseURL = front.URL })
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("first run exit %d", code)
	}
	ids := r10Says(fp, r10Msg("see both",
		map[string]any{"id": "att-x", "kind": "image", "content_type": "image/png", "filename": "one.png", "size": 3, "url": "/api/v1/attachments/att-x"},
		map[string]any{"id": "att-y", "kind": "image", "content_type": "image/png", "filename": "two.png", "size": 3, "url": "/api/v1/attachments/att-y"}))
	t.Setenv("FINALECHAT_URL", front.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	host := &k1ResumingHost{g: g}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, g.project, host, t.Logf)
	defer stop()
	defer cancel()
	told := func(name string) int {
		n := 0
		for _, p := range fp.postsWhere(k1Kind("attachment_refused")) {
			if body, _ := p["body"].(string); strings.Contains(body, name) {
				n++
			}
		}
		return n
	}
	waitFor(t, "both files told", func() bool { return told("one.png") >= 1 && told("two.png") >= 1 })
	cancel()
	stop()
	g.cancel()
	g.exit(20 * time.Second)
	if n1, n2 := told("one.png"), told("two.png"); n1 != 1 || n2 != 1 {
		t.Fatalf("told about one.png %d times and two.png %d times, want once each", n1, n2)
	}
	host.mu.Lock()
	n := len(host.resumed)
	host.mu.Unlock()
	words := 0
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == ids[0] {
			words++
			if len(d.Attachments) != 0 {
				t.Fatalf("taken with %d files", len(d.Attachments))
			}
		}
	}
	if n != 1 || words != 1 {
		t.Fatalf("resumed %d times, the words taken %d times; want once each", n, words)
	}
}
