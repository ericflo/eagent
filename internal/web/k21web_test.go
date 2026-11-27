package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// sr11Accounts is one phone service with two accounts: the personal
// token is no longer valid (401 on every request), the work token has the
// session's thread "t".
type sr11Accounts struct {
	mu   sync.Mutex
	seen []string
}

func (f *sr11Accounts) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+tok)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case tok != "fc_work":
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"token revoked"}}`))
	case strings.HasSuffix(r.URL.Path, "/me"):
		_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
	case r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
	default:
		_, _ = w.Write([]byte(`{"thread":{"id":"t"}}`))
	}
}

// The project's file reads a token the service no longer takes (401); the
// serve runs the bundle "work", whose token on the same service has the
// session's thread. "A stop from outside a run tells the session's thread
// on the first of two accounts that has it": the work account has it, but
// the stop asks the first account who it is, is refused, and gives up
// without trying the second. The phone keeps showing the session's agent
// at work and gets no closing note.
func TestSR11WebAStopIsNotToldOnTheSecondAccountWhenTheFirstIsRefused(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr11Accounts{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_URL", srv.URL)
	t.Setenv("SR11_WORK", "fc_work")
	t.Setenv("SR11_PERSONAL", "fc_personal")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"token_env": "SR11_PERSONAL"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"finalechat": {"token_env": "SR11_WORK"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Bundle = "work"
	info := sr9Session(t, project, srv.URL)
	cfgs := s.stopConfigs(info)
	if len(cfgs) != 2 {
		t.Fatalf("setup: stopConfigs gave %d account(s), want both", len(cfgs))
	}
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if _, _, err := stopAgents(project, info.Path, cfgs, logf); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, seen := range f.seen {
		if strings.HasPrefix(seen, "POST ") && strings.HasSuffix(seen, " fc_work") {
			return
		}
	}
	t.Fatalf("the work account has the thread, but the stop told it nothing after the first account was refused; requests %q; log %q", f.seen, logged)
}
