package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

const SchemaVersion = "eagent.project-settings/v2"

// suggestionLists is how many suggestion lists a project settings document
// carries: eight, what FinaleChat accepted before its limit was raised to
// 16. Documents keep to it until FinaleChat with the higher limit is the
// one in use, though the protocol (control.Descriptor.Validate) takes 16.
const suggestionLists = 8

type RemoteView struct {
	Key        string             `json:"key"`
	Label      string             `json:"label"`
	Scope      string             `json:"scope"`
	Generation string             `json:"generation"`
	Descriptor control.Descriptor `json:"descriptor"`
	Snapshot   control.Snapshot   `json:"snapshot"`
}

func (s *Service) Grant() control.Grant {
	project, _ := filepath.Abs(s.Project)
	if real, err := filepath.EvalSymlinks(project); err == nil {
		project = real
	}
	return control.Grant{Key: "project-" + artifact.Digest([]byte(project))[:24], Label: filepath.Base(project), Scope: "project", Operations: []string{"settings.apply", "settings.refresh", "prompt.set", "prompt.reset", "bundle.save", "bundle.delete", "settings.undo", "route.test", "session.start"}, Classes: []string{"preference", "credential_reference", "permissions", "cost"}}
}

// RemoteSnapshot includes only known configuration fields. Raw unknown keys,
// credential values, instructions and connector credentials are never copied.
func (s *Service) RemoteSnapshot(grant control.Grant) (RemoteView, error) {
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	if res.LoadError != "" {
		return RemoteView{}, fmt.Errorf("repair the local configuration before remote editing: %s", res.LoadError)
	}
	var effective map[string]any
	raw, _ := json.Marshal(res.Effective)
	_ = json.Unmarshal(raw, &effective)
	onDisk, err := fileValues(res.File.Raw)
	if err != nil {
		return RemoteView{}, err
	}
	dedicated := res.Effective.Summarizer.Mode == config.SummarizerDedicated
	out := RemoteView{Key: grant.Key, Label: grant.Label, Scope: grant.Scope, Descriptor: control.Descriptor{Format: control.Format, SchemaVersion: SchemaVersion, AdapterVersion: "1", Fields: []control.Field{}}, Snapshot: control.Snapshot{Context: s.Project, Saved: map[string]any{}, Effective: map[string]any{}, RuntimeKnown: false}}
	// The addresses a route may use: the providers', then any written in
	// the project's configuration by hand, which a save keeps.
	var baseURLs []any
	for _, pr := range config.AllProviders(s.Project) {
		baseURLs = append(baseURLs, pr.BaseURL)
	}
	for _, a := range s.savedRoutes() {
		if a.BaseURL != "" && !slices.ContainsFunc(baseURLs, func(v any) bool { return strings.TrimRight(v.(string), "/") == strings.TrimRight(a.BaseURL, "/") }) {
			baseURLs = append(baseURLs, a.BaseURL)
		}
	}
	var visit func(reflect.Type, string, int)
	visit = func(t reflect.Type, prefix string, depth int) {
		if depth > 5 {
			return
		}
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" || name == "-" || (prefix == "" && (name == "name" || name == "description")) {
					continue
				}
				visit(field.Type, prefix+"/"+name, depth+1)
			}
			return
		}
		shape := control.Shape{Type: "string", MaxLength: 8192}
		switch t.Kind() {
		case reflect.Bool:
			shape.Type = "boolean"
		case reflect.Int:
			shape.Type = "integer"
			min, max := float64(0), float64(100000000)
			shape.Minimum = &min
			shape.Maximum = &max
		case reflect.String:
		default:
			return
		}
		class := "preference"
		if strings.HasSuffix(prefix, "/api_key_env") || strings.HasSuffix(prefix, "/base_url") || prefix == "/finalechat/token_env" {
			class = "credential_reference"
		}
		if prefix == "/default_config" {
			class = "permissions"
		}
		field := control.Field{Key: prefix, Label: strings.ReplaceAll(strings.TrimPrefix(prefix, "/"), "_", " "), Shape: shape, Writable: true, Unset: true, Class: class, EffectiveWhen: "new_or_resumed_session", Source: res.Sources[prefix]}
		if strings.HasPrefix(field.Source, "env:") || strings.HasPrefix(field.Source, "bundle:") {
			field.Writable = false
			field.LockedReason = "Overridden by " + field.Source
		}
		if strings.HasSuffix(prefix, "/protocol") {
			field.Shape.Enum = []any{"openai-chat", "openai-responses", "anthropic"}
		}
		if strings.HasSuffix(prefix, "/base_url") && prefix != "/finalechat/base_url" {
			field.Shape.Enum = baseURLs
		}
		if strings.HasSuffix(prefix, "/api_key_env") {
			field.Shape.Enum = append(field.Shape.Enum, "")
			for _, v := range s.KnownKeyEnvs() {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		// Leaving these unset is how "none" is chosen.
		if prefix == "/preset" {
			field.Shape.Enum = []any{}
			for _, v := range config.PresetNames() {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
			field.Suggest = &control.Suggest{List: "presets"}
		}
		if prefix == "/default_config" {
			field.Shape.Enum = []any{}
			names, _ := config.ListBundles(s.Project)
			for _, v := range names {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		if prefix == "/relay/default" {
			field.Shape.Enum = []any{}
			for _, v := range res.Effective.Relay.RelayProfileNames() {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		// A route's model offers the models eagent knows of, registered
		// servers' first, each in the group of its provider's address: picking
		// one at another provider sets the route's base_url with it, and
		// applying the pick gives the route the rest of that provider's
		// settings (routePicks). Any other id can still be typed.
		if route, ok := strings.CutSuffix(prefix, "/model"); ok && (route == "/model" || route == "/summarizer/model") {
			field.Suggest = &control.Suggest{List: "models", GroupBy: route + "/base_url"}
		}
		if prefix == "/summarizer/mode" {
			field.Shape.Enum = []any{config.SummarizerInline, config.SummarizerDedicated}
			field.Suggest = &control.Suggest{List: "summarizer_modes"}
		}
		placeProjectField(&field)
		// The summarizer's own route matters only when it is dedicated: it
		// is described either way, so choosing dedicated can give it one,
		// and kept out of the way until then.
		if strings.HasPrefix(prefix, "/summarizer/model/") && !dedicated {
			field.Advanced = true
		}
		if prefix == "/default_config" && len(field.Shape.Enum) == 0 {
			field.Advanced, field.Writable = true, false
			if field.LockedReason == "" {
				field.LockedReason = "No configuration is saved in this project yet."
			}
		}
		out.Descriptor.Fields = append(out.Descriptor.Fields, field)
		if v, ok := pointerValue(onDisk, prefix); ok && v != nil {
			out.Snapshot.Saved[prefix] = v
		}
		if v, ok := pointerValue(effective, prefix); ok && v != nil {
			out.Snapshot.Effective[prefix] = v
		}
	}
	visit(reflect.TypeOf(config.Config{}), "", 0)
	// Local & custom model server registration fields, placed in Models so
	// third-party UIs (like FinaleChat) can add, probe, edit, and remove local
	// servers without leaving the app.
	servers, _ := config.LoadServers(s.Project)
	serverList := make([]any, 0, len(servers))
	for _, srv := range servers {
		entry := map[string]any{
			"id":       srv.ID,
			"url":      srv.BaseURL,
			"label":    srv.Label,
			"protocol": srv.Protocol,
		}
		if srv.KeyEnv != "" {
			entry["key_env"] = srv.KeyEnv
		}
		if srv.Note != "" {
			entry["note"] = srv.Note
		}
		serverList = append(serverList, entry)
	}
	serverFields := []control.Field{
		{
			Key:           "/add_server_url",
			Label:         "Add server URL",
			Description:   "Enter any local or custom model server URL (e.g. http://localhost:1234 or http://gpu-box:8000). Saving queries /models and probes Chat Completions, Responses, and Anthropic Messages automatically.",
			Shape:         control.Shape{Type: "string", MaxLength: 500},
			Writable:      true,
			Unset:         true,
			Class:         "credential_reference",
			EffectiveWhen: "new_or_resumed_session",
			Section:       "Models",
			Group:         "Local servers",
		},
		{
			Key:         "/servers",
			Label:       "Registered servers",
			Description: "Local or custom model servers registered in this project. Edit, switch default protocol, or remove entries.",
			Shape: control.Shape{
				Type: "array",
				Items: &control.Shape{
					Type: "object",
					Properties: map[string]control.Shape{
						"id":         {Type: "string", MaxLength: 48},
						"url":        {Type: "string", MaxLength: 500},
						"label":      {Type: "string", MaxLength: 120},
						"protocol":   {Type: "string", MaxLength: 32, Enum: []any{"", "openai-chat", "openai-responses", "anthropic"}},
						"key_env":    {Type: "string", MaxLength: 120},
						"note":       {Type: "string", MaxLength: 300},
						"skip_probe": {Type: "boolean"},
					},
					Required: []string{"url"},
				},
			},
			Writable:      true,
			Unset:         true,
			Class:         "credential_reference",
			EffectiveWhen: "new_or_resumed_session",
			Section:       "Models",
			Group:         "Local servers",
			Advanced:      true,
		},
	}
	if len(serverList) > 0 {
		out.Snapshot.Saved["/servers"] = serverList
		out.Snapshot.Effective["/servers"] = serverList
	}
	out.Descriptor.Fields = append(out.Descriptor.Fields, serverFields...)
	// The preset comes first among configuration settings: it is the usual choice.
	if p := slices.IndexFunc(out.Descriptor.Fields, func(f control.Field) bool { return f.Key == "/preset" }); p > 0 {
		if d := slices.IndexFunc(out.Descriptor.Fields, func(f control.Field) bool { return f.Key == "/default_config" }); d >= 0 && d < p {
			preset := out.Descriptor.Fields[p]
			out.Descriptor.Fields = slices.Insert(slices.Delete(out.Descriptor.Fields, p, p+1), d, preset)
		}
	}
	savePresetField := control.Field{
		Key:           "/save_preset",
		Label:         "Save as new preset",
		Description:   "Type a name (letters, digits, '-', '_', '.') and tap Save to save this configuration as a reusable preset in this project.",
		Shape:         control.Shape{Type: "string", MaxLength: 64},
		Writable:      true,
		Unset:         true,
		Class:         "permissions",
		EffectiveWhen: "new_or_resumed_session",
		Section:       "Models",
		Group:         "Configuration",
	}
	if d := slices.IndexFunc(out.Descriptor.Fields, func(f control.Field) bool { return f.Key == "/default_config" }); d >= 0 {
		out.Descriptor.Fields = slices.Insert(out.Descriptor.Fields, d+1, savePresetField)
	} else {
		out.Descriptor.Fields = append(out.Descriptor.Fields, savePresetField)
	}
	out.Descriptor.Actions = s.Actions(s.Grant())
	contextFiles := map[string]string{}
	promptViews := map[string]any{}
	if set, err := prompts.Load(s.Project); err == nil {
		for _, name := range prompts.Names {
			contextFiles["prompt:"+name] = artifact.Digest([]byte(set.Text(name)))
			entry := map[string]any{"source": set.Source[name], "size": len(set.Text(name)), "editable": len(set.Text(name)) <= 32768}
			if len(set.Text(name)) <= 32768 {
				entry["text"] = set.Text(name)
			}
			promptViews[name] = entry
		}
	}
	// Overrides of retired prompts are read by nothing; they are listed so
	// they can be reset (deleted), and are never editable.
	for _, path := range prompts.Ignored(s.Project) {
		name := filepath.Base(path)
		raw, err := boundedfile.Read(path, 1<<20)
		if err != nil {
			continue
		}
		contextFiles["prompt:"+name] = artifact.Digest(raw)
		entry := map[string]any{"source": path, "size": len(raw), "editable": false, "retired": true}
		if len(raw) <= 32768 {
			entry["text"] = string(raw)
		}
		promptViews[name] = entry
	}
	names, _ := config.ListBundles(s.Project)
	for _, name := range names {
		if b, err := boundedfile.Read(config.BundlePath(s.Project, name), 1<<20); err == nil {
			contextFiles["bundle:"+name] = artifact.Digest(b)
		}
	}
	if b, err := boundedfile.Read(config.ServersPath(s.Project), 1<<20); err == nil {
		contextFiles["servers"] = artifact.Digest(b)
	}
	// Capability changes must trigger publication even when config bytes stay
	// unchanged, and must invalidate proposals reviewed against an older form.
	versionRaw, _ := json.Marshal(map[string]any{"file": res.File.ETag, "effective": effective, "preset": s.Preset, "bundle": s.Bundle, "descriptor": out.Descriptor, "context": contextFiles})
	out.Snapshot.Version = artifact.Digest(versionRaw)
	keys := map[string]bool{}
	for _, key := range s.KnownKeyEnvs() {
		keys[key] = os.Getenv(key) != ""
	}
	out.Snapshot.Details = map[string]any{"project": s.Project, "file_etag": res.File.ETag, "sources": res.Sources, "active": res.Active, "keys_present": keys, "context_digests": contextFiles, "prompts": promptViews, "bundles": names, "servers": servers}
	// The effective routes, so a reader can show what is actually in force
	// (file overrides included) rather than the preset's own model.
	routeView := func(a config.ModelRoute) map[string]any {
		return map[string]any{"model": a.Model, "base_url": a.BaseURL, "reasoning_effort": a.ReasoningEffort}
	}
	effectiveRoutes := map[string]any{"model": routeView(res.Effective.Model)}
	if dedicated {
		effectiveRoutes["summarizer"] = routeView(res.Effective.Summarizer.Route(res.Effective.Model))
	}
	out.Snapshot.Details["effective_routes"] = effectiveRoutes
	// One configuration version is shared by its archive and every reader.
	// Per-connector write permissions are applied after hashing capabilities.
	for i := range out.Descriptor.Fields {
		field := &out.Descriptor.Fields[i]
		if !slices.Contains(grant.Classes, field.Class) || !slices.Contains(grant.Operations, "settings.apply") {
			field.Writable = false
			if field.LockedReason == "" {
				field.LockedReason = "This connector has no grant for " + field.Class
			}
		}
	}
	out.Descriptor.Actions = s.Actions(grant)
	// A descriptor holds at most suggestionLists lists. Presets, the summarizer's
	// modes, eagent's own model list and the providers (which name the groups
	// every model picker shows) always have theirs; each agent's models take
	// one while there is room, and an input whose list does not fit is left
	// to typing. eagent's model list keeps to what the agents' lists leave of
	// its budget. Saved presets' list comes last: /default_config offers
	// their names without it.
	out.Descriptor.Suggestions = map[string][]control.Suggestion{"presets": PresetSuggestions(s.Project), "summarizer_modes": SummarizerModeSuggestions()}
	relayBytes := 0
	for ai := range out.Descriptor.Actions {
		a := &out.Descriptor.Actions[ai]
		if a.Operation != "session.start" {
			continue
		}
		for i := range a.Inputs {
			in := &a.Inputs[i]
			if in.Suggest == nil || in.Suggest.List == "models" {
				continue
			}
			if agent, ok := strings.CutSuffix(in.Suggest.List, "_models"); ok && out.Descriptor.Suggestions[in.Suggest.List] == nil {
				if len(out.Descriptor.Suggestions) >= suggestionLists-2 {
					in.Suggest = nil
					continue
				}
				list := RelayModelSuggestions(agent, s.Project)
				for _, sug := range list {
					relayBytes += suggestionSize(sug)
				}
				out.Descriptor.Suggestions[in.Suggest.List] = list
			}
		}
	}
	out.Descriptor.Suggestions["models"] = projectModelSuggestions(s.Project, modelListBudget-relayBytes)
	// The providers name the groups models come in, by address: a reader
	// shows "Together AI", not its URL.
	out.Descriptor.Suggestions["providers"] = providerAddressSuggestions(s.Project, baseURLs)
	for i := range out.Descriptor.Fields {
		if f := &out.Descriptor.Fields[i]; strings.HasSuffix(f.Key, "/base_url") && f.Key != "/finalechat/base_url" {
			f.Suggest = &control.Suggest{List: "providers"}
		}
	}
	if bundleSugs := BundleSuggestions(s.Project); len(bundleSugs) > 0 && len(out.Descriptor.Suggestions) < suggestionLists {
		out.Descriptor.Suggestions["bundles"] = bundleSugs
		for i := range out.Descriptor.Fields {
			if out.Descriptor.Fields[i].Key == "/default_config" {
				out.Descriptor.Fields[i].Suggest = &control.Suggest{List: "bundles"}
				break
			}
		}
	}
	if err := out.Descriptor.Validate(grant); err != nil {
		return out, err
	}
	return out, out.Snapshot.Validate()
}

// fileValues decodes config.json's bytes, carried over to today's keys when
// the file still uses those of eagent's earlier design, so every pointer
// reads and edits today's layout and a remote save writes it.
func fileValues(raw string) (map[string]any, error) {
	values := map[string]any{}
	if raw == "" {
		return values, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	if m == nil {
		// A file holding null loads as no file; edits start from nothing.
		return values, nil
	}
	if !config.HasLegacyKeys(m) {
		return values, json.Unmarshal([]byte(raw), &values)
	}
	m, _ = config.MigrateLegacy(m)
	migrated, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return values, json.Unmarshal(migrated, &values)
}

func pointerValue(m map[string]any, p string) (any, bool) {
	var v any = m
	for _, key := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return v, true
}
func setPointer(m map[string]any, p string, value any, unset bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	var edit func(map[string]any, []string)
	edit = func(parent map[string]any, keys []string) {
		key := keys[0]
		if len(keys) == 1 {
			if unset {
				delete(parent, key)
			} else {
				parent[key] = value
			}
			return
		}
		next, ok := parent[key].(map[string]any)
		if !ok {
			if unset {
				return
			}
			next = map[string]any{}
			parent[key] = next
		}
		edit(next, keys[1:])
		if unset && len(next) == 0 {
			delete(parent, key)
		}
	}
	edit(m, parts)
}

// ValidateSavePresetName checks that a user-entered preset name is valid and
// does not collide with a built-in preset name or alias.
func ValidateSavePresetName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("preset name is required")
	}
	if !config.ValidBundleName(name) {
		return "", fmt.Errorf("bundle names use letters, digits, '-', '_' and '.' only")
	}
	if _, builtIn := config.ResolvePreset(name); builtIn {
		return "", fmt.Errorf("%q is a built-in preset name; choose a different name", name)
	}
	return name, nil
}

// SplitSpecialEdits separates virtual action fields (/add_server_url, /servers,
// /save_preset) from ordinary config.json edits.
func SplitSpecialEdits(p control.Proposal) (serverEdits []control.Edit, savePreset string, hasSavePreset bool, configEdits []control.Edit) {
	for _, e := range p.Edits {
		switch e.Key {
		case "/add_server_url", "/servers":
			serverEdits = append(serverEdits, e)
		case "/save_preset":
			if e.Op == "set" {
				s, _ := e.Value.(string)
				savePreset = strings.TrimSpace(s)
				hasSavePreset = true
			}
		default:
			configEdits = append(configEdits, e)
		}
	}
	return serverEdits, savePreset, hasSavePreset, configEdits
}

// PrepareSavePreset prepares a journalable bundle write for a settings.apply
// proposal that snapshots the project's current effective configuration via
// /save_preset (with no other edits).
func (s *Service) PrepareSavePreset(editor *config.Editor, p control.Proposal, grant control.Grant) (ResourceChange, error) {
	var change ResourceChange
	if _, err := s.ValidateServerEdits(p, grant); err != nil {
		return change, err
	}
	_, rawName, hasSavePreset, _ := SplitSpecialEdits(p)
	if !hasSavePreset {
		return change, fmt.Errorf("preset name is required")
	}
	name, err := ValidateSavePresetName(rawName)
	if err != nil {
		return change, err
	}
	cfg, err := CurrentLayers(s.Project, s.Preset, s.Bundle)
	if err != nil {
		return change, err
	}
	cfg.Preset, cfg.Name, cfg.DefaultConfig, cfg.Instructions = "", "", "", ""
	if err := cfg.Validate(); err != nil {
		return change, err
	}
	if err := s.CheckRoutes(cfg); err != nil {
		return change, err
	}
	after, err := config.BundleBytes(name, "", cfg)
	if err != nil {
		return change, err
	}
	path, err := editor.RelatedPath("bundle", name)
	if err != nil {
		return change, err
	}
	before, err := boundedfile.Read(path, 1<<20)
	if err != nil && !os.IsNotExist(err) {
		return change, err
	}
	if len(before) > 1<<20 {
		return change, fmt.Errorf("resource exceeds 1 MiB")
	}
	return ResourceChange{
		Kind:         "bundle",
		Name:         name,
		Before:       before,
		BeforeExists: err == nil,
		After:        after,
		AfterExists:  true,
	}, nil
}

// SaveEffectiveAsPreset writes the project's current effective configuration
// as a standalone named preset while the caller holds editor's lock.
// CurrentLayers is the configuration in force as every save of it keeps
// it, with the layers below the environment: never a value only an
// environment variable gives (a bundle EAGENT_CONFIG chooses is a file,
// and its values count). Resolve lays a preset and a bundle as loading
// does (a saved bundle an explicit preset names, a built-in named as the
// bundle being the preset), but leaves the bundle loading takes by
// default to its callers: that is config.DefaultBundle, the loader's own.
func CurrentLayers(project, preset, bundle string) (config.Config, error) {
	if _, err := config.LoadBundle(project, preset, bundle); err != nil {
		return config.Config{}, err // what is in force must load, as on main
	}
	res := config.Resolve(project, preset, bundle)
	// Whether a bundle is laid is the bundle Resolve applied (Active), never
	// a "name" config.json may carry (`config --write` once wrote one). A
	// file that is whole only with its default bundle does not check out
	// without it, and is read with it all the same.
	if bundle == "" && res.Active.Kind != "bundle" {
		var file map[string]json.RawMessage
		_ = json.Unmarshal([]byte(res.File.Raw), &file)
		if b := config.DefaultBundle(file); b != "" {
			res = config.Resolve(project, preset, b)
		}
	}
	if res.LoadError != "" {
		return config.Config{}, errors.New(res.LoadError)
	}
	return res.Layered, nil
}

func (s *Service) SaveEffectiveAsPreset(editor *config.Editor, name string) (string, error) {
	name, err := ValidateSavePresetName(name)
	if err != nil {
		return "", err
	}
	cfg, err := CurrentLayers(s.Project, s.Preset, s.Bundle)
	if err != nil {
		return "", err
	}
	cfg.Preset, cfg.Name, cfg.DefaultConfig, cfg.Instructions = "", "", "", ""
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if err := s.CheckRoutes(cfg); err != nil {
		return "", err
	}
	return editor.SaveBundle(name, "", cfg)
}

// ValidateServerEdits checks a proposal containing /add_server_url or /servers
// against the current snapshot and grant without touching disk or network.
// The proposal's other edits are judged when they are prepared, after the
// servers are in place, except a preset pick that would be refused.
func (s *Service) ValidateServerEdits(p control.Proposal, grant control.Grant) (RemoteView, error) {
	before, err := s.RemoteSnapshot(grant)
	if err != nil {
		return RemoteView{}, err
	}
	if err := p.Validate(before.Descriptor, grant); err != nil {
		return RemoteView{}, err
	}
	if p.ExpectedVersion != before.Snapshot.Version || p.Generation != "" {
		return RemoteView{}, &config.ErrConflict{ETag: before.Snapshot.Version}
	}
	if p.Operation != "settings.apply" {
		return RemoteView{}, fmt.Errorf("operation is not a configuration edit")
	}
	for _, e := range p.Edits {
		if e.Key != "/servers" || e.Op == "unset" {
			continue
		}
		specs, err := serverSpecs(e.Value)
		if err != nil {
			return RemoteView{}, err
		}
		if err := s.checkServerKeys(specs); err != nil {
			return RemoteView{}, err
		}
	}
	// A preset pick in the same proposal that is refused (routePicks) is
	// refused here, before a server is touched: its refusal says nothing
	// was changed. What else the file's edits come to is judged once the
	// servers are in place, as they may name one of them.
	if _, _, _, err := s.Prepare(p, grant); errors.As(err, new(*pickRefused)) {
		return RemoteView{}, err
	}
	return before, nil
}

// pickRefused is the refusal of a preset pick whose preset would not run
// as it is (routePicks), or of a summarizer route the proposal may not
// write whole (prepare). Nothing is written for a proposal it refuses.
type pickRefused struct{ why string }

func (e *pickRefused) Error() string { return e.why }

func serverSpecs(v any) ([]config.SyncServerSpec, error) {
	var specs []config.SyncServerSpec
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("/servers: %w", err)
	}
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("/servers: %w", err)
	}
	return specs, nil
}

// checkServerKeys holds a remote server list to the rule CheckRoutes holds
// routes to: probing or saving a server sends its key variable's value to its
// address, so a remote edit may name a key variable only for an address the
// local user registered with that same variable. Anything else is registered
// without a key, or from the CLI or the local web UI.
func (s *Service) checkServerKeys(specs []config.SyncServerSpec) error {
	var existing []config.RegisteredServer
	for _, sp := range specs {
		keyEnv := strings.TrimSpace(sp.KeyEnv)
		if keyEnv == "" {
			continue
		}
		if existing == nil {
			var err error
			if existing, err = config.LoadServers(s.Project); err != nil {
				return err
			}
		}
		cands, _, err := config.NormalizeServerURL(sp.URL)
		if err != nil {
			return fmt.Errorf("/servers: %w", err)
		}
		if !slices.ContainsFunc(existing, func(r config.RegisteredServer) bool {
			return r.KeyEnv == keyEnv && slices.Contains(cands, strings.TrimRight(r.BaseURL, "/"))
		}) {
			return fmt.Errorf("/servers: %s is not the key variable registered for %s; a server's key is set from the CLI (eagent servers) or eagent's own web UI", keyEnv, strings.TrimSpace(sp.URL))
		}
	}
	return nil
}

// ApplyServerEdits executes /servers and /add_server_url edits from a validated
// proposal (probing new or changed URLs outside any file lock, then atomically
// updating servers.json and live-models.json). Callers must NOT hold LockEditor.
func (s *Service) ApplyServerEdits(ctx context.Context, serverEdits []control.Edit) ([]config.RegisteredServer, error) {
	var (
		hasServers bool
		specs      []config.SyncServerSpec
		addURL     string
	)
	for _, e := range serverEdits {
		switch e.Key {
		case "/servers":
			hasServers = true
			if e.Op == "unset" {
				specs = nil
				continue
			}
			var err error
			if specs, err = serverSpecs(e.Value); err != nil {
				return nil, err
			}
		case "/add_server_url":
			if e.Op == "set" {
				u, _ := e.Value.(string)
				addURL = strings.TrimSpace(u)
			}
		}
	}
	if hasServers {
		// Checked again here: servers.json may have changed since the
		// proposal was reviewed.
		if err := s.checkServerKeys(specs); err != nil {
			return nil, err
		}
		if addURL != "" {
			specs = append(specs, config.SyncServerSpec{URL: addURL})
		}
		return config.SyncServers(ctx, s.Project, specs)
	}
	if addURL != "" {
		if _, err := config.RegisterServer(ctx, s.Project, config.ProbeOptions{URL: addURL}, false); err != nil {
			return nil, err
		}
	}
	return config.LoadServers(s.Project)
}

// Prepare produces the exact intended file bytes while the caller holds the
// project lock. A connector journals these bytes before performing the write.
func (s *Service) Prepare(p control.Proposal, grant control.Grant) (raw string, etag string, before RemoteView, err error) {
	return s.prepare(p, grant, false, nil)
}

// CheckWrite holds a prepared configuration file to the check a fresh
// write of it meets, when it is written: what it makes must load (a saved
// preset its default_config names may have been deleted since).
func (s *Service) CheckWrite(raw string) error {
	var file map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		return err
	}
	_, err := config.LoadBundleWithFile(s.Project, s.Preset, s.Bundle, file)
	return err
}

// prepare is Prepare. A person's pick of a route's model, address or preset
// brings the rest of the route with it (the summarizer's seed and
// routePicks); an undo's reversal (undo) writes what it names and no other,
// and may set a route with no value, which writes it as null (a fallback
// turned off). Either file is checked as a person's save of it is, except
// that for a reversal a route pairing an address and a key variable as a
// route in force at the command did (inForce, RoutePairs) is checked as
// one saved here.
func (s *Service) prepare(p control.Proposal, grant control.Grant, undo bool, inForce [][2]string) (raw string, etag string, before RemoteView, err error) {
	picks := !undo
	before, err = s.RemoteSnapshot(grant)
	if err != nil {
		return
	}
	// A reversal's null route removes the fields the command wrote into it,
	// which are the ones the file has there now (its conflict check saw the
	// route as the command left it): it is checked as an unset of each, and
	// of no other field.
	checks := p
	if undo {
		current, _ := fileValues(config.Resolve(s.Project, s.Preset, s.Bundle).File.Raw)
		checks.Edits = nil
		for _, e := range p.Edits {
			if e.Op != "set" || e.Value != nil || !isRoute(e.Key) {
				checks.Edits = append(checks.Edits, e)
				continue
			}
			for _, f := range before.Descriptor.Fields {
				if v, ok := pointerValue(current, f.Key); ok && strings.HasPrefix(f.Key, e.Key+"/") {
					if _, object := v.(map[string]any); !object {
						checks.Edits = append(checks.Edits, control.Edit{Op: "unset", Key: f.Key})
					}
				}
			}
		}
	}
	// A reversal may put back the address and the key variable of a pair
	// in force at the command: the command may have removed the one place
	// either was written (a preset pick drops the file's own route).
	d := before.Descriptor
	var recorded []config.ModelRoute
	if undo && len(inForce) > 0 {
		for _, pair := range inForce {
			recorded = append(recorded, config.ModelRoute{BaseURL: pair[0], APIKeyEnv: pair[1]})
		}
		d.Fields = slices.Clone(d.Fields)
		for i, f := range d.Fields {
			enum := slices.Clone(f.Shape.Enum)
			switch {
			case strings.HasSuffix(f.Key, "/api_key_env"):
				for _, pair := range inForce {
					if pair[1] != "" && !slices.Contains(enum, any(pair[1])) {
						enum = append(enum, pair[1])
					}
				}
			case strings.HasSuffix(f.Key, "/base_url") && f.Key != "/finalechat/base_url":
				for _, e := range p.Edits {
					v, _ := e.Value.(string)
					if e.Key == f.Key && v != "" && !slices.Contains(enum, any(v)) && slices.ContainsFunc(inForce, func(pair [2]string) bool { return pair[0] == strings.TrimRight(v, "/") }) {
						enum = append(enum, v)
					}
				}
			default:
				continue
			}
			d.Fields[i].Shape.Enum = enum
		}
	}
	if err = checks.Validate(d, grant); err != nil {
		return
	}
	if p.ExpectedVersion != before.Snapshot.Version || p.Generation != "" {
		err = &config.ErrConflict{ETag: before.Snapshot.Version}
		return
	}
	if p.Operation != "settings.apply" {
		err = fmt.Errorf("operation is not a configuration edit")
		return
	}
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	etag = res.File.ETag
	var values map[string]any
	if values, err = fileValues(res.File.Raw); err != nil {
		return
	}
	// The layers the file builds on, as the loader lays them: a bundle in
	// force through EAGENT_CONFIG or default_config included, else (one
	// that does not load, which an edit may be clearing) the file's own.
	layers := res.Layered
	if inForce, err := CurrentLayers(s.Project, s.Preset, s.Bundle); err == nil {
		layers = inForce
	}
	// A dedicated summarizer with no route written calls the main model's.
	// An edit of its route starts from that one, so a model picked alone
	// keeps the provider it is reached by. A route written as null is none.
	// Where a saved preset in force gives the summarizer its route, the
	// edit is of that one, and only the fields it names are written;
	// unless the file itself makes the summarizer dedicated: a field alone
	// there would not load without that preset. The mode is the one this
	// change leaves.
	mode, _ := pointerValue(values, "/summarizer/mode")
	for _, e := range p.Edits {
		if e.Key == "/summarizer/mode" {
			mode = nil
			if e.Op == "set" {
				mode = e.Value
			}
		}
	}
	if v, written := pointerValue(values, "/summarizer/model"); picks && (!written || v == nil) && (layers.Summarizer.Model == nil || mode == config.SummarizerDedicated) && slices.ContainsFunc(p.Edits, func(e control.Edit) bool {
		return e.Op == "set" && strings.HasPrefix(e.Key, "/summarizer/model/")
	}) {
		// The route as the layers write it: a variable's value is not the
		// file's to keep.
		seed := layers.Model
		seed.Fallback = nil
		var route map[string]any
		b, _ := json.Marshal(seed)
		_ = json.Unmarshal(b, &route)
		// The whole route is written, so the proposal must be allowed
		// every field of it: one it may not write (its grant does not
		// cover where a model is reached, or its key variable) is not
		// written for it, and its undo could not take it back.
		for _, f := range before.Descriptor.Fields {
			leaf, under := strings.CutPrefix(f.Key, "/summarizer/model/")
			if _, seeded := route[leaf]; under && seeded && !f.Writable {
				err = &pickRefused{fmt.Sprintf("the project file gives the summarizer no model of its own yet, and giving it one there writes %s, which this change may not write (%s); nothing was changed", f.Key, f.LockedReason)}
				return
			}
		}
		setPointer(values, "/summarizer/model", route, false)
	}
	var named string // the preset the file names, before this change
	if v, _ := pointerValue(values, "/preset"); v != nil {
		named, _ = v.(string)
	}
	for _, e := range p.Edits {
		if e.Key == "/add_server_url" || e.Key == "/servers" {
			continue
		}
		if e.Key == "/save_preset" {
			if e.Op == "set" {
				rawName, _ := e.Value.(string)
				if _, err = ValidateSavePresetName(rawName); err != nil {
					return
				}
			}
			continue
		}
		setPointer(values, e.Key, e.Value, e.Op == "unset")
	}
	// Picks are compared with the layers the file builds on, which the
	// summarizer's route above is seeded from: what the environment makes
	// of a route is not the file's.
	if picks {
		if err = s.routePicks(values, p.Edits, layers, named, before.Descriptor); err != nil {
			return
		}
	}
	// No change leaves the file with part of a route for a summarizer that
	// is dedicated, where it had no such part before (the mode set over a
	// field kept for it, or a field unset from a whole route): that file
	// loads only under a saved preset that gives the rest, and without it
	// cannot be read here at all.
	if was, _ := fileValues(res.File.Raw); picks && needsPreset(values) && !needsPreset(was) {
		err = &pickRefused{"the project file would make the summarizer dedicated with only part of a model of its own, and would then load only with a saved preset that gives the rest; nothing was changed"}
		return
	}
	var candidate []byte
	candidate, err = json.MarshalIndent(values, "", "  ")
	if err != nil {
		return
	}
	candidate = append(candidate, '\n')
	var file map[string]json.RawMessage
	err = json.Unmarshal(candidate, &file)
	if err != nil {
		return
	}
	var cfg config.Config
	cfg, err = config.LoadBundleWithFile(s.Project, s.Preset, s.Bundle, file)
	if err != nil {
		return
	}
	// The route checks take a recorded pair as one saved here.
	if err = s.CheckRemoteRoutes(cfg, res.Effective, recorded...); err != nil {
		return
	}
	for _, problem := range cfg.Problems() {
		if problem.Severity == "error" {
			err = fmt.Errorf("%s: %s", problem.Path, problem.Message)
			return
		}
	}
	raw = string(candidate)
	return
}

// needsPreset reports whether a project file's values make the summarizer
// dedicated and write only part of a route for it: one with no protocol,
// address or model, where no variable gives it either. The loader takes a
// route as written, so that file loads only under a saved preset that
// gives the rest.
func needsPreset(values map[string]any) bool {
	mode, _ := pointerValue(values, "/summarizer/mode")
	own, _ := pointerValue(values, "/summarizer/model")
	has, _ := own.(map[string]any)
	if mode != config.SummarizerDedicated || len(has) == 0 {
		return false
	}
	env := config.EnvOverrides()
	return slices.ContainsFunc([]string{"protocol", "base_url", "model"}, func(leaf string) bool {
		v, _ := has[leaf].(string)
		_, given := env["/summarizer/model/"+leaf]
		return v == "" && !given
	})
}

// routePicks gives a route whose model or base_url a proposal changes the
// rest of what that model at that provider takes, the way a pick in the
// local editor does: its protocol, key variable, an effort it accepts, and
// the new model's context window. A field the proposal
// sets itself, or one the connector may not write, is left as it is. A
// model no list knows gets the provider's protocol and key variable when
// the address changes, keeps the rest of the route, and its window becomes
// unknown. The fallback is kept, as the local editor keeps it. named is
// the preset the file named before the proposal's edits.
func (s *Service) routePicks(values map[string]any, edits []control.Edit, prior config.Config, named string, d control.Descriptor) error {
	writable, locked := map[string]bool{}, map[string]string{}
	for _, f := range d.Fields {
		writable[f.Key], locked[f.Key] = f.Writable, f.LockedReason
	}
	// sets says the proposal gives a field a value of its own. One it
	// unsets takes what the layers give, like one it leaves alone (the
	// settings page unsets the file's model beside a preset it chooses).
	sets := func(key string) bool {
		return slices.ContainsFunc(edits, func(e control.Edit) bool { return e.Key == key && e.Op == "set" })
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return err
	}
	after, err := config.LoadBundleWithFile(s.Project, s.Preset, s.Bundle, file)
	if err != nil && after.Model.Model == "" {
		return nil // reported once the edits are applied
	}
	// A preset chosen or unset without a model is a pick of the main route:
	// choosing one sets the model to it, as /preset says. (A saved preset
	// chosen or unset, /default_config, can bring another preset with it
	// and is no pick: the file's own route is not that change's to remove.)
	// The pick is the preset the proposal leaves the file naming ("" for
	// none) where the file named another, whatever a layer above the file
	// names or then makes of it: the file's own choice of model goes all
	// the same, and where that layer keeps another model running, the
	// check below refuses the pick.
	asked := ""
	if v, _ := pointerValue(values, "/preset"); v != nil {
		asked, _ = v.(string)
	}
	if canon, ok := config.ResolvePreset(asked); ok {
		asked = canon
	}
	if canon, ok := config.ResolvePreset(named); ok {
		named = canon
	}
	presetPick := asked != named && !sets("/model/model") && !sets("/model/base_url")
	if presetPick {
		// The file's own choice of model and of where it is reached goes,
		// by the rule `eagent presets use` and a session's pick have
		// (config.DropModelChoice): left in the file it would run under
		// the preset in part (the file's address with the preset's model
		// or protocol). Unsetting the preset chooses the defaults, and
		// goes by the same rule. The route's effort and reply ceiling
		// stay, and so does its fallback, a route of its own, as for
		// every pick here. A field the proposal names keeps the
		// proposal's value. A field the connector may not write (a
		// variable or a saved preset sets it here, or its grant does not
		// cover it) stays as it is.
		rest, _ := config.DropModelChoice(file)
		var was, left map[string]json.RawMessage
		_ = json.Unmarshal(file["model"], &was)
		_ = json.Unmarshal(rest["model"], &left)
		dropped := false
		for leaf := range was {
			if _, kept := left[leaf]; !kept && leaf != "fallback" && !sets("/model/"+leaf) && writable["/model/"+leaf] {
				setPointer(values, "/model/"+leaf, nil, true)
				dropped = true
			}
		}
		if dropped {
			return s.routePicks(values, edits, prior, named, d)
		}
		// What then runs must be the preset's route (the defaults', when
		// the preset was unset), but for what the proposal names. Where a
		// part that stayed makes it another, nothing is written, as
		// `eagent presets use` writes nothing: the settings would name a
		// preset whose model runs at another address or with another key.
		if err == nil {
			want := config.Defaults()
			if canon, ok := config.ResolvePreset(asked); ok {
				config.Presets[canon](&want)
			}
			var parts []string
			for _, f := range []struct{ leaf, now, want string }{
				{"model", after.Model.Model, want.Model.Model},
				{"base_url", strings.TrimRight(after.Model.BaseURL, "/"), strings.TrimRight(want.Model.BaseURL, "/")},
				{"protocol", after.Model.Protocol, want.Model.Protocol},
				{"api_key_env", after.Model.APIKeyEnv, want.Model.APIKeyEnv},
			} {
				if f.now == f.want || sets("/model/"+f.leaf) {
					continue
				}
				why := "set outside the project file"
				if name, ok := config.EnvOverrides()["/model/"+f.leaf]; ok {
					why = name + " sets it"
				} else if _, own := pointerValue(values, "/model/"+f.leaf); own {
					if why = locked["/model/"+f.leaf]; why == "" {
						why = "this change may not write it"
					}
				} else if after.Name != "" {
					why = fmt.Sprintf("the saved preset %q sets it", after.Name)
				}
				parts = append(parts, fmt.Sprintf("%s stays %s (%s)", f.leaf, f.now, why))
			}
			if len(parts) > 0 {
				chosen := fmt.Sprintf("the preset %s (%s @ %s) would not run as it is", asked, want.Model.Model, want.Model.BaseURL)
				if asked == "" {
					chosen = fmt.Sprintf("the defaults (%s @ %s) would not run as they are", want.Model.Model, want.Model.BaseURL)
				}
				return &pickRefused{fmt.Sprintf("eagent's %s, so %s; nothing was changed", strings.Join(parts, ", and its "), chosen)}
			}
		}
	}
	summarizer := prior.Summarizer.Route(prior.Model)
	if prior.Summarizer.Model == nil {
		summarizer.Fallback = nil
	}
	// after has the environment applied; a field a variable sets is not
	// the proposal's to change, so it reads as the layers have it.
	env := config.EnvOverrides()
	layered := func(r config.ModelRoute, ptr string, base config.ModelRoute) config.ModelRoute {
		for leaf, f := range map[string][2]*string{
			"model":            {&r.Model, &base.Model},
			"base_url":         {&r.BaseURL, &base.BaseURL},
			"protocol":         {&r.Protocol, &base.Protocol},
			"api_key_env":      {&r.APIKeyEnv, &base.APIKeyEnv},
			"reasoning_effort": {&r.ReasoningEffort, &base.ReasoningEffort},
		} {
			if _, ok := env[ptr+"/"+leaf]; ok {
				*f[0] = *f[1]
			}
		}
		return r
	}
	afterMain := layered(after.Model, "/model", prior.Model)
	afterSummarizer := afterMain
	if after.Summarizer.Model != nil && after.Summarizer.Model.Model != "" {
		afterSummarizer = layered(*after.Summarizer.Model, "/summarizer/model", summarizer)
	}
	// Every route the file can hold: each one's fallback is a route too,
	// with an address and a key variable of its own.
	type routePair struct {
		ptr           string
		before, after config.ModelRoute
	}
	var routes []routePair
	var walk func(ptr string, before, after *config.ModelRoute, depth int)
	walk = func(ptr string, before, after *config.ModelRoute, depth int) {
		if after == nil || depth > 3 {
			return
		}
		var b config.ModelRoute
		if before != nil {
			b = *before
		}
		routes = append(routes, routePair{ptr, b, *after})
		walk(ptr+"/fallback", b.Fallback, after.Fallback, depth+1)
	}
	walk("/model", &prior.Model, &afterMain, 0)
	walk("/summarizer/model", &summarizer, &afterSummarizer, 0)
	for _, r := range routes {
		// set holds the fields the proposal names, which keep its values.
		// Setting or unsetting a model or an address is a pick: the route
		// is reached at what results, and the rest of it must match. A
		// route the edit leaves with no field of its own in the file is the
		// layers' as a whole, and nothing is given to it.
		set, isPick := map[string]bool{}, false
		for _, e := range edits {
			if leaf, ok := strings.CutPrefix(e.Key, r.ptr+"/"); ok && !strings.Contains(leaf, "/") {
				set[leaf] = true
				isPick = isPick || leaf == "model" || leaf == "base_url"
			}
		}
		if !isPick && !(presetPick && r.ptr == "/model") {
			continue
		}
		own, _ := pointerValue(values, r.ptr)
		leaves, _ := own.(map[string]any)
		if len(leaves) == 0 || len(leaves) == 1 && leaves["fallback"] != nil {
			continue
		}
		if r.after.Model == r.before.Model && strings.TrimRight(r.after.BaseURL, "/") == strings.TrimRight(r.before.BaseURL, "/") {
			continue
		}
		give := func(leaf string, v any) {
			if !set[leaf] && writable[r.ptr+"/"+leaf] {
				setPointer(values, r.ptr+"/"+leaf, v, v == nil)
			}
		}
		// The window is the new model's: what the file says for the old
		// one goes, and loading names the new one's (or 0, unknown).
		give("context_tokens", nil)
		pr, ok := config.ProviderForURLFor(s.Project, r.after.BaseURL)
		if !ok {
			continue
		}
		// A preset chosen in the same proposal brings its own effort, as
		// its suggestion implies: the old preset's is not carried over.
		seed := r.before
		if after.Preset != prior.Preset {
			seed.ReasoningEffort = r.after.ReasoningEffort
		}
		picked, err := config.PickRoute(s.Project, seed, pr.ID, r.after.Model, "")
		if err != nil {
			// A model no list knows keeps the rest of the route at the same
			// provider. At a new address it takes that provider's protocol
			// and key variable: the old provider's key is not sent there.
			if strings.TrimRight(r.after.BaseURL, "/") == strings.TrimRight(r.before.BaseURL, "/") {
				continue
			}
			picked = r.after
			picked.Protocol, picked.APIKeyEnv = pr.Protocol, pr.KeyEnv
		}
		if picked.Protocol != r.after.Protocol {
			give("protocol", picked.Protocol)
		}
		if picked.APIKeyEnv != r.after.APIKeyEnv {
			give("api_key_env", picked.APIKeyEnv)
		}
		if picked.ReasoningEffort != r.after.ReasoningEffort {
			give("reasoning_effort", picked.ReasoningEffort)
		}
	}
	return nil
}

func (s *Service) Apply(ctx context.Context, p control.Proposal, grant control.Grant) (config.SaveResult, RemoteView, error) {
	serverEdits, savePreset, hasSavePreset, configEdits := SplitSpecialEdits(p)
	if hasSavePreset {
		var err error
		if savePreset, err = ValidateSavePresetName(savePreset); err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
	}
	if len(serverEdits) > 0 {
		if _, err := s.ValidateServerEdits(p, grant); err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
		if _, err := s.ApplyServerEdits(ctx, serverEdits); err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
		if len(configEdits) == 0 && !hasSavePreset {
			after, err := s.RemoteSnapshot(grant)
			return config.SaveResult{Path: config.ServersPath(s.Project)}, after, err
		}
		// Refresh version after server sync so remaining edits validate
		// against the updated server list (e.g. newly added host).
		mid, err := s.RemoteSnapshot(grant)
		if err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
		remaining := append([]control.Edit{}, configEdits...)
		if hasSavePreset {
			remaining = append(remaining, control.Edit{Op: "set", Key: "/save_preset", Value: savePreset})
		}
		p.Edits = remaining
		p.ExpectedVersion = mid.Snapshot.Version
	}
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return config.SaveResult{}, RemoteView{}, err
	}
	defer editor.Close()
	if len(configEdits) == 0 && hasSavePreset {
		change, err := s.PrepareSavePreset(editor, p, grant)
		if err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
		if err := change.Apply(editor); err != nil {
			return config.SaveResult{}, RemoteView{}, err
		}
		after, err := s.RemoteSnapshot(grant)
		return config.SaveResult{Path: config.BundlePath(s.Project, change.Name)}, after, err
	}
	raw, etag, _, err := s.Prepare(p, grant)
	if err != nil {
		return config.SaveResult{}, RemoteView{}, err
	}
	result, err := editor.SaveRaw(raw, etag, true)
	if err != nil {
		return result, RemoteView{}, err
	}
	if hasSavePreset {
		if _, err := s.SaveEffectiveAsPreset(editor, savePreset); err != nil {
			return result, RemoteView{}, err
		}
	}
	after, err := s.RemoteSnapshot(grant)
	return result, after, err
}
