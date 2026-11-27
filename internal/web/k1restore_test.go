package web

import (
	"context"
	"errors"
	"testing"

	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A session to bring back after a restart has a reply waiting, and cannot
// come back with it (restoreSession fails): the restore gives the reply
// back to the reply watcher (integration.ReleaseReply), which otherwise
// leaves it to the restore for good.
func TestK1ARestoreThatFailsGivesTheReplyBack(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	oldRestore, oldReply, oldRelease := restoreSession, pendingReply, releaseReply
	t.Cleanup(func() { restoreSession, pendingReply, releaseReply = oldRestore, oldReply, oldRelease })
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{ID: "m1", Text: "/quit"}, true, nil
	}
	restoreSession = func(*Server, string, integration.PhoneMessage) error {
		return errors.New("config.json is being edited")
	}
	var released []string
	releaseReply = func(_ string, info store.Info, msg integration.PhoneMessage) error {
		released = append(released, info.ID+" "+msg.ID)
		return nil
	}
	New(project, nil).restoreHosted(context.Background())
	if len(released) != 1 || released[0] != sess.ID+" m1" {
		t.Fatalf("released %q, want the reply of %s", released, sess.ID)
	}
}
