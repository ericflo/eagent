package relay

import (
	"strings"
	"testing"
	"time"
)

// An agent's prompt is known by its words at the foot of the screen, where
// the real programs draw it (the screens of dialog_test.go).
func TestK14EachPromptIsSeenAsTheRealProgramDrawsIt(t *testing.T) {
	t.Parallel()
	output := []string{"● Bash(make build)", "  ⎿  ok", "", "● Built."}
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
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
		h := &fakeHost{alive: true, screen: c.screen}
		s := newSeat(Spec{}, c.a, h, "k14", "id")
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
			t.Errorf("%s: state %s, send err %v, typed %q, keys %q; want the seat still blocked on the dialog, the send refused, nothing typed or pressed", c.what, st, err, typed, keys)
		}
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
