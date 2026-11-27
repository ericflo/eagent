package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The reader's case for 6e75493 (kr-wire rv15_window_test.go), as written.

// EAGENT_BASE_URL gives the address the route already has, with a
// trailing slash: it moves nothing (moved() ignores the slash), and the
// window in force is the preset's, not a server's. A client that sends no
// "loaded" saves a window of 120000. Nothing of the variable's makes that
// window, so it should be written and apply, as at f36e19c and as without
// the slash (TestR14RVWindowSavedUnderUnmovedAddressVariable).
func TestRV15DAWindowSavedUnderASlashOnlyAddressVariable(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_BASE_URL", "https://api.together.xyz/v1/")
	s := &Service{Project: t.TempDir()}
	before := config.Resolve(s.Project, "", "")
	t.Logf("before: window %d from %q, address from %q", before.Effective.Model.ContextTokens, before.Sources["/model/context_tokens"], before.Sources["/model/base_url"])
	cfg := before.Effective
	cfg.Model.ContextTokens = 120_000
	raw, _ := json.Marshal(cfg)
	res, err := s.Save(context.Background(), Request{Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Resolution.Effective.Model.ContextTokens; got != 120_000 {
		t.Fatalf("window after save = %d (skipped %+v), want 120000", got, res.Skipped)
	}
}
