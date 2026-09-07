package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
)

// The configuration API behind the web editor. Every mutating route is
// guarded against cross-site requests, every write goes through the config
// package's atomic writer with an etag, and a live test of a route can only
// send a key to a host the catalog (or the saved configuration) already
// names, so a browser request can never point a credential at an arbitrary
// server.

// ---- request guard -----------------------------------------------------------------

// newToken makes the per-process token the page must echo on writes.
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "eagent-" + fmt.Sprint(time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// guard rejects mutating requests that did not come from this server's own
// page: the Host must be ours, an Origin (when present) must match, the
// browser's Sec-Fetch-Site must be same-origin, the body must be JSON, and
// the page's token must be echoed.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeErr(w, 403, errors.New("request Host is not this server"))
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "null" {
			u, err := url.Parse(o)
			if err != nil || !s.hostAllowed(u.Host) {
				writeErr(w, 403, errors.New("cross-origin request refused"))
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeErr(w, 403, errors.New("cross-site request refused"))
			return
		}
		if ct := r.Header.Get("Content-Type"); r.ContentLength != 0 && !strings.HasPrefix(ct, "application/json") {
			writeErr(w, 415, errors.New("send application/json"))
			return
		}
		if r.Header.Get("X-Eagent-Token") != s.token {
			writeErr(w, 403, errors.New("missing or stale page token; reload the page"))
			return
		}
		next(w, r)
	}
}

// hostAllowed accepts the loopback names and whatever address the server
// was started on.
func (s *Server) hostAllowed(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if s.addr != "" {
		ah, _, err := net.SplitHostPort(s.addr)
		if err == nil && ah != "" && ah != "0.0.0.0" && ah != "::" && strings.Trim(ah, "[]") == h {
			return true
		}
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// ---- views -----------------------------------------------------------------------------

type configView struct {
	Resolution config.Resolution `json:"resolution"`
	Presets    []bundleView      `json:"presets"`
	Bundles    []bundleView      `json:"bundles"`
	Prompts    []promptView      `json:"prompts"`
	Keys       map[string]bool   `json:"keys"` // env var -> present, for names the catalog or the saved config knows
	Project    string            `json:"project"`
	Files      map[string]string `json:"files"`
	Phone      phoneView         `json:"phone"`
	Running    []runningView     `json:"running"`
	Defaults   config.Config     `json:"defaults"`
	Server     serverView        `json:"server"`
}

type phoneView struct {
	State  string `json:"state"` // on | off | disabled
	Source string `json:"source,omitempty"`
	Detail string `json:"detail"`
}

type runningView struct {
	ID     string            `json:"id"`
	Models map[string]string `json:"models"`
}

type serverView struct {
	Preset string `json:"preset,omitempty"` // --preset the server was started with
	Bundle string `json:"bundle,omitempty"` // --config the server was started with
}

type presetConfigView struct {
	Name   string        `json:"name"`
	Config config.Config `json:"config"`
}

// getConfig explains the configuration new sessions will get: the effective
// values, where each came from, the file on disk, and everything the editor
// needs to offer alternatives. It always answers 200; a broken file is
// reported in resolution.load_error so the page can offer a repair.
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	bundle := r.URL.Query().Get("bundle")
	preset := r.URL.Query().Get("preset")
	if bundle == "" {
		bundle = s.Bundle
	}
	if preset == "" {
		preset = s.Preset
	}
	res := config.Resolve(s.Project, preset, bundle)
	v := configView{Resolution: res, Project: s.Project, Keys: map[string]bool{}, Files: map[string]string{}, Defaults: config.Defaults(), Server: serverView{Preset: s.Preset, Bundle: s.Bundle}}
	for _, name := range config.PresetNames() {
		c := config.Defaults()
		config.Presets[name](&c)
		v.Presets = append(v.Presets, bundleView{Name: name, Description: config.PresetDescription(name), Models: modelsLine(c), Active: res.Active.Kind == "preset" && res.Active.Name == name})
	}
	names, _ := config.ListBundles(s.Project)
	for _, name := range names {
		bv := bundleView{Name: name, Active: res.Active.Kind == "bundle" && res.Active.Name == name}
		if c, err := config.LoadBundle(s.Project, "", name); err != nil {
			bv.Invalid = err.Error()
		} else {
			bv.Description, bv.Models = c.Description, modelsLine(c)
		}
		v.Bundles = append(v.Bundles, bv)
	}
	for _, env := range s.knownKeyEnvs() {
		v.Keys[env] = os.Getenv(env) != ""
	}
	if set, err := prompts.Load(s.Project); err == nil {
		for _, name := range prompts.Names {
			v.Prompts = append(v.Prompts, promptView{Name: name, Source: set.Source[name]})
		}
	}
	fc := res.Effective.Finalechat
	switch client, found := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL); {
	case !fc.Wanted():
		v.Phone = phoneView{State: "disabled", Detail: "turned off in the configuration"}
	case !found:
		v.Phone = phoneView{State: "off", Detail: "no token in $" + fc.TokenEnvName() + " or ~/.config/finalechat/config.json"}
	default:
		v.Phone = phoneView{State: "on", Source: client.Source, Detail: "new sessions are mirrored to your phone (token from " + client.Source + ")"}
	}
	v.Files["config"] = config.File(s.Project)
	v.Files["bundles"] = config.BundlesDir(s.Project)
	v.Files["prompts"] = prompts.Dir(s.Project)
	s.mu.Lock()
	for id, h := range s.running {
		v.Running = append(v.Running, runningView{ID: id, Models: h.rt.State().Models})
	}
	s.mu.Unlock()
	sort.Slice(v.Running, func(i, j int) bool { return v.Running[i].ID < v.Running[j].ID })
	if v.Bundles == nil {
		v.Bundles = []bundleView{}
	}
	if v.Running == nil {
		v.Running = []runningView{}
	}
	sort.Slice(v.Bundles, func(i, j int) bool { return v.Bundles[i].Name < v.Bundles[j].Name })
	writeJSON(w, v)
}

// getPresetConfig returns a preset or bundle as a full configuration, for
// loading into the editor.
func (s *Server) getPresetConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if canon, ok := config.ResolvePreset(name); ok {
		cfg := config.Defaults()
		config.Presets[canon](&cfg)
		cfg.Preset = canon
		writeJSON(w, presetConfigView{Name: canon, Config: cfg})
		return
	}
	// Only a bundle the directory listing knows may be opened, and it is
	// shown on its own: defaults, its own preset, its contents. Never the
	// project file or the environment.
	names, _ := config.ListBundles(s.Project)
	if !slices.Contains(names, name) {
		writeErr(w, 404, fmt.Errorf("no preset or bundle named %q", name))
		return
	}
	cfg, err := config.BundleAlone(s.Project, name)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, presetConfigView{Name: name, Config: cfg})
}

// knownKeyEnvs is the closed list of environment variable names whose
// presence the UI may learn: the catalog's, plus any already written into
// the project's configuration or bundles. Never a name typed in a request.
func (s *Server) knownKeyEnvs() []string {
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
func (s *Server) savedActors() []config.Actor {
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
func (s *Server) checkRoutes(cfg config.Config) error {
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

// ---- saving -----------------------------------------------------------------------------

type putConfigBody struct {
	Config      json.RawMessage `json:"config"`
	BasePreset  string          `json:"base_preset"`    // the preset the file should build on; "" for none
	Mode        string          `json:"mode"`           // overlay (default) | pin
	IfMatch     *string         `json:"if_match"`       // the file etag the editor loaded; omit to skip the check
	AllowShadow bool            `json:"allow_shadowed"` // write fields an EAGENT_* variable currently overrides anyway
	Default     *string         `json:"default_config"` // set or clear default_config in the file
}

type envField struct {
	Pointer string `json:"pointer"`
	Env     string `json:"env"`
}

type putConfigResult struct {
	Saved      config.SaveResult `json:"saved"`
	Shadowed   []envField        `json:"shadowed_by_env,omitempty"` // fields written but overridden by the environment
	Skipped    []envField        `json:"skipped_by_env,omitempty"`  // fields not written because the environment overrides them
	Kept       []envField        `json:"kept_from_file,omitempty"`  // pinned fields whose saved value was carried over unchanged
	Resolution config.Resolution `json:"resolution"`
}

// putConfig saves the editor's configuration to the project file.
func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var b putConfigBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, fmt.Errorf("bad request: %w", err))
		return
	}
	var cfg config.Config
	dec := json.NewDecoder(strings.NewReader(string(b.Config)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeErr(w, 400, fmt.Errorf("config: %w", err))
		return
	}
	cfg.Name, cfg.Description, cfg.Instructions, cfg.Preset, cfg.DefaultConfig = "", "", "", "", ""
	if err := s.checkRoutes(cfg); err != nil {
		writeErr(w, 422, err)
		return
	}
	problems := cfg.Problems()
	var hard []config.Problem
	for _, p := range problems {
		if p.Severity == "error" {
			hard = append(hard, p)
		}
	}
	if len(hard) > 0 {
		writeJSONStatus(w, 422, map[string]any{"error": "the configuration has problems", "problems": problems})
		return
	}
	var edits map[string]json.RawMessage
	var err error
	if b.Mode == "pin" {
		edits = config.Pin(cfg, b.BasePreset)
	} else {
		edits, err = config.Overlay(cfg, b.BasePreset)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
	}
	if b.Default != nil {
		if *b.Default == "" {
			edits["default_config"] = nil
		} else {
			names, _ := config.ListBundles(s.Project)
			if !slices.Contains(names, *b.Default) {
				writeErr(w, 422, fmt.Errorf("default_config: no bundle named %q", *b.Default))
				return
			}
			edits["default_config"], _ = json.Marshal(*b.Default)
		}
	}
	// Fields the environment overrides cannot take effect from the file, so
	// the editor's value for them is not written. Whatever the file already
	// says for such a field is carried over unchanged: it is what will apply
	// once the variable is unset, and a save of something else must not lose it.
	var shadowed, skipped, kept []envField
	env := config.EnvOverrides()
	if len(env) > 0 && !b.AllowShadow {
		onDisk := map[string]json.RawMessage{}
		if raw, err := os.ReadFile(config.File(s.Project)); err == nil {
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
				kept = append(kept, envField{ptr, env[ptr]})
			case touched:
				skipped = append(skipped, envField{ptr, env[ptr]})
			}
		}
	} else if len(env) > 0 {
		for ptr, v := range env {
			shadowed = append(shadowed, envField{ptr, v})
		}
		sort.Slice(shadowed, func(i, j int) bool { return shadowed[i].Pointer < shadowed[j].Pointer })
	}
	ifMatch := ""
	if b.IfMatch != nil {
		ifMatch = *b.IfMatch
	}
	saved, err := config.SaveFile(s.Project, edits, ifMatch, b.IfMatch != nil)
	if err != nil {
		var conflict *config.ErrConflict
		if errors.As(err, &conflict) {
			writeJSONStatus(w, 409, map[string]any{"error": conflict.Error(), "current": conflict.Current, "etag": conflict.ETag})
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.logf("web: configuration saved to %s (%s mode, base preset %q)", saved.Path, orDefault(b.Mode, "overlay"), b.BasePreset)
	if cfg.AllowOutsideProject {
		s.logf("web: allow_outside_project is on in the saved configuration")
	}
	writeJSON(w, putConfigResult{Saved: saved, Shadowed: shadowed, Skipped: skipped, Kept: kept, Resolution: config.Resolve(s.Project, s.Preset, s.Bundle)})
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

// putConfigRaw writes the file verbatim (after checking it parses and
// loads): the repair path for a broken file and the undo path after a save.
func (s *Server) putConfigRaw(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Raw     string  `json:"raw"`
		IfMatch *string `json:"if_match"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, fmt.Errorf("bad request: %w", err))
		return
	}
	// The raw text must load as a configuration before it is written, so a
	// repair cannot leave the file worse than it found it.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b.Raw), &probe); err != nil {
		writeErr(w, 422, fmt.Errorf("not valid JSON: %w", err))
		return
	}
	trial := config.Defaults()
	if raw, _ := json.Marshal(probe); len(raw) > 0 {
		if err := json.Unmarshal(raw, &trial); err != nil {
			writeErr(w, 422, fmt.Errorf("does not load as a configuration: %w", err))
			return
		}
	}
	if err := s.checkRoutes(trial); err != nil {
		writeErr(w, 422, err)
		return
	}
	ifMatch := ""
	if b.IfMatch != nil {
		ifMatch = *b.IfMatch
	}
	saved, err := config.SaveRaw(s.Project, b.Raw, ifMatch, b.IfMatch != nil)
	if err != nil {
		var conflict *config.ErrConflict
		if errors.As(err, &conflict) {
			writeJSONStatus(w, 409, map[string]any{"error": conflict.Error(), "current": conflict.Current, "etag": conflict.ETag})
			return
		}
		writeErr(w, 422, err)
		return
	}
	s.logf("web: configuration file written verbatim (%d bytes)", len(b.Raw))
	writeJSON(w, putConfigResult{Saved: saved, Resolution: config.Resolve(s.Project, s.Preset, s.Bundle)})
}

// ---- live route test -------------------------------------------------------------------

type testBody struct {
	Actor  config.Actor `json:"actor"`
	Effort *string      `json:"effort"` // override the actor's effort for this one call
	Route  string       `json:"route"`  // primary (default) | fallback
}

// TestResult is one live call through one route.
type TestResult struct {
	Route      string         `json:"route"`
	BaseURL    string         `json:"base_url"`
	Model      string         `json:"model"`
	Protocol   string         `json:"protocol"`
	Effort     string         `json:"effort"`
	Status     string         `json:"status"` // tool_call | no_tool_call | failed | timeout | no_key
	MS         int64          `json:"ms"`
	Usage      event.Usage    `json:"usage"`
	CostUSD    float64        `json:"est_cost_usd"`
	Priced     bool           `json:"priced"`
	Sent       map[string]any `json:"sent"`
	Text       string         `json:"text,omitempty"`
	Error      string         `json:"error,omitempty"`
	HTTPStatus int            `json:"http_status,omitempty"`
	At         time.Time      `json:"at"`
}

var (
	testSem   = make(chan struct{}, 4)
	testMu    sync.Mutex
	testTimes []time.Time
)

// testRoute makes one tiny tool call through the given actor route so the
// editor can show whether a provider, model, and effort work together, how
// long they take, and how much they reason. Serialized to four at a time
// and forty a minute; each call is capped at 200 output tokens.
func (s *Server) testRoute(w http.ResponseWriter, r *http.Request) {
	var b testBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, fmt.Errorf("bad request: %w", err))
		return
	}
	a := b.Actor
	route := "primary"
	if b.Route == "fallback" {
		if a.Fallback == nil {
			writeErr(w, 400, errors.New("this route has no fallback"))
			return
		}
		a = *a.Fallback
		route = "fallback"
	}
	a.Fallback = nil
	if b.Effort != nil {
		a.ReasoningEffort = *b.Effort
	}
	probe := config.Defaults()
	probe.Orchestrator, probe.Task, probe.Narrator = a, a, a
	if err := s.checkRoutes(probe); err != nil {
		writeErr(w, 422, err)
		return
	}
	for _, p := range probe.Problems() {
		if p.Severity == "error" && strings.HasPrefix(p.Path, "/orchestrator") {
			writeErr(w, 422, errors.New(strings.TrimPrefix(p.Path, "/orchestrator/")+": "+p.Message))
			return
		}
	}
	testMu.Lock()
	cutoff := time.Now().Add(-time.Minute)
	kept := testTimes[:0]
	for _, t := range testTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	testTimes = kept
	if len(testTimes) >= 40 {
		testMu.Unlock()
		writeErr(w, 429, errors.New("more than forty route tests in a minute; wait a little"))
		return
	}
	testTimes = append(testTimes, time.Now())
	testMu.Unlock()

	res := TestResult{Route: route, BaseURL: a.BaseURL, Model: a.Model, Protocol: a.Protocol, Effort: a.ReasoningEffort, At: time.Now(), Sent: sentFor(a)}
	ep, err := a.Endpoint()
	if err != nil {
		res.Status = "no_key"
		res.Error = err.Error()
		writeJSON(w, res)
		return
	}
	select {
	case testSem <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() { <-testSem }()
	c := llm.NewClient(ep)
	c.MaxAttempts = 1
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := c.Complete(ctx, llm.Request{
		System:    "Reply with a single tool call.",
		Messages:  []llm.Message{{Role: "user", Text: "Call ping with message=\"pong\"."}},
		Tools:     []llm.Tool{{Name: "ping", Description: "Ping.", Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`)}},
		MaxTokens: 200,
	}, nil)
	res.MS = time.Since(start).Milliseconds()
	switch {
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)):
		res.Status = "timeout"
		res.Error = "no answer within two minutes"
	case err != nil:
		res.Status = "failed"
		res.Error = shortError(err)
		var ae *llm.APIError
		if errors.As(err, &ae) {
			res.HTTPStatus = ae.Status
		}
	case len(resp.ToolCalls) == 0:
		res.Status = "no_tool_call"
		res.Text = clipText(resp.Text, 200)
		res.Usage = resp.Usage
	default:
		res.Status = "tool_call"
		res.Usage = resp.Usage
	}
	if res.Usage.Input > 0 {
		res.CostUSD, res.Priced = priceFor(a.BaseURL, a.Model, res.Usage)
	}
	s.recordCheck(res)
	writeJSON(w, res)
}

// sentFor says how the effort reaches the wire for this route.
func sentFor(a config.Actor) map[string]any {
	e := a.ReasoningEffort
	switch a.Protocol {
	case llm.ProtocolResponses:
		if e == "" || e == "none" {
			return map[string]any{"reasoning": nil, "note": "no reasoning parameter is sent; the model uses its default"}
		}
		return map[string]any{"reasoning": map[string]any{"effort": e}}
	case llm.ProtocolAnthropic:
		if e == "" || e == "none" {
			return map[string]any{"thinking": nil, "note": "no thinking block is requested"}
		}
		return map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": e}, "note": "older models get a budget instead: low 2048, medium 8192, high 24576"}
	default:
		if e == "" {
			return map[string]any{"note": "no reasoning_effort is sent; the provider default applies"}
		}
		return map[string]any{"reasoning_effort": e}
	}
}

func shortError(err error) string {
	s := err.Error()
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func clipText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- remembered checks -------------------------------------------------------------------

// checks are the outcomes of past route tests, kept in the project so the
// editor can annotate each effort with what actually happened last time.
type checkRecord struct {
	Status    string `json:"status"`
	MS        int64  `json:"ms"`
	Output    int    `json:"output"`
	Reasoning int    `json:"reasoning"`
	Error     string `json:"error,omitempty"`
	At        string `json:"at"`
}

func (s *Server) checksPath() string {
	return filepath.Join(s.Project, ".agents", "eagent", "route-checks.json")
}

func (s *Server) loadChecks() map[string]checkRecord {
	out := map[string]checkRecord{}
	raw, err := os.ReadFile(s.checksPath())
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func checkKey(baseURL, model, effort string) string {
	return strings.TrimRight(baseURL, "/") + "|" + model + "|" + effort
}

func (s *Server) recordCheck(res TestResult) {
	s.checksMu.Lock()
	defer s.checksMu.Unlock()
	checks := s.loadChecks()
	checks[checkKey(res.BaseURL, res.Model, res.Effort)] = checkRecord{Status: res.Status, MS: res.MS, Output: res.Usage.Output, Reasoning: res.Usage.Reasoning, Error: res.Error, At: res.At.UTC().Format(time.RFC3339)}
	raw, err := json.MarshalIndent(checks, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.checksPath()), 0o755)
	_ = os.WriteFile(s.checksPath(), append(raw, '\n'), 0o644)
}

// ---- catalog -----------------------------------------------------------------------------

type catalogView struct {
	Providers   []config.Provider      `json:"providers"`
	Models      []config.Model         `json:"models"`
	EffortOrder []string               `json:"effort_order"`
	Transports  map[string]string      `json:"transports"` // protocol -> how the effort travels
	Keys        map[string]bool        `json:"keys"`       // catalog key env -> present
	Checks      map[string]checkRecord `json:"checks"`     // "base|model|effort" -> last observed outcome
}

func (s *Server) getCatalog(w http.ResponseWriter, r *http.Request) {
	v := catalogView{Providers: config.Providers, Models: config.Models, EffortOrder: config.EffortOrder, Transports: map[string]string{}, Keys: map[string]bool{}}
	for _, p := range []string{llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic} {
		v.Transports[p] = config.Transport(p)
	}
	for _, env := range s.knownKeyEnvs() {
		v.Keys[env] = os.Getenv(env) != ""
	}
	s.checksMu.Lock()
	v.Checks = s.loadChecks()
	s.checksMu.Unlock()
	writeJSON(w, v)
}

// writeJSONStatus writes a JSON body with a status code.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
