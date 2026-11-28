package harness

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A serve restart restores a stopped session whose waiting reply carries
// a file too large to take. The restore (PendingReply) resumes the session
// with the words, the file refused; the resumed session records it and its
// mirror tells the sender, once, on a service with client keys and on one
// without, and when the restore's service would have refused the note.
func TestK1ARestoredTooLargeFileIsToldOnce(t *testing.T) {
	for _, c := range []struct {
		name          string
		keys, refuses bool
	}{{"keys", true, false}, {"no keys", false, false}, {"note refused at the restore", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			if !c.keys {
				fp.features = slices.DeleteFunc(fp.features, func(f string) bool { return f == "idempotency" })
			}
			t.Cleanup(fp.srv.Close)
			restoreURL := fp.srv.URL
			if c.refuses {
				u, err := url.Parse(fp.srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				proxy := httputil.NewSingleHostReverseProxy(u)
				var refused atomic.Bool
				front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.Body != nil {
						raw, _ := io.ReadAll(r.Body)
						r.Body = io.NopCloser(bytes.NewReader(raw))
						r.ContentLength = int64(len(raw))
						if bytes.Contains(raw, []byte("attachment_refused")) && !refused.Swap(true) {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"not now"}}`))
							return
						}
					}
					proxy.ServeHTTP(w, r)
				}))
				t.Cleanup(front.Close)
				restoreURL = front.URL
			}
			g := phoneStart(t, fp, "first ask", false, phoneStopAndClose("Nothing to do."), nil)
			if code := g.exit(20 * time.Second); code != 0 {
				t.Fatalf("first run exit %d", code)
			}
			r10Says(fp, r10Msg("here it is", map[string]any{"id": "att-big", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/att-big"}))
			t.Setenv("FINALECHAT_URL", restoreURL)
			t.Setenv("FINALECHAT_TOKEN", "fc_test")
			info, err := store.Resolve(store.Root(g.project), g.rt.sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			msg, ok, err := integration.PendingReply(context.Background(), g.project, info)
			if err != nil || !ok {
				t.Fatalf("pending reply %#v ok=%v err=%v", msg, ok, err)
			}
			g.resume(Options{Interactive: true, Prompt: msg.Text, PromptSource: "finalechat", PromptAttachments: msg.Attachments, PromptPhoneID: msg.ID, PromptRefused: msg.Refused})
			told := func() int {
				n := 0
				for _, p := range fp.postsWhere(k1Kind("attachment_refused")) {
					if body, _ := p["body"].(string); strings.Contains(body, "clip.mov") {
						n++
					}
				}
				return n
			}
			waitFor(t, "the sender told", func() bool { return told() >= 1 })
			g.cancel()
			g.exit(20 * time.Second)
			if n := told(); n != 1 {
				t.Fatalf("told about clip.mov %d times, want once", n)
			}
		})
	}
}
