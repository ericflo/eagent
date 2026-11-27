package relay

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Antigravity (Google's agy CLI). Its status line is this binary's
// relay-agy-status command, which saves the conversation id, agent state
// and quota to a file the seat polls.
type antigravityAgent struct{ agentBase }

func init() { register(antigravityAgent{}) }

func (antigravityAgent) Name() string        { return "antigravity" }
func (antigravityAgent) Label() string       { return "Antigravity" }
func (antigravityAgent) DisplayName() string { return "Antigravity" }
func (antigravityAgent) Binary() string      { return "agy" }

func (antigravityAgent) newParser(Spec) parser { return antigravityParser{} }

// interruptKeys: agy stops a turn on Ctrl+C.
func (antigravityAgent) interruptKeys() []string { return []string{"C-c"} }

// ready matches the status line the relay-agy-status hook prints.
func (antigravityAgent) ready(screen string) bool {
	return strings.Contains(screen, "Antigravity relay ·")
}

// busy: the relay line is drawn whatever agy does, and names its state.
// Only "idle" is known for certain (agy's own run statuses are idle,
// running, busy and canceling, but how it names them here is not
// documented), so any other name reads as busy. The seat asks only when an
// agent that went quiet would otherwise be called idle, and such an agent
// still takes messages and goes back to work on its next transcript line.
func (antigravityAgent) busy(screen string) bool {
	// The line is drawn at the bottom; the same words in output above it
	// are not it.
	_, rest, ok := strings.Cut(lastLines(screen, 3), "Antigravity relay · ")
	if !ok {
		return false
	}
	state, _, _ := strings.Cut(rest, " ·")
	return !strings.EqualFold(strings.TrimSpace(state), "idle")
}

func (antigravityAgent) dialogs() []dialog {
	return []dialog{
		{name: "antigravity-trust", match: []string{"Antigravity CLI requires permission", "> Yes, I trust this folder"}, end: "enter Confirm", under: 1, keys: []string{"Enter"}, what: "accepted Antigravity’s workspace trust prompt"},
	}
}

func (antigravityAgent) prepare(spec *Spec, stateDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	spec.StatusFile = filepath.Join(stateDir, "agy-status.json")
	spec.Hook = ShellQuote(exe) + " relay-agy-status " + ShellQuote(spec.StatusFile)
	return nil
}

func (antigravityAgent) command(p config.RelayProfile, id string, resume bool, hook string) []string {
	argv := []string{"agy"}
	if resume && id != "" {
		argv = append(argv, "--conversation", id)
	}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "--effort", p.Effort)
	}
	settings := map[string]any{"queuedMessages": "queue"}
	if hook != "" {
		settings["statusLine"] = map[string]string{"type": "command", "command": hook}
	}
	raw, _ := json.Marshal(settings)
	argv = append(argv, "--settings", string(raw))
	return append(argv, p.Args...)
}

// afterTurn types the text: agy's native queue delivers it after the
// running turn. A failure here may come after the paste went in, so the
// seat must not type it again.
func (antigravityAgent) afterTurn(s *Seat, _, text string) error {
	if err := s.typeText(text); err != nil {
		return &TypingError{err}
	}
	return nil
}

func antigravityHome() string {
	return filepath.Join(home(), ".gemini", "antigravity-cli")
}

var conversationID = regexp.MustCompile(`^[a-fA-F0-9]{8}(-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`)

type agyStatus struct {
	ID    string `json:"conversation_id"`
	State string `json:"agent_state"`
	Model struct {
		Name string `json:"display_name"`
	} `json:"model"`
	Quota map[string]struct {
		Remaining *float64 `json:"remaining_fraction"`
		Reset     string   `json:"reset_time"`
	} `json:"quota"`
}

// WriteAntigravityStatus is agy's per-terminal statusLine command. Only save
// the fields the relay consumes. The input also contains account details.
func WriteAntigravityStatus(path string, stdin io.Reader, stdout io.Writer) int {
	var status agyStatus
	if json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&status) != nil {
		return 1
	}
	fmt.Fprintf(stdout, "Antigravity relay · %s · %s\n", status.State, status.Model.Name)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return 1
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agy-status-*")
	if err != nil {
		return 1
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(status)
	if closeErr := f.Close(); err != nil || closeErr != nil {
		return 1
	}
	if os.Rename(f.Name(), path) != nil {
		return 1
	}
	return 0
}

func antigravityStatus(file string) agyStatus {
	var status agyStatus
	if raw, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(raw, &status)
	}
	return status
}

func (antigravityAgent) locate(spec Spec, _ time.Time, id string) (string, string) {
	if id == "" {
		id = antigravityStatus(spec.StatusFile).ID
	}
	if !conversationID.MatchString(id) {
		return "", ""
	}
	// agy 1.5 reports the desktop app's brain path even for a CLI session.
	path := filepath.Join(antigravityHome(), "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
	if _, err := os.Stat(path); err != nil {
		return "", ""
	}
	return path, id
}

// An interrupted agy turn can end without a final transcript step. Wait for
// the transcript writer to settle before reporting a turn without a reply.
// A message handed to agy's queue that a turn's end left there is noted
// too (reportLeftOvers).
func (antigravityAgent) poll(s *Seat, now time.Time) {
	s.reportLeftOvers(now)
	status := antigravityStatus(s.spec.StatusFile)
	if status.ID == "" || status.ID != s.innerID() {
		return
	}
	s.mu.Lock()
	interrupted := status.State == "idle" && s.state == "working" && now.Sub(s.lastLine) > 4*time.Second
	s.mu.Unlock()
	if interrupted {
		s.fold(obs{kind: "aborted", at: now})
	}
	group := "3p-"
	if strings.Contains(strings.ToLower(status.Model.Name), "gemini") || strings.Contains(strings.ToLower(status.Model.Name), "orchestrator") {
		group = "gemini-"
	}
	var limit obs
	for name, q := range status.Quota {
		if strings.HasPrefix(name, group) && q.Remaining != nil {
			used := 100 * (1 - *q.Remaining)
			if used > limit.used {
				limit = obs{kind: "limit", used: used, window: strings.TrimPrefix(name, group), resets: q.Reset, at: now}
			}
		}
	}
	if limit.used > 0 {
		s.fold(limit)
	}
}

type antigravityParser struct{}

func (antigravityParser) line(raw []byte) []obs {
	var step struct {
		Type, Content, Source string
		Created               string `json:"created_at"`
		Calls                 []struct {
			Name string
			Args struct {
				Summary      string `json:"toolSummary"`
				AbsolutePath string
			}
		} `json:"tool_calls"`
	}
	if json.Unmarshal(raw, &step) != nil {
		return nil
	}
	at := parseTime(step.Created)
	switch step.Type {
	case "USER_INPUT":
		if step.Source != "USER_EXPLICIT" {
			return nil
		}
		prompt := step.Content
		if body, ok := strings.CutPrefix(prompt, "<USER_REQUEST>\n"); ok {
			if end := strings.LastIndex(body, "\n</USER_REQUEST>"); end >= 0 {
				prompt = body[:end]
			}
		}
		return []obs{{kind: "user", text: prompt, at: at}, {kind: "working", at: at}}
	case "PLANNER_RESPONSE":
		if len(step.Calls) == 0 && step.Content != "" {
			return []obs{{kind: "turn", text: step.Content, at: at}}
		}
		var out []obs
		if step.Content != "" {
			out = append(out, obs{kind: "progress", text: step.Content, at: at})
		}
		for _, call := range step.Calls {
			summary := call.Args.Summary
			if summary == "" {
				summary = "using " + call.Name
			}
			if isImagePath(call.Args.AbsolutePath) && (call.Name == "view_file" || call.Name == "view_image") {
				out = append(out, obs{kind: "image", text: call.Args.AbsolutePath, tool: true, at: at})
			} else {
				out = append(out, obs{kind: "activity", text: oneLine(summary, 160), tool: true, at: at})
			}
		}
		return out
	case "ERROR_MESSAGE":
		out := []obs{{kind: "aborted", text: step.Content, at: at}}
		if msg := limitText(step.Content); msg != "" {
			out = append(out, obs{kind: "limit", exhausted: true, text: msg, at: at})
		}
		return out
	}
	return nil
}

// Models are account-specific. Keep a successful listing through transient
// failures, and avoid running the network-backed CLI on every settings poll.
var agyCatalog struct {
	sync.Mutex
	models []ModelChoice
	retry  time.Time
}

func antigravityModels() []ModelChoice {
	agyCatalog.Lock()
	defer agyCatalog.Unlock()
	if time.Now().After(agyCatalog.retry) {
		raw, err := catalog(8*time.Second, nil, "agy", "models")
		agyCatalog.retry = time.Now().Add(time.Minute)
		if err == nil {
			var models []ModelChoice
			seen := map[string]bool{}
			for _, line := range strings.Split(string(raw), "\n") {
				id, label, ok := strings.Cut(strings.TrimSpace(line), "\t")
				if ok && id != "" && !seen[id] && len(models) < 100 {
					models = append(models, ModelChoice{ID: id, Label: strings.TrimSpace(label)})
					seen[id] = true
				}
			}
			if len(models) > 0 {
				agyCatalog.models = models
				agyCatalog.retry = time.Now().Add(time.Hour)
			}
		}
	}
	return slices.Clone(agyCatalog.models)
}

func (antigravityAgent) defaults() (string, string) { return antigravityDefaults() }

func (antigravityAgent) models(string) []ModelChoice { return antigravityModels() }

func (antigravityAgent) efforts() []string { return slices.Clone(standardEfforts) }

// validModel: agy takes a model by its label too ("Gemini 3.8 Flash (Low)").
func (antigravityAgent) validModel(string) bool { return true }

func (antigravityAgent) checkPick(p config.RelayProfile) error {
	return CheckAntigravityEffort(p.Model, p.Effort)
}

// resolve reads a model given by label or variant id as its variant id,
// and its variant as the effort when none is given.
func (antigravityAgent) resolve(model, effort string) (string, string) {
	if model != "" {
		id, variant := agyModelVariant(model)
		return id, cmp.Or(effort, variant)
	}
	return resolveDefaults(antigravityAgent{}, model, effort)
}

// trimModelLabel drops the variant from "Gemini 3.8 Flash (High)" when the
// label says the effort anyway.
func (antigravityAgent) trimModelLabel(label, effort string) string {
	if effort != "" {
		if stem, suffix, ok := strings.Cut(label, " ("); ok && slices.Contains(standardEfforts, strings.ToLower(strings.TrimSuffix(suffix, ")"))) {
			return stem
		}
	}
	return label
}

func antigravityDefaults() (string, string) {
	var settings struct{ Model string }
	raw, err := os.ReadFile(filepath.Join(antigravityHome(), "settings.json"))
	if err != nil || json.Unmarshal(raw, &settings) != nil || settings.Model == "" {
		return "", ""
	}
	return agyModelVariant(settings.Model)
}

// CheckAntigravityEffort catches unavailable variants before a settings
// change stops the current relay. Leave unknown or unlisted models to agy.
func CheckAntigravityEffort(model, effort string) error {
	if effort == "" {
		return nil
	}
	if model == "" {
		model, _ = antigravityDefaults()
	}
	models := antigravityModels()
	for _, m := range models {
		if model != m.ID && model != m.Label {
			continue
		}
		var family, tier string
		index := strings.LastIndexByte(m.ID, '-')
		if index >= 0 {
			family, tier = m.ID[:index], m.ID[index+1:]
		}
		if !slices.Contains(standardEfforts, tier) {
			return fmt.Errorf("%s has fixed reasoning effort", m.Label)
		}
		var choices []string
		for _, candidate := range models {
			if suffix, ok := strings.CutPrefix(candidate.ID, family+"-"); ok && slices.Contains(standardEfforts, suffix) {
				if suffix == effort {
					return nil
				}
				choices = append(choices, suffix)
			}
		}
		return fmt.Errorf("%s offers reasoning efforts %s", family, strings.Join(choices, ", "))
	}
	return nil
}

func agyModelVariant(model string) (string, string) {
	for _, m := range antigravityModels() {
		if model == m.Label || model == m.ID {
			for _, effort := range standardEfforts {
				if strings.HasSuffix(m.ID, "-"+effort) {
					return m.ID, effort
				}
			}
			return m.ID, ""
		}
	}
	return model, ""
}
