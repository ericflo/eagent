package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// A session to bring back whose thread was read, but whose reply could not
// be left for the reply watcher (integration.ErrHandOver): the terminal is
// not told its thread could not be checked.
func TestK1ARestoreWhoseHandOverFailsSaysSo(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	oldRestore, oldReply := restoreSession, pendingReply
	t.Cleanup(func() { restoreSession, pendingReply = oldRestore, oldReply })
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, fmt.Errorf("%w: disk full", integration.ErrHandOver)
	}
	restoreSession = func(*Server, string, integration.PhoneMessage) error {
		t.Fatal("brought back although its reply could not be left for the watcher")
		return nil
	}
	var lines []string
	New(project, func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }).restoreHosted(context.Background())
	said := strings.Join(lines, "\n")
	if strings.Contains(said, "could not check its thread") || !strings.Contains(said, "disk full") {
		t.Fatalf("said %q", said)
	}
}

// A reply from the phone whose files the reply watcher could not fetch
// resumes the session with them refused: the resumed session records them
// (phone.refused) and its mirror tells the sender.
func TestK1APhoneResumeCarriesTheRefusedFiles(t *testing.T) {
	s, _ := newTestServer(t)
	refused := []integration.RefusedFile{{AttachmentID: "att-1", Text: "eagent could not fetch shot.png from the phone"}}
	o := s.phoneResumeOptions(integration.PhoneMessage{ID: "m1", Text: "see the screenshot", Refused: refused})
	if len(o.PromptRefused) != 1 || o.PromptRefused[0] != refused[0] || o.PromptPhoneID != "m1" {
		t.Fatalf("resumed with refused %#v", o.PromptRefused)
	}
}
