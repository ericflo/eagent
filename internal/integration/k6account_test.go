package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// k6Service stands in front of an r6Thread fixture as one FinaleChat
// service: it serves only its own file (own), and answers 404 for any
// other, as another service does not have it.
func k6Service(t *testing.T, f *r6Thread, own string) string {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") && r.URL.Path != "/files/"+own {
			http.Error(w, `{"error":{"code":"not_found","message":"no such file"}}`, http.StatusNotFound)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// k6Session is a stopped session that keeps the ledger, whose thread on f
// has the mirror's first post and then a reply with a file (att).
func k6Session(t *testing.T, project string, f *r6Thread, msgID, att string) (sid string, msg map[string]any) {
	t.Helper()
	sid = finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.say(sid, msgID, "see the screenshot", stop.Add(time.Minute), nil)
	file := map[string]any{"id": att, "kind": "image", "content_type": "image/png", "filename": att + ".png", "size": 3, "url": "/files/" + att}
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.threads[sid][1]["attachments"] = []map[string]any{file}
	msg = f.threads[sid][1]
	f.mu.Unlock()
	return sid, msg
}

// The project's file names one service (A) and the serve's own
// configuration another (B). A reply kept on each (its resume failed, the
// thread told eagent keeps trying) is written down for the next watcher.
// Each connection retries only its own account's: each session is resumed
// once, with its file fetched from its own service, none refused.
func TestK6AKeptReplyIsRetriedOnItsOwnAccount(t *testing.T) {
	project := t.TempDir()
	fa, fb := newR6Thread(t), newR6Thread(t)
	sa, ma := k6Session(t, project, fa, "ma", "att-a")
	sb, mb := k6Session(t, project, fb, "mb", "att-b")
	urlA, urlB := k6Service(t, fa, "att-a"), k6Service(t, fb, "att-b")
	t.Setenv("FINALECHAT_URL", urlA)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	kept := map[string]any{
		"ma": map[string]any{"thread": "eagent:" + sa, "message": ma, "since": time.Now(), "told": true, "account": urlA},
		"mb": map[string]any{"thread": "eagent:" + sb, "message": mb, "since": time.Now(), "told": true, "account": urlB},
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keptFile(project), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	host := &r9Host{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, t: t, project: project}
	wa := fa.watcher(project, host, t)
	wa.client = &finalechat.Client{BaseURL: urlA, Token: "fc_fixture"}
	wb := fb.watcher(project, host, t)
	wb.client = &finalechat.Client{BaseURL: urlB, Token: "fc_fixture"}
	wb.retries = wa.retries // one watcher, a connection per account
	wa.sweep(context.Background())
	wb.sweep(context.Background())
	if host.calls() != 2 {
		t.Fatalf("resumed %#v, want each session once", host.resumed)
	}
	for i, m := range host.resumed {
		if len(m.Attachments) != 1 || len(m.Refused) != 0 {
			t.Fatalf("resume %d (%s of %s): %d files, %d refused; want its file from its own service", i, m.ID, host.ids[i], len(m.Attachments), len(m.Refused))
		}
	}
}
