package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// EAGENT_BASE_URL set to the address the route already has (the preset's
// Together address): it moves nothing, the window in effect is the file's
// and the editor shows it as the file's (editable). A save of a new window
// must write it; at ecf0cf8 it was written. Now it is skipped as the
// variable's, and the save reports it as overridden by EAGENT_BASE_URL.
func TestR14RVWindowSavedUnderUnmovedAddressVariable(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_BASE_URL", "https://api.together.xyz/v1")
	s := &Service{Project: t.TempDir()}
	before := config.Resolve(s.Project, "", "")
	if src := before.Sources["/model/context_tokens"]; src != "default" && src != "file" && src != "preset:glm" {
		t.Logf("window source before save: %q", src)
	}
	cfg := before.Layered
	cfg.Model.ContextTokens = 120_000
	raw, _ := json.Marshal(cfg)
	res, err := s.Save(context.Background(), Request{Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Resolution.Effective.Model.ContextTokens; got != 120_000 {
		t.Fatalf("window after save = %d (source before %q, skipped %+v, kept %+v), want 120000", got, before.Sources["/model/context_tokens"], res.Skipped, res.Kept)
	}
}
