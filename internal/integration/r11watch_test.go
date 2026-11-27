package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

// r11Keys stands in front of target and keeps the idempotency key of
// every post that goes through it.
func r11Keys(t *testing.T, target string) (front string, keys func() []string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				ClientKey string `json:"client_key"`
			}
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			seen = append(seen, body.ClientKey)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// Finding 14: the notice about a file too large to take is posted under
// the key the running mirror posts it under (phone.go's p.key("refused",
// message, file): "eagent:<session>:refused:<message>:<file>"), so the
// thread shows it once whoever tells the sender first.
func TestR11ARefusalIsPostedUnderTheMirrorsKey(t *testing.T) {
	big := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, _, fr, _, _ := r9Stopped(t, "here it is", big)
	front, keys := r11Keys(t, fr.srv.URL)
	t.Setenv("FINALECHAT_URL", front)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("msg %#v ok=%v err=%v", msg, ok, err)
	}
	if got, want := keys(), "eagent:"+sid+":refused:m1:att-2"; len(got) != 1 || got[0] != want {
		t.Fatalf("posted under %q, want one post under %q", got, want)
	}
}

// r11Restore is a stopped session whose reply carries a file too large
// to take (clip.mov) and then second, as the serve restores it: its
// account is the r10Front's, and so is the watcher's.
func r11Restore(t *testing.T, second map[string]any) (project, sid string, f *r6Thread, fr *r10Front, host *r9Host, w *resumeWatcher) {
	t.Helper()
	big := map[string]any{"id": "att-1", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-1"}
	project, sid, f, _, host, w = r9Stopped(t, "please look at both", big)
	f.mu.Lock()
	f.threads[sid][1]["attachments"] = []map[string]any{big, second}
	f.mu.Unlock()
	fr = newR10Front(t, f)
	t.Setenv("FINALECHAT_URL", fr.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	return project, sid, f, fr, host, w
}

// r11Told counts the notices posted about name.
func r11Told(fr *r10Front, name string) int {
	n := 0
	for _, s := range fr.posted() {
		if strings.Contains(s, name) {
			n++
		}
	}
	return n
}

// Finding 12: restoring a session after a restart of the serve, a reply
// whose first file is too large to take and whose second cannot be
// fetched yet is not taken, and nothing is posted (the thread's latest
// message is still the reply), so the watcher's sweep finds it: once all
// is well again it resumes the session with it, once, with the second
// file, the first refused on the resume for the resumed session to tell.
func TestR11ARestoreThatCannotFetchALaterFileLeavesTheReplyForTheSweep(t *testing.T) {
	project, sid, f, fr, host, w := r11Restore(t, r10File("att-2", "shot.png"))
	fr.files.Store(http.StatusBadGateway)
	if msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid)); err == nil || ok {
		t.Fatalf("taken while shot.png could not be fetched: msg %#v ok=%v err=%v", msg, ok, err)
	}
	if p := r10Preview(f, sid); p != "user" {
		t.Fatalf("the thread's latest message is from %q after the restore gave up, want the reply (notices %q)", p, fr.posted())
	}
	fr.files.Store(0)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "please look at both" || len(host.resumed[0].Attachments) != 1 || !k3Refused(host.resumed[0], "clip.mov") {
		t.Fatalf("after all is well again: resumed %#v", host.resumed)
	}
	if n := r11Told(fr, "clip.mov"); n != 0 {
		t.Fatalf("the watcher posted %d notices about clip.mov: %q", n, fr.posted())
	}
}

// Finding 12, its other case: both files too large, the notice about the
// first posted and the one about the second not. The restore takes
// nothing, and the first notice is the thread's latest message by then,
// which the sweep passes over: the restore leaves the reply with the
// project's watcher (B11), whose sweeps resume the session once all is
// well again.
func TestR11ARestoreWhoseSecondNoticeFailedLeavesTheReplyForTheSweep(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, _, fr, host, w := r11Restore(t, big2)
	fr.failPosts(0, http.StatusForbidden)
	if msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid)); err == nil || ok {
		t.Fatalf("taken while nobody could be told about clip2.mov: msg %#v ok=%v err=%v", msg, ok, err)
	}
	for i := 0; i < 3; i++ {
		w.sweep(context.Background())
	}
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 0 {
		t.Fatalf("after all is well again: resumed %#v", host.resumed)
	}
}
