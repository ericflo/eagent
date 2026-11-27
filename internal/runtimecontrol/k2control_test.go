package runtimecontrol

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A redelivered command (reconcileOnly) whose earlier delivery is still
// waiting in the session's pending directory is not answered "it will not
// be applied again" while the session can still apply it: the answer waits
// for the session's acknowledgement or the request's own deadline, as the
// first delivery's does. It used to be answered at once, and the session
// applied it afterwards.
func TestK2AReconciledCommandStillPendingIsNotSaidNeverToApply(t *testing.T) {
	applied := 0
	c, err := New(t.TempDir(), "session", FromConfig(config.Defaults()), func(_ Request, _ Values, _ map[string]any) error { applied++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q := requestFor(t, c, 5)
	q.Expires = time.Now().Add(300 * time.Millisecond)
	// The first delivery wrote it and then its connector went away.
	if err := writeCommitted(c.pending, q.ID, q); err != nil {
		t.Fatal(err)
	}
	out, err := Deliver(context.Background(), c.Project, c.Session, q, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(); err != nil {
		t.Fatal(err)
	}
	msg, _ := out.Result["message"].(string)
	if out.Status == "unknown" && strings.Contains(msg, "will not be applied") && applied != 0 {
		t.Fatalf("answered %q, and the session applied it after", msg)
	}
	if out.Status != "unknown" || applied != 0 {
		t.Fatalf("answered %s %q, applied %d times", out.Status, msg, applied)
	}
}

// A switch the summarizer's model variable cannot hold under is refused
// with the variable named, not with a hint that a catalog fetch would
// help: no fetch makes Anthropic list GLM-5.3 Flash.
func TestK2APinnedModelAProviderDoesNotListIsRefusedWithTheVariable(t *testing.T) {
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
	if err != nil || out.Status == "succeeded" || len(applied) != 0 {
		t.Fatalf("outcome %s %q (%v), applied %+v", out.Status, msg, err, applied)
	}
	if strings.Contains(msg, "catalog fetch") || !strings.Contains(msg, "EAGENT_SUMMARIZER_MODEL") {
		t.Fatalf("refused with %q, want the variable that pins the model named", msg)
	}
}

// With the session polling, the redelivery reads its acknowledgement.
func TestK2AReconciledCommandStillPendingReadsItsAcknowledgement(t *testing.T) {
	applied := 0
	c, err := New(t.TempDir(), "session", FromConfig(config.Defaults()), func(_ Request, _ Values, _ map[string]any) error { applied++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q := requestFor(t, c, 5)
	if err := writeCommitted(c.pending, q.ID, q); err != nil {
		t.Fatal(err)
	}
	stop := runLoop(t, c)
	out, err := Deliver(context.Background(), c.Project, c.Session, q, true)
	stop()
	if err != nil || out.Status != "succeeded" || applied != 1 {
		t.Fatalf("answered %s %v, applied %d times", out.Status, err, applied)
	}
}
