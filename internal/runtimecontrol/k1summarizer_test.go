package runtimecontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// E5 (not changed): EAGENT_SUMMARIZER_* apply to a chosen preset as the
// environment applies everywhere, as EAGENT_MODEL does (the owner's
// decision for round 7 finding 26). With summaries on GLM-5.3 Flash by
// EAGENT_SUMMARIZER_MODEL, anthropic-high would put that model at
// Anthropic's address: a running session's switch to it is refused with
// the reason, and nothing is applied.
func TestK1APresetTheSummarizerVariablesCannotHoldUnderIsRefused(t *testing.T) {
	r14Env(t)
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	t.Setenv("EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3-Flash")
	t.Setenv("TOGETHER_API_KEY", "x")
	t.Setenv("ANTHROPIC_API_KEY", "y")
	project := t.TempDir()
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	var applied []Values
	c := r14Session(t, project, "s", cfg, &applied)
	q := r14Request(t, c, control.Edit{Op: "set", Key: "/config", Value: "anthropic-high"})
	stop := runLoop(t, c)
	out, err := Deliver(context.Background(), project, "s", q, false)
	stop()
	msg, _ := out.Result["message"].(string)
	if err != nil || out.Status == "succeeded" || !strings.Contains(msg, "zai-org/GLM-5.3-Flash") || len(applied) != 0 {
		t.Fatalf("outcome %s %q (%v), applied %+v", out.Status, msg, err, applied)
	}
}
