package harness

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// When the restart a refusal called for could not be made (the file for
// the next conversation could not be created), the conversation in use was
// never restarted: the user is not told it is refused "even after I
// restarted it from my notes", but what any failed call tells them.
func TestRefusedAfterAFailedRestartIsNotCalledRestarted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var tooLong, writing atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[:min(len(p), 4)])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: tooLongBody}
		}
		return hold()
	}, nil)
	t.Cleanup(release)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	tooLong.Store(true)
	s.type_("@e are you there?")
	s.waitFor("the dossier call to go out", writing.Load)
	dir := s.rt.sess.Path
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	release()
	told := func() string {
		for _, m := range s.ui.said() {
			if strings.Contains(m, "refuses my conversation") || strings.Contains(m, "can't reach my own model") {
				return m
			}
		}
		return ""
	}
	s.waitFor("the user to be told about the failing calls", func() bool { return told() != "" })
	if n := len(s.events(event.SubsessionStart)); n != 1 {
		t.Fatalf("%d conversations begun; the restart was meant to fail", n)
	}
	if m := told(); strings.Contains(m, "restarted it from my notes") {
		t.Fatalf("the user was told of a restart that was never made: %q", m)
	}
}
