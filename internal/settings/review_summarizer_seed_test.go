package settings

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A dedicated summarizer with no route written is seeded from the main
// model's route; values the environment gives that route are not the
// file's and must not be written into the summarizer's.
func TestRemoteSummarizerSeedLeavesEnvOut(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_MAX_TOKENS", "1234")
	project := t.TempDir()
	writeProjectFile(t, project, `{"summarizer":{"mode":"dedicated"}}`)
	s := &Service{Project: project}
	apply(t, s, control.Edit{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"})
	written, _ := os.ReadFile(config.File(project))
	os.Unsetenv("EAGENT_MAX_TOKENS")
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	r := loaded.Summarizer.Route(loaded.Model)
	if r.MaxTokens != loaded.Model.MaxTokens {
		t.Fatalf("summarizer max_tokens %d (main %d): EAGENT_MAX_TOKENS was written\n%s", r.MaxTokens, loaded.Model.MaxTokens, written)
	}
}
