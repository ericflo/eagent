package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// A message from the phone whose file cannot be fetched, then /quit. The
// words are taken without the file, and the note's first post meets one
// 503. The note is queued on the mirror's one worker ahead of the closing
// note, and its post has a client key, so it is tried again and goes out,
// once, before the session's closing note.
func TestSR6PhoneARefusalNoteMeetingA503GoesOutBeforeTheClosingNote(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var blipped atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) && !blipped.Swap(true) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = front.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp,
		r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}),
		r10Msg("/quit"))
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	taken := false
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == ids[0] {
			taken = true
		}
	}
	if !taken {
		t.Fatalf("setup: the words were not taken; logs %v", g.ui.logged())
	}
	if !blipped.Load() {
		t.Fatal("setup: the note was never tried")
	}
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("the session quit with its refusal note posted %d times", n)
	}
}
