package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This file is what the web configuration editor stands on: an explanation
// of where every effective value came from, structured validation, and a
// file writer that only touches the keys the editor owns.

// ---- provenance --------------------------------------------------------------

// Active says which layer decides the models a new session gets.
type Active struct {
	Kind string `json:"kind"` // preset | file | bundle
	Name string `json:"name"`
}

// Resolution is a configuration with its layers laid bare.
type Resolution struct {
	Effective Config `json:"effective"`
	// Sources maps a JSON pointer ("/orchestrator/model") to the layer that
	// set the effective value: "default", "preset:NAME", "file",
	// "bundle:NAME", or "env:VAR".
	Sources map[string]string `json:"sources"`
	Active  Active            `json:"active"`
	Preset  string            `json:"preset"` // the preset in force before the file, if any
	// File is the project's config.json as found on disk.
	File FileState `json:"file"`
	// LoadError is set when the configuration could not be loaded; Effective
	// then holds the last layer that did load, so a repair UI has something
	// to show.
	LoadError string `json:"load_error,omitempty"`
}

// FileState describes .agents/eagent/config.json.
type FileState struct {
	Path        string   `json:"path"`
	Exists      bool     `json:"exists"`
	Raw         string   `json:"raw,omitempty"`
	ETag        string   `json:"etag"`             // sha256 of the exact bytes; "" when absent
	Preset      string   `json:"preset,omitempty"` // the preset the file itself names, if any
	ParseError  string   `json:"parse_error,omitempty"`
	UnknownKeys []string `json:"unknown_keys,omitempty"`
}

// Resolve loads a configuration the way LoadBundle does while recording
// which layer set each value. Unlike LoadBundle it does not fail on a broken
// file: the error is reported and the layers that did load are returned.
func Resolve(project, preset, bundle string) Resolution {
	res := Resolution{Sources: map[string]string{}}
	cfg := Defaults()
	prev := flatten(cfg)
	for k := range prev {
		res.Sources[k] = "default"
	}
	mark := func(cur map[string]string, layer string) map[string]string {
		for k, v := range cur {
			if prev[k] != v {
				res.Sources[k] = layer
			}
		}
		for k := range prev {
			if _, ok := cur[k]; !ok {
				res.Sources[k] = layer // removed (a fallback set to null)
			}
		}
		return cur
	}

	path := File(project)
	res.File.Path = path
	fileMap := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		res.File.Exists = true
		res.File.Raw = string(raw)
		res.File.ETag = etag(raw)
		if err := json.Unmarshal(raw, &fileMap); err != nil {
			res.File.ParseError = err.Error()
			fileMap = nil
		} else {
			res.File.UnknownKeys = unknownKeys(fileMap)
			if raw, ok := fileMap["preset"]; ok {
				_ = json.Unmarshal(raw, &res.File.Preset)
			}
		}
	}
	bundleMap := map[string]json.RawMessage{}
	if bundle != "" {
		if canon, isPreset := ResolvePreset(bundle); isPreset {
			if preset == "" {
				preset = canon
			}
			bundle = ""
		} else if !validName(bundle) {
			res.LoadError = "config bundle names use letters, digits, '-', '_' and '.' only"
			bundle = ""
		} else if m, err := readJSONMap(BundlePath(project, bundle)); err != nil {
			res.LoadError = err.Error()
		} else if m == nil {
			res.LoadError = fmt.Sprintf("no config bundle %q in %s", bundle, BundlesDir(project))
		} else {
			bundleMap = m
		}
	}
	if preset == "" {
		preset = os.Getenv("EAGENT_PRESET")
	}
	if preset == "" && bundleMap != nil {
		if raw, ok := bundleMap["preset"]; ok {
			_ = json.Unmarshal(raw, &preset)
		}
	}
	if preset == "" && fileMap != nil {
		if raw, ok := fileMap["preset"]; ok {
			_ = json.Unmarshal(raw, &preset)
		}
	}
	if preset != "" {
		if canon, ok := ResolvePreset(preset); ok {
			Presets[canon](&cfg)
			cfg.Preset = canon
			res.Preset = canon
			prev = mark(flatten(cfg), "preset:"+canon)
		} else if res.LoadError == "" {
			res.LoadError = fmt.Sprintf("unknown preset %q (have %s)", preset, strings.Join(PresetNames(), ", "))
		}
	}
	if fileMap != nil && len(fileMap) > 0 {
		if err := overlay(&cfg, fileMap, path); err != nil {
			if res.LoadError == "" {
				res.LoadError = err.Error()
			}
		} else {
			prev = mark(flatten(cfg), "file")
		}
	} else if res.File.ParseError != "" && res.LoadError == "" {
		res.LoadError = path + ": " + res.File.ParseError
	}
	if bundle != "" && len(bundleMap) > 0 {
		if err := overlay(&cfg, bundleMap, BundlePath(project, bundle)); err != nil {
			if res.LoadError == "" {
				res.LoadError = err.Error()
			}
		} else {
			cfg.Name = bundle
			prev = mark(flatten(cfg), "bundle:"+bundle)
		}
	}
	envVars := EnvOverrides()
	applyEnv(&cfg)
	cur := flatten(cfg)
	for k, v := range cur {
		if prev[k] != v {
			if name, ok := envVars[k]; ok {
				res.Sources[k] = "env:" + name
			} else {
				res.Sources[k] = "env"
			}
		}
	}
	cfg.Instructions = loadInstructions(project)
	if res.LoadError == "" {
		if err := cfg.Validate(); err != nil {
			res.LoadError = err.Error()
		}
	}
	res.Effective = cfg
	switch {
	case bundle != "":
		res.Active = Active{Kind: "bundle", Name: bundle}
	case fileSetsRoutes(res.Sources):
		res.Active = Active{Kind: "file", Name: filepath.Base(path)}
	default:
		name := res.Preset
		if name == "" {
			name = "glm"
		}
		res.Active = Active{Kind: "preset", Name: name}
	}
	return res
}

// fileSetsRoutes reports whether the project file decides any actor's route.
func fileSetsRoutes(sources map[string]string) bool {
	for k, v := range sources {
		if v == "file" && (strings.HasPrefix(k, "/orchestrator/") || strings.HasPrefix(k, "/task/") || strings.HasPrefix(k, "/narrator/")) {
			return true
		}
	}
	return false
}

// EnvOverrides maps the JSON pointer of every field an EAGENT_* variable
// currently overrides to that variable's name.
func EnvOverrides() map[string]string {
	out := map[string]string{}
	for name, ptr := range map[string]string{"ORCHESTRATOR": "/orchestrator", "TASK": "/task", "NARRATOR": "/narrator"} {
		for suffix, field := range map[string]string{"MODEL": "model", "PROTOCOL": "protocol", "BASE_URL": "base_url", "API_KEY_ENV": "api_key_env", "REASONING_EFFORT": "reasoning_effort", "MAX_TOKENS": "max_tokens", "REPLAY_REASONING": "replay_reasoning"} {
			v := "EAGENT_" + name + "_" + suffix
			if os.Getenv(v) != "" {
				out[ptr+"/"+field] = v
			}
		}
	}
	for v, ptr := range map[string]string{"EAGENT_TASK_CONCURRENCY": "/task_concurrency", "EAGENT_ROLLOVER_TOKENS": "/rollover_tokens", "EAGENT_NARRATOR_TICK_SECONDS": "/narrator_tick_seconds", "EAGENT_NARRATOR_QUIET_SECONDS": "/narrator_quiet_seconds", "EAGENT_PERSONA": "/persona", "EAGENT_FINALECHAT": "/finalechat/enabled", "EAGENT_FINALECHAT_ARTIFACTS": "/finalechat/artifacts"} {
		if os.Getenv(v) != "" {
			out[ptr] = v
		}
	}
	return out
}

// flatten renders a configuration as JSON-pointer -> canonical JSON leaf.
func flatten(cfg Config) map[string]string {
	raw, _ := json.Marshal(cfg)
	var v any
	_ = json.Unmarshal(raw, &v)
	out := map[string]string{}
	var walk func(prefix string, x any)
	walk = func(prefix string, x any) {
		switch t := x.(type) {
		case map[string]any:
			if len(t) == 0 {
				out[prefix] = "{}"
			}
			for k, val := range t {
				walk(prefix+"/"+k, val)
			}
		default:
			b, _ := json.Marshal(t)
			out[prefix] = string(b)
		}
	}
	walk("", v)
	delete(out, "/instructions")
	return out
}

// editorKeys are the top-level keys the editor may write. Anything else in
// the file is left byte for byte as it was.
var editorKeys = []string{"preset", "default_config", "orchestrator", "task", "narrator", "task_concurrency", "max_task_turns", "max_orchestrator_calls_per_turn", "narrator_tick_seconds", "narrator_quiet_seconds", "rollover_tokens", "bash_wait_seconds", "bash_timeout_seconds", "tool_output_max_chars", "persona", "allow_outside_project", "finalechat"}

func unknownKeys(m map[string]json.RawMessage) []string {
	known := map[string]bool{"name": true, "description": true}
	for _, k := range editorKeys {
		known[k] = true
	}
	var out []string
	for k := range m {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ---- serialization for saving ------------------------------------------------

// Overlay returns the JSON the file needs so that Defaults()+preset+file
// equals cfg, touching only editor-owned keys: values equal to the base are
// omitted (a key present on disk but now equal to the base is deleted, sent
// as a nil RawMessage), and actors carry only the fields that differ.
func Overlay(cfg Config, preset string) (map[string]json.RawMessage, error) {
	base := Defaults()
	if preset != "" {
		canon, ok := ResolvePreset(preset)
		if !ok {
			return nil, fmt.Errorf("unknown preset %q", preset)
		}
		Presets[canon](&base)
		preset = canon
	}
	out := map[string]json.RawMessage{}
	if preset != "" {
		out["preset"], _ = json.Marshal(preset)
	} else {
		out["preset"] = nil
	}
	want := topLevel(cfg)
	have := topLevel(base)
	for _, k := range editorKeys {
		if k == "preset" || k == "default_config" {
			continue
		}
		w, h := want[k], have[k]
		switch k {
		case "orchestrator", "task", "narrator":
			diff := diffObjects(h, w)
			if len(diff) == 0 {
				out[k] = nil
			} else {
				out[k], _ = json.Marshal(diff)
			}
		default:
			if bytes.Equal(canon(w), canon(h)) {
				out[k] = nil
			} else {
				out[k] = w
			}
		}
	}
	return out, nil
}

// BundleAlone loads a named bundle over the defaults and the preset the
// bundle itself names, without the project file or the environment: what
// the bundle says, for showing or copying it.
func BundleAlone(project, name string) (Config, error) {
	if !validName(name) {
		return Config{}, fmt.Errorf("config bundle names use letters, digits, '-', '_' and '.' only")
	}
	raw, err := os.ReadFile(BundlePath(project, name))
	if err != nil {
		return Config{}, err
	}
	return BundleFromBytes(name, raw)
}

// BundleFromBytes resolves a captured bundle without re-reading a changing file.
// It does not load the project configuration or process environment.
func BundleFromBytes(name string, raw []byte) (Config, error) {
	if !validName(name) {
		return Config{}, fmt.Errorf("config bundle names use letters, digits, '-', '_' and '.' only")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return Config{}, err
	}
	if m == nil {
		return Config{}, fmt.Errorf("no bundle named %q", name)
	}
	cfg := Defaults()
	preset := ""
	if raw, ok := m["preset"]; ok {
		_ = json.Unmarshal(raw, &preset)
	}
	if preset != "" {
		canon, ok := ResolvePreset(preset)
		if !ok {
			return Config{}, fmt.Errorf("bundle %s names unknown preset %q", name, preset)
		}
		Presets[canon](&cfg)
		preset = canon
	}
	if err := overlay(&cfg, m, "bundle "+name); err != nil {
		return Config{}, err
	}
	cfg.Preset = preset
	return cfg, nil
}

// Pin returns the JSON that spells every editor-owned value out in full.
func Pin(cfg Config, preset string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if preset != "" {
		out["preset"], _ = json.Marshal(preset)
	} else {
		out["preset"] = nil // a pinned file stands alone; drop any preset the file had
	}
	for k, v := range topLevel(cfg) {
		for _, ek := range editorKeys {
			if ek == k && k != "preset" && k != "default_config" {
				out[k] = v
			}
		}
	}
	return out
}

func topLevel(cfg Config) map[string]json.RawMessage {
	raw, _ := json.Marshal(cfg)
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func canon(raw json.RawMessage) []byte {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	b, _ := json.Marshal(v)
	return b
}

// diffObjects returns the keys of want whose value differs from have, plus
// explicit nulls for keys have has and want lacks.
func diffObjects(have, want json.RawMessage) map[string]json.RawMessage {
	h := map[string]json.RawMessage{}
	w := map[string]json.RawMessage{}
	_ = json.Unmarshal(have, &h)
	_ = json.Unmarshal(want, &w)
	out := map[string]json.RawMessage{}
	for k, v := range w {
		if hv, ok := h[k]; !ok || !bytes.Equal(canon(hv), canon(v)) {
			out[k] = v
		}
	}
	for k := range h {
		if _, ok := w[k]; !ok {
			out[k] = json.RawMessage("null")
		}
	}
	return out
}

// ---- writing -----------------------------------------------------------------

// ErrConflict is returned when the file changed since the caller read it.
type ErrConflict struct {
	Current string
	ETag    string
}

func (e *ErrConflict) Error() string {
	return "the configuration file changed on disk since you loaded it"
}

// SaveResult reports a write.
type SaveResult struct {
	Path     string `json:"path"`
	ETag     string `json:"etag"`
	Previous string `json:"previous,omitempty"` // the bytes that were replaced
	PrevETag string `json:"prev_etag,omitempty"`
}

// SaveFile applies editor edits to the project's config.json: existing keys
// the editor does not own are kept byte for byte, a nil value deletes a key,
// the write is atomic, the previous bytes go to config.json.bak, and when
// ifMatch is given the write is refused unless the file still has that etag
// ("" meaning "there was no file").
func SaveFile(project string, edits map[string]json.RawMessage, ifMatch string, checkMatch bool) (SaveResult, error) {
	editor, err := LockEditor(context.Background(), project)
	if err != nil {
		return SaveResult{}, err
	}
	defer editor.Close()
	return editor.SaveFile(edits, ifMatch, checkMatch)
}

func saveFile(project string, edits map[string]json.RawMessage, ifMatch string, checkMatch bool) (SaveResult, error) {
	path := File(project)
	var res SaveResult
	res.Path = path
	existing := map[string]json.RawMessage{}
	prev, err := os.ReadFile(path)
	switch {
	case err == nil:
		res.Previous = string(prev)
		res.PrevETag = etag(prev)
		if err := json.Unmarshal(prev, &existing); err != nil {
			return res, fmt.Errorf("%s is not valid JSON (%v); repair it first", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return res, err
	}
	if checkMatch && ifMatch != res.PrevETag {
		return res, &ErrConflict{Current: res.Previous, ETag: res.PrevETag}
	}
	for k, v := range edits {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}
	raw, err := marshalOrdered(existing)
	if err != nil {
		return res, err
	}
	if err := writeAtomic(path, raw, prev); err != nil {
		return res, err
	}
	res.ETag = etag(raw)
	return res, nil
}

// SaveRaw writes the file exactly as given after checking it parses and
// loads; for repairs and undo.
func SaveRaw(project, raw, ifMatch string, checkMatch bool) (SaveResult, error) {
	editor, err := LockEditor(context.Background(), project)
	if err != nil {
		return SaveResult{}, err
	}
	defer editor.Close()
	return editor.SaveRaw(raw, ifMatch, checkMatch)
}

func saveRaw(project, raw, ifMatch string, checkMatch bool) (SaveResult, error) {
	path := File(project)
	var res SaveResult
	res.Path = path
	prev, err := os.ReadFile(path)
	if err == nil {
		res.Previous = string(prev)
		res.PrevETag = etag(prev)
	} else if !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	if checkMatch && ifMatch != res.PrevETag {
		return res, &ErrConflict{Current: res.Previous, ETag: res.PrevETag}
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return res, fmt.Errorf("not valid JSON: %v", err)
	}
	out := []byte(raw)
	if !strings.HasSuffix(raw, "\n") {
		out = append(out, '\n')
	}
	if err := writeAtomic(path, out, prev); err != nil {
		return res, err
	}
	res.ETag = etag(out)
	return res, nil
}

func writeAtomic(path string, raw, prev []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if prev != nil {
		_ = os.WriteFile(path+".bak", prev, 0o600)
	}
	tmp, err := os.CreateTemp(dir, ".config.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// marshalOrdered writes the map with the editor's keys in a stable order
// and any other keys after them, alphabetically.
func marshalOrdered(m map[string]json.RawMessage) ([]byte, error) {
	var keys []string
	seen := map[string]bool{}
	for _, k := range editorKeys {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, canon(m[k]), "  ", "  "); err != nil {
			return nil, fmt.Errorf("%s: %v", k, err)
		}
		b.WriteString("  ")
		b.Write(kb)
		b.WriteString(": ")
		b.Write(pretty.Bytes())
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

func etag(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ---- structured validation ---------------------------------------------------

// Problem is one thing wrong with a configuration, at a JSON pointer.
type Problem struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // error | warning
}

var keyEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Problems lists everything wrong or questionable about a configuration.
// Validate fails on the errors; the editor shows warnings too.
func (c Config) Problems() []Problem {
	var out []Problem
	add := func(sev, path, code, msg string) {
		out = append(out, Problem{Path: path, Code: code, Message: msg, Severity: sev})
	}
	for name, a := range map[string]Actor{"/orchestrator": c.Orchestrator, "/task": c.Task, "/narrator": c.Narrator} {
		out = append(out, actorProblems(name, a, 0)...)
	}
	if c.TaskConcurrency < 1 {
		add("error", "/task_concurrency", "min", "task_concurrency must be at least 1")
	}
	if c.MaxTaskTurns < 1 {
		add("error", "/max_task_turns", "min", "max_task_turns must be at least 1")
	}
	if c.MaxOrchestratorCallsPerTurn < 1 {
		add("error", "/max_orchestrator_calls_per_turn", "min", "max_orchestrator_calls_per_turn must be at least 1")
	}
	if c.NarratorTickSeconds < 5 {
		add("error", "/narrator_tick_seconds", "min", "narrator_tick_seconds must be at least 5")
	}
	if c.NarratorQuietSeconds < 0 {
		add("error", "/narrator_quiet_seconds", "min", "narrator_quiet_seconds cannot be negative (0 disables the reminder)")
	}
	if c.RolloverTokens < 20_000 {
		add("error", "/rollover_tokens", "min", "rollover_tokens must be at least 20000")
	}
	if w := c.Orchestrator.ContextTokens; w > 0 && float64(c.RolloverTokens) > 0.9*float64(w) {
		add("warning", "/rollover_tokens", "near_window", fmt.Sprintf("rollover_tokens (%d) is within 10%% of the orchestrator's context window (%d); the context can overflow before the dossier is written", c.RolloverTokens, w))
	}
	if c.BashWaitSeconds < 0 || c.BashWaitSeconds > 300 {
		add("error", "/bash_wait_seconds", "range", "bash_wait_seconds must be between 0 and 300")
	}
	if c.BashTimeoutSeconds < 1 {
		add("error", "/bash_timeout_seconds", "min", "bash_timeout_seconds must be at least 1")
	}
	if c.ToolOutputMaxChars < 1000 {
		add("error", "/tool_output_max_chars", "min", "tool_output_max_chars must be at least 1000")
	}
	if len(c.Persona) > 8192 {
		add("error", "/persona", "too_long", "persona must be at most 8 KB")
	}
	if c.Finalechat.QuestionTimeoutSeconds < 0 || c.Finalechat.QuestionTimeoutSeconds > 604800 {
		add("error", "/finalechat/question_timeout_seconds", "range", "question_timeout_seconds must be between 0 and 604800")
	}
	if c.Finalechat.TokenEnv != "" && !keyEnvPattern.MatchString(c.Finalechat.TokenEnv) {
		add("error", "/finalechat/token_env", "name", "token_env must look like an environment variable name")
	}
	return out
}

func actorProblems(path string, a Actor, depth int) []Problem {
	var out []Problem
	add := func(sev, p, code, msg string) {
		out = append(out, Problem{Path: p, Code: code, Message: msg, Severity: sev})
	}
	switch a.Protocol {
	case "openai-chat", "openai-responses", "anthropic":
	default:
		add("error", path+"/protocol", "enum", fmt.Sprintf("unknown protocol %q", a.Protocol))
	}
	if strings.TrimSpace(a.Model) == "" {
		add("error", path+"/model", "required", "model is required")
	}
	if a.BaseURL == "" {
		add("error", path+"/base_url", "required", "base_url is required")
	} else if u, err := url.Parse(a.BaseURL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		add("error", path+"/base_url", "url", "base_url must be an http(s) URL")
	} else if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		add("warning", path+"/base_url", "plain_http", "base_url uses plain http; the key travels unencrypted")
	}
	if a.APIKeyEnv == "" {
		add("error", path+"/api_key_env", "required", "api_key_env is required")
	} else if !keyEnvPattern.MatchString(a.APIKeyEnv) {
		add("error", path+"/api_key_env", "name", "api_key_env must look like an environment variable name")
	}
	if a.MaxTokens != 0 && a.MaxTokens < 256 {
		add("error", path+"/max_tokens", "min", "max_tokens must be 0 (default) or at least 256")
	}
	if a.ContextTokens < 0 {
		add("error", path+"/context_tokens", "min", "context_tokens cannot be negative")
	}
	if e := a.ReasoningEffort; e != "" {
		if a.Protocol == "anthropic" && EffortIndex(e) == len(EffortOrder) {
			add("error", path+"/reasoning_effort", "enum", fmt.Sprintf("%q is not an effort value (use none, low, medium, high)", e))
		}
		if m, ok := Lookup(a.BaseURL, a.Model); ok && len(m.Efforts) > 0 && !contains(m.Efforts, e) {
			add("warning", path+"/reasoning_effort", "unchecked", fmt.Sprintf("%s at this provider is known to accept %s; %q is unchecked, test it", m.Label, strings.Join(m.Efforts, ", "), e))
		}
	}
	if a.Fallback != nil {
		if depth >= 1 {
			add("error", path+"/fallback", "too_deep", "only one level of fallback is supported")
		} else {
			out = append(out, actorProblems(path+"/fallback", *a.Fallback, depth+1)...)
		}
	}
	return out
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
