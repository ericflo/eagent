package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/integration"
)

// C9's rule, behind a message that holds nothing to take: an empty one,
// read while the session is ending, is not a message left for the next
// run, so a command after it is still taken and said not run (left for
// the next run, it would have the reply watcher start one only for it).
func TestK13ACommandAfterAnEmptyMessageWhileEndingIsTakenAndSaidNotRun(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g, ids := k2BehindAQuit(t, fp, r10Msg(""), r10Msg("/kill"))
	if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[2]] {
		t.Fatalf("the command after an empty message was left for the next run: taken %v, cursor %q", l.Taken, l.Cursor)
	}
	if got := fp.postsWhere(k1Kind("command_not_run")); len(got) != 1 {
		t.Fatalf("the thread was told %d times that the command was not run", len(got))
	}
}
