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
	return control.Grant{Key: "project-" + artifact.Digest([]byte(project))[:24], Label: filepath.Base(project), Scope: "project", Operations: []string{"settings.apply", "settings.refresh", "prompt.set", "prompt.reset", "bundle.save", "bundle.delete", "settings.undo", "route.test"}, Classes: []string{"preference", "credential_reference", "permissions", "cost"}}
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
		field := control.Field{Key: prefix, Label: strings.ReplaceAll(strings.TrimPrefix(prefix, "/"), "_", " "), Shape: shape, Writable: slices.Contains(grant.Classes, class) && slices.Contains(grant.Operations, "settings.apply"), Unset: true, Class: class, EffectiveWhen: "new_or_resumed_session", Source: res.Sources[prefix]}
		if strings.HasPrefix(field.Source, "env:") || strings.HasPrefix(field.Source, "bundle:") {
			field.Writable = false
			field.LockedReason = "Overridden by " + field.Source
		}
		if !field.Writable && field.LockedReason == "" {
			field.LockedReason = "This connector has no grant for " + class
		}
		if strings.HasSuffix(prefix, "/protocol") {
			field.Shape.Enum = []any{"openai-chat", "openai-responses", "anthropic"}
		}
		if strings.HasSuffix(prefix, "/api_key_env") {
			for _, v := range s.KnownKeyEnvs() {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		if prefix == "/preset" {
			field.Shape.Enum = []any{""}
			for _, v := range config.PresetNames() {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		if prefix == "/default_config" {
			field.Shape.Enum = []any{""}
			names, _ := config.ListBundles(s.Project)
			for _, v := range names {
				field.Shape.Enum = append(field.Shape.Enum, v)
			}
		}
		if prefix == "/persona" {
			field.Description = "Narrator voice for new or resumed sessions."
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
	out.Descriptor.Actions = s.Actions(grant)
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
		if b, err := os.ReadFile(config.BundlePath(s.Project, name)); err == nil {
			contextFiles["bundle:"+name] = artifact.Digest(b)
		}
	}
	versionRaw, _ := json.Marshal(map[string]any{"file": res.File.ETag, "effective": effective, "preset": s.Preset, "bundle": s.Bundle, "schema": SchemaVersion, "context": contextFiles})
	out.Snapshot.Version = artifact.Digest(versionRaw)
	keys := map[string]bool{}
	for _, key := range s.KnownKeyEnvs() {
		keys[key] = os.Getenv(key) != ""
	}
	out.Snapshot.Details = map[string]any{"project": s.Project, "file_etag": res.File.ETag, "sources": res.Sources, "active": res.Active, "keys_present": keys, "context_digests": contextFiles, "prompts": promptViews, "bundles": names}
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
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return config.SaveResult{}, RemoteView{}, err
	}
	defer editor.Close()
	raw, etag, _, err := s.Prepare(p, grant)
	if err != nil {
		return config.SaveResult{}, RemoteView{}, err
	}
	result, err := editor.SaveRaw(raw, etag, true)
	if err != nil {
		return result, RemoteView{}, err
	}
	after, err := s.RemoteSnapshot(grant)
	return result, after, err
}
