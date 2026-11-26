package relay

import (
	"bufio"
	"encoding/json"
	"errors"
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

// Codex (OpenAI's codex CLI). Its session id is known only once its rollout
// appears; held messages go through `codex queue`.
type codexAgent struct{ agentBase }

func init() { register(codexAgent{}) }

func (codexAgent) Name() string        { return "codex" }
func (codexAgent) Label() string       { return "Codex" }
func (codexAgent) DisplayName() string { return "Codex" }
func (codexAgent) Binary() string      { return "codex" }

func (codexAgent) newParser(Spec) parser { return codexParser{} }

func (codexAgent) ready(screen string) bool {
	return strings.Contains(screen, "Ask Codex to do anything")
}

// busy: Codex's working line, drawn just above its composer, says how to
// stop it.
func (codexAgent) busy(screen string) bool { return spinnerShown(screen) }

func (codexAgent) dialogs() []dialog {
	return []dialog{
		// Codex: the default choice is "Trust and continue".
		{name: "codex-trust", match: []string{"Trust this folder?", "Trust and continue"}, keys: []string{"Enter"}, what: "accepted Codex's folder-trust prompt"},
		// Codex's update prompt: choose "2. Skip" explicitly (never Escape,
		// which quits on the trust prompt). Upgrading is the user's call; the
		// notice says one is available. Without the menu, the "Update
		// available" box is only a notice above a working prompt.
		{name: "codex-update", match: []string{"Update now", "Skip until next version"}, keys: []string{"Down", "Enter"}, what: "skipped Codex's update prompt for now (a newer Codex is available; upgrade with `codex update` and a restart)"},
	}
}

func (codexAgent) command(p config.RelayProfile, innerID string, resume bool, _ string) []string {
	argv := []string{"codex"}
	if resume && innerID != "" {
		argv = append(argv, "resume", innerID)
	}
	argv = append(argv, p.Args...)
	if p.Model != "" {
		argv = append(argv, "-m", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "-c", fmt.Sprintf("model_reasoning_effort=%q", p.Effort))
	}
	return argv
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(home(), ".codex")
}

// locate scans the rollout folders for the days around the launch for the
// TUI session that started in dir after the launch (or, on a resume, the
// newest rollout of the known id).
func (codexAgent) locate(spec Spec, launched time.Time, innerID string) (string, string) {
	root := filepath.Join(codexHome(), "sessions")
	want := cleanDir(spec.Dir)
	type cand struct {
		path, id string
		start    time.Time
		mod      time.Time
	}
	var cands []cand
	// A known id names its rollout, whatever day it began (a resumed
	// session appends to the rollout it started, possibly days ago);
	// otherwise the new session's rollout is in the days around the launch.
	var paths []string
	if innerID != "" {
		paths, _ = filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*-"+innerID+".jsonl"))
	}
	if len(paths) == 0 {
		for _, day := range []time.Time{launched.Add(-24 * time.Hour), launched, launched.Add(24 * time.Hour)} {
			folder := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
			entries, _ := os.ReadDir(folder)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "rollout-") && strings.HasSuffix(e.Name(), ".jsonl") {
					paths = append(paths, filepath.Join(folder, e.Name()))
				}
			}
		}
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.ModTime().Before(launched.Add(-5*time.Second)) {
			continue
		}
		if _, taken := claimed.Load(path); taken {
			continue
		}
		meta, ok := codexMeta(path)
		if !ok {
			continue
		}
		if innerID != "" {
			if meta.ID == innerID {
				cands = append(cands, cand{path, meta.ID, meta.start, info.ModTime()})
			}
			continue
		}
		if cleanDir(meta.Cwd) != want || meta.start.Before(launched.Add(-10*time.Second)) {
			continue
		}
		cands = append(cands, cand{path, meta.ID, meta.start, info.ModTime()})
	}
	if len(cands) == 0 {
		return "", ""
	}
	if innerID != "" {
		sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	} else {
		sort.Slice(cands, func(i, j int) bool { return cands[i].start.Before(cands[j].start) })
	}
	return cands[0].path, cands[0].id
}

type rolloutMeta struct {
	ID, Cwd string
	start   time.Time
}

func codexMeta(path string) (rolloutMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return rolloutMeta{}, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	line, err := r.ReadSlice('\n')
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
		return rolloutMeta{}, false
	}
	// The meta line carries the whole base prompt; only its head is needed,
	// and a cut-off line still has the fields that come first.
	var ev struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &ev) != nil {
		head := string(line)
		ev.Type = between(head, `"type":"`, `"`)
		ev.Payload.ID = between(head, `"id":"`, `"`)
		ev.Payload.Cwd = between(head, `"cwd":"`, `"`)
		ev.Payload.Timestamp = between(head[strings.Index(head, `"payload"`)+1:], `"timestamp":"`, `"`)
	}
	if ev.Type != "session_meta" || ev.Payload.ID == "" {
		return rolloutMeta{}, false
	}
	return rolloutMeta{ID: ev.Payload.ID, Cwd: ev.Payload.Cwd, start: parseTime(ev.Payload.Timestamp)}, true
}

// afterTurn uses Codex's own queue: the message becomes the next turn.
func (codexAgent) afterTurn(s *Seat, _ string, text string) error {
	id := s.innerID()
	if id == "" {
		return errors.New("Codex's session id is not known yet")
	}
	argv := codexQueueArgs(id, text)
	cmd, cancel := bounded(time.Minute, argv[0], argv[1:]...)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex queue: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// codexQueueArgs is the codex queue command for a message. The values go
// joined to their flags: codex's parser takes a separate "-x ..." for an
// unknown flag and refuses the command, and "--" does not help there.
func codexQueueArgs(id, text string) []string {
	return []string{"codex", "queue", "--thread=" + id, "--message=" + text}
}

// ---- choices ------------------------------------------------------------------

// codexEfforts are offered when Codex's model cache cannot be read: the
// levels its current models accept.
var codexEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// defaults are config.toml's model and model_reasoning_effort.
func (codexAgent) defaults() (string, string) {
	raw, err := os.ReadFile(filepath.Join(codexHome(), "config.toml"))
	if err != nil {
		return "", ""
	}
	top := tomlTop(string(raw))
	return top["model"], top["model_reasoning_effort"]
}

// tomlTop reads the top-level string keys of a TOML file (those before its
// first table), which is all a model default needs.
func tomlTop(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || (value[0] != '"' && value[0] != '\'') {
			continue
		}
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			out[strings.TrimSpace(key)] = value[1 : 1+end]
		}
	}
	return out
}

// codexCache is the part of Codex's models_cache.json a picker needs: the
// models its own /model picker lists, best first, and the efforts each takes.
type codexCache struct {
	Models []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		Levels      []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

func readCodexCache() (codexCache, bool) {
	var c codexCache
	raw, err := os.ReadFile(filepath.Join(codexHome(), "models_cache.json"))
	if err != nil || json.Unmarshal(raw, &c) != nil || len(c.Models) == 0 {
		return c, false
	}
	return c, true
}

// models are the ones Codex's own picker lists (from its model cache),
// else the OpenAI models in eagent's catalog.
func (codexAgent) models(string) []ModelChoice {
	l := choiceList{}
	if c, ok := readCodexCache(); ok {
		for _, m := range c.Models {
			if m.Visibility == "list" {
				l.add(ModelChoice{ID: m.Slug, Label: m.DisplayName, Detail: m.Description})
			}
		}
		return l.out
	}
	for _, m := range config.Models {
		if m.Provider == "openai" {
			l.add(ModelChoice{ID: m.ID, Label: m.Label})
		}
	}
	return l.out
}

// efforts are every level its listed models take.
func (codexAgent) efforts() []string {
	c, ok := readCodexCache()
	if !ok {
		return slices.Clone(codexEfforts)
	}
	var out []string
	for _, m := range c.Models {
		if m.Visibility != "list" {
			continue
		}
		for _, l := range m.Levels {
			if l.Effort != "" && len(l.Effort) <= 40 && !slices.Contains(out, l.Effort) {
				out = append(out, l.Effort)
			}
		}
	}
	if len(out) == 0 {
		return slices.Clone(codexEfforts)
	}
	// Mildest first: eagent's scale, then Codex's "ultra" (max with
	// delegation), then any level neither knows, as Codex lists them.
	scale := append(slices.Clone(config.EffortOrder), "ultra")
	rank := func(e string) int {
		if i := slices.Index(scale, e); i >= 0 {
			return i
		}
		return len(scale) + slices.Index(out, e)
	}
	ranked := slices.Clone(out)
	slices.SortFunc(ranked, func(a, b string) int { return rank(a) - rank(b) })
	return ranked
}
