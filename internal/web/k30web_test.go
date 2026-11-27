package web

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A stop from outside a run settles what a stopped agent is still waited
// on for (harness.EndedWaiting) though a fresh start has taken its name
// since. The user's message r1 was typed into Codex; the switch to Claude
// left it to the brief (r2); Codex was then started again, fresh, and sent
// r3. Where the brief was not typed, nothing carried r1: it is settled
// unconfirmed with the old Codex. Where the brief went, r1 stays as the
// switch left it. r3 is the running Codex's either way. (Only the newest
// agent under a name was looked at, and r1 kept no outcome for good.)
func TestK30AStopSettlesAnAgentWhoseNameWasTakenAgain(t *testing.T) {
	old := killAgent
	t.Cleanup(func() { killAgent = old })
	killAgent = func(string, string) error { return nil }
	for _, c := range []struct {
		name  string
		brief event.RelayDeliveredData
		want  map[string][]string
	}{
		{"the brief was not typed", event.RelayDeliveredData{Name: "claude", ID: "r2", OK: false, Outcome: event.DeliveryNotSent},
			map[string][]string{"r1": {event.DeliveryUnconfirmed}, "r2": {event.DeliveryNotSent}, "r3": {event.DeliveryUnconfirmed}}},
		{"the brief went", event.RelayDeliveredData{Name: "claude", ID: "r2", OK: true},
			map[string][]string{"r1": nil, "r2": {""}, "r3": {event.DeliveryUnconfirmed}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, project := newTestServer(t)
			sess := newSession(t, project)
			codex := event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: project, Host: "tmux", Target: "eagent-k30-codex"}
			for _, ev := range []event.Event{
				event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
				event.New(event.RelayStart, event.ActorHarness, codex),
				event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r1", From: "user", Mode: "queue", Text: "run the tests"}),
				event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "codex", Reason: "handed over to claude"}),
				event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-k30-claude"}),
				event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r2", From: "eagent", Mode: "queue", Text: "You are taking over from Codex. The user said: run the tests"}),
				event.New(event.RelayDelivered, event.ActorHarness, c.brief),
				event.New(event.RelayStart, event.ActorHarness, codex),
				event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r3", From: "user", Mode: "queue", Text: "and the linter"}),
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
			for range 2 { // a second stop writes nothing more
				if _, _, err := stopAgents(project, info.Path, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			evs, err := store.Read(info.Path)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			for _, ev := range evs {
				var d event.RelayDeliveredData
				if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil {
					got[d.ID] = append(got[d.ID], d.Outcome)
				}
			}
			for id, want := range c.want {
				if len(got[id]) != len(want) || len(want) == 1 && got[id][0] != want[0] {
					t.Errorf("%s has the outcomes %q, want %q", id, got[id], want)
				}
			}
		})
	}
}
