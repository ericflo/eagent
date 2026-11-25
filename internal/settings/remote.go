package settings

import (
	"context"
	"encoding/json"
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
	var effective, onDisk map[string]any
	raw, _ := json.Marshal(res.Effective)
	_ = json.Unmarshal(raw, &effective)
	if res.File.Raw != "" {
		if err := json.Unmarshal([]byte(res.File.Raw), &onDisk); err != nil {
			return RemoteView{}, err
		}
	}
	if onDisk == nil {
		onDisk = map[string]any{}
	}
	out := RemoteView{Key: grant.Key, Label: grant.Label, Scope: grant.Scope, Descriptor: control.Descriptor{Format: control.Format, SchemaVersion: SchemaVersion, AdapterVersion: "1", Fields: []control.Field{}}, Snapshot: control.Snapshot{Context: s.Project, Saved: map[string]any{}, Effective: map[string]any{}, RuntimeKnown: false}}
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
		if prefix == "/allow_outside_project" || prefix == "/default_config" {
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
			for _, pr := range config.AllProviders(s.Project) {
				field.Shape.Enum = append(field.Shape.Enum, pr.BaseURL)
			}
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
		placeProjectField(&field)
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
	// The effective per-actor routes, so a reader can show what is actually
	// in force (file overrides included) rather than the preset's own lineup.
	out.Snapshot.Details["effective_routes"] = map[string]any{
		"orchestrator": map[string]any{"model": res.Effective.Orchestrator.Model, "base_url": res.Effective.Orchestrator.BaseURL, "reasoning_effort": res.Effective.Orchestrator.ReasoningEffort},
		"task":         map[string]any{"model": res.Effective.Task.Model, "base_url": res.Effective.Task.BaseURL, "reasoning_effort": res.Effective.Task.ReasoningEffort},
		"narrator":     map[string]any{"model": res.Effective.Narrator.Model, "base_url": res.Effective.Narrator.BaseURL, "reasoning_effort": res.Effective.Narrator.ReasoningEffort},
	}
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
	out.Descriptor.Suggestions = map[string][]control.Suggestion{"configs": ConfigSuggestions(s.Project), "presets": PresetSuggestions(s.Project), "providers": ProviderSuggestions(s.Project)}
	for ai := range out.Descriptor.Actions {
		a := &out.Descriptor.Actions[ai]
		if a.Operation != "session.start" {
			continue
		}
		// Each relayed agent's models, for its model input. A descriptor
		// holds at most eight lists (the "models" list is the eighth here);
		// an input whose list does not fit is left to typing. eagent's own
		// model list keeps to what the relay lists leave of its budget.
		relayBytes := 0
		for i := range a.Inputs {
			in := &a.Inputs[i]
			if in.Suggest == nil || in.Suggest.List == "models" {
				continue
			}
			if agent, ok := strings.CutSuffix(in.Suggest.List, "_models"); ok && out.Descriptor.Suggestions[in.Suggest.List] == nil {
				if len(out.Descriptor.Suggestions) >= 7 {
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
		out.Descriptor.Suggestions["models"] = modelSuggestionsWithin(s.Project, modelListBudget-relayBytes)
	}
	if bundleSugs := BundleSuggestions(s.Project); len(bundleSugs) > 0 && len(out.Descriptor.Suggestions) < 8 {
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

// HasServerEdits reports whether a settings.apply proposal modifies registered
// local/custom model servers (/add_server_url or /servers).
func HasServerEdits(p control.Proposal) bool {
	if p.Operation != "settings.apply" {
		return false
	}
	for _, e := range p.Edits {
		if e.Key == "/add_server_url" || e.Key == "/servers" {
			return true
		}
	}
	return false
}

// SplitServerEdits separates server-registry edits (/add_server_url, /servers)
// from config.json edits.
func SplitServerEdits(p control.Proposal) (serverEdits, configEdits []control.Edit) {
	serverEdits, _, _, configEdits = SplitSpecialEdits(p)
	return serverEdits, configEdits
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
	cfg, err := config.LoadBundle(s.Project, s.Preset, s.Bundle)
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
func (s *Service) SaveEffectiveAsPreset(editor *config.Editor, name string) (string, error) {
	name, err := ValidateSavePresetName(name)
	if err != nil {
		return "", err
	}
	cfg, err := config.LoadBundle(s.Project, s.Preset, s.Bundle)
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
	return before, nil
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
			raw, err := json.Marshal(e.Value)
			if err != nil {
				return nil, fmt.Errorf("/servers: %w", err)
			}
			if err := json.Unmarshal(raw, &specs); err != nil {
				return nil, fmt.Errorf("/servers: %w", err)
			}
		case "/add_server_url":
			if e.Op == "set" {
				u, _ := e.Value.(string)
				addURL = strings.TrimSpace(u)
			}
		}
	}
	if hasServers {
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
	before, err = s.RemoteSnapshot(grant)
	if err != nil {
		return
	}
	if err = p.Validate(before.Descriptor, grant); err != nil {
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
	values := map[string]any{}
	if res.File.Raw != "" {
		err = json.Unmarshal([]byte(res.File.Raw), &values)
		if err != nil {
			return
		}
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
	if err = s.CheckRoutes(cfg); err != nil {
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
