package settings

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// reversal is the undo of a command journaled as the file before it
// (beforeRaw) and the file it wrote (desiredRaw), and the one place that
// decides what an undo restores. It is every key whose value differs
// between the two files, put back to its value before, or unset where
// that file had none; keys the command did not change are never in it.
// edits holds the fields, the command's own keys first in its order; nulls
// holds the routes the file before had as null that the command wrote one
// into (a fallback turned off, a summarizer route of none), which no field
// can write and which are put back to null.
func reversal(order []control.Edit, beforeRaw, desiredRaw string) (edits []control.Edit, nulls []string, err error) {
	before, err := fileValues(beforeRaw)
	if err != nil {
		return nil, nil, err
	}
	desired, err := fileValues(desiredRaw)
	if err != nil {
		return nil, nil, err
	}
	var keys []string
	var walk func(a, b any, hadA, hadB bool, ptr string)
	walk = func(a, b any, hadA, hadB bool, ptr string) {
		am, aObj := a.(map[string]any)
		bm, bObj := b.(map[string]any)
		if !aObj && !bObj {
			if hadA != hadB || !reflect.DeepEqual(a, b) {
				keys = append(keys, ptr)
			}
			return
		}
		if bObj && !aObj && hadA && a == nil {
			nulls = append(nulls, ptr)
		}
		names := []string{}
		for k := range am {
			names = append(names, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				names = append(names, k)
			}
		}
		slices.Sort(names)
		for _, k := range names {
			av, ha := am[k]
			bv, hb := bm[k]
			walk(av, bv, ha, hb, ptr+"/"+k)
		}
	}
	walk(before, desired, true, true, "")
	rank := func(key string) int {
		if i := slices.IndexFunc(order, func(e control.Edit) bool { return e.Key == key }); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(keys, func(a, b string) int { return rank(a) - rank(b) })
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
		edits = append(edits, reverse)
	}
	return edits, nulls, nil
}

// ConfigInverse is the reversal of a command, as the proposal an undo
// reviews. desiredRaw is the file the command wrote (a journal's desired).
func ConfigInverse(original control.Proposal, beforeRaw, desiredRaw string) (control.Proposal, error) {
	inverse := control.Proposal{Operation: "settings.apply"}
	if original.Operation != "settings.apply" || len(original.Edits) == 0 {
		return inverse, fmt.Errorf("this command has no reversible settings edits")
	}
	edits, _, err := reversal(original.Edits, beforeRaw, desiredRaw)
	if err != nil {
		return inverse, err
	}
	if len(edits) == 0 {
		return inverse, fmt.Errorf("this command changed no settings")
	}
	inverse.Edits = edits
	return inverse, nil
}

// PrepareUndo writes a reversal under the caller's project lock. It is a
// conflict when any key of the reversal now differs from the file the
// command wrote, and nothing else is; the file it writes is checked as a
// person's save of it would be. beforeRaw and desiredRaw are the journal's
// before_raw and desired.
func (s *Service) PrepareUndo(inverse control.Proposal, beforeRaw, desiredRaw string, grant control.Grant) (string, string, RemoteView, error) {
	desired, err := fileValues(desiredRaw)
	if err != nil {
		return "", "", RemoteView{}, err
	}
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	current, err := fileValues(res.File.Raw)
	if err != nil {
		return "", "", RemoteView{}, err
	}
	_, nulls, err := reversal(inverse.Edits, beforeRaw, desiredRaw)
	if err != nil {
		return "", "", RemoteView{}, err
	}
	keys := slices.Clone(nulls)
	for _, edit := range inverse.Edits {
		keys = append(keys, edit.Key)
	}
	for _, key := range keys {
		want, wantExists := pointerValue(desired, key)
		got, gotExists := pointerValue(current, key)
		if wantExists != gotExists || !reflect.DeepEqual(want, got) {
			return "", "", RemoteView{}, &config.ErrConflict{ETag: res.File.ETag}
		}
	}
	return s.prepare(inverse, grant, true, nulls)
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
