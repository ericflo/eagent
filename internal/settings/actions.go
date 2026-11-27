package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

func (s *Service) Actions(grant control.Grant) []control.Action {
	text := func(max int) control.Shape { return control.Shape{Type: "string", MaxLength: max} }
	choices := func(values []string) control.Shape {
		shape := text(120)
		for _, value := range values {
			shape.Enum = append(shape.Enum, value)
		}
		return shape
	}
	object := func(properties map[string]control.Shape, required ...string) control.Shape {
		return control.Shape{Type: "object", Properties: properties, Required: required}
	}
	actions := []control.Action{
		{Operation: "settings.undo", Label: "Undo reviewed settings change", Class: "preference", Parameters: object(map[string]control.Shape{"command_id": text(80), "restore_sha256": text(64)}, "command_id", "restore_sha256")},
		{Operation: "route.test", Label: "Test saved model route", Class: "cost", Parameters: object(map[string]control.Shape{"target": choices([]string{"model", "summarizer"}), "route": choices([]string{"primary", "fallback"}), "effort": text(32)}, "target")},
		{Operation: "prompt.set", Label: "Save prompt override", Class: "preference", Parameters: object(map[string]control.Shape{"name": choices(prompts.Names), "text": text(32768)}, "name", "text")},
		// A retired prompt's override can be reset (deleted), never set.
		{Operation: "prompt.reset", Label: "Restore built-in prompt", Class: "preference", Parameters: object(map[string]control.Shape{"name": choices(append(append([]string{}, prompts.Names...), prompts.Retired...))}, "name")},
		{Operation: "bundle.save", Label: "Save named configuration", Class: "permissions", Parameters: object(map[string]control.Shape{"name": text(64), "description": text(1024), "from": text(64), "config_json": text(32768)}, "name")},
		{Operation: "bundle.delete", Label: "Delete named configuration", Class: "permissions", Parameters: object(map[string]control.Shape{"name": text(64)}, "name")},
		// Starting a session spends model credit and runs commands on the
		// machine, so it is a cost-class action like a route test.
		s.sessionStartAction(text, choices, object),
	}
	var out []control.Action
	for _, action := range actions {
		if slices.Contains(grant.Operations, action.Operation) && slices.Contains(grant.Classes, action.Class) {
			out = append(out, action)
		}
	}
	return out
}

// ResourceChange is an exact, journalable file intent. Kind/name are resolved
// through Editor.RelatedPath again at execution, including journal recovery.
type ResourceChange struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Before       []byte `json:"before"`
	BeforeExists bool   `json:"before_exists"`
	After        []byte `json:"after"`
	AfterExists  bool   `json:"after_exists"`
}

func (s *Service) ValidateAction(p control.Proposal, grant control.Grant) (RemoteView, error) {
	view, err := s.RemoteSnapshot(grant)
	if err != nil {
		return view, err
	}
	if err := p.Validate(view.Descriptor, grant); err != nil {
		return view, err
	}
	if p.Generation != "" {
		return view, &config.ErrConflict{ETag: view.Snapshot.Version}
	}
	// Starting a session reads the configuration but does not change it, so
	// a settings save that landed after the phone loaded its page is no
	// reason to refuse; every other action targets the exact reviewed version.
	if p.Operation != "session.start" && p.ExpectedVersion != view.Snapshot.Version {
		return view, &config.ErrConflict{ETag: view.Snapshot.Version}
	}
	return view, nil
}

func (s *Service) PrepareResource(editor *config.Editor, p control.Proposal, grant control.Grant) (ResourceChange, error) {
	return s.prepareResource(editor, p, grant, nil)
}

// restored, for an undo, is the bundle bytes eagent's own record kept.
func (s *Service) prepareResource(editor *config.Editor, p control.Proposal, grant control.Grant, restored []byte) (ResourceChange, error) {
	var change ResourceChange
	if _, err := s.ValidateAction(p, grant); err != nil {
		return change, err
	}
	name, _ := p.Parameters["name"].(string)
	change.Name = name
	switch p.Operation {
	case "prompt.set", "prompt.reset":
		change.Kind = "prompt"
		retired := slices.Contains(prompts.Retired, name)
		if !slices.Contains(prompts.Names, name) && !(retired && p.Operation == "prompt.reset") {
			return change, fmt.Errorf("unknown prompt")
		}
		if p.Operation == "prompt.set" {
			text, _ := p.Parameters["text"].(string)
			if err := ValidatePrompt(name, text); err != nil {
				return change, err
			}
			change.After, change.AfterExists = []byte(text), true
		}
	case "bundle.save", "bundle.delete":
		change.Kind = "bundle"
		if p.Operation == "bundle.save" {
			request := BundleRequest{Name: name}
			request.Description, _ = p.Parameters["description"].(string)
			request.From, _ = p.Parameters["from"].(string)
			if raw, ok := p.Parameters["config_json"].(string); ok && raw != "" {
				request.Config = json.RawMessage(raw)
			}
			if len(restored) > 0 {
				// An undo puts back eagent's own bytes, which may be a
				// bundle main saved: they are checked in today's keys (the
				// undo writes the bytes themselves back).
				var m map[string]json.RawMessage
				if json.Unmarshal(restored, &m) == nil {
					today, _ := config.MigrateLegacy(m)
					request.Config, _ = json.Marshal(today)
				}
			}
			cfg, err := s.BundleConfig(request)
			if err != nil {
				return change, err
			}
			// A bundle can become the default from here, so its routes pair
			// keys with addresses only as a remote edit may.
			if err := s.CheckRemoteRoutes(cfg, config.Resolve(s.Project, s.Preset, s.Bundle).Effective); err != nil {
				return change, err
			}
			if err := s.checkRemoteProfiles(cfg, restored); err != nil {
				return change, err
			}
			change.After, err = config.BundleBytes(name, request.Description, cfg)
			if err != nil {
				return change, err
			}
			if len(restored) > 0 {
				change.After = restored // as it was, byte for byte
			}
			change.AfterExists = true
		} else if err := s.inForce(name); err != nil {
			return change, err
		}
	default:
		return change, fmt.Errorf("operation does not target a prompt or bundle")
	}
	path, err := editor.RelatedPath(change.Kind, change.Name)
	if err != nil {
		return change, err
	}
	change.Before, err = boundedfile.Read(path, 1<<20)
	if err != nil && !os.IsNotExist(err) {
		return change, err
	}
	change.BeforeExists = err == nil
	if len(change.Before) > 1<<20 {
		return change, fmt.Errorf("resource exceeds 1 MiB")
	}
	return change, nil
}

func (change ResourceChange) State(editor *config.Editor) (before, after bool, err error) {
	path, err := editor.RelatedPath(change.Kind, change.Name)
	if err != nil {
		return false, false, err
	}
	raw, err := boundedfile.Read(path, 1<<20)
	if err != nil && !os.IsNotExist(err) {
		return false, false, err
	}
	exists := err == nil
	digest := artifact.Digest(raw)
	return exists == change.BeforeExists && digest == artifact.Digest(change.Before), exists == change.AfterExists && digest == artifact.Digest(change.After), nil
}

// CheckResource holds a prepared change to the guards again when it is
// written: deleting the saved preset sessions start on is refused, also
// when the command was prepared before that preset became the default.
func (s *Service) CheckResource(change ResourceChange) error {
	if change.Kind == "bundle" && !change.AfterExists {
		return s.inForce(change.Name)
	}
	return nil
}

func (change ResourceChange) Apply(editor *config.Editor) error {
	before, after, err := change.State(editor)
	if err != nil || after {
		return err
	}
	if !before {
		return &config.ErrConflict{}
	}
	if change.Kind == "prompt" && change.AfterExists {
		if err := ValidatePrompt(change.Name, string(change.After)); err != nil {
			return err
		}
	}
	_, err = editor.WriteRelated(change.Kind, change.Name, change.After, !change.AfterExists)
	return err
}

func ResourceEffect(change ResourceChange) map[string]any {
	return map[string]any{"key": strings.Join([]string{change.Kind, change.Name}, ":"), "saved": true, "runtime_applied": false, "effective_when": "new_or_resumed_session"}
}

// relayChoices are the values session.start accepts for relay: the relay
// profile names of the configuration sessions start on.
func (s *Service) relayChoices() []string {
	return s.Runs().Relay.RelayProfileNames()
}

// RelayInput names the session.start input for a relay profile's model or
// effort ("relay_codex_model"), "" for a profile whose name cannot be one.
func RelayInput(profile, field string) string {
	if profile == "" || len(profile) > 40 || strings.Trim(profile, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return ""
	}
	return "relay_" + profile + "_" + field
}

// relayListBudget bounds one agent's model list in a settings document
// (256 KiB in all), by the same measure as ModelSuggestions' budget.
const relayListBudget = 16 << 10

// RelayModelSuggestions are the models an agent's relay can run, for its
// model inputs and fields (any other name can still be typed).
func RelayModelSuggestions(agent, project string) []control.Suggestion {
	out := []control.Suggestion{}
	budget := relayListBudget
	for _, m := range relay.AgentModels(agent, project) {
		if len(m.ID) > 500 {
			continue
		}
		sug := control.Suggestion{Value: m.ID, Label: clip(m.Label, 200), Detail: clip(m.Detail, 300)}
		if budget -= suggestionSize(sug); budget < 0 {
			break
		}
		out = append(out, sug)
	}
	return out
}

// relayStartInputs are each relay profile's model and effort for a session
// started with it, asked only when that profile is chosen. Left alone, the
// agent runs what the profile names, else its own configured default; each
// input's default shows which.
func (s *Service) relayStartInputs(text func(int) control.Shape, choices func([]string) control.Shape, params map[string]control.Shape) []control.Input {
	cfg := s.Runs()
	profiles := cfg.Relay.RelayProfiles()
	var inputs []control.Input
	for _, name := range cfg.Relay.RelayProfileNames() {
		p := profiles[name]
		modelName, effortName := RelayInput(name, "model"), RelayInput(name, "effort")
		efforts := relay.AgentEfforts(p.Agent)
		if modelName == "" || len(efforts) == 0 {
			continue
		}
		who := relay.AgentName(p.Agent)
		group := who
		if name != p.Agent {
			group = who + " (" + name + ")"
		}
		defaultModel, defaultEffort := relay.ProfileDefaults(p.Agent, p.Model, p.Effort)
		when := map[string][]any{"relay": {name}}
		params[modelName] = text(200)
		params[effortName] = choices(efforts)
		model := control.Input{Name: modelName, Label: "Model", Group: group, When: when, Suggest: &control.Suggest{List: relay.ModelListName(p.Agent)},
			Description: "Left alone, " + who + " uses the model in its own configuration."}
		effort := control.Input{Name: effortName, Label: "Reasoning effort", Group: group, When: when,
			Description: "Left alone, " + who + " uses the effort in its own configuration."}
		if defaultModel != "" && len(defaultModel) <= 200 {
			model.Default = defaultModel
		}
		if slices.Contains(efforts, defaultEffort) {
			effort.Default = defaultEffort
		}
		if p.Model != "" {
			model.Description = "Left alone, the " + name + " profile's model."
		}
		if p.Effort != "" {
			effort.Description = "Left alone, the " + name + " profile's effort."
		}
		inputs = append(inputs, model, effort)
	}
	return inputs
}

// sessionStartAction declares what a session started from the phone may
// choose: the first message, where it starts, the agent it runs, and that
// agent's model and effort. Each choice left out comes from the project's
// own configuration (or the agent's), which each input's default shows.
func (s *Service) sessionStartAction(text func(int) control.Shape, choices func([]string) control.Shape, object func(map[string]control.Shape, ...string) control.Shape) control.Action {
	params := map[string]control.Shape{"prompt": text(32768), "cwd": text(4096), "relay": choices(s.relayChoices())}
	inputs := s.relayStartInputs(text, choices, params)
	return control.Action{Operation: "session.start", Label: "Start a new session", Class: "cost", Parameters: object(params, "prompt"), Inputs: inputs}
}
