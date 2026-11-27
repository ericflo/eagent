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

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
)

// Known limit (from the eleventh review): "A phone file whose refusal note
// cannot be posted is lost." The person sends words and a picture from the
// phone to a running session; the picture cannot be fetched (it is gone
// from the service), and the note telling them so cannot be posted. The
// running poll takes the words in without the picture and the person on
// the phone is never told; only the terminal log says it. The reply
// watcher and a restore, in the same case, take nothing and leave the
// message for a later read, because nobody on the phone would know.
func TestSR4PhoneAFileWhoseRefusalCannotBePostedIsNotTakenWithoutAWord(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var refusedTries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) {
				refusedTries.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"not now"}}`))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}))
	waitFor(t, "the refusal note tried", func() bool { return refusedTries.Load() > 0 })
	g.rt.sync(func() {})
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == ids[0] {
			t.Fatalf("the words came in without the picture (%d attachments) and the person on the phone was never told it was not taken (its note could not be posted; logs %q)", len(d.Attachments), g.ui.logged())
		}
	}
}

// C9's rule, behind a message that holds nothing to take: an empty one,
// read while the session is ending, is not a message left for the next
// run, so a command after it is still taken and said not run (left for
// the next run, it would have the reply watcher start one only for it).
func TestK13ACommandAfterAnEmptyMessageWhileEndingIsTakenAndSaidNotRun(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g, ids := k2BehindAQuit(t, fp, r10Msg(""), r10Msg("/kill"))
	if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[2]] {
		t.Fatalf("the command after an empty message was left for the next run: taken %v, cursor %q", l.Taken, l.Cursor)
	}
	if got := fp.postsWhere(k1Kind("command_not_run")); len(got) != 1 {
		t.Fatalf("the thread was told %d times that the command was not run", len(got))
	}
}
