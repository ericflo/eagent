package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
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
	var err error
	if len(b.Config) > 0 && string(b.Config) != "null" {
		cfg = config.Defaults()
		dec := json.NewDecoder(strings.NewReader(string(b.Config)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return cfg, &Error{Status: 400, Message: "config: " + err.Error()}
		}
		cfg.Name, cfg.Instructions = "", ""
	} else {
		cfg, err = config.LoadBundle(s.Project, "", b.From)
		if err != nil {
			return cfg, &Error{Status: 400, Message: err.Error()}
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, &Error{Status: 422, Message: err.Error()}
	}
	if err := s.CheckRoutes(cfg); err != nil {
		return cfg, &Error{Status: 422, Message: err.Error()}
	}
	return cfg, nil
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

func PromptName(raw string) (string, error) {
	name := strings.ToUpper(strings.TrimSuffix(raw, ".md")) + ".md"
	if slices.Contains(prompts.Names, name) {
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
