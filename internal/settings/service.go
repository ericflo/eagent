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

// KnownKeyEnvs is the closed list of environment variable names whose
// presence the UI may learn: the catalog's, registered custom servers', plus
// any already written into the project's configuration or bundles. Never a
// name typed in a request.
func (s *Service) KnownKeyEnvs() []string {
	seen := map[string]bool{}
	for _, k := range config.KeyEnvsFor(s.Project) {
		seen[k] = true
	}
	for _, a := range s.savedRoutes() {
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

// configRoutes are the model routes a configuration names: the model's and,
// when one is written, the summarizer's.
func configRoutes(cfg config.Config) map[string]config.ModelRoute {
	out := map[string]config.ModelRoute{"model": cfg.Model}
	if cfg.Summarizer.Model != nil {
		out["summarizer"] = *cfg.Summarizer.Model
	}
	return out
}

// savedRoutes lists every route (and fallback) already on disk in the
// project file or its bundles; their hosts and key names are trusted.
func (s *Service) savedRoutes() []config.ModelRoute {
	var out []config.ModelRoute
	collect := func(cfg config.Config) {
		for _, a := range configRoutes(cfg) {
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

// CheckRoutes refuses hosts and key names that neither the catalog, registered
// servers, nor the saved configuration knows: custom endpoints must be
// registered explicitly (via servers.json, CLI, or local UI) or written to
// config.json, never smuggled in via a config save or remote edit.
func (s *Service) CheckRoutes(cfg config.Config) error {
	saved := s.savedRoutes()
	known := func(a config.ModelRoute) error {
		trimmed := strings.TrimRight(a.BaseURL, "/")
		if pr, ok := config.ProviderForURLFor(s.Project, a.BaseURL); ok && pr.Custom {
			// A registered custom/local server may use no key ("") or its own
			// registered KeyEnv (or one already saved on that same host), never
			// an unrelated cloud provider's API key variable.
			if a.APIKeyEnv == "" || a.APIKeyEnv == pr.KeyEnv {
				return nil
			}
			for _, sa := range saved {
				if strings.TrimRight(sa.BaseURL, "/") == trimmed && sa.APIKeyEnv == a.APIKeyEnv {
					return nil
				}
			}
			return fmt.Errorf("%s is not a key variable registered for %s; update the server registration or edit %s", a.APIKeyEnv, pr.Label, config.File(s.Project))
		}
		hostOK := config.KnownBaseURL(a.BaseURL)
		keyOK := a.APIKeyEnv == "" || config.KnownKeyEnv(a.APIKeyEnv)
		for _, sa := range saved {
			if strings.TrimRight(sa.BaseURL, "/") == trimmed {
				hostOK = true
			}
			if sa.APIKeyEnv == a.APIKeyEnv {
				keyOK = true
			}
		}
		if !hostOK {
			return fmt.Errorf("%s is not a catalog provider or registered server; register it or edit %s (or EAGENT_*_BASE_URL)", a.BaseURL, config.File(s.Project))
		}
		if !keyOK {
			return fmt.Errorf("%s is not a key variable the catalog or your saved configuration uses; custom key names are added by editing %s", a.APIKeyEnv, config.File(s.Project))
		}
		return nil
	}
	for name, a := range configRoutes(cfg) {
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
		onDisk, _ = config.MigrateLegacy(onDisk)
		// With no summarizer route written, the one in effect is made by the
		// environment from the main model's: it is the environment's as a
		// whole, and none of it is written.
		if _, written := pointerIn(onDisk, []string{"summarizer", "model"}); !written {
			name := ""
			for ptr, v := range env {
				if strings.HasPrefix(ptr, "/summarizer/model/") {
					if name == "" || v < name {
						name = v
					}
					delete(env, ptr)
				}
			}
			if name != "" {
				env["/summarizer/model"] = name
			}
		}
		var ptrs []string
		for ptr := range env {
			ptrs = append(ptrs, ptr)
		}
		sort.Strings(ptrs)
		for _, ptr := range ptrs {
			segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
			fileVal, fileHas := pointerIn(onDisk, segs)
			touched := false
			if len(segs) == 1 {
				if v, present := edits[segs[0]]; present && v != nil {
					touched = true
				}
				if fileHas {
					edits[segs[0]] = fileVal
				} else {
					delete(edits, segs[0])
				}
			} else {
				var obj map[string]json.RawMessage
				if v, present := edits[segs[0]]; present && v != nil {
					_ = json.Unmarshal(v, &obj)
				}
				if obj == nil {
					obj = map[string]json.RawMessage{}
				}
				touched = replaceIn(obj, segs[1:], fileVal, fileHas)
				if len(obj) == 0 {
					if _, present := edits[segs[0]]; present {
						edits[segs[0]] = nil
					}
				} else {
					edits[segs[0]], _ = json.Marshal(obj)
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

// pointerIn reads a pointer, split into its segments, out of a raw JSON
// object.
func pointerIn(m map[string]json.RawMessage, segs []string) (json.RawMessage, bool) {
	v, ok := m[segs[0]]
	if !ok || v == nil || string(v) == "null" {
		return nil, false
	}
	if len(segs) == 1 {
		return v, true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(v, &obj) != nil {
		return nil, false
	}
	return pointerIn(obj, segs[1:])
}

// replaceIn removes the value at segs inside obj and, when has, puts val
// there instead, creating and dropping the objects on the way as needed. It
// reports whether obj had a value there.
func replaceIn(obj map[string]json.RawMessage, segs []string, val json.RawMessage, has bool) bool {
	key := segs[0]
	if len(segs) == 1 {
		_, had := obj[key]
		delete(obj, key)
		if has {
			obj[key] = val
		}
		return had
	}
	var child map[string]json.RawMessage
	_ = json.Unmarshal(obj[key], &child)
	if child == nil {
		if !has {
			return false
		}
		child = map[string]json.RawMessage{}
	}
	had := replaceIn(child, segs[1:], val, has)
	if len(child) == 0 {
		delete(obj, key)
	} else {
		obj[key], _ = json.Marshal(child)
	}
	return had
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
	// Check the routes as they will load: keys of eagent's earlier design
	// are carried over first.
	probe, _ = config.MigrateLegacy(probe)
	migrated, _ := json.Marshal(probe)
	trial := config.Defaults()
	if err := json.Unmarshal(migrated, &trial); err != nil {
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
