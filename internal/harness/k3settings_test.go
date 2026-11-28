package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// Item 2. A settings change the session refuses before it records anything
// (here a model change for Codex while a switch replaces it) is reported to
// the app as rejected, with the reason, not as a change that may or may not
// have been made ("unknown").
func TestK3ARefusedRelayPickIsReportedRejected(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rt.startRuntimeSettings()
		rt.syncRelaySettings()
	})
	t.Cleanup(func() { rt.sync(rt.stopRuntimeSettings) })
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	project, id := rt.opts.Project, rt.sess.ID
	q := coreRequest(t, project, id, "refused", "settings.apply", control.Edit{Op: "set", Key: "/relays/codex/model", Value: "gpt-6.2-astra"})
	// The session's loop polls its controller, as Run does.
	stop := make(chan struct{})
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				rt.sync(rt.pollRuntimeSettings)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := runtimecontrol.Deliver(ctx, project, id, q, false)
	close(stop)
	<-polled
	msg, _ := out.Result["message"].(string)
	if err != nil || out.Status != "rejected" || !strings.Contains(msg, "being replaced") {
		t.Errorf("outcome %s %q (%v); want rejected, saying Codex is being replaced", out.Status, msg, err)
	}
	g.release <- struct{}{}
	recv(t, switched, "switch")
	waitRelayStarts(t, rt)
}
