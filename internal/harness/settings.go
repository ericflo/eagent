package harness

import (
	"fmt"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

func (r *Runtime) startRuntimeSettings() {
	c, err := runtimecontrol.New(r.opts.Project, r.sess.ID, r.activeSettings, r.applyRuntimeSettings)
	if err != nil {
		r.ui.Log("live settings unavailable: %v", err)
		return
	}
	r.runtimeControl = c
	r.append(event.New("settings.runtime_started", event.ActorHarness, map[string]any{"format": runtimecontrol.Schema, "generation": c.Generation, "values": r.activeSettings, "persistence": "this process only"}))
}

func (r *Runtime) stopRuntimeSettings() {
	if r.runtimeControl != nil {
		if err := r.runtimeControl.Close(); err != nil {
			r.ui.Log("live settings shutdown: %v", err)
		}
		r.runtimeControl = nil
	}
}

func (r *Runtime) pollRuntimeSettings() {
	if r.runtimeControl != nil && !r.ending {
		if err := r.runtimeControl.Poll(); err != nil {
			r.ui.Log("live settings: %v", err)
		}
	}
}

func (r *Runtime) applyRuntimeSettings(q runtimecontrol.Request, values runtimecontrol.Values, result map[string]any) error {
	if r.ending || r.ctx.Err() != nil || !q.Expires.After(time.Now()) || r.runtimeControl == nil || q.Proposal.Generation != r.runtimeControl.Generation {
		return fmt.Errorf("the targeted runtime stopped before applying the change")
	}
	previousSeq := r.st.LastSeq()
	ev := r.append(event.New(runtimecontrol.EventType, event.ActorHarness, map[string]any{
		"format": runtimecontrol.Schema, "command_id": q.ID, "user_id": q.UserID, "proposal_sha256": q.Digest,
		"generation": q.Proposal.Generation, "resource_key": q.Grant.Key, "previous": r.activeSettings, "values": values, "result": result,
	}))
	if ev.Seq <= previousSeq || ev.Seq != r.st.LastSeq() {
		return fmt.Errorf("the settings event was not durably recorded")
	}
	changedTimer := r.activeSettings.NarratorTickSeconds != values.NarratorTickSeconds
	r.activeSettings = values
	if changedTimer {
		r.armNarratorTimer()
	}
	return nil
}
