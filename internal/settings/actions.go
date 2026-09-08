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
		{Operation: "route.test", Label: "Test saved model route", Class: "cost", Parameters: object(map[string]control.Shape{"actor": choices([]string{"orchestrator", "task", "narrator"}), "route": choices([]string{"primary", "fallback"}), "effort": text(32)}, "actor")},
		{Operation: "prompt.set", Label: "Save prompt override", Class: "preference", Parameters: object(map[string]control.Shape{"name": choices(prompts.Names), "text": text(32768)}, "name", "text")},
		{Operation: "prompt.reset", Label: "Restore built-in prompt", Class: "preference", Parameters: object(map[string]control.Shape{"name": choices(prompts.Names)}, "name")},
		{Operation: "bundle.save", Label: "Save named configuration", Class: "permissions", Parameters: object(map[string]control.Shape{"name": text(64), "description": text(1024), "from": text(64), "config_json": text(32768)}, "name")},
		{Operation: "bundle.delete", Label: "Delete named configuration", Class: "permissions", Parameters: object(map[string]control.Shape{"name": text(64)}, "name")},
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
	if p.ExpectedVersion != view.Snapshot.Version || p.Generation != "" {
		return view, &config.ErrConflict{ETag: view.Snapshot.Version}
	}
	return view, nil
}

func (s *Service) PrepareResource(editor *config.Editor, p control.Proposal, grant control.Grant) (ResourceChange, error) {
	var change ResourceChange
	if _, err := s.ValidateAction(p, grant); err != nil {
		return change, err
	}
	name, _ := p.Parameters["name"].(string)
	change.Name = name
	switch p.Operation {
	case "prompt.set", "prompt.reset":
		change.Kind = "prompt"
		if !slices.Contains(prompts.Names, name) {
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
			cfg, err := s.BundleConfig(request)
			if err != nil {
				return change, err
			}
			change.After, err = config.BundleBytes(name, request.Description, cfg)
			if err != nil {
				return change, err
			}
			change.AfterExists = true
		} else {
			res := config.Resolve(s.Project, s.Preset, s.Bundle)
			if res.Active.Kind == "bundle" && res.Active.Name == name || res.Effective.DefaultConfig == name {
				return change, fmt.Errorf("select a different default before deleting its bundle")
			}
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
