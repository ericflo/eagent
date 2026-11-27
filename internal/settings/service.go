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
	"sync"
)

type Service struct{ Project, Preset, Bundle string }

// host gives the configuration this process's host starts and resumes
// sessions on (web.Server's loadConfig), once it registered one.
var (
	hostMu sync.Mutex
	host   func(project string) (config.Config, error)
)

// SetHost registers the configuration the host starts sessions on and
// returns the one it replaces (nil for none).
func SetHost(fn func(project string) (config.Config, error)) func(project string) (config.Config, error) {
	hostMu.Lock()
	defer hostMu.Unlock()
	previous := host
	host = fn
	return previous
}

// Runs is the configuration a session started for the project runs on:
// its host's, else the project's own (with this service's preset and
// bundle). What the phone is offered to start, and the saved preset it may
// not delete, follow it.
func (s *Service) Runs() config.Config {
	hostMu.Lock()
	fn := host
	hostMu.Unlock()
	if fn != nil {
		if cfg, err := fn(s.Project); err == nil {
			return cfg
		}
	}
	if cfg, err := config.LoadBundle(s.Project, s.Preset, s.Bundle); err == nil {
		return cfg
	}
	return config.Defaults()
}

// inForce says whether sessions start on the saved preset name, so it is
// not to be deleted: by the loader's rule, or as the host's bundle.
func (s *Service) inForce(name string) bool {
	return config.IsDefaultBundle(config.Resolve(s.Project, s.Preset, s.Bundle), name) || name != "" && s.Runs().Name == name
}

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

// CheckRemoteRoutes is CheckRoutes for a configuration an edit from a
// connector would put in force, before being the one in force now. Beyond
// known hosts and known key names, each route must pair them the way the
// local user has: no key, the address's own provider's key variable, or a
// pairing of that address and variable already in force or in the saved
// files. So a remote edit never sends one provider's key to another, in one
// edit or several, nor by moving an address while the environment pins the
// key variable.
func (s *Service) CheckRemoteRoutes(cfg, before config.Config) error {
	if err := s.CheckRoutes(cfg); err != nil {
		return err
	}
	type pair struct{ host, key string }
	trusted := map[pair]bool{}
	for _, a := range s.savedRoutes() {
		trusted[pair{strings.TrimRight(a.BaseURL, "/"), a.APIKeyEnv}] = true
	}
	for _, a := range configRoutes(before) {
		for cur := &a; cur != nil; cur = cur.Fallback {
			trusted[pair{strings.TrimRight(cur.BaseURL, "/"), cur.APIKeyEnv}] = true
		}
	}
	for name, a := range configRoutes(cfg) {
		for cur := &a; cur != nil; cur = cur.Fallback {
			host := strings.TrimRight(cur.BaseURL, "/")
			if cur.APIKeyEnv == "" || trusted[pair{host, cur.APIKeyEnv}] {
				continue
			}
			pr, ok := config.ProviderForURLFor(s.Project, cur.BaseURL)
			if ok && (pr.Custom || pr.KeyEnv == cur.APIKeyEnv) {
				continue // a registered server's key CheckRoutes has checked
			}
			who := cur.BaseURL
			if ok {
				who = pr.Label
			}
			why := fmt.Sprintf("pairing them is done in eagent's own web UI or %s", config.File(s.Project))
			for ptr, v := range config.EnvOverrides() {
				if strings.HasSuffix(ptr, "/api_key_env") {
					why = "$" + v + " sets the key variable in eagent's shell, so the address cannot move from here"
				}
			}
			return fmt.Errorf("%s: %s is not the key variable for %s; %s", name, cur.APIKeyEnv, who, why)
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
	// Loaded is the configuration the form was loaded with, before the
	// user's changes; omitted, it is the one in force. A window the form
	// still shows as loaded is not the user's entry (SaveLocked).
	Loaded json.RawMessage `json:"loaded,omitempty"`
}

type EnvField struct {
	Pointer string `json:"pointer"`
	Env     string `json:"env"`
}

type Result struct {
	Saved      config.SaveResult `json:"saved"`
	Shadowed   []EnvField        `json:"shadowed_by_env,omitempty"` // fields written but overridden by the environment
	Skipped    []EnvField        `json:"skipped_by_env,omitempty"`  // fields not written because the environment overrides them; also a window following the server a variable moves its route to, which a request without loaded does not set
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
	// A route's window follows a model a variable moved it to, so it is
	// that variable's too: the window of the environment's model is not
	// written. A variable naming the model the route already has moved
	// nothing (Resolve shows the model as the layers'), and the window
	// stays the user's.
	var res *config.Resolution
	resolved := func() *config.Resolution {
		if res == nil {
			r := config.Resolve(s.Project, s.Preset, s.Bundle)
			res = &r
		}
		return res
	}
	for _, route := range []string{"/model", "/summarizer/model"} {
		name, ok := env[route+"/model"]
		if !ok {
			continue
		}
		if moved, _ := resolved().EnvMoved(route); moved {
			if _, has := env[route+"/context_tokens"]; !has {
				env[route+"/context_tokens"] = name
			}
		}
	}
	var onDisk map[string]json.RawMessage
	readDisk := func() map[string]json.RawMessage {
		if onDisk == nil {
			onDisk = map[string]json.RawMessage{}
			if raw, err := boundedfile.Read(config.File(s.Project), 1<<20); err == nil {
				_ = json.Unmarshal(raw, &onDisk)
			}
			onDisk, _ = config.MigrateLegacy(onDisk)
		}
		return onDisk
	}
	// A window no variable owns, on a route whose model or address a
	// variable pins, is the file's to keep: the form shows what the
	// environment makes of the route (another server's window, or one a
	// list has since changed), not what the file says. One the user
	// entered (it differs from the one the form was loaded with) is
	// written, and holds as a window the file names; one left as loaded,
	// with the route's model, is written as the file has it. A form that
	// does not say what it was loaded with is compared with what is in
	// force, except on a route an address variable moved: there a server's
	// list may have changed since the form was read, so its window is
	// taken as left as loaded, and one it carries that differs from the
	// window in force is reported skipped as that variable's.
	route := func(c *config.Config, ptr string) *config.ModelRoute {
		if ptr == "/model" {
			return &c.Model
		}
		return c.Summarizer.Model
	}
	var loaded config.Config
	haveLoaded := len(b.Loaded) > 0 && string(b.Loaded) != "null" && json.Unmarshal(b.Loaded, &loaded) == nil
	for _, ptr := range []string{"/model", "/summarizer/model"} {
		_, model := env[ptr+"/model"]
		_, addr := env[ptr+"/base_url"]
		if _, owned := env[ptr+"/context_tokens"]; owned || !model && !addr {
			continue
		}
		was := &resolved().Effective
		if haveLoaded {
			was = &loaded
		}
		want, then := route(&cfg, ptr), route(was, ptr)
		if want == nil || then == nil || want.Model != then.Model {
			continue
		}
		unsaid, name := false, env[ptr+"/base_url"]
		if !haveLoaded && addr {
			res := resolved()
			_, moved := res.EnvMoved(ptr)
			unsaid = moved && route(&res.Layered, ptr) != nil
		}
		if want.ContextTokens != then.ContextTokens && !unsaid {
			continue
		}
		segs := append(strings.Split(strings.TrimPrefix(ptr, "/"), "/"), "context_tokens")
		fileVal, fileHas := pointerIn(readDisk(), segs)
		keepFromFile(edits, segs, fileVal, fileHas)
		if unsaid && want.ContextTokens != then.ContextTokens {
			skipped = append(skipped, EnvField{ptr + "/context_tokens", name})
		}
	}
	if len(env) > 0 && !b.AllowShadow {
		onDisk := readDisk()
		// With no summarizer route written, the one in effect is made by the
		// environment from the main model's: it is the environment's as a
		// whole, and none of it is written.
		if v, written := pointerIn(onDisk, []string{"summarizer", "model"}); !written || string(v) == "null" {
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
				touched = keepFromFile(edits, segs, fileVal, fileHas)
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

// keepFromFile puts the file's value of a field inside one of edits'
// objects (segs, two or more segments) in place of the editor's, or
// removes the editor's when the file has none. It reports whether edits
// had a value there.
func keepFromFile(edits map[string]json.RawMessage, segs []string, fileVal json.RawMessage, fileHas bool) bool {
	var obj map[string]json.RawMessage
	if v, present := edits[segs[0]]; present && v != nil {
		_ = json.Unmarshal(v, &obj)
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	touched := replaceIn(obj, segs[1:], fileVal, fileHas)
	if len(obj) == 0 {
		if _, present := edits[segs[0]]; present {
			edits[segs[0]] = nil
		}
	} else {
		edits[segs[0]], _ = json.Marshal(obj)
	}
	return touched
}

// pointerIn reads a pointer, split into its segments, out of a raw JSON
// object.
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
	if probe == nil {
		return Result{}, &Error{Status: 422, Message: "the configuration must be a JSON object, not null"}
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
