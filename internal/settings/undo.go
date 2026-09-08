package settings

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// ConfigInverse contains only the known fields the original command touched.
// Unknown local values and unrelated edits never enter the reversal or result.
func ConfigInverse(original control.Proposal, beforeRaw string) (control.Proposal, error) {
	inverse := control.Proposal{Operation: "settings.apply"}
	if original.Operation != "settings.apply" || len(original.Edits) == 0 {
		return inverse, fmt.Errorf("this command has no reversible settings edits")
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(beforeRaw), &before); err != nil {
		return inverse, err
	}
	for _, edit := range original.Edits {
		value, exists := pointerValue(before, edit.Key)
		reverse := control.Edit{Key: edit.Key, Op: "unset"}
		if exists && value != nil {
			reverse.Op, reverse.Value = "set", value
		}
		inverse.Edits = append(inverse.Edits, reverse)
	}
	return inverse, nil
}

// PrepareUndo compares the affected fields against the original saved result
// under the caller's project lock, then uses today's ordinary typed writer.
// Unrelated local edits are preserved; changed affected fields cause conflict.
func (s *Service) PrepareUndo(inverse control.Proposal, desiredRaw string, grant control.Grant) (string, string, RemoteView, error) {
	var desired, current map[string]any
	if err := json.Unmarshal([]byte(desiredRaw), &desired); err != nil {
		return "", "", RemoteView{}, err
	}
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	if res.File.Raw != "" {
		if err := json.Unmarshal([]byte(res.File.Raw), &current); err != nil {
			return "", "", RemoteView{}, err
		}
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
