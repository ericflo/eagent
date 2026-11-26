package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Claude Code. eagent picks its session id up front; held messages wait in
// a spool that its Stop hook (this binary's relay-hook command) hands over.
type claudeAgent struct{ agentBase }

func init() { register(claudeAgent{}) }

func (claudeAgent) Name() string        { return "claude" }
func (claudeAgent) Label() string       { return "Claude" }
func (claudeAgent) DisplayName() string { return "Claude Code" }
func (claudeAgent) Binary() string      { return "claude" }

func (claudeAgent) newParser(Spec) parser { return &claudeParser{} }
func (claudeAgent) chooseID() string      { return newUUID() }

func (claudeAgent) ready(screen string) bool {
	return strings.Contains(screen, "bypass permissions on") || strings.Contains(screen, "for shortcuts")
}

// busy: Claude's footer stays while it works; its spinner line says how
// to stop it.
func (claudeAgent) busy(screen string) bool { return strings.Contains(screen, "esc to interrupt") }

func (claudeAgent) dialogs() []dialog {
	return []dialog{
		// Claude Code: the default choice is "No, exit".
		{name: "claude-trust", match: []string{"Yes, I trust this folder"}, keys: []string{"Down", "Enter"}, what: "accepted Claude Code's folder-trust prompt"},
		{name: "claude-bypass", match: []string{"Bypass Permissions mode", "Yes, I accept"}, keys: []string{"Down", "Enter"}, what: "accepted Claude Code's bypass-permissions warning"},
	}
}

// prepare sets up the Stop hook: this binary, reading the relay's spool.
func (claudeAgent) prepare(spec *Spec, stateDir string) error {
	spec.Spool = filepath.Join(stateDir, "after_turn")
	// Only an eagent binary has the relay-hook command (not a test
	// binary); without the hook, held messages are typed instead.
	if exe, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(exe), "eagent") {
		spec.Hook = ShellQuote(exe) + " relay-hook " + ShellQuote(spec.Spool)
	}
	return nil
}

func (claudeAgent) command(p config.RelayProfile, innerID string, resume bool, hook string) []string {
	argv := []string{"claude"}
	if resume {
		argv = append(argv, "--resume", innerID)
	} else {
		argv = append(argv, "--session-id", innerID)
	}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "--effort", p.Effort)
	}
	if hook != "" {
		settings, _ := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hook}}}}}})
		argv = append(argv, "--settings", string(settings))
	}
	return append(argv, p.Args...)
}

func claudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	return filepath.Join(home(), ".claude")
}

func (claudeAgent) locate(_ Spec, _ time.Time, innerID string) (string, string) {
	if innerID == "" {
		return "", ""
	}
	matches, _ := filepath.Glob(filepath.Join(claudeHome(), "projects", "*", innerID+".jsonl"))
	if len(matches) == 0 {
		return "", ""
	}
	return matches[0], innerID
}

// afterTurn leaves the text in the spool the Stop hook reads: when Claude's
// turn ends, the hook hands it over as the next thing to do.
func (claudeAgent) afterTurn(s *Seat, id, text string) error {
	return spoolMessage(s.spec.Spool, id, text)
}

// RunStopHook is Claude Code's Stop hook for a relay: it takes every message
// waiting in spool and returns them as the reason to keep going, which
// Claude reads as its next instruction. With nothing waiting it lets the
// turn end.
func RunStopHook(spool string, stdin io.Reader, stdout io.Writer) int {
	_, _ = io.Copy(io.Discard, io.LimitReader(stdin, 1<<20))
	entries, err := os.ReadDir(spool)
	if err != nil {
		return 0
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var texts []string
	for _, name := range names {
		path := filepath.Join(spool, name)
		taken := path + ".taken"
		if os.Rename(path, taken) != nil {
			continue // another hook got it
		}
		raw, err := os.ReadFile(taken)
		if err == nil && strings.TrimSpace(string(raw)) != "" {
			texts = append(texts, strings.TrimSpace(string(raw)))
		}
		_ = os.Remove(taken)
	}
	if len(texts) == 0 {
		return 0
	}
	out, _ := json.Marshal(map[string]string{"decision": "block", "reason": strings.Join(texts, "\n\n")})
	fmt.Fprintln(stdout, string(out))
	return 0
}

// ---- choices ------------------------------------------------------------------

// defaults are settings.json's model and effortLevel; ANTHROPIC_MODEL
// overrides the model.
func (claudeAgent) defaults() (string, string) {
	var s struct {
		Model       string `json:"model"`
		EffortLevel string `json:"effortLevel"`
	}
	if raw, err := os.ReadFile(filepath.Join(claudeHome(), "settings.json")); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if m := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); m != "" {
		s.Model = m
	}
	return strings.TrimSpace(s.Model), strings.TrimSpace(s.EffortLevel)
}

func (claudeAgent) efforts() []string { return slices.Clone(standardEfforts) }

// models are Claude Code's aliases for the latest of each family, then the
// Anthropic models eagent knows.
func (claudeAgent) models(project string) []ModelChoice {
	l := choiceList{}
	for _, a := range []struct{ id, label string }{{"fable", "Fable"}, {"opus", "Opus"}, {"sonnet", "Sonnet"}, {"haiku", "Haiku"}} {
		l.add(ModelChoice{ID: a.id, Label: a.label + " (latest)", Detail: "Claude Code's alias for the newest " + a.label})
	}
	var models []config.Model
	for _, m := range config.KnownModels(project) {
		if m.Provider == "anthropic" {
			models = append(models, m)
		}
	}
	// By family as the aliases are, newest first within each.
	slices.SortStableFunc(models, func(a, b config.Model) int {
		fa, va := claudeVersion(a.ID)
		fb, vb := claudeVersion(b.ID)
		if fa != fb {
			return fa - fb
		}
		return slices.Compare(vb, va)
	})
	for _, m := range models {
		label := m.Label
		if label == m.ID {
			label = ""
		}
		l.add(ModelChoice{ID: m.ID, Label: label})
	}
	return l.out
}

// modelLabel reads "Opus (latest)" and "Claude Opus 5.5" as "Opus" and
// "Opus 5.5".
func (claudeAgent) modelLabel(c ModelChoice) string {
	return strings.TrimPrefix(strings.TrimSuffix(c.Label, " (latest)"), "Claude ")
}

// claudeVersion reads "claude-opus-4-5-20251101" as the Opus family (by the
// aliases' order, unknown ones last) and version [4 5 20251101].
func claudeVersion(id string) (family int, version []int) {
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	family = slices.Index([]string{"fable", "opus", "sonnet", "haiku"}, parts[0])
	if family < 0 {
		family = 4
	}
	for _, p := range parts[1:] {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return family, version
			}
			n = n*10 + int(c-'0')
		}
		version = append(version, n)
	}
	return family, version
}
