package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// ConfigInverse contains only the known fields the original command touched.
// Unknown local values and unrelated edits never enter the reversal or result.
func ConfigInverse(original control.Proposal, beforeRaw string) (control.Proposal, error) {
	inverse := control.Proposal{Operation: "settings.apply"}
	if original.Operation != "settings.apply" || len(original.Edits) == 0 {
		return inverse, fmt.Errorf("this command has no reversible settings edits")
	}
	before, err := fileValues(beforeRaw)
	if err != nil {
		return inverse, err
	}
	// A fallback the file turned off with null, over a preset that gives
	// one, cannot be turned off again by a remote edit (no field says
	// "none"): unsetting its keys would bring the preset's back while the
	// undo said it worked. It is refused instead, with where to do it.
	if v, had := pointerValue(before, "/model/fallback"); had && v == nil && slices.ContainsFunc(original.Edits, func(e control.Edit) bool { return strings.HasPrefix(e.Key, "/model/fallback/") }) {
		return inverse, fmt.Errorf("this undo would turn the preset's fallback model back on, which the project file had turned off (\"fallback\": null); put that back in %s by hand", config.File(""))
	}
	keys := []string{}
	for _, edit := range original.Edits {
		keys = append(keys, edit.Key)
	}
	// Applying a pick of a route's model also set the fields that go with it
	// (routePicks), and an edit of a dedicated summarizer with no route of
	// its own (none, or null) wrote it one: those are put back too.
	for _, route := range []string{"/model", "/model/fallback", "/summarizer/model", "/summarizer/model/fallback"} {
		picked := slices.ContainsFunc(original.Edits, func(e control.Edit) bool { return e.Key == route+"/model" || e.Key == route+"/base_url" })
		extra := []string{}
		if picked {
			extra = routePickFields
		}
		if v, had := pointerValue(before, route); (!had || v == nil) && route == "/summarizer/model" && slices.ContainsFunc(original.Edits, func(e control.Edit) bool { return e.Op == "set" && strings.HasPrefix(e.Key, route+"/") }) {
			extra = routeLeaves
		}
		for _, leaf := range extra {
			if !slices.Contains(keys, route+"/"+leaf) {
				keys = append(keys, route+"/"+leaf)
			}
		}
	}
	for _, key := range keys {
		value, exists := pointerValue(before, key)
		reverse := control.Edit{Key: key, Op: "unset"}
		switch {
		case exists && value != nil:
			reverse.Op, reverse.Value = "set", value
		// A field the file cleared with null is cleared again with the
		// value the remote editor clears it with, which loads as the null
		// did. A null replay_reasoning has no such value and is unset, and
		// so is a null context window: a window follows the model in use
		// (a 0 would pin it where the null let it follow a changed model).
		case exists && strings.HasSuffix(key, "/reasoning_effort"):
			reverse.Op, reverse.Value = "set", ""
		case exists && strings.HasSuffix(key, "/max_tokens"):
			reverse.Op, reverse.Value = "set", float64(0)
		}
		inverse.Edits = append(inverse.Edits, reverse)
	}
	return inverse, nil
}

// PrepareUndo compares the affected fields against the original saved result
// under the caller's project lock, then uses today's ordinary typed writer.
// Unrelated local edits are preserved; changed affected fields cause conflict.
func (s *Service) PrepareUndo(inverse control.Proposal, desiredRaw string, grant control.Grant) (string, string, RemoteView, error) {
	var desired map[string]any
	if err := json.Unmarshal([]byte(desiredRaw), &desired); err != nil {
		return "", "", RemoteView{}, err
	}
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	current, err := fileValues(res.File.Raw)
	if err != nil {
		return "", "", RemoteView{}, err
	}
	for _, edit := range inverse.Edits {
		want, wantExists := pointerValue(desired, edit.Key)
		got, gotExists := pointerValue(current, edit.Key)
		if wantExists != gotExists || !reflect.DeepEqual(want, got) {
			return "", "", RemoteView{}, &config.ErrConflict{ETag: res.File.ETag}
		}
	}
	return s.Prepare(inverse, grant)
}

func (change ResourceChange) Inverse() (control.Proposal, error) {
	p := control.Proposal{Parameters: map[string]any{"name": change.Name}}
	switch change.Kind {
	case "prompt":
		p.Operation = "prompt.reset"
		if change.BeforeExists && slices.Contains(prompts.Retired, change.Name) {
			// Undo would write the override back, and a retired prompt's
			// override can only be deleted.
			return p, fmt.Errorf("a deleted override of a retired prompt cannot be restored")
		}
		if change.BeforeExists {
			p.Operation = "prompt.set"
			p.Parameters["text"] = string(change.Before)
		}
	case "bundle":
		p.Operation = "bundle.delete"
		if change.BeforeExists {
			p.Operation = "bundle.save"
			p.Parameters["config_json"] = string(change.Before)
		}
	default:
		return p, fmt.Errorf("this resource does not support undo")
	}
	return p, nil
}
