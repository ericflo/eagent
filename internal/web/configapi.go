package web

import (
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
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/settings"
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
	if canon, ok := config.ResolvePreset(name); ok && r.URL.Query().Get("kind") != "bundle" {
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

func (s *Server) settingsService() *settings.Service {
	return &settings.Service{Project: s.Project, Preset: s.Preset, Bundle: s.Bundle}
}
func (s *Server) knownKeyEnvs() []string            { return s.settingsService().KnownKeyEnvs() }
func (s *Server) checkRoutes(c config.Config) error { return s.settingsService().CheckRoutes(c) }

type putConfigBody = settings.Request
type putConfigResult = settings.Result

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var b settings.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, err)
		return
	}
	result, err := s.settingsService().Save(r.Context(), b)
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	s.logf("web: configuration saved to %s", result.Saved.Path)
	writeJSON(w, result)
}
func (s *Server) putConfigRaw(w http.ResponseWriter, r *http.Request) {
	var b settings.RawRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, err)
		return
	}
	result, err := s.settingsService().SaveRaw(r.Context(), b)
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	s.logf("web: configuration file saved to %s", result.Saved.Path)
	writeJSON(w, result)
}
func writeSettingsError(w http.ResponseWriter, err error) {
	var conflict *config.ErrConflict
	if errors.As(err, &conflict) {
		writeJSONStatus(w, 409, map[string]any{"error": conflict.Error(), "current": conflict.Current, "etag": conflict.ETag})
		return
	}
	var validation *settings.Error
	if errors.As(err, &validation) {
		writeJSONStatus(w, validation.Status, map[string]any{"error": validation.Message, "problems": validation.Problems})
		return
	}
	writeErr(w, 500, err)
}
func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// ---- live route test -------------------------------------------------------------------

type testBody = settings.RouteTestRequest
type TestResult = settings.RouteTestResult

func (s *Server) testRoute(w http.ResponseWriter, r *http.Request) {
	var b testBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, fmt.Errorf("bad request: %w", err))
		return
	}
	result, err := s.settingsService().TestRoute(r.Context(), b)
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	s.recordCheck(result)
	writeJSON(w, result)
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
