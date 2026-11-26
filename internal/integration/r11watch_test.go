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
