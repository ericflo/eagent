// Package settings is the shared configuration service used by the loopback
// web UI and authenticated remote connector. Transport guards live outside it.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"slices"
	"sort"
	"strings"
)

type Service struct{ Project, Preset, Bundle string }
type Error struct {
	Status   int
	Message  string
	Problems []config.Problem
}

func (e *Error) Error() string { return e.Message }

// knownKeyEnvs is the closed list of environment variable names whose
// presence the UI may learn: the catalog's, plus any already written into
// the project's configuration or bundles. Never a name typed in a request.
func (s *Service) KnownKeyEnvs() []string {
	seen := map[string]bool{}
	for _, k := range config.KeyEnvs() {
		seen[k] = true
	}
	for _, a := range s.savedActors() {
		if a.APIKeyEnv != "" {
			seen[a.APIKeyEnv] = true
		}
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// savedActors lists every actor (and fallback) already on disk in the
// project file or its bundles; their hosts and key names are trusted.
func (s *Service) savedActors() []config.Actor {
	var out []config.Actor
	collect := func(cfg config.Config) {
		for _, a := range []config.Actor{cfg.Orchestrator, cfg.Task, cfg.Narrator} {
			for cur := &a; cur != nil; cur = cur.Fallback {
				out = append(out, *cur)
			}
		}
	}
	if cfg, err := config.LoadBundle(s.Project, "", ""); err == nil {
		collect(cfg)
	}
	names, _ := config.ListBundles(s.Project)
	for _, n := range names {
		if cfg, err := config.LoadBundle(s.Project, "", n); err == nil {
			collect(cfg)
		}
	}
	return out
}

// checkRoutes refuses hosts and key names that neither the catalog nor the
// saved configuration knows: the only way to add a custom endpoint is by
// editing the file, never from a browser request.
func (s *Service) CheckRoutes(cfg config.Config) error {
	saved := s.savedActors()
	known := func(a config.Actor) error {
		hostOK := config.KnownBaseURL(a.BaseURL)
		keyOK := config.KnownKeyEnv(a.APIKeyEnv)
		for _, sa := range saved {
			if strings.TrimRight(sa.BaseURL, "/") == strings.TrimRight(a.BaseURL, "/") {
				hostOK = true
			}
			if sa.APIKeyEnv == a.APIKeyEnv {
				keyOK = true
			}
		}
		if !hostOK {
			return fmt.Errorf("%s is not a catalog provider; custom base URLs are added by editing %s (or EAGENT_*_BASE_URL), not from the browser", a.BaseURL, config.File(s.Project))
		}
		if !keyOK {
			return fmt.Errorf("%s is not a key variable the catalog or your saved configuration uses; custom key names are added by editing %s", a.APIKeyEnv, config.File(s.Project))
		}
		return nil
	}
	for name, a := range map[string]config.Actor{"orchestrator": cfg.Orchestrator, "task": cfg.Task, "narrator": cfg.Narrator} {
		for cur := &a; cur != nil; cur = cur.Fallback {
			if err := known(*cur); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	// The phone token is a credential too: the browser may not point it at a
	// new address or read it from a new variable.
	if fc := cfg.Finalechat; fc.BaseURL != "" || fc.TokenEnv != "" {
		savedFC := config.Finalechat{}
		if saved, err := config.LoadBundle(s.Project, "", ""); err == nil {
			savedFC = saved.Finalechat
		}
		if fc.BaseURL != "" && fc.BaseURL != savedFC.BaseURL {
			return fmt.Errorf("finalechat: a custom service address is added by editing %s, not from the browser", config.File(s.Project))
		}
		if fc.TokenEnv != "" && fc.TokenEnv != savedFC.TokenEnv && fc.TokenEnv != (config.Finalechat{}).TokenEnvName() {
			return fmt.Errorf("finalechat: a custom token variable is added by editing %s, not from the browser", config.File(s.Project))
		}
	}
	return nil
}

type Request struct {
	Config      json.RawMessage `json:"config"`
	BasePreset  string          `json:"base_preset"`    // the preset the file should build on; "" for none
	Mode        string          `json:"mode"`           // overlay (default) | pin
	IfMatch     *string         `json:"if_match"`       // the file etag the editor loaded; omit to skip the check
	AllowShadow bool            `json:"allow_shadowed"` // write fields an EAGENT_* variable currently overrides anyway
	Default     *string         `json:"default_config"` // set or clear default_config in the file
}

type EnvField struct {
	Pointer string `json:"pointer"`
	Env     string `json:"env"`
}

type Result struct {
	Saved      config.SaveResult `json:"saved"`
	Shadowed   []EnvField        `json:"shadowed_by_env,omitempty"` // fields written but overridden by the environment
	Skipped    []EnvField        `json:"skipped_by_env,omitempty"`  // fields not written because the environment overrides them
	Kept       []EnvField        `json:"kept_from_file,omitempty"`  // pinned fields whose saved value was carried over unchanged
	Resolution config.Resolution `json:"resolution"`
}

func (s *Service) Save(ctx context.Context, b Request) (Result, error) {
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return Result{}, err
	}
	defer editor.Close()
	return s.SaveLocked(editor, b)
}

// SaveLocked validates and writes while a caller owns the resource lock.
func (s *Service) SaveLocked(editor *config.Editor, b Request) (Result, error) {
	var cfg config.Config
	dec := json.NewDecoder(strings.NewReader(string(b.Config)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Result{}, &Error{Status: 400, Message: (fmt.Errorf("config: %w", err)).Error()}
	}
	cfg.Name, cfg.Description, cfg.Instructions, cfg.Preset, cfg.DefaultConfig = "", "", "", "", ""
	if err := s.CheckRoutes(cfg); err != nil {
		return Result{}, &Error{Status: 422, Message: (err).Error()}
	}
	problems := cfg.Problems()
	var hard []config.Problem
	for _, p := range problems {
		if p.Severity == "error" {
			hard = append(hard, p)
		}
	}
	if len(hard) > 0 {
		return Result{}, &Error{Status: 422, Message: "the configuration has problems", Problems: problems}
	}
	var edits map[string]json.RawMessage
	var err error
	if b.Mode == "pin" {
		edits = config.Pin(cfg, b.BasePreset)
	} else {
		edits, err = config.Overlay(cfg, b.BasePreset)
		if err != nil {
			return Result{}, &Error{Status: 422, Message: (err).Error()}
		}
	}
	if b.Default != nil {
		if *b.Default == "" {
			edits["default_config"] = nil
		} else {
			// Validate the way the resolver will: a listed file with a name
			// the loader rejects, or unloadable contents, would make every
			// later load fail.
			names, _ := config.ListBundles(s.Project)
			if !slices.Contains(names, *b.Default) {
				return Result{}, &Error{Status: 422, Message: (fmt.Errorf("default_config: no bundle named %q", *b.Default)).Error()}
			}
			if _, err := config.LoadBundle(s.Project, "", *b.Default); err != nil {
				return Result{}, &Error{Status: 422, Message: (fmt.Errorf("default_config %q: %w", *b.Default, err)).Error()}
			}
			edits["default_config"], _ = json.Marshal(*b.Default)
		}
	}
	// Fields the environment overrides cannot take effect from the file, so
	// the editor's value for them is not written. Whatever the file already
	// says for such a field is carried over unchanged: it is what will apply
	// once the variable is unset, and a save of something else must not lose it.
	var shadowed, skipped, kept []EnvField
	env := config.EnvOverrides()
	if len(env) > 0 && !b.AllowShadow {
		onDisk := map[string]json.RawMessage{}
		if raw, err := boundedfile.Read(config.File(s.Project), 1<<20); err == nil {
			_ = json.Unmarshal(raw, &onDisk)
		}
		var ptrs []string
		for ptr := range env {
			ptrs = append(ptrs, ptr)
		}
		sort.Strings(ptrs)
		for _, ptr := range ptrs {
			top := strings.SplitN(strings.TrimPrefix(ptr, "/"), "/", 2)
			fileVal, fileHas := pointerIn(onDisk, top)
			touched := false
			if len(top) == 1 {
				if v, present := edits[top[0]]; present && v != nil {
					touched = true
				}
				if fileHas {
					edits[top[0]] = fileVal
				} else {
					delete(edits, top[0])
				}
			} else {
				var obj map[string]json.RawMessage
				if v, present := edits[top[0]]; present && v != nil {
					_ = json.Unmarshal(v, &obj)
				}
				if _, in := obj[top[1]]; in {
					touched = true
				}
				if obj == nil {
					obj = map[string]json.RawMessage{}
				}
				delete(obj, top[1])
				if fileHas {
					obj[top[1]] = fileVal
				}
				if len(obj) == 0 {
					if _, present := edits[top[0]]; present {
						edits[top[0]] = nil
					}
				} else {
					edits[top[0]], _ = json.Marshal(obj)
				}
			}
			switch {
			case fileHas:
				kept = append(kept, EnvField{ptr, env[ptr]})
			case touched:
				skipped = append(skipped, EnvField{ptr, env[ptr]})
			}
		}
	} else if len(env) > 0 {
		for ptr, v := range env {
			shadowed = append(shadowed, EnvField{ptr, v})
		}
		sort.Slice(shadowed, func(i, j int) bool { return shadowed[i].Pointer < shadowed[j].Pointer })
	}
	ifMatch := ""
	if b.IfMatch != nil {
		ifMatch = *b.IfMatch
	}

	saved, err := editor.SaveFile(edits, ifMatch, b.IfMatch != nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Saved: saved, Shadowed: shadowed, Skipped: skipped, Kept: kept, Resolution: config.Resolve(s.Project, s.Preset, s.Bundle)}, nil
}

// pointerIn reads a one- or two-segment pointer out of a raw JSON object.
func pointerIn(m map[string]json.RawMessage, segs []string) (json.RawMessage, bool) {
	v, ok := m[segs[0]]
	if !ok || v == nil {
		return nil, false
	}
	if len(segs) == 1 {
		return v, true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(v, &obj) != nil {
		return nil, false
	}
	f, ok := obj[segs[1]]
	return f, ok && f != nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

type RawRequest struct {
	Raw     string  `json:"raw"`
	IfMatch *string `json:"if_match"`
}

func (s *Service) SaveRaw(ctx context.Context, b RawRequest) (Result, error) {
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return Result{}, err
	}
	defer editor.Close()
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b.Raw), &probe); err != nil {
		return Result{}, &Error{Status: 422, Message: "not valid JSON: " + err.Error()}
	}
	trial := config.Defaults()
	if err := json.Unmarshal([]byte(b.Raw), &trial); err != nil {
		return Result{}, &Error{Status: 422, Message: err.Error()}
	}
	if err := s.CheckRoutes(trial); err != nil {
		return Result{}, &Error{Status: 422, Message: err.Error()}
	}
	ifMatch := ""
	if b.IfMatch != nil {
		ifMatch = *b.IfMatch
	}
	saved, err := editor.SaveRaw(b.Raw, ifMatch, b.IfMatch != nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Saved: saved, Resolution: config.Resolve(s.Project, s.Preset, s.Bundle)}, nil
}
