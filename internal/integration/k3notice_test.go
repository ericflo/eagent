package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

// The reply watcher's side of the running poll's refusal before taking: a
// reply with two files neither of which can be fetched; the notice about
// the first goes, the one about the second cannot, and the reply is kept
// for the sweeps. Once the files can be fetched the session is resumed
// with both, and the thread still says the first was not passed on.
func TestSR5WatchAFileSaidNotPassedOnIsResumedWith(t *testing.T) {
	_, sid, f, _, host, w := r9Stopped(t, "please fix the login crash", r9Shot())
	f.mu.Lock()
	f.threads[sid][1]["attachments"] = []map[string]any{r10File("att-1", "one.png"), r10File("att-2", "two.png")}
	f.mu.Unlock()
	fr := newR10Front(t, f)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	fr.files.Store(http.StatusBadGateway)
	fr.failPosts(0, http.StatusForbidden)
	w.sweep(context.Background())
	fr.files.Store(0)
	for i := 0; i < 3 && host.calls() == 0; i++ {
		w.sweep(context.Background())
	}
	if host.calls() != 1 {
		t.Fatalf("setup: resumed %#v", host.resumed)
	}
	var told string
	for _, s := range fr.posted() {
		if strings.Contains(s, "one.png") {
			told = s
		}
	}
	if len(host.resumed[0].Attachments) == 2 && told != "" {
		t.Fatalf("the session was resumed with one.png and two.png, yet the thread was told %q", told)
	}
}

// k3Refused reports whether the resume carries a file named name refused.
func k3Refused(m PhoneMessage, name string) bool {
	for _, f := range m.Refused {
		if strings.Contains(f.Text, name) {
			return true
		}
	}
	return false
}
