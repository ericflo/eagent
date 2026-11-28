package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// k27Held is a session whose agent (profile, Claude) was stopped while a
// message held for the end of its turn still waited, unread, in its spool:
// eagent ended before the stop's settle recorded it.
func k27Held(t *testing.T, project, profile string) store.Info {
	t.Helper()
	sess := newSession(t, project)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "helper", Profile: profile, Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-sr14-helper"}),
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "helper", ID: "h1", From: "user", Mode: "after_turn", Text: "then run the linter"}),
		event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "helper", Reason: "stopped"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(info.Path, "relays", "helper", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "1700000000000000000-h1.txt"), []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	return info
}

func k27Outcome(t *testing.T, info store.Info, id string) string {
	t.Helper()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			out += d.Outcome + ";"
		}
	}
	return out
}

// A resume settles a held message whose copy is still unclaimed in the
// spool as not_sent, also when the agent's profile is no longer in the
// configuration (the user renamed or removed it): the spool is found from
// the record (harness.SpoolOf). (The resume looked it up through the
// profile, found none, recorded the message unconfirmed and left the copy
// in the spool.)
func TestK27AResumeTakesAHeldMessageBackWhenItsProfileIsGone(t *testing.T) {
	_, project := newTestServer(t)
	cfg := r11Model(t)
	if _, ok := cfg.Relay.RelayProfiles()["helper"]; ok {
		t.Fatal("setup: the configuration has a helper profile")
	}
	byResume := k27Held(t, project, "helper")
	ui := newWebUI(func(string, ...any) {})
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, byResume.Path)
	if err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	rt.Run(stopped)

	left := relay.Unspool(filepath.Join(byResume.Path, "relays", "helper", "after_turn"), "h1")
	resumeSays := k27Outcome(t, byResume, "h1")
	t.Logf("resume: %q; copy still in the spool after the resume: %v", resumeSays, left)
	// Control: the same record under a profile the configuration has.
	if _, ok := cfg.Relay.RelayProfiles()["claude"]; ok {
		ctl := k27Held(t, project, "claude")
		rt2, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, ctl.Path)
		if err != nil {
			t.Fatal(err)
		}
		rt2.Run(stopped)
		t.Logf("control (profile claude present): resume says %q", k27Outcome(t, ctl, "h1"))
	}
	if resumeSays != event.DeliveryNotSent+";" {
		t.Errorf("a held message whose copy is unclaimed in the spool, after a resume: %q; want not_sent", resumeSays)
	}
}
