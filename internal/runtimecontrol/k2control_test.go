package runtimecontrol

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
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
