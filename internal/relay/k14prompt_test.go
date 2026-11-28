package relay

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An agent's prompt is known by its words at the foot of the screen, where
// the real programs draw it (the screens of dialog_test.go).
func TestK14EachPromptIsSeenAsTheRealProgramDrawsIt(t *testing.T) {
	t.Parallel()
	output := []string{"● Bash(make build)", "  ⎿  ok", "", "● Built."}
	// A long message pasted into the box: the box grows upward, and what
	// the prompt is known by stays under it.
	tall := func(first, rest string) []string {
		lines := []string{first + "line 1 of a long message"}
		for i := 2; i <= 60; i++ {
			lines = append(lines, fmt.Sprintf("%sline %d", rest, i))
		}
		return lines
	}
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
		{"Claude Code, sixty lines in its input box", claudeAgent{}, shown(output, "", append(append([]string{"", realRule}, tall("❯ ", "  ")...), realRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"))},
		{"Codex, sixty lines in its composer", codexAgent{}, shown(output, "", append(append([]string{""}, tall("› ", "  ")...), "", "  98% context left · ? for shortcuts"))},
		{"Antigravity, sixty lines in its input box", antigravityAgent{}, shown(output, "", append(append([]string{"", realRule}, tall("> ", "  ")...), realRule, "Antigravity relay · idle"))},
		{"Claude Code, fresh", claudeAgent{}, shown(nil, "", claudePromptLines)},
		{"Claude Code under its output", claudeAgent{}, shown(output, "", claudePromptLines)},
		{"Claude Code in a turn", claudeAgent{}, shown(output, "", []string{"", "✻ Slithering… (2s · ↓ 118 tokens · thinking)", "", realRule, "❯ ", realRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"})},
		{"Claude Code with lines under its mode line", claudeAgent{}, shown(output, "", append(append([]string{}, claudePromptLines...), "  main · 3 files changed", "  1 shell running", "  ctrl+t to hide tasks", "  /status for more"))},
		{"Claude Code without bypass", claudeAgent{}, shown(output, "", []string{"", realRule, "❯ ", realRule, "  ? for shortcuts"})},
		{"Codex, fresh", codexAgent{}, shown(nil, "", codexPromptLines)},
		{"Codex under its output", codexAgent{}, shown(output, "", codexPromptLines)},
		{"Codex holding text", codexAgent{}, shown(output, "", []string{"", "› run the tests", "", "  98% context left · ? for shortcuts"})},
		{"Antigravity, fresh", antigravityAgent{}, shown(nil, "", agyPromptLines)},
		{"Antigravity under its output", antigravityAgent{}, shown(output, "", agyPromptLines)},
		{"pi, the key hints at the top of a fresh screen", piAgent{}, shown([]string{" █▀ █ escape interrupt · ctrl+c/ctrl+d clear/exit", "", "", "", "", "", "", "", ""}, "", []string{realRule, realRule, "/home/u/project"})},
	} {
		if !c.a.ready(c.screen) {
			t.Errorf("%s: its prompt is not seen", c.what)
		}
	}
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
		{"Claude Code's trust dialog", claudeAgent{}, claudeTrustScreen},
		{"Claude Code's bypass dialog", claudeAgent{}, claudeBypassScreen},
		{"Codex's trust dialog", codexAgent{}, codexTrustScreen},
		{"Codex's update prompt", codexAgent{}, codexUpdateScreen},
		{"Antigravity's trust dialog over its status line", antigravityAgent{}, agyTrustScreen},
	} {
		if c.a.ready(c.screen) {
			t.Errorf("%s is taken for the prompt", c.what)
		}
	}
}

// The prompt's words in an agent's output are not its prompt. A working
// agent went quiet for ten minutes on a dialog the seat does not know and
// is blocked (hung); a send waits for the agent's own prompt. With the
// prompt's words in the output above the dialog (the agent works on
// eagent's own source, which names them), the screen was called the prompt:
// the seat went idle, the send was typed into the dialog, Enter answered
// it for the user, and the message was lost. Antigravity draws its status
// line under its dialogs, so its screen was called the prompt whatever the
// output held. The seat stays blocked and the send is refused, as for the
// same dialog with no such words above it (the second case).
//
// Only the layouts are the real programs' (a dialog takes the place of
// Claude Code's and Codex's prompt; Antigravity's status line is drawn
// under its dialog's hint). The dialogs' own words are not captures.
func TestK14ThePromptsWordsInTheOutputAreNotThePrompt(t *testing.T) {
	savedSettle, savedPause := typeSettle, dialogPause
	typeSettle, dialogPause = 0, 0
	t.Cleanup(func() { typeSettle, dialogPause = savedSettle, savedPause })
	claudeDialog := []string{"", realRule, " Which environment should I deploy to?", "", " ❯ 1. staging", "   2. production", "   3. Type something.", "", " Enter to select · ↑/↓ to navigate · Esc to cancel"}
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
		{"Claude Code", claudeAgent{}, shown([]string{
			"● Bash(rg -n 'for shortcuts' internal/relay)",
			"  ⎿  internal/relay/claude.go:36:\treturn strings.Contains(f, \"bypass permissions on\") || strings.Contains(f, \"for shortcuts\")",
		}, "", claudeDialog)},
		{"Claude Code, no words in the output", claudeAgent{}, shown([]string{"● Bash(make build)", "  ⎿  ok"}, "", claudeDialog)},
		{"Claude Code, a capture of its prompt in the output", claudeAgent{}, shown(append([]string{"● Bash(cat prompt.txt)"}, claudePromptLines...), "     ", claudeDialog)},
		{"Antigravity", antigravityAgent{}, strings.Join([]string{"Run command?", "", "make deploy", "", "> Yes", "  No", "", "  ↑/↓ Navigate · enter Confirm", "Antigravity relay · idle"}, "\n") + "\n"},
		{"Codex", codexAgent{}, shown([]string{
			"• Ran rg -n 'Ask Codex' internal/relay",
			"  └ internal/relay/codex.go:38:\treturn strings.Contains(f, \"Ask Codex to do anything\") || strings.Contains(f, \"% context left\")",
		}, "", []string{"", "  Would you like to run the following command?", "", "  $ make deploy", "", "› 1. Yes, proceed (y)", "  2. No, and tell Codex what to do differently (esc)", "", "  Press enter to confirm or esc to cancel"})},
	} {
		if c.a.ready(c.screen) {
			t.Errorf("%s: the screen is taken for the prompt", c.what)
		}
		k14HungOnADialog(t, c.what, c.a, c.screen)
	}
}

// k14HungOnADialog: a working agent quiet for ten minutes on screen, a
// dialog the seat does not know. The seat is blocked and stays so, and a
// send is refused with nothing typed or pressed.
func k14HungOnADialog(t *testing.T, what string, a Agent, screen string) {
	t.Helper()
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{}, a, h, "k14", "id")
	quiet := time.Now().Add(-11 * time.Minute)
	s.mu.Lock()
	s.state, s.stateAt, s.lastLine = "working", quiet, quiet
	s.mu.Unlock()
	now := time.Now()
	for i := range 3 { // quiet ten minutes: blocked, and it stays so
		s.checkScreen(now.Add(time.Duration(2*i) * time.Second))
	}
	st := s.Snapshot().State
	_, err := s.Send("m1", "deploy it after lunch", "queue")
	h.mu.Lock()
	typed, keys := append([]string{}, h.typed...), append([]string{}, h.keys...)
	h.mu.Unlock()
	if st != "blocked" || err == nil || len(typed) != 0 || len(keys) != 0 {
		t.Errorf("%s: state %s, send err %v, typed %q, keys %q; want the seat still blocked on the dialog, the send refused, nothing typed or pressed", what, st, err, typed, keys)
	}
}

// An agent at work is known by what it draws at the foot of its screen
// while it works. Claude Code 2.1.332 says how to stop it at the end of
// its mode line, and its spinner line no longer does (the two screens of
// 2026-11-27, captured in auto mode: in a turn, and after it; its own
// source draws the hint among the footer's parts while a turn can be
// interrupted, and drops the mode line's "(shift+tab to cycle)" where the
// line does not fit); earlier versions said it on the spinner line, as
// Codex does on its working line. The other screens here are drawn after
// those, not captured.
func TestK14EachWorkScreenIsSeenAsTheRealProgramDrawsIt(t *testing.T) {
	t.Parallel()
	output := []string{"● Bash(make build)", "  ⎿  ok", "", "●"}
	turn := func(mode string) []string {
		return []string{"✻ Slithering… (2s · ↓ 118 tokens · thinking)", realRule, "❯ ", realRule, mode}
	}
	for _, c := range []struct {
		what   string
		a      busyScreen
		screen string
		want   bool
	}{
		{"Claude Code 2.1.332 in a turn, as captured", claudeAgent{}, shown(output, "", turn("  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt")), true},
		{"Claude Code 2.1.332 in a turn", claudeAgent{}, shown(output, "", turn("  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt")), true},
		{"Claude Code 2.1.332 in a turn, too narrow for the mode line's own hint", claudeAgent{}, shown(output, "", turn("  ⏵⏵ bypass permissions on · esc to interrupt")), true},
		{"Claude Code 2.1.332 in a turn, lines under its mode line", claudeAgent{}, shown(output, "", append(turn("  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"), "  main · 3 files changed", "  1 shell running")), true},
		{"Claude Code 2.1.332 after a turn, as captured", claudeAgent{}, shown(output, "", []string{"✻ Sautéed for 2s · done 3:03 PM", realRule, "❯ ", realRule, "  ⏵⏵ auto mode on (shift+tab to cycle)"}), false},
		{"Claude Code at its prompt, the hint's words in its last answer", claudeAgent{}, shown([]string{"● Press esc to interrupt it."}, "", claudePromptLines), false},
		{"Claude Code at its prompt, the hint itself the last line of its output", claudeAgent{}, shown([]string{"● Bash(tail -1 footer.txt)", "  ⎿    ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "", claudePromptLines), false},
		{"an earlier Claude Code in a turn", claudeAgent{}, shown(output, "", append([]string{"✻ Thinking… (12s · esc to interrupt)"}, claudePromptLines...)), true},
		{"Codex in a turn", codexAgent{}, shown(output, "", append([]string{"• Working (3s • esc to interrupt)"}, codexPromptLines...)), true},
		{"Codex in a turn, lines between its working line and its composer", codexAgent{}, shown(output, "", append([]string{"• Working (3s • esc to interrupt)", "", "  ↳ and the README too", "  ↳ then run the tests", "  ↳ then commit"}, codexPromptLines...)), true},
		{"Codex in a turn, its composer holding text", codexAgent{}, shown(output, "", []string{"• Working (3s • esc to interrupt)", "", "› and the README too", "", "  GPT-6.2-Astra max · /home/u/project", "  ← for agents · ? for shortcuts"}), true},
		{"Codex at its prompt", codexAgent{}, shown(output, "", codexPromptLines), false},
		{"Antigravity in a turn", antigravityAgent{}, shown(output, "", []string{"", realRule, "> ", realRule, "Antigravity relay · running · Gemini 4 Neon"}), true},
		{"Antigravity at its prompt", antigravityAgent{}, shown(output, "", agyPromptLines), false},
		{"Antigravity's trust dialog over its status line", antigravityAgent{}, agyTrustScreen, false},
	} {
		if got := c.a.busy(c.screen); got != c.want {
			t.Errorf("%s: at work %v, want %v", c.what, got, c.want)
		}
	}
}

// The busy line's words in an agent's output are not the agent at work
// (the prompt's words there are not its prompt, above). A working agent
// went quiet for ten minutes on a dialog the seat does not know; its
// output above the dialog holds a capture of its own busy line (it works
// on eagent's own source and prints a screen kept for a test; a tool's
// output is indented with spaces). The screen was called the agent at
// work, and a send was typed into the dialog as into a working agent.
// Antigravity draws its status line under its dialogs: one it shows in the
// middle of a turn has the turn's state on that line, which read as work.
// main refused every send to a seat blocked on such a screen.
func TestK14TheBusyLinesWordsInTheOutputAreNotWork(t *testing.T) {
	savedSettle, savedPause := typeSettle, dialogPause
	typeSettle, dialogPause = 0, 0
	t.Cleanup(func() { typeSettle, dialogPause = savedSettle, savedPause })
	claudeDialog := []string{"", realRule, " Which environment should I deploy to?", "", " ❯ 1. staging", "   2. production", "   3. Type something.", "", " Enter to select · ↑/↓ to navigate · Esc to cancel"}
	codexDialog := []string{"", "  Would you like to run the following command?", "", "  $ make deploy", "", "› 1. Yes, proceed (y)", "  2. No, and tell Codex what to do differently (esc)", "", "  Press enter to confirm or esc to cancel"}
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
		{"an earlier Claude Code's spinner line", claudeAgent{}, shown([]string{"● Bash(cat testdata/working.txt)", "  ⎿  ● Reading the file", "     ✻ Thinking… (12s · esc to interrupt)"}, "", claudeDialog)},
		{"Claude Code's mode line in a turn", claudeAgent{}, shown([]string{"● Bash(cat testdata/working.txt)", "  ⎿  ❯ ", "       ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "", claudeDialog)},
		{"Codex's working line", codexAgent{}, shown([]string{"• Ran cat testdata/working.txt", "  └ › fix it", "    • Working (3s • esc to interrupt)"}, "", codexDialog)},
		{"Antigravity's status line in a turn", antigravityAgent{}, strings.Join([]string{"Run command?", "", "make deploy", "", "> Yes", "  No", "", "  ↑/↓ Navigate · enter Confirm", "Antigravity relay · running · Gemini 4 Neon"}, "\n") + "\n"},
	} {
		if b := c.a.(busyScreen); b.busy(c.screen) {
			t.Errorf("%s: the screen is taken for the agent at work", c.what)
		}
		k14HungOnADialog(t, c.what, c.a, c.screen)
	}
}

// Claude Code as it draws a turn now (2.1.332: the hint on its mode line),
// quiet for ten minutes on a long command, with a message held for the end
// of the turn: the seat stays blocked on a turn that is still running, and
// the held message is neither typed nor reported; the Stop hook has it.
// The screen read as Claude at its prompt with no work (the spinner line
// has no hint any more), so the quiet turn was called over, and the
// message was reported as not arrived while it still waited in the spool
// for the turn's real end.
func TestK14AHeldMessageWaitsThroughAQuietTurnOfClaudeCodeAsItDrawsItNow(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	screen := shown([]string{"● Bash(go test ./...)", "  ⎿  Running…", ""}, "", []string{"✻ Slithering… (14m 2s · ↓ 3.1k tokens)", realRule, "❯ ", realRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"})
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "k14", "")
	s.state = "working"
	if _, err := s.Send("m1", "when you are done, commit and push", "after_turn"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.mu.Lock()
	s.stateAt, s.lastLine = now.Add(-11*time.Minute), now.Add(-11*time.Minute)
	s.mu.Unlock()
	for i := range 3 {
		s.checkScreen(now.Add(time.Duration(2*i) * time.Second))
	}
	s.reportLeftOvers(now.Add(time.Hour))
	typed, lost := reportsOf(s, h)
	if st := s.Snapshot(); st.State != "blocked" || st.Stuck || len(typed) != 0 || len(lost) != 0 {
		t.Fatalf("state %s, stuck %v, typed %q, reported as not arrived %v; want the turn still running: blocked, not stuck, nothing typed or reported", st.State, st.Stuck, typed, lost)
	}
	if names, _ := filepath.Glob(filepath.Join(spool, "*.txt")); len(names) != 1 {
		t.Fatalf("the spool holds %v; want the held message still there", names)
	}
}

// A send looks at the screen once more before it types, and answers a
// dialog of the agent's own that shows (one that goes away when answered
// is typed after: k3seat_test.go). One that still shows after it was
// answered and waited on three times is not typed into: the send is
// refused, and nothing is typed. (After three looks the text was typed
// whatever the screen showed, into the dialog; main does the same.)
func TestK14ASendIsNotTypedIntoADialogThatStays(t *testing.T) {
	savedSettle, savedPause := typeSettle, dialogPause
	typeSettle, dialogPause = 0, 0
	t.Cleanup(func() { typeSettle, dialogPause = savedSettle, savedPause })
	h := &fakeHost{alive: true, screen: claudeTrustScreen}
	s := newSeat(Spec{}, claudeAgent{}, h, "k14", "id")
	s.mu.Lock()
	s.state, s.stateAt = "idle", time.Now()
	s.mu.Unlock()
	_, err := s.Send("m1", "deploy it after lunch", "queue")
	h.mu.Lock()
	typed, keys := append([]string{}, h.typed...), strings.Join(h.keys, " ")
	h.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "still shows a dialog") || len(typed) != 0 {
		t.Errorf("send err %v, typed %q; want the send refused and nothing typed", err, typed)
	}
	if keys != "Down Enter" {
		t.Errorf("pressed %q; want the dialog answered once", keys)
	}
}
