package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/integration"
)

// r11Gate stands in front of a phoneFake (in place of its server, before
// any session talks to it): while status is not 0 it answers every
// refusal notice with it, and counts the ones it turned away.
type r11Gate struct{ status, turned atomic.Int32 }

func r11FailRefusals(fp *phoneFake) *r11Gate {
	g := &r11Gate{}
	old := fp.srv
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := g.status.Load(); s != 0 && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			if bytes.Contains(raw, []byte("attachment_refused")) {
				g.turned.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(int(s))
				_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"refused"}}`))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		fp.handle(w, r)
	}))
	old.Close()
	return g
}

// r11Refusals are the notices posted about files that were not taken.
func r11Refusals(fp *phoneFake) []map[string]any {
	return fp.postsWhere(func(m map[string]any) bool {
		meta, _ := m["meta"].(map[string]any)
		return meta["kind"] == "attachment_refused"
	})
}

// Finding 14: a file that cannot be taken, in a message read in the same
// page as a /quit before it, is not refused in the thread by the session
// the /quit ends: that session does not take the message (it is left for
// the next run, which tells the sender if it cannot take the file
// either), so nothing about it is said now. A file whose fetch fails, and
// one too large to take.
func TestR11NoRefusalIsPostedForAMessageTheEndingSessionLeaves(t *testing.T) {
	for _, tc := range []struct {
		name string
		file map[string]any
	}{
		{"fetch fails", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}},
		{"too large", map[string]any{"id": "big-9", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/big-9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g, ids := r10BehindAQuit(t, fp, r10Msg("and this", tc.file))
			if refused := r11Refusals(fp); len(refused) > 0 {
				t.Fatalf("the ending session told the phone the file was refused: %v", refused)
			}
			r10Untaken(t, g, ids[1], "and this")
		})
	}
}

// Finding 13: a file the running session could not fetch, whose refusal
// notice cannot be posted, is not left out untold: neither the message nor
// the one after it is taken in, and the poll reads them again. Once the
// notice goes, the message is taken in without the file, then the one
// after it: each once, in order.
func TestR11AMessageIsTakenWithoutItsFileOnlyOnceTheSenderIsTold(t *testing.T) {
	fp := newPhoneFake(true)
	gate := r11FailRefusals(fp)
	t.Cleanup(fp.srv.Close)
	gate.status.Store(http.StatusForbidden) // not retried by the client
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the agent", func() bool {
		n := 0
		g.rt.sync(func() { n = len(g.rt.st.ActiveRelays()) })
		return n == 1
	})
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	gone := map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}
	ids := r10Says(fp, r10Msg("here is the screenshot", gone), r10Msg("and the next thing"))
	waitFor(t, "the notice to be turned away", func() bool { return gate.turned.Load() > 0 })
	gate.status.Store(0)
	told := -1
	waitFor(t, "the message to be taken in", func() bool {
		if !integration.ReadPhoneLedger(g.log()).Taken[ids[0]] {
			return false
		}
		told = len(r11Refusals(fp))
		return true
	})
	if told != 1 {
		t.Fatalf("taken in with %d notices about IMG_9.png in the thread, want it taken after the one", told)
	}
	waitFor(t, "the next message to be taken in", func() bool { return integration.ReadPhoneLedger(g.log()).Taken[ids[1]] })
	if got := r6PhoneIDs(g.rt.sess.Path); len(got) != 2 || got[0] != ids[0] || got[1] != ids[1] {
		t.Fatalf("taken in %v, want %v", got, ids)
	}
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == ids[0] && (d.Text != "here is the screenshot" || len(d.Attachments) != 0) {
			t.Fatalf("taken in as %+v", d)
		}
	}
	if n := len(r11Refusals(fp)); n != 1 {
		t.Fatalf("%d notices about IMG_9.png", n)
	}
}
