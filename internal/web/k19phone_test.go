package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Two accounts on one service. The project's file reads the personal
// token; the serve runs the bundle "work" (its own configuration, which
// every run it hosts runs on), with the work token on the same service.
// A session the serve ran mirrored to its thread "t" on the work account.
// The stop the serve finishes picks the project file's account (same
// address, checked first), on which the thread is not, and tells nothing,
// though the serve's own account has the thread.
func TestSR10StopOnTheServesAccountWhenTheFileNamesTheSameService(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &k18Accounts{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_URL", srv.URL)
	t.Setenv("SR10_WORK", "fc_work")
	t.Setenv("SR10_PERSONAL", "fc_personal")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"token_env": "SR10_PERSONAL"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"finalechat": {"token_env": "SR10_WORK"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Bundle = "work"
	info := sr9Session(t, project, srv.URL)
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if _, _, err := stopAgents(project, info.Path, s.stopConfigs(info), logf); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, seen := range f.seen {
		if strings.HasPrefix(seen, "POST ") && strings.HasSuffix(seen, " fc_work") {
			return
		}
	}
	t.Fatalf("the stop told the work account (which has the thread, and is the serve's own) nothing; requests %q; log %q", f.seen, logged)
}
