package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// agentHomes points the agents' own configuration at empty folders, so a
// test does not read the machine's ~/.codex or ~/.claude.
func agentHomes(t *testing.T) (codex, claude string) {
	t.Helper()
	codex, claude = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("ANTHROPIC_MODEL", "")
	return codex, claude
}

// A relay starts with the model and effort the session picked, says so in
// its label, and a change made while it works waits for its turn to end,
// then starts it again on the same conversation.
func TestRelayModelPickedAtStartAndChangedBetweenTurns(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	codexHome, _ := agentHomes(t)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-6.2-astra\"\nmodel_reasoning_effort = \"xhigh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex", RelayModel: "gpt-6.2-sol", RelayEffort: "high"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*launched) != 1 || (*launched)[0].Profile.Model != "gpt-6.2-sol" || (*launched)[0].Profile.Effort != "high" {
		t.Fatalf("launched %+v", *launched)
	}
	rel := rt.st.Relays["codex"]
	if rel == nil || rel.Model != "gpt-6.2-sol" || rel.Effort != "high" || rel.Label != "Codex · gpt-6.2-sol high" {
		t.Fatalf("relay %+v", rel)
	}
	rt.syncRelaySettings()
	want := runtimecontrol.RelayRoute{Agent: "codex", Model: "gpt-6.2-sol", Effort: "high", DefaultModel: "gpt-6.2-astra", DefaultEffort: "xhigh"}
	if got := rt.activeSettings.Relays["codex"]; got != want {
		t.Fatalf("live settings relay %+v", got)
	}

	// An effort Codex does not take is refused before anything restarts.
	if _, err := rt.repickRelay("codex", relayPick{"", "bogus"}); err == nil {
		t.Fatal("a bad effort was accepted")
	}

	// Mid-turn: the change waits.
	rel.State = "working"
	msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""})
	if err != nil || !strings.Contains(msg, "when the turn ends") || len(*launched) != 1 {
		t.Fatalf("repick mid-turn: %q %v, launched %d", msg, err, len(*launched))
	}
	if got := rt.activeSettings.Relays["codex"]; !got.Waiting || got.Model != "gpt-6.2-astra" || got.Effort != "" {
		t.Fatalf("waiting change not shown: %+v", got)
	}
	// The turn ends: Codex starts again, continuing its conversation, with
	// the new model and its own effort.
	rt.onRelayEvent("codex", relay.Event{Kind: relay.KindTurn, Text: "finished"})
	if len(*launched) != 2 || (*launched)[1].Profile.Model != "gpt-6.2-astra" || (*launched)[1].Profile.Effort != "" || !seat.wasKilled() {
		t.Fatalf("after the turn: launched %+v killed %v", *launched, seat.wasKilled())
	}
	if rel := rt.st.Relays["codex"]; rel.Model != "gpt-6.2-astra" || rel.Effort != "" || rel.Label != "Codex · gpt-6.2-astra xhigh" {
		t.Fatalf("restarted relay %+v", rel)
	}
	if got := rt.activeSettings.Relays["codex"]; got.Waiting || got.Model != "gpt-6.2-astra" {
		t.Fatalf("live settings after restart %+v", got)
	}

	// Between turns a change applies at once; the same pick changes nothing.
	rt.st.Relays["codex"].State = "idle"
	if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"}); err != nil || len(*launched) != 3 || (*launched)[2].Profile.Effort != "max" {
		t.Fatalf("idle repick: %v launched %d", err, len(*launched))
	}
	if msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"}); err != nil || !strings.Contains(msg, "already") || len(*launched) != 3 {
		t.Fatalf("unchanged repick: %q %v", msg, err)
	}
}

// relay_control model takes "default" for going back to the agent's own.
func TestRelayControlModelKeepsTheOtherPick(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	agentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude", RelayModel: "opus", RelayEffort: "max"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for fn := range rt.loop {
			fn()
		}
	}()
	rt.st.Relays["claude"].State = "idle"
	args := map[string]string{"action": "model", "effort": "default"}
	out, isErr := rt.relayTool("relay_control", func(k string) string { return args[k] }, func(string, int) int { return 0 }, func(string) []string { return nil })
	if isErr || len(*launched) != 2 || (*launched)[1].Profile.Model != "opus" || (*launched)[1].Profile.Effort != "" {
		t.Fatalf("%q, launched %+v", out, *launched)
	}
}

// /model changes the relay's model and effort from any composer, telling
// a model from an effort by what the agent takes.
func TestModelCommand(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	agentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for fn := range rt.loop {
			fn()
		}
	}()
	said := func() string {
		var text string
		rt.sync(func() { text = rt.narrLastSaid })
		return text
	}
	// The loop goroutine launches; read what it launched on the loop.
	launches := func() []relay.Spec {
		var out []relay.Spec
		rt.sync(func() { out = append(out, *launched...) })
		return out
	}
	wait := func(cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out; last said %q, launched %d", said(), len(launches()))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	rt.sync(func() { rt.st.Relays["claude"].State = "idle" })
	rt.sync(func() { rt.relayCommand([]string{"/model"}, nil) })
	wait(func() bool { return strings.Contains(said(), "/model <model> [effort]") })
	rt.sync(func() { rt.relayCommand([]string{"/model", "sonnet", "High"}, nil) })
	wait(func() bool { return len(launches()) == 2 })
	if p := launches()[1].Profile; p.Model != "sonnet" || p.Effort != "high" {
		t.Fatalf("relaunched with %+v", p)
	}
	rt.sync(func() { rt.st.Relays["claude"].State = "idle" })
	rt.sync(func() { rt.relayCommand([]string{"/model", "max"}, nil) })
	wait(func() bool { return len(launches()) == 3 })
	if p := launches()[2].Profile; p.Model != "sonnet" || p.Effort != "max" {
		t.Fatalf("effort alone relaunched with %+v", p)
	}
	rt.sync(func() { rt.st.Relays["claude"].State = "idle" })
	rt.sync(func() { rt.relayCommand([]string{"/model", "default"}, nil) })
	wait(func() bool { return len(launches()) == 4 })
	if p := launches()[3].Profile; p.Model != "" || p.Effort != "max" {
		t.Fatalf("default model relaunched with %+v", p)
	}
}

// A change from the thread's settings goes through the live-settings
// controller: it waits for the turn, and the published values say so.
func TestRelayChangeFromLiveSettings(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	agentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	rt.startRuntimeSettings()
	defer rt.stopRuntimeSettings()
	rt.syncRelaySettings()
	c := rt.runtimeControl
	if c == nil || c.Values.Relays["claude"].Agent != "claude" {
		t.Fatalf("controller relays %+v", c)
	}
	view, err := runtimecontrol.View(rt.opts.Project, rt.sess.ID, c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := runtimecontrol.Grant(rt.opts.Project, rt.sess.ID)
	proposal := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation,
		Edits: []control.Edit{{Op: "set", Key: "/relays/claude/model", Value: "sonnet"}}}
	values, err := runtimecontrol.ApplyValues(rt.opts.Project, c.Values, proposal, view, grant)
	if err != nil {
		t.Fatal(err)
	}
	rt.st.Relays["claude"].State = "working"
	q := runtimecontrol.Request{ID: "c1", Expires: time.Now().Add(time.Minute), Grant: grant, Proposal: proposal}
	if err := rt.applyRuntimeSettings(q, values, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	c.Values = values // as the controller does once applied
	rt.pollRuntimeSettings()
	if got := c.Values.Relays["claude"]; !got.Waiting || got.Model != "sonnet" || len(*launched) != 1 {
		t.Fatalf("published %+v, launched %d", got, len(*launched))
	}
	rt.onRelayEvent("claude", relay.Event{Kind: relay.KindState, State: "idle"})
	if got := c.Values.Relays["claude"]; got.Waiting || len(*launched) != 2 || (*launched)[1].Profile.Model != "sonnet" {
		t.Fatalf("after the turn: published %+v, launched %+v", got, *launched)
	}
}
