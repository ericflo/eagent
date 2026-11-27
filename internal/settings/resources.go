package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"text/template"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/control"
)

type BundleRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	From        string          `json:"from"`
	Config      json.RawMessage `json:"config"`
}

// BundleConfig uses the same endpoint/key and configuration validation for
// local forms and remote actions. It has no network or filesystem side effects.
func (s *Service) BundleConfig(b BundleRequest) (config.Config, error) {
	var cfg config.Config
	if len(b.Config) > 0 && string(b.Config) != "null" {
		cfg = config.Defaults()
		// A bundle builds on the preset it names, as it loads (config.BundleFromBytes).
		var named struct {
			Preset string `json:"preset"`
		}
		layer := false
		if json.Unmarshal(b.Config, &named) == nil {
			if canon, ok := config.ResolvePreset(named.Preset); ok {
				config.Presets[canon](&cfg)
				layer = true
			}
		}
		dec := json.NewDecoder(strings.NewReader(string(b.Config)))
		dec.DisallowUnknownFields()
		into := &cfg
		if layer {
			into = new(config.Config) // checked here, laid below
		}
		if err := dec.Decode(into); err != nil {
			return cfg, &Error{Status: 400, Message: "config: " + err.Error()}
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(b.Config, &m) == nil {
			if layer {
				// Over the preset it names, laid as the loader lays it: a
				// route it moves to another model or address takes the
				// window there unless it names one (followModel).
				if err := config.Layer(s.Project, &cfg, m, "bundle "+b.Name); err != nil {
					return cfg, &Error{Status: 400, Message: err.Error()}
				}
			} else {
				// A whole configuration (the editor's) over the defaults:
				// a field the editor left empty is written as null, which
				// the decode above leaves at the default's value.
				config.ClearNulls(&cfg, m)
			}
		}
		cfg.Name, cfg.DefaultConfig, cfg.Instructions = "", "", ""
	} else {
		// What that bundle says over the files, never a value only a
		// variable gives.
		res := config.Resolve(s.Project, "", b.From)
		if res.LoadError != "" {
			return cfg, &Error{Status: 400, Message: res.LoadError}
		}
		cfg = res.Layered
	}
	if err := cfg.Validate(); err != nil {
		return cfg, &Error{Status: 422, Message: err.Error()}
	}
	if err := s.CheckRoutes(cfg); err != nil {
		return cfg, &Error{Status: 422, Message: err.Error()}
	}
	return cfg, nil
}

// CheckRemoteProfiles refuses agent profiles in a configuration a connector
// would save, unless this machine already defines each one exactly, under
// the same name: in config.json, a saved bundle, the configuration in force
// or the built-in ones. A profile names a program and its arguments, so a
// connector may not write one; the remote settings editor never offers
// them for the same reason. Copying a saved bundle (from) or restoring its
// bytes keeps working, since its profiles are already on this machine.
func (s *Service) CheckRemoteProfiles(cfg config.Config) error {
	return s.checkRemoteProfiles(cfg, nil)
}

// checkRemoteProfiles also knows the profiles in restored: bundle bytes an
// undo puts back, taken from the record eagent kept of what was on this
// machine before, never from the caller.
func (s *Service) checkRemoteProfiles(cfg config.Config, restored []byte) error {
	if len(cfg.Relay.Profiles) == 0 {
		return nil
	}
	known := []map[string]config.RelayProfile{config.BuiltinRelayProfiles(), config.Resolve(s.Project, s.Preset, s.Bundle).Effective.Relay.Profiles}
	if len(restored) > 0 {
		var prior struct {
			Relay config.Relay `json:"relay"`
		}
		if json.Unmarshal(restored, &prior) == nil {
			known = append(known, prior.Relay.Profiles)
		}
	}
	if c, err := config.LoadBundle(s.Project, "", ""); err == nil {
		known = append(known, c.Relay.Profiles)
	}
	names, _ := config.ListBundles(s.Project)
	for _, n := range names {
		if c, err := config.LoadBundle(s.Project, "", n); err == nil {
			known = append(known, c.Relay.Profiles)
		}
	}
	for name, p := range cfg.Relay.Profiles {
		if !slices.ContainsFunc(known, func(m map[string]config.RelayProfile) bool {
			have, ok := m[name]
			return ok && reflect.DeepEqual(have, p)
		}) {
			return &Error{Status: 422, Message: fmt.Sprintf("relay.profiles.%s: an agent profile names a program to run, so it is added or changed on this machine (eagent's web UI or %s), not from a connector", name, config.File(s.Project))}
		}
	}
	return nil
}

// PrepareRestore is PrepareResource for an undo that puts back a bundle's
// bytes from eagent's own record of the command it reverses (restored).
// Agent profiles those bytes carry were on this machine, so they are
// allowed; everything else is held to today's policy as usual. The caller
// passes the record's bytes, never anything the request carried.
func (s *Service) PrepareRestore(editor *config.Editor, inverse control.Proposal, grant control.Grant, restored []byte) (ResourceChange, error) {
	if raw, _ := inverse.Parameters["config_json"].(string); inverse.Operation != "bundle.save" || raw != string(restored) {
		restored = nil
	}
	return s.prepareResource(editor, inverse, grant, restored)
}

func (s *Service) SaveBundle(ctx context.Context, b BundleRequest) (string, error) {
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return "", err
	}
	defer editor.Close()
	cfg, err := s.BundleConfig(b)
	if err != nil {
		return "", err
	}
	path, err := editor.SaveBundle(strings.TrimSpace(b.Name), strings.TrimSpace(b.Description), cfg)
	if err != nil {
		return "", &Error{Status: 400, Message: err.Error()}
	}
	return path, nil
}

func (s *Service) DeleteBundle(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if !config.ValidBundleName(name) {
		return &Error{Status: 400, Message: "bundle names use letters, digits, '-', '_' and '.' only"}
	}
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return err
	}
	defer editor.Close()
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	if res.Active.Kind == "bundle" && res.Active.Name == name || res.Effective.DefaultConfig == name {
		return &Error{Status: 409, Message: "select a different default before deleting its preset"}
	}
	path, err := editor.RelatedPath("bundle", name)
	if err != nil {
		return &Error{Status: 400, Message: err.Error()}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &Error{Status: 404, Message: fmt.Sprintf("no saved preset named %q", name)}
	}
	_, err = editor.WriteRelated("bundle", name, nil, true)
	return err
}

// PromptName resolves a prompt's name as the editors spell it. A retired
// prompt's name resolves too, so its override can be deleted; SavePrompt
// refuses to write one.
func PromptName(raw string) (string, error) {
	name := strings.ToUpper(strings.TrimSuffix(raw, ".md")) + ".md"
	if slices.Contains(prompts.Names, name) || slices.Contains(prompts.Retired, name) {
		return name, nil
	}
	return "", &Error{Status: 404, Message: fmt.Sprintf("unknown prompt %s", raw)}
}

func ValidatePrompt(name, text string) error {
	if !slices.Contains(prompts.Names, name) {
		return &Error{Status: 404, Message: "unknown prompt"}
	}
	if strings.TrimSpace(text) == "" || len(text) > 1<<20 {
		return &Error{Status: 400, Message: "text is required and must be at most 1 MiB"}
	}
	if _, err := template.New(name).Parse(text); err != nil {
		return &Error{Status: 400, Message: "template error: " + err.Error()}
	}
	return nil
}

func (s *Service) SavePrompt(ctx context.Context, name, text string, remove bool) (string, error) {
	name, err := PromptName(name)
	if err != nil {
		return "", err
	}
	if !remove && slices.Contains(prompts.Retired, name) {
		return "", &Error{Status: 400, Message: name + " is no longer read; its override can only be deleted"}
	}
	if !remove {
		if err := ValidatePrompt(name, text); err != nil {
			return "", err
		}
	}
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return "", err
	}
	defer editor.Close()
	return editor.WriteRelated("prompt", name, []byte(text), remove)
}
