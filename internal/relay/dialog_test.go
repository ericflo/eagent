package relay

import (
	"strings"
	"testing"
	"time"
)

// The agents' own dialogs and prompts as the real programs drew them in a
// 200-column terminal on 2026-11-27 (Claude Code 2.1.332, Codex 0.171.0,
// Antigravity CLI 1.5.18 started with a status-line command as the seat
// starts it, pi 1.6.1). The folder's path is replaced, the rules are
// shortened, and the blank lines a screen ends with are left out.
const realRule = "────────────────────────────────────────────────────────────"

var claudeTrustLines = []string{
	"",
	realRule,
	" Accessing workspace:",
	"",
	" /home/u/project",
	"",
	" Quick safety check: Is this a project you created or one you trust? (Like your own code, a well-known open source project, or work from your team). If not, take a moment to review what's in this",
	" folder first.",
	"",
	" Claude Code'll be able to read, edit, and execute files here.",
	"",
	" Security guide",
	"",
	" ❯ No, exit",
	"   Yes, I trust this folder",
	"",
	" Enter to confirm · Esc to cancel",
}

var (
	claudeTrustScreen  = strings.Join(claudeTrustLines, "\n") + "\n\n\n"
	claudeBypassScreen = strings.Join([]string{
		"",
		realRule,
		"  WARNING: Claude Code running in Bypass Permissions mode",
		"",
		"  In Bypass Permissions mode, Claude Code will not ask for your approval before running potentially dangerous commands.",
		"  This mode should only be used in a sandboxed container/VM that has restricted internet access and can easily be restored if damaged.",
		"",
		"  By proceeding, you accept all responsibility for actions taken while running in Bypass Permissions mode.",
		"",
		"  https://code.claude.com/docs/en/security",
		"",
		"  ❯ No, exit",
		"    Yes, I accept",
		"",
		"  Enter to confirm · Esc to cancel",
		"", "",
	}, "\n")
	codexTrustLines = []string{
		"",
		"  Folder access",
		"  /home/u/project",
		"",
		"  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings. Folder settings can run code automatically, even without a model request. Continue only if you",
		"  trust these files. Your trust decision will be saved.",
		"",
		"› 1. Trust and continue",
		"  2. Back to Agent Command Center",
		"",
		"  enter continue · esc back",
	}
	codexTrustScreen  = strings.Join(codexTrustLines, "\n") + "\n\n"
	codexUpdateScreen = strings.Join([]string{
		"",
		"  Update available · 0.171.0 → 0.172.0",
		"  Release notes: https://github.com/openai/codex/releases/latest",
		"",
		"› 1. Update now (runs `npm install -g @openai/codex`)",
		"  2. Skip",
		"  3. Skip until next version",
		"",
		"  enter continue · esc skip",
		"",
	}, "\n")
	agyTrustLines = []string{
		"Accessing workspace:",
		"",
		"/home/u/project",
		"",
		"Do you trust the contents of this project?",
		"",
		"Antigravity CLI requires permission to read, edit, and execute files here.",
		"",
		"> Yes, I trust this folder",
		"  No, exit",
		"",
		"  ↑/↓ Navigate · enter Confirm",
	}
	// The line under the hint is Antigravity's status line: the seat's own
	// command prints it, and without one agy draws its own there.
	agyTrustScreen = strings.Join(agyTrustLines, "\n") + "\nAntigravity relay · idle\n\n"
	piTrustLines   = []string{
		realRule,
		"",
		" Trust project folder?",
		" /home/u/project",
		"",
		" This allows pi to load .pi settings and resources, install missing project packages, and execute project extensions.",
		"",
		" → Trust",
		"   Trust parent folder (/home/u)",
		"   Trust (this session only)",
		"   Do not trust",
		"   Do not trust (this session only)",
		"",
		" ↑↓ navigate  enter select  escape/ctrl+c cancel",
		"",
		realRule,
	}
	piTrustScreen = strings.Join(piTrustLines, "\n") + "\n\n"

	// What each agent draws under its output while it is at its prompt.
	claudePromptLines = []string{"", realRule, "❯ ", realRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	codexPromptLines  = []string{"", "› Ask Codex to do anything", "", "  GPT-6.2-Astra max · /home/u/project", "  ← for agents · ? for shortcuts"}
	agyPromptLines    = []string{"", realRule, "> Auto mode: edits & safe commands auto-approved (shift+tab to cycle)", realRule, "Antigravity relay · idle"}
	piPromptLines     = []string{"", realRule, realRule, "/home/u/project", "0.0%/262k (auto)                                    (anthropic) claude-haiku-5-5 • low"}
)

// shown is a screen with some lines of output above an agent's prompt.
func shown(output []string, indent string, prompt []string) string {
	var lines []string
	for _, line := range output {
		if line != "" {
			line = indent + line
		}
		lines = append(lines, line)
	}
	return strings.Join(append(lines, prompt...), "\n") + "\n"
}

// dialogSeat is a seat that is starting, looking at screen.
func dialogSeat(a Agent, screen string) (*Seat, *fakeHost) {
	h := &fakeHost{alive: true, screen: screen}
	return newSeat(Spec{}, a, h, "x", ""), h
}

// Each agent's dialogs are answered where the real program shows them.
func TestEachDialogIsAnsweredAsTheRealProgramDrawsIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a            Agent
		name, screen string
	}{
		{claudeAgent{}, "claude-trust", claudeTrustScreen},
		{claudeAgent{}, "claude-bypass", claudeBypassScreen},
		{codexAgent{}, "codex-trust", codexTrustScreen},
		{codexAgent{}, "codex-update", codexUpdateScreen},
		{antigravityAgent{}, "antigravity-trust", agyTrustScreen},
		// Without a status-line command of the seat's, agy draws its own.
		{antigravityAgent{}, "antigravity-trust", strings.Join(agyTrustLines, "\n") + "\n" + strings.Repeat(" ", 160) + "auto · Gemini 4 Neon · xhigh\n"},
		{piAgent{}, "pi-trust", piTrustScreen},
	} {
		var want *dialog
		for _, d := range c.a.dialogs() {
			if d.name == c.name {
				want = &d
			} else if d.shows(c.screen) {
				t.Errorf("%s: %s is seen on its screen as well", c.name, d.name)
			}
		}
		if want == nil || !want.shows(c.screen) {
			t.Errorf("%s is not seen on the screen the program draws", c.name)
			continue
		}
		s, h := dialogSeat(c.a, c.screen)
		s.checkScreen(time.Now())
		if strings.Join(h.keys, " ") != strings.Join(want.keys, " ") {
			t.Errorf("%s: pressed %q, want %q", c.name, h.keys, want.keys)
		}
	}
}

// A dialog's words in an agent's output are not the dialog: the agent
// works on eagent's own source, or reads a capture of the dialog, and its
// prompt is drawn under them. No key is pressed, and the seat is not
// blocked on a dialog. (The words anywhere on the screen were taken for
// the dialog, and its keys were pressed every three seconds while they
// showed.) The first two screens are Claude Code's own, drawn after it ran
// `cat` on a capture of its trust dialog; the others put each dialog's
// lines above the prompt its agent draws.
func TestADialogsWordsInTheOutputAreNotTheDialog(t *testing.T) {
	t.Parallel()
	claudeOutput := append([]string{"", "! cat dialog.txt"}, claudeTrustLines[1:]...)
	claudeOutput = append(claudeOutput, "", "● dialog.txt holds a capture of Claude Code's workspace trust prompt.", "", "✻ Sautéed for 2s")
	working := append(append([]string{}, claudeOutput[:len(claudeOutput)-1]...), "✻ Slithering… (2s · ↓ 118 tokens · thinking)")
	for _, c := range []struct {
		what   string
		a      Agent
		screen string
	}{
		{"Claude Code at its prompt", claudeAgent{}, shown(claudeOutput, "     ", []string{"", realRule, "❯ ", realRule, "  ⏵⏵ auto mode on (shift+tab to cycle)"})},
		{"Claude Code in a turn", claudeAgent{}, shown(working, "     ", []string{"", realRule, "❯ ", realRule, "  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt"})},
		{"Claude Code, the dialog's lines last and unindented", claudeAgent{}, shown(claudeTrustLines, "", claudePromptLines)},
		{"Claude Code's bypass warning in its output", claudeAgent{}, shown(strings.Split(claudeBypassScreen, "\n"), "", claudePromptLines)},
		{"Codex's trust dialog in its output", codexAgent{}, shown(codexTrustLines, "", codexPromptLines)},
		{"Codex's update prompt in its output", codexAgent{}, shown(strings.Split(strings.TrimRight(codexUpdateScreen, "\n"), "\n"), "", codexPromptLines)},
		{"Antigravity's trust dialog in its output", antigravityAgent{}, shown(agyTrustLines, "", agyPromptLines)},
		{"Antigravity's trust dialog typed into its prompt", antigravityAgent{}, shown(agyTrustLines[:8], "", []string{"", realRule, "> Yes, I trust this folder   No, exit   ↑/↓ Navigate · enter Confirm", realRule, "Antigravity relay · idle"})},
		{"pi's trust dialog in its output", piAgent{}, shown(piTrustLines, "", piPromptLines)},
		// Another dialog of the same program, closing with the same hint
		// (Claude Code's question about a key in the environment), with
		// the trust dialog's words in the output above it.
		{"another Claude Code dialog under the words", claudeAgent{}, shown(claudeTrustLines, "     ", []string{"", realRule, "  Detected a custom API key in your environment", "", "  ANTHROPIC_API_KEY: sk-ant-...", "", "  Do you want to use this API key?", "", "    Yes", "  ❯ No (recommended)", "", "  Enter to confirm · Esc to cancel"})},
	} {
		for _, d := range c.a.dialogs() {
			if d.shows(c.screen) {
				t.Errorf("%s: taken for %s", c.what, d.name)
			}
		}
		s, h := dialogSeat(c.a, c.screen)
		start := time.Now()
		s.checkScreen(start)
		s.checkScreen(start.Add(4 * time.Second))
		if len(h.keys) != 0 {
			t.Errorf("%s: pressed %q", c.what, h.keys)
		}
		if st := s.Snapshot().State; st == "blocked" {
			t.Errorf("%s: the seat is blocked on a dialog", c.what)
		}
	}
}
