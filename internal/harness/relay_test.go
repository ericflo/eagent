package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// fakeSeat stands in for an agent in a terminal: whatever is sent to it is
// recorded, and it answers each message with one finished turn.
type fakeSeat struct {
	mu     sync.Mutex
	sent   []string
	modes  []string
	events chan relay.Event
	once   sync.Once
	killed bool
	reply  string
}

func newFakeSeat(reply string) *fakeSeat {
	return &fakeSeat{events: make(chan relay.Event, 64), reply: reply}
}

func (f *fakeSeat) Events() <-chan relay.Event { return f.events }
func (f *fakeSeat) Send(id, text, mode string) (string, error) {
	f.mu.Lock()
	f.sent = append(f.sent, text)
	f.modes = append(f.modes, mode)
	f.mu.Unlock()
	f.events <- relay.Event{Kind: relay.KindState, State: "working"}
	f.events <- relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}
	f.events <- relay.Event{Kind: relay.KindProgress, Text: "Listing the open PRs."}
	f.events <- relay.Event{Kind: relay.KindActivity, Text: "running gh pr list"}
	f.events <- relay.Event{Kind: relay.KindTurn, Text: f.reply, DurationMS: 4000, Tools: 3, Images: []string{"/w/shot.png"}}
	return "typed into Codex", nil
}
func (f *fakeSeat) Interrupt() error           { return nil }
func (f *fakeSeat) Keys(...string) error       { return nil }
func (f *fakeSeat) Screen(int) (string, error) { return "› Ask Codex to do anything", nil }
func (f *fakeSeat) Kill() error                { f.mu.Lock(); f.killed = true; f.mu.Unlock(); f.Close(); return nil }
func (f *fakeSeat) Close()                     { f.once.Do(func() { close(f.events) }) }
func (f *fakeSeat) Snapshot() relay.Status     { return relay.Status{State: "idle", Since: time.Now()} }
func (f *fakeSeat) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}
func (f *fakeSeat) wasKilled() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.killed }

func useFakeRelays(t *testing.T, seat *fakeSeat) (*[]relay.Spec, *[]string) {
	t.Helper()
	var launched []relay.Spec
	var attached []string
	oldLaunch, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		launched = append(launched, spec)
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		attached = append(attached, target+"@"+since.Format(time.RFC3339Nano))
		return seat, nil
	}
	t.Cleanup(func() { launchRelay, attachRelay = oldLaunch, oldAttach })
	return &launched, &attached
}

func TestRelaySessionForwardsVerbatimAndRefusesToDoTheWork(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	seat := newFakeSeat("Merged #42 and #44; main is green at c92b5bad.")
	launched, _ := useFakeRelays(t, seat)
	const brief = "Merge every pending PR, then do a deep dependency upgrade. On MAIN, no branches!!"
	var mu sync.Mutex
	var relayPrompt, narratorRelay, steerSeen bool
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch model {
		case "orch":
			mu.Lock()
			relayPrompt = relayPrompt || strings.Contains(all, "this session is a relay")
			steerSeen = steerSeen || strings.Contains(all, "You are the relay: Codex does the work") && strings.Contains(all, "already went to Codex verbatim")
			mu.Unlock()
			switch {
			case !strings.Contains(all, "Refused: this session is a relay"):
				return reply{calls: []event.ToolCall{tc("write_file", `{"path":"x.txt","content":"doing it myself"}`)}}
			case !strings.Contains(all, "already forwarded"):
				// The harness sent the brief on arrival; a second forward is refused.
				return reply{calls: []event.ToolCall{tc("relay_forward", `{}`)}}
			case !strings.Contains(all, "[Codex finished a turn"):
				return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting for Codex"}`)}}
			case !strings.Contains(all, "Stopped Codex"):
				return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"user is done"}`)}}
			default:
				return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"Codex merged the PRs"}`)}}
			}
		default:
			mu.Lock()
			narratorRelay = narratorRelay || strings.Contains(all, "## This session is a relay")
			mu.Unlock()
			if strings.Contains(all, "DECLARED THE WORK DONE") || strings.Contains(lastUserText(msgs), "final") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Codex merged #42 and #44; main is green at c92b5bad."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{"reason":"nothing yet"}`)}}
		}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: brief, Relay: "codex"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit code %d; logs: %v", code, ui.logs)
	}
	if got := seat.sentTexts(); len(got) != 1 || got[0] != brief {
		t.Fatalf("sent to the agent %q, want the brief verbatim", got)
	}
	if len(*launched) != 1 || (*launched)[0].Profile.Model != "gpt-6-astra" || (*launched)[0].Dir != rt.projectPath() {
		t.Fatalf("launched %+v", *launched)
	}
	if !seat.wasKilled() {
		t.Fatalf("relay_control stop did not end the agent")
	}
	mu.Lock()
	defer mu.Unlock()
	if !relayPrompt || !steerSeen || !narratorRelay {
		t.Fatalf("relay prompt=%v steer=%v narrator relay section=%v", relayPrompt, steerSeen, narratorRelay)
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(evs)
	rel := st.Relays["codex"]
	if !st.RelayMode() || rel == nil || rel.Turns != 1 || rel.State != "stopped" || len(rel.Undelivered) != 0 || rel.Sends != 1 {
		t.Fatalf("replayed relay = %+v", rel)
	}
	var send event.RelaySendData
	for _, ev := range evs {
		if ev.Type == event.RelaySend {
			_ = ev.Decode(&send)
		}
		if ev.Type == event.ToolResult {
			var d event.ToolResultData
			_ = ev.Decode(&d)
			if d.Name == "write_file" && !d.IsError {
				t.Fatalf("write_file was allowed in a relay: %s", d.Output)
			}
		}
	}
	if send.From != "user" || send.Seq != st.LastUserSeq || send.Text != brief || !send.Auto || send.Mode != "queue" {
		t.Fatalf("relay.send = %+v (last user seq %d)", send, st.LastUserSeq)
	}
	// The orchestrator was woken by the agent's turn, not by a poll.
	if len(st.ActiveSchedules()) != 0 || len(st.ScheduleOrder) != 0 {
		t.Fatalf("a relay should never need schedules: %v", st.ScheduleOrder)
	}
}

func TestRelayQuestionIsLeftToTheOrchestratorAndRemindedOnce(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Done.")
	useFakeRelays(t, seat)
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model != "orch" {
			if strings.Contains(lastUserText(msgs), "final") || strings.Contains(all, "DECLARED THE WORK DONE") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		switch {
		case !strings.Contains(all, "has not reached Codex"):
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"forgot"}`)}}
		case !strings.Contains(all, "noted as your answer"):
			return reply{calls: []event.ToolCall{tc("note", `{"text":"Codex is idle; nothing has been sent yet.","answer":true}`)}}
		case !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"ok"}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Prompt: "How's it going? Is it stuck?", Relay: "codex"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d: %v", code, ui.logs)
	}
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("a question about the relay was sent to the agent: %q", got)
	}
}

// TestRelayMessageReachesTheAgentWhileTheOrchestratorIsBusy is the point of
// forwarding on arrival: a message typed while the orchestrator is in the
// middle of a long model call reaches the agent at once, and "Stop" stops it.
func TestRelayMessageReachesTheAgentWhileTheOrchestratorIsBusy(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	release := make(chan struct{})
	brain := func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}, block: release}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	defer close(release)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Prompt: "Build the importer.", Relay: "codex", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go rt.Run(ctx)
	waitFor := func(n int) []string {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if got := seat.sentTexts(); len(got) >= n {
				return got
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("the agent got %q, want %d messages while the orchestrator's call is still blocked", seat.sentTexts(), n)
		return nil
	}
	waitFor(1)
	ui.input <- "STOP, wrong directory!"
	got := waitFor(2)
	if got[0] != "Build the importer." || got[1] != "STOP, wrong directory!" {
		t.Fatalf("sent %q", got)
	}
	seat.mu.Lock()
	modes := append([]string{}, seat.modes...)
	seat.mu.Unlock()
	if modes[1] != "interrupt" {
		t.Fatalf("an urgent message should interrupt, modes %v", modes)
	}
	cancel()
}

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

func TestRelayIsReattachedOnResume(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	seat := newFakeSeat("Working on it.")
	_, attached := useFakeRelays(t, seat)
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model != "orch" {
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		_ = all // the brief went to the agent on arrival
		return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: project, Prompt: "keep it running", Relay: "claude"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt.Run(ctx) // a batch relay session stays up until stopped; the deadline interrupts it
	if seat.wasKilled() {
		t.Fatalf("ending the eagent process must not kill the agent")
	}
	path := rt.sess.Path
	seat2 := newFakeSeat("again")
	_, attached = useFakeRelays(t, seat2)
	rt2, err := Resume(testConfig(s.srv.URL), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(*attached) != 1 || !strings.HasPrefix((*attached)[0], relay.Target(rt.sess.ID, "claude")+"@") || strings.HasSuffix((*attached)[0], "@0001-01-01T00:00:00Z") {
		t.Fatalf("attached %v", *attached)
	}
	rt2.sess.Close()
}

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

// useFakeRelayFactory gives every launch a fresh fake seat.
func useFakeRelayFactory(t *testing.T, reply string) *[]*fakeSeat {
	t.Helper()
	var seats []*fakeSeat
	var mu sync.Mutex
	oldLaunch := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		mu.Lock()
		defer mu.Unlock()
		s := newFakeSeat(reply)
		seats = append(seats, s)
		return s, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay = oldLaunch })
	return &seats
}

func TestRelayAutopilotNudgesAndSwitchHandsOver(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seats := useFakeRelayFactory(t, "Round done; three items left.")
	var mu sync.Mutex
	sawAutopilotSteer := false
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model != "orch" {
			if strings.Contains(lastUserText(msgs), "final") || strings.Contains(all, "DECLARED THE WORK DONE") {
				return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Done."}`)}}
			}
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		mu.Lock()
		sawAutopilotSteer = sawAutopilotSteer || strings.Contains(all, "Autopilot on for Codex until")
		mu.Unlock()
		switch {
		case !strings.Contains(all, "Autopilot on for Codex"):
			return reply{calls: []event.ToolCall{tc("relay_autopilot", `{"instruction":"keep polishing until it is excellent","until":"8h"}`)}}
		case !strings.Contains(all, "(r2)"): // r1 was the brief, forwarded on arrival
			return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"Keep going: the three items you listed, then commit."}`)}}
		case !strings.Contains(all, "Handed over from Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"switch","profile":"claude"}`)}}
		case !strings.Contains(all, "Stopped Claude"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"ok"}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Prompt: "Polish the onboarding flow. Keep going until I say stop!", Relay: "codex"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d: %v", code, ui.logs)
	}
	if len(*seats) != 2 {
		t.Fatalf("launched %d agents, want codex then claude", len(*seats))
	}
	codex, claude := (*seats)[0].sentTexts(), (*seats)[1].sentTexts()
	if len(codex) != 2 || codex[0] != "Polish the onboarding flow. Keep going until I say stop!" || !strings.HasPrefix(codex[1], "Keep going: the three items") {
		t.Fatalf("codex got %q", codex)
	}
	if len(claude) != 1 || !strings.Contains(claude[0], "taking over this work from Codex") || !strings.Contains(claude[0], "- Polish the onboarding flow.") || !strings.Contains(claude[0], "Round done; three items left.") {
		t.Fatalf("handoff brief %q", claude)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawAutopilotSteer {
		t.Fatalf("the steer never carried the autopilot")
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if st.Relays["codex"].State != "stopped" || st.Relays["claude"].State != "stopped" {
		t.Fatalf("relays %+v %+v", st.Relays["codex"], st.Relays["claude"])
	}
}

func TestRelayAutopilotDeadlineWakesTheOrchestrator(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelayFactory(t, "ok")
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model != "orch" {
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		switch {
		case !strings.Contains(all, "Autopilot on for Codex"):
			return reply{calls: []event.ToolCall{tc("relay_autopilot", `{"until":"2s"}`)}}
		case !strings.Contains(all, "[Autopilot for Codex ended"):
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}}
		case !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"ok"}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Prompt: "work", Relay: "codex"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		t.Fatalf("exit %d (the deadline never woke it?): %v", code, ui.logs)
	}
}

func TestRelayExplicitRouteFromTheClient(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	release := make(chan struct{})
	defer close(release)
	brain := func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}, block: release}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	ui := &fakeUI{input: make(chan string, 1)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go rt.Run(ctx)
	post := func(m InboxMessage) {
		done := make(chan struct{})
		rt.post(func() { rt.handleInbox(m); close(done) })
		<-done
	}
	// "How's it going?" would stay with eagent; the composer said "to Codex".
	post(InboxMessage{Type: "message", Text: "How's it going?", From: "finalechat", Route: "agent", Delivery: "after_turn"})
	// A work-looking message the composer addressed to eagent stays here.
	post(InboxMessage{Type: "message", Text: "Clean up docker images", From: "finalechat", Route: "eagent"})
	deadline := time.Now().Add(5 * time.Second)
	for len(seat.sentTexts()) < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	got := seat.sentTexts()
	seat.mu.Lock()
	modes := append([]string{}, seat.modes...)
	seat.mu.Unlock()
	if len(got) != 1 || got[0] != "How's it going?" || modes[0] != "after_turn" {
		t.Fatalf("sent %q modes %v", got, modes)
	}
	cancel()
}

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
