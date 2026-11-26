package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// Ports of the old relay_test.go. Each test names the old one it stands
// for and what that one protected.

// Old: TestRelaySessionForwardsVerbatimAndRefusesToDoTheWork. The brief goes
// to the agent verbatim, once (a second forward of it is refused), the agent
// is launched in the project with its own configured model, stopping it with
// relay_control ends it, and the replayed record agrees: one send, one turn,
// nothing undelivered, stopped, and no timers needed. (The refusal of
// write_file is gone with eagent's own file tools; the relay prompt text
// checks are gone with the old prompt files.)
func TestRelayBriefGoesOnceAndStopEndsTheAgent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	seat := newFakeSeat("Merged #42 and #44; main is green at c92b5bad.")
	launched, _ := useFakeRelays(t, seat)
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now"):
			return say("Codex merged #42 and #44; main is green at c92b5bad.")
		case !strings.Contains(all, "finished a turn"):
			return hold()
		case !strings.Contains(all, "already went to Codex as written"):
			return reply{calls: []event.ToolCall{tc("relay_forward", `{}`)}}
		case !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"user is done"}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
	rt, ui := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: project, Prompt: brief, Relay: "codex"})
	if got := seat.sentTexts(); len(got) != 1 || got[0] != brief {
		t.Fatalf("sent to the agent %q, want the brief verbatim once", got)
	}
	if len(launchedSpecs(launched)) != 1 || launchedSpecs(launched)[0].Profile.Model != "" || launchedSpecs(launched)[0].Dir != rt.projectPath() {
		t.Fatalf("launched %+v", launchedSpecs(launched))
	}
	if !seat.wasKilled() {
		t.Fatal("relay_control stop did not end the agent")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(evs)
	rel := st.Relays["codex"]
	if rel == nil || rel.Turns != 1 || rel.State != "stopped" || len(rel.Undelivered) != 0 || rel.Sends != 1 {
		t.Fatalf("replayed relay = %+v", rel)
	}
	var send event.RelaySendData
	refused := false
	for _, ev := range evs {
		switch ev.Type {
		case event.RelaySend:
			_ = ev.Decode(&send)
		case event.ToolResult:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			if d.Name == "relay_forward" {
				refused = d.IsError && strings.Contains(d.Output, "already went to Codex as written")
			}
		}
	}
	if !refused {
		t.Fatal("a second forward of the brief was not refused")
	}
	if send.From != "user" || send.Seq != st.LastUserSeq || send.Text != brief || !send.Auto || send.Mode != "queue" {
		t.Fatalf("relay.send = %+v (last user seq %d)", send, st.LastUserSeq)
	}
	if len(st.ActiveSchedules()) != 0 || len(st.ScheduleOrder) != 0 {
		t.Fatalf("a relay should never need timers: %v", st.ScheduleOrder)
	}
	if said := ui.said(); len(said) != 1 || !strings.Contains(said[0], "c92b5bad") {
		t.Fatalf("said %q", said)
	}
}

// Old: TestRelayQuestionIsLeftToTheOrchestratorAndRemindedOnce. A question
// about the session as the opening message is not typed into the agent; it
// stays with eagent, which answers it. (The old reminder named the gone
// `note` tool; the reminder that a kept message is owed an answer is
// kept_test.go:TestAMessageKeptForEagentIsNeverLeftUnanswered.)
func TestRelayOpeningQuestionAboutTheSessionStaysWithEagent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Done.")
	useFakeRelays(t, seat)
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "kept for you") && !strings.Contains(all, "nothing has been sent yet"):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Codex is idle; nothing has been sent yet."}`),
				tc("relay_control", `{"action":"stop"}`),
			}}
		}
		return hold()
	})
	defer model.srv.Close()
	rt, ui := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "How's it going? Is it stuck?", Relay: "codex"})
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("a question about the relay was sent to the agent: %q", got)
	}
	evs, _ := store.Read(rt.sess.Path)
	var d event.UserMessageData
	for _, ev := range evs {
		if ev.Type == event.UserMessage {
			_ = ev.Decode(&d)
		}
	}
	if d.Held != "it asks about the session" {
		t.Fatalf("held = %q", d.Held)
	}
	if said := ui.said(); len(said) == 0 || said[0] != "Codex is idle; nothing has been sent yet." {
		t.Fatalf("said %q", said)
	}
}

// Old: TestRelayMessageReachesTheAgentWhileTheOrchestratorIsBusy. A message
// typed while eagent's model is in the middle of a long call reaches the
// agent at once, and "STOP, ..." interrupts it.
func TestRelayMessageReachesTheAgentWhileTheModelIsBusy(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := startSession(t, "Build the importer.", func(string, []map[string]any) reply {
		r := hold()
		r.block = release
		return r
	}, nil)
	s.waitFor("the brief", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.waitFor("a model call in flight", func() bool { return s.model.count(testModel) > 0 })
	s.type_("STOP, wrong directory!")
	s.waitFor("the second message while the call is still blocked", func() bool { return len(s.seat.sentTexts()) == 2 })
	got, modes := s.seat.sentTexts(), s.seat.sentModes()
	if got[0] != "Build the importer." || got[1] != "STOP, wrong directory!" {
		t.Fatalf("sent %q", got)
	}
	if modes[1] != "interrupt" {
		t.Fatalf("an urgent message should interrupt, modes %v", modes)
	}
}

// Old: TestRelayRouting. Which short messages are about the session, and
// which should interrupt the agent.
func TestRelayRouting(t *testing.T) {
	for text, want := range map[string][2]bool{ // meta, urgent
		"How's it going?": {true, false},
		"How far along are we now, in your estimation?": {true, false},
		"Upgrade and then restart the session plz":      {true, false},
		"Give me updates every 5 mins":                  {true, false},
		"Stop":                                          {false, true},
		"Wait, don't push that":                         {false, true},
		"Tell codex to fix the mobile header":           {false, false},
		"Why is the build so slow? Make it under 10 minutes and keep it there, that is non-negotiable. " + strings.Repeat("Details. ", 30): {false, false},
	} {
		if got := [2]bool{relayMeta(text), relayUrgent(text)}; got != want {
			t.Errorf("%q: meta,urgent = %v, want %v", text, got, want)
		}
	}
}

// Old: TestRelayIsReattachedOnResume. Ending the eagent process leaves the
// agent running, and a resumed session reattaches to its terminal from
// where the record left off.
func TestRelayIsReattachedOnResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	seat := newFakeSeat("Working on it.")
	useFakeRelays(t, seat)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	defer model.srv.Close()
	rt, err := New(testConfig(model.srv.URL), Options{Project: project, Prompt: "keep it running", Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, "the agent's turn", func() bool {
		n := 0
		rt.sync(func() { n = rt.st.Relays["claude"].Turns })
		return n == 1
	})
	cancel() // a batch session with its agent running stays up until stopped
	<-done
	if seat.wasKilled() {
		t.Fatal("ending the eagent process must not kill the agent")
	}
	seat2 := newFakeSeat("again")
	_, attached := useFakeRelays(t, seat2)
	rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project}, &fakeUI{}, rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	if len(attachedTargets(attached)) != 1 || !strings.HasPrefix(attachedTargets(attached)[0], relay.Target(rt.sess.ID, "claude")+"@") || strings.HasSuffix(attachedTargets(attached)[0], "@0001-01-01T00:00:00Z") {
		t.Fatalf("attached %v", attachedTargets(attached))
	}
}

// Old: TestAddressedToEagent.
func TestAddressedToEagent(t *testing.T) {
	for text, want := range map[string]bool{
		"@e how much disk is free?":                    true,
		"eagent: clean up docker images":               true,
		"(Not for relay to codex) give me an overview": true,
		"This can be you and not relayed":              true,
		"Tell codex to fix the header":                 false,
		"How's it going?":                              false,
		"Have claude use the eagle images":             false,
	} {
		if got := addressedToEagent(text); got != want {
			t.Errorf("addressedToEagent(%q) = %v, want %v", text, got, want)
		}
	}
}

// Old: TestRelayAutopilotNudgesAndSwitchHandsOver. With autopilot on, eagent
// sends the agent on; the standing order is in what eagent reads at the
// next turn end; a switch stops Codex and gives Claude a handoff brief with
// the user's words verbatim and Codex's last report; both end stopped.
func TestRelayAutopilotNudgesAndSwitchHandsOver(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	f := useRelayFactory(t, "Round done; three items left.")
	var seen tails
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		seen.add(tail)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now"):
			return say("Done.")
		case !strings.Contains(tail, "finished a turn"):
			return hold()
		case !strings.Contains(all, "Autopilot on for Codex"):
			return reply{calls: []event.ToolCall{
				tc("relay_autopilot", `{"instruction":"keep polishing until it is excellent","until":"8h"}`),
				tc("relay_send", `{"text":"Keep going: the three items you listed, then commit."}`),
			}}
		case !strings.Contains(all, "Handed over from Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"switch","profile":"claude"}`)}}
		case !strings.Contains(all, "Stopped Claude"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
	rt, _ := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "Polish the onboarding flow. Keep going until I say stop!", Relay: "codex"})
	seats := f.all()
	if len(seats) != 2 {
		t.Fatalf("launched %d agents, want codex then claude", len(seats))
	}
	codex, claude := seats[0].sentTexts(), seats[1].sentTexts()
	if len(codex) != 2 || codex[0] != "Polish the onboarding flow. Keep going until I say stop!" || !strings.HasPrefix(codex[1], "Keep going: the three items") {
		t.Fatalf("codex got %q", codex)
	}
	if len(claude) != 1 || !strings.Contains(claude[0], "taking over this work from Codex") || !strings.Contains(claude[0], "- Polish the onboarding flow.") || !strings.Contains(claude[0], "Round done; three items left.") {
		t.Fatalf("handoff brief %q", claude)
	}
	if seen.find("The standing order: keep polishing until it is excellent") == "" {
		t.Fatal("the turn end after the nudge did not carry the autopilot's standing order")
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if st.Relays["codex"].State != "stopped" || st.Relays["claude"].State != "stopped" {
		t.Fatalf("relays %+v %+v", st.Relays["codex"], st.Relays["claude"])
	}
}

// Old: TestRelayAutopilotLeavesAWaitingAgentAlone. An agent on autopilot that
// answers eagent's nudge without running anything is waiting on its own
// work: the reply is marked idle and eagent is told so; the idle check
// wakes eagent later to look.
func TestRelayAutopilotLeavesAWaitingAgentAlone(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := autopilotCheckAfter
	autopilotCheckAfter = 500 * time.Millisecond
	t.Cleanup(func() { autopilotCheckAfter = old })
	seat := newFakeSeat("Holding on `task-7` (next check 14:05).")
	seat.waiting = true
	useFakeRelays(t, seat)
	var mu sync.Mutex
	sawHint := false
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now"):
			return say("Done.")
		case !strings.Contains(all, "Autopilot on for Codex") && strings.Contains(tail, "finished a turn"):
			return reply{calls: []event.ToolCall{
				tc("relay_autopilot", `{"instruction":"render the film, then publish"}`),
				tc("relay_send", `{"text":"Keep going: render, then publish."}`),
			}}
		case strings.Contains(all, "[Autopilot check: Codex has been idle") && !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		mu.Lock()
		sawHint = sawHint || strings.Contains(all, "[It ran nothing in reply to what you sent.")
		mu.Unlock()
		return hold()
	})
	defer model.srv.Close()
	rt, _ := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "Render the film overnight. Keep going until I say stop!", Relay: "codex"})
	if got := seat.sentTexts(); len(got) != 2 {
		t.Fatalf("codex got %q, want the brief and one nudge", got)
	}
	evs, _ := store.Read(rt.sess.Path)
	var idle []bool
	checks := 0
	for _, ev := range evs {
		switch ev.Type {
		case event.RelayTurn:
			var d event.RelayTurnData
			_ = ev.Decode(&d)
			idle = append(idle, d.Idle)
		case event.HarnessMessage:
			var d event.HarnessMessageData
			_ = ev.Decode(&d)
			if strings.HasPrefix(d.Text, "[Autopilot check: Codex has been idle since its last turn") {
				checks++
			}
		}
	}
	// The reply to the user's brief is not idle (the user sent it); the
	// reply to eagent's own nudge is.
	if len(idle) != 2 || idle[0] || !idle[1] {
		t.Fatalf("turns idle=%v, want [false true]", idle)
	}
	if checks == 0 {
		t.Fatal("no idle check")
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawHint {
		t.Fatal("eagent never heard the agent was waiting")
	}
}

// Old: TestRelayAutopilotDeadlineWakesTheOrchestrator. When an autopilot's
// deadline passes eagent is woken and told it ended.
func TestRelayAutopilotDeadlineWakesEagent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	// The deadline is kept to the second, so "1ms" has passed when it is
	// armed, and the autopilot ends after this floor.
	setFor(t, &autopilotEndFloor, 50*time.Millisecond)
	useRelayFactory(t, "ok")
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now"):
			return say("Done.")
		case !strings.Contains(all, "Autopilot on for Codex"):
			return reply{calls: []event.ToolCall{tc("relay_autopilot", `{"until":"1ms"}`)}}
		case strings.Contains(all, "[Autopilot for Codex ended") && !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
	rt, _ := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "work", Relay: "codex"})
	evs, _ := store.Read(rt.sess.Path)
	off := false
	for _, ev := range evs {
		if ev.Type == event.RelayAutopilot {
			var d event.RelayAutopilotData
			_ = ev.Decode(&d)
			off = d.Off
		}
	}
	if !off {
		t.Fatal("the autopilot was not turned off at its deadline")
	}
}

// Old: TestRelayExplicitRouteFromTheClient. What the composer chose wins
// over what the text suggests: "How's it going?" addressed to the agent
// goes to it, held for its turn's end; work addressed to eagent stays.
func TestRelayExplicitRouteFromTheClient(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	post := func(m InboxMessage) { s.rt.sync(func() { s.rt.handleInbox(m) }) }
	post(InboxMessage{Type: "message", Text: "How's it going?", From: "finalechat", Route: "agent", Delivery: "after_turn"})
	post(InboxMessage{Type: "message", Text: "Clean up docker images", From: "finalechat", Route: "eagent"})
	s.waitFor("the forward", func() bool { return len(s.seat.sentTexts()) >= 1 })
	time.Sleep(200 * time.Millisecond)
	got, modes := s.seat.sentTexts(), s.seat.sentModes()
	if len(got) != 1 || got[0] != "How's it going?" || modes[0] != "after_turn" {
		t.Fatalf("sent %q modes %v", got, modes)
	}
	var held []string
	for _, ev := range s.events(event.UserMessage) {
		var d event.UserMessageData
		_ = ev.Decode(&d)
		held = append(held, d.Held)
	}
	if strings.Join(held, "|") != "|the user addressed it to you" {
		t.Fatalf("held = %q", held)
	}
}

// Old: TestParseUntil.
func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 10, 28, 22, 30, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"10:00":            time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local),
		"23:00":            time.Date(2026, 10, 28, 23, 0, 0, 0, time.Local),
		"8h":               now.Add(8 * time.Hour),
		"2026-10-30T09:15": time.Date(2026, 10, 30, 9, 15, 0, 0, time.Local),
	} {
		got, err := parseUntil(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseUntil(%q) = %v %v, want %v", in, got, err, want)
		}
	}
	if _, err := parseUntil("whenever", now); err == nil {
		t.Errorf("nonsense should not parse")
	}
}

// Old: TestRelaySlashCommandsActWithoutTheOrchestrator. While the model's
// call is blocked, /interrupt, /autopilot and an old client's /updates are
// acted on and acknowledged in the harness's words; the quiet limit stays;
// the agent's own command (/goal) goes to it verbatim.
func TestRelaySlashCommandsActWithoutTheModel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := startSession(t, "", func(string, []map[string]any) reply {
		r := hold()
		r.block = release
		return r
	}, nil)
	s.type_("/interrupt")
	s.type_("/autopilot until 10:00 polish the onboarding until it shines")
	s.type_("/updates off") // from an older client: answered, no longer a setting
	s.type_("/goal ship the onboarding polish")
	relaySaid(t, s.ui, "Interrupted Codex.")
	relaySaid(t, s.ui, "Autopilot on for Codex until")
	relaySaid(t, s.ui, "Updates are not a setting any more")
	s.seat.mu.Lock()
	n := s.seat.interrupts
	s.seat.mu.Unlock()
	var ap *event.RelayAutopilotData
	var quiet time.Duration
	s.rt.sync(func() {
		ap = s.rt.st.Relays["codex"].Autopilot
		quiet = s.rt.quietLimit()
	})
	if n != 1 || ap == nil || ap.Instruction != "polish the onboarding until it shines" || ap.Until == "" || quiet != 180*time.Second {
		t.Fatalf("interrupts=%d autopilot=%+v quiet=%v", n, ap, quiet)
	}
	s.waitFor("the agent's own command", func() bool { return len(s.seat.sentTexts()) >= 1 })
	if got := s.seat.sentTexts(); len(got) != 1 || got[0] != "/goal ship the onboarding polish" {
		t.Fatalf("sent to the agent %q", got)
	}
}

// Old: TestLiveRelaySession. A whole session against the real Claude Code
// CLI (Haiku, low effort) on a private tmux socket: the brief is forwarded,
// Claude's answer comes back as a turn eagent reads, and stopping the agent
// ends the session. Spends a cent of usage, so only with EAGENT_LIVE_RELAY=1
// and EAGENT_REAL_HOME; never part of the hermetic suite.
func TestLiveRelaySession(t *testing.T) {
	if os.Getenv("EAGENT_LIVE_RELAY") != "1" {
		t.Skip("set EAGENT_LIVE_RELAY=1 to run against the real Claude Code CLI")
	}
	real := os.Getenv("EAGENT_REAL_HOME")
	if real == "" {
		t.Skip("set EAGENT_REAL_HOME to the home directory holding the Claude Code login")
	}
	t.Setenv("HOME", real) // the hermetic HOME would hide the Claude login
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := relay.Socket
	relay.Socket = fmt.Sprintf("eagent-live-%d", os.Getpid())
	oldLaunch := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		s, started, err := relay.Launch(spec, innerID, resume)
		if err != nil {
			return nil, started, err
		}
		return s, started, nil
	}
	defer func() {
		launchRelay = oldLaunch
		relay.KillSocket(relay.Socket)
		relay.Socket = old
	}()
	var mu sync.Mutex
	var turnText string
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now"):
			return say("Done.")
		case strings.Contains(all, "Claude finished a turn") && !strings.Contains(all, "Stopped Claude"):
			i := strings.Index(all, "Claude finished a turn")
			mu.Lock()
			turnText = all[i:min(len(all), i+300)]
			mu.Unlock()
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
	cfg := testConfig(model.srv.URL)
	cfg.Relay.Profiles = map[string]config.RelayProfile{"haiku": {Agent: "claude", Model: "claude-haiku-5-5", Effort: "low", Args: []string{"--dangerously-skip-permissions"}, Host: "tmux"}}
	rt, _ := relayRunBatch(t, cfg, Options{Project: t.TempDir(), Prompt: "Reply with exactly the word PELICAN and nothing else.", Relay: "haiku"})
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(turnText, "PELICAN") {
		t.Fatalf("eagent saw %q", turnText)
	}
	evs, _ := store.Read(rt.sess.Path)
	rel := state.Replay(evs).Relays["haiku"]
	if rel == nil || rel.Turns < 1 || len(rel.Undelivered) != 0 || rel.InnerID == "" || rel.Transcript == "" {
		t.Fatalf("relay state %+v", rel)
	}
}

// Old: TestRelayCommandFromAPhoneUsesItsTimeZoneAndAnyCase. "/Autopilot
// until 10:00" from a phone in Manila is the command, and 10:00 is Manila's.
func TestRelayCommandFromAPhoneUsesItsTimeZoneAndAnyCase(t *testing.T) {
	manila, err := time.LoadLocation("Asia/Manila")
	if err != nil {
		t.Skip("no tz database")
	}
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.rt.sync(func() {
		s.rt.handleInbox(InboxMessage{Type: "message", Text: "/Autopilot until 10:00 finish the importer", From: "finalechat", Client: &clientcaps.Caps{Source: "finalechat", Timezone: "Asia/Manila"}})
	})
	var ap *event.RelayAutopilotData
	s.waitFor("the autopilot", func() bool {
		s.rt.sync(func() { ap = s.rt.st.Relays["codex"].Autopilot })
		return ap != nil
	})
	until, _ := time.Parse(time.RFC3339, ap.Until)
	if got := until.In(manila); got.Hour() != 10 || got.Minute() != 0 || ap.Instruction != "finish the importer" {
		t.Fatalf("until %s in Manila = %s, instruction %q", ap.Until, got, ap.Instruction)
	}
	if got := s.seat.sentTexts(); len(got) != 0 {
		t.Fatalf("the command was sent to the agent: %q", got)
	}
}

// Old: TestRelayThreadOnThePhoneGetsReceiptsReportsAndScreens. On the phone:
// a message typed into the agent gets a "delivered" receipt; each turn gets
// the agent's own report, quiet, unless /notify-done asked for a buzz; a
// status question gets an "eagent" receipt; /screen posts the terminal; and
// commands and the status question never reach the agent.
func TestRelayThreadOnThePhoneGetsReceiptsReportsAndScreens(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Fixed the flaky login test; main is green.")
	useFakeRelays(t, seat)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close) // after the session has stopped: its long poll holds a request
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	rt, err := New(relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	lastReceipt := func(id string) string {
		got := ""
		for _, m := range fp.metaPatches() {
			if rr, ok := m["relay_receipts"].(map[string]any); ok {
				if v, ok := rr[id].(string); ok {
					got = v
				}
			}
		}
		return got
	}
	reports := func() []map[string]any {
		return fp.postsWhere(func(p map[string]any) bool {
			meta, _ := p["meta"].(map[string]any)
			return meta["kind"] == "relay_report"
		})
	}
	// Replies sent before the mirror's first poll predate the session.
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	fp.userReply("Fix the flaky login test")
	first := fp.lastMessageID()
	waitFor(t, "a delivered receipt", func() bool { return lastReceipt(first) == "delivered" })
	waitFor(t, "the agent's report", func() bool { return len(reports()) == 1 })
	if r := reports()[0]; r["body"] != "Fixed the flaky login test; main is green." || r["importance"] == "important" {
		t.Fatalf("report %v", r)
	}
	fp.userReply("/notify-done")
	waitFor(t, "notify_done in the relay meta", func() bool {
		for _, m := range fp.metaPatches() {
			if rel, ok := m["relay"].(map[string]any); ok && rel["notify_done"] == true {
				return true
			}
		}
		return false
	})
	fp.userReply("Also update the docs")
	waitFor(t, "a second report", func() bool { return len(reports()) == 2 })
	if r := reports()[1]; r["importance"] != "important" {
		t.Fatalf("the report after /notify-done should buzz: %v", r)
	}
	fp.userReply("How's it going?")
	kept := fp.lastMessageID()
	waitFor(t, "a kept-for-eagent receipt", func() bool { return lastReceipt(kept) == "eagent" })
	fp.userReply("/screen")
	waitFor(t, "a screen card", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool {
			meta, _ := p["meta"].(map[string]any)
			body, _ := p["body"].(string)
			return meta["kind"] == "relay_screen" && strings.Contains(body, "Ask Codex to do anything")
		})) == 1
	})
	if got := seat.sentTexts(); len(got) != 2 {
		t.Fatalf("the agent got %q (commands and the status question stay with eagent)", got)
	}
}

// Old: TestRelayStartReadsTheHomeFolder. "Start codex in
// ~/Development/snowdown": the folder is read the way the user wrote it,
// not as a "~" directory inside the working directory.
func TestRelayStartReadsTheHomeFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, "Development", "snowdown")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex","dir":"~/Development/snowdown"}`)}}
		}
		return hold()
	}, nil)
	launched, _ := useFakeRelays(t, s.seat) // from here on: the session's first agent is already up
	s.type_("@e start codex in ~/Development/snowdown")
	s.waitFor("the second agent", func() bool {
		n := 0
		s.rt.sync(func() { n = len(launchedSpecs(launched)) })
		return n == 1
	})
	var dir string
	s.rt.sync(func() { dir = launchedSpecs(launched)[0].Dir })
	if dir != want {
		t.Fatalf("launched in %s, want %s", dir, want)
	}
}

// Old: TestRelaySwitchCarriesModelAndEffort. /switch takes the new agent's
// model and effort, the app's header data says what the running agent runs,
// and a pick the new agent cannot take leaves the running one alone.
func TestRelaySwitchCarriesModelAndEffort(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := useRelayFactory(t, "ok")
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	// An effort Claude does not take: Codex keeps running.
	ui.input <- "/switch claude opus ludicrous"
	relaySaid(t, ui, "Claude Code takes reasoning effort")
	if seats := f.all(); len(seats) != 1 || seats[0].wasKilled() {
		t.Fatalf("a refused switch touched the running agent (%d launched)", len(seats))
	}
	ui.input <- `/switch claude opus high`
	waitFor(t, "Claude to take over", func() bool {
		ok := false
		rt.sync(func() {
			rel := rt.st.Relays["claude"]
			ok = rel != nil && rel.Active() && rel.Model == "opus" && rel.Effort == "high"
		})
		return ok
	})
	specs := f.launched()
	if got := specs[len(specs)-1].Profile; got.Model != "opus" || got.Effort != "high" {
		t.Fatalf("launched with %+v", got)
	}
	if !f.all()[0].wasKilled() {
		t.Fatal("Codex was not stopped by the switch")
	}
	var meta map[string]any
	rt.sync(func() { meta, _ = rt.relayThreadMeta().(map[string]any) })
	if meta["profile"] != "claude" || meta["model"] != "opus" || meta["effort"] != "high" || meta["updates"] != nil || meta["pending"] != nil {
		t.Fatalf("thread header data %+v", meta)
	}
	if alt, _ := meta["switch_profiles"].([]string); len(alt) == 0 || slices.Contains(alt, "claude") || !slices.Contains(alt, "codex") {
		t.Fatalf("switch_profiles %v", meta["switch_profiles"])
	}
}

// Old: TestSplitArgsKeepsQuotedModelNames.
func TestSplitArgsKeepsQuotedModelNames(t *testing.T) {
	got := splitArgs(`/switch antigravity "Gemini 3.1 Pro" high`)
	if strings.Join(got, "|") != "/switch|antigravity|Gemini 3.1 Pro|high" {
		t.Fatalf("%q", got)
	}
	params := map[string]any{}
	modelAndEffort(params, got[2:], []string{"low", "medium", "high"})
	if params["model"] != "Gemini 3.1 Pro" || params["effort"] != "high" {
		t.Fatalf("%v", params)
	}
	params = map[string]any{}
	modelAndEffort(params, []string{"default", "default"}, []string{"low", "high"})
	if params["model"] != "default" || params["effort"] != "default" {
		t.Fatalf("%v", params)
	}
	if got := splitArgs("/model   opus\tmax "); strings.Join(got, "|") != "/model|opus|max" {
		t.Fatalf("%q", got)
	}
}
