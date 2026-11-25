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

// Ports of the old relaypick_test.go: choosing the agent's model and
// effort at start and changing them live.

// relayAgentHomes points the agents' own configuration at empty folders, so
// a test does not read the machine's ~/.codex or ~/.claude.
func relayAgentHomes(t *testing.T) (codex, claude string) {
	t.Helper()
	codex, claude = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("ANTHROPIC_MODEL", "")
	return codex, claude
}

// relayServeLoop drains a runtime's loop when Run is not running, so tools
// that go through r.sync can be called directly.
func relayServeLoop(t *testing.T, rt *Runtime) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case fn := <-rt.loop:
				fn()
			case <-stop:
				return
			}
		}
	}()
}

// waitRelayStarts waits until no relay start is under way (they run off
// the loop goroutine and are recorded on it when done).
func waitRelayStarts(t *testing.T, rt *Runtime) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		busy := false
		rt.sync(func() {
			rt.relays.mu.Lock()
			busy = len(rt.relays.launching) > 0
			rt.relays.mu.Unlock()
		})
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a relay start never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Old: TestRelayModelPickedAtStartAndChangedBetweenTurns. A relay starts with
// the model and effort the session picked, says so in its label and the
// live settings; an effort the agent does not take is refused; a change
// made while it works waits for its turn to end, then starts it again on
// the same conversation; between turns a change applies at once; the same
// pick changes nothing.
func TestRelayModelPickedAtStartAndChangedBetweenTurns(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	codexHome, _ := relayAgentHomes(t)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-6.2-astra\"\nmodel_reasoning_effort = \"xhigh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex", RelayModel: "gpt-6.2-sol", RelayEffort: "high"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	if len(launchedSpecs(launched)) != 1 || launchedSpecs(launched)[0].Profile.Model != "gpt-6.2-sol" || launchedSpecs(launched)[0].Profile.Effort != "high" {
		t.Fatalf("launched %+v", launchedSpecs(launched))
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
	if err != nil || !strings.Contains(msg, "when the turn ends") || len(launchedSpecs(launched)) != 1 {
		t.Fatalf("repick mid-turn: %q %v, launched %d", msg, err, len(launchedSpecs(launched)))
	}
	if got := rt.activeSettings.Relays["codex"]; !got.Waiting || got.Model != "gpt-6.2-astra" || got.Effort != "" {
		t.Fatalf("waiting change not shown: %+v", got)
	}
	// The turn ends: Codex starts again, continuing its conversation, with
	// the new model and its own effort.
	relayServeLoop(t, rt)
	rt.sync(func() { rt.onRelayEvent("codex", relay.Event{Kind: relay.KindTurn, Text: "finished"}) })
	waitRelayStarts(t, rt)
	if len(launchedSpecs(launched)) != 2 || launchedSpecs(launched)[1].Profile.Model != "gpt-6.2-astra" || launchedSpecs(launched)[1].Profile.Effort != "" || !seat.wasKilled() {
		t.Fatalf("after the turn: launched %+v killed %v", launchedSpecs(launched), seat.wasKilled())
	}
	if rel := rt.st.Relays["codex"]; rel.Model != "gpt-6.2-astra" || rel.Effort != "" || rel.Label != "Codex · gpt-6.2-astra xhigh" {
		t.Fatalf("restarted relay %+v", rel)
	}
	if got := rt.activeSettings.Relays["codex"]; got.Waiting || got.Model != "gpt-6.2-astra" {
		t.Fatalf("live settings after restart %+v", got)
	}

	// Between turns a change applies at once; the same pick changes nothing.
	rt.sync(func() {
		rt.st.Relays["codex"].State = "idle"
		_, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"})
	})
	waitRelayStarts(t, rt)
	if err != nil || len(launchedSpecs(launched)) != 3 || launchedSpecs(launched)[2].Profile.Effort != "max" {
		t.Fatalf("idle repick: %v launched %d", err, len(launchedSpecs(launched)))
	}
	rt.sync(func() { msg, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"}) })
	if err != nil || !strings.Contains(msg, "already") || len(launchedSpecs(launched)) != 3 {
		t.Fatalf("unchanged repick: %q %v", msg, err)
	}
}

// Old: TestRelayControlModelKeepsTheOtherPick. relay_control model takes
// "default" for going back to the agent's own, and keeps the other pick.
func TestRelayControlModelKeepsTheOtherPick(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude", RelayModel: "opus", RelayEffort: "max"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	rt.sync(func() { rt.st.Relays["claude"].State = "idle" })
	args := map[string]any{"action": "model", "effort": "default"}
	out, isErr := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
	waitRelayStarts(t, rt)
	var specs []relay.Spec
	rt.sync(func() { specs = append(specs, launchedSpecs(launched)...) })
	if isErr || len(specs) != 2 || specs[1].Profile.Model != "opus" || specs[1].Profile.Effort != "" {
		t.Fatalf("%q, launched %+v", out, specs)
	}
}

// Old: TestModelCommand. /model changes the relay's model and effort from
// any composer, telling a model from an effort by what the agent takes;
// with no argument it says what runs and how to change it.
func TestModelCommand(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	ui := &fakeUI{}
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	launches := func() []relay.Spec {
		var out []relay.Spec
		rt.sync(func() { out = append(out, launchedSpecs(launched)...) })
		return out
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; said %q, launched %d", what, ui.said(), len(launches()))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	idle := func() { rt.sync(func() { rt.st.Relays["claude"].State = "idle" }) }
	idle()
	rt.sync(func() { rt.relayCommand([]string{"/model"}, nil) })
	wait("the usage line", func() bool { return strings.Contains(strings.Join(ui.said(), "|"), "/model <model> [effort]") })
	rt.sync(func() { rt.relayCommand([]string{"/model", "sonnet", "High"}, nil) })
	wait("the relaunch", func() bool { return len(launches()) == 2 })
	if p := launches()[1].Profile; p.Model != "sonnet" || p.Effort != "high" {
		t.Fatalf("relaunched with %+v", p)
	}
	idle()
	rt.sync(func() { rt.relayCommand([]string{"/model", "max"}, nil) })
	wait("the effort relaunch", func() bool { return len(launches()) == 3 })
	if p := launches()[2].Profile; p.Model != "sonnet" || p.Effort != "max" {
		t.Fatalf("effort alone relaunched with %+v", p)
	}
	idle()
	rt.sync(func() { rt.relayCommand([]string{"/model", "default"}, nil) })
	wait("the default relaunch", func() bool { return len(launches()) == 4 })
	if p := launches()[3].Profile; p.Model != "" || p.Effort != "max" {
		t.Fatalf("default model relaunched with %+v", p)
	}
}

// Old: TestRelayChangeFromLiveSettings. A change from the thread's settings
// goes through the live-settings controller: it waits for the turn, the
// published values say so, and the turn's end applies it.
func TestRelayChangeFromLiveSettings(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	seat := newFakeSeat("done")
	launched, _ := useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
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
	if got := c.Values.Relays["claude"]; !got.Waiting || got.Model != "sonnet" || len(launchedSpecs(launched)) != 1 {
		t.Fatalf("published %+v, launched %d", got, len(launchedSpecs(launched)))
	}
	relayServeLoop(t, rt)
	rt.sync(func() { rt.onRelayEvent("claude", relay.Event{Kind: relay.KindState, State: "idle"}) })
	waitRelayStarts(t, rt)
	rt.sync(func() { rt.pollRuntimeSettings() })
	if got := c.Values.Relays["claude"]; got.Waiting || len(launchedSpecs(launched)) != 2 || launchedSpecs(launched)[1].Profile.Model != "sonnet" {
		t.Fatalf("after the turn: published %+v, launched %+v", got, launchedSpecs(launched))
	}
}
