package runtimecontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The user's own pick of a model the provider does not list (one a catalog
// fetch would bring) is refused; the variable that pins the summarizer's
// model played no part in it (the user named the model), so the refusal
// must not blame the variable.
func TestRV16OwnPickNotBlamedOnPinnedVariable(t *testing.T) {
	r14Env(t)
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	t.Setenv("EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3-Flash")
	t.Setenv("TOGETHER_API_KEY", "x")
	project := t.TempDir()
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	var applied []Values
	c := r14Session(t, project, "s", cfg, &applied)
	t.Logf("before: %+v", c.Values.Routes)
	q := r14Request(t, c, control.Edit{Op: "set", Key: "/routes/summarizer/model", Value: "zai-org/a-live-model-not-yet-fetched"})
	stop := runLoop(t, c)
	out, err := Deliver(context.Background(), project, "s", q, false)
	stop()
	msg, _ := out.Result["message"].(string)
	t.Logf("outcome %s %q (%v)", out.Status, msg, err)
	if err != nil || out.Status == "succeeded" {
		t.Fatalf("outcome %s %q (%v)", out.Status, msg, err)
	}
	if strings.Contains(msg, "EAGENT_SUMMARIZER_MODEL") {
		t.Fatalf("refusal blames the variable for the user's own pick: %q", msg)
	}
}
