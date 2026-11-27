package settings

import (
	"encoding/json"
	"errors"
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
// The command's own keys come first, in its order. A route the file before
// had as null that the command wrote one into (a fallback turned off, a
// summarizer route of none) is one edit, a set of the route with no value,
// which writes the null back; the fields under it are not listed.
func reversal(order []control.Edit, beforeRaw, desiredRaw string) ([]control.Edit, error) {
	before, err := fileValues(beforeRaw)
	if err != nil {
		return nil, err
	}
	desired, err := fileValues(desiredRaw)
	if err != nil {
		return nil, err
	}
	var keys []string
	nulls := map[string]bool{}
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
		if bObj && !aObj && hadA && a == nil && isRoute(ptr) {
			keys, nulls[ptr] = append(keys, ptr), true
			return
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
	var edits []control.Edit
	for _, key := range keys {
		value, exists := pointerValue(before, key)
		reverse := control.Edit{Key: key, Op: "unset"}
		switch {
		case nulls[key]:
			reverse.Op = "set"
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
	return edits, nil
}

// ConfigInverse is the reversal of a command, as the proposal an undo
// reviews. desiredRaw is the file the command wrote (a journal's desired).
func ConfigInverse(original control.Proposal, beforeRaw, desiredRaw string) (control.Proposal, error) {
	inverse := control.Proposal{Operation: "settings.apply"}
	if original.Operation != "settings.apply" || len(original.Edits) == 0 {
		return inverse, fmt.Errorf("this command has no reversible settings edits")
	}
	edits, err := reversal(original.Edits, beforeRaw, desiredRaw)
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
// command wrote, or when the reversal changes the model or address a route
// resolves to and that route resolves otherwise now than the command left
// it (or would end otherwise than before the command), and nothing else
// is; the file it writes is checked as a
// person's save of it would be, except that a route pairing an address and
// a key variable as a route in force at the command did (inForce, the
// journal's in_force) passes the key-choice and pairing checks. beforeRaw
// and desiredRaw are the journal's before_raw and desired.
func (s *Service) PrepareUndo(inverse control.Proposal, beforeRaw, desiredRaw string, grant control.Grant, inForce ...[2]string) (string, string, RemoteView, error) {
	desired, err := fileValues(desiredRaw)
	if err != nil {
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
	// A route's model goes to its address. Each route is resolved, over
	// today's layers, as the command left the file (wrote), as it is now, as
	// the undo would leave it (undone) and as it was before the command
	// (was). An undo that changes how a route resolves conflicts when the
	// route resolves otherwise now than the command left it, or when the
	// undo would leave it otherwise than it was.
	load := func(values map[string]any) (config.Config, error) {
		file := map[string]json.RawMessage{}
		b, _ := json.Marshal(values)
		_ = json.Unmarshal(b, &file)
		return config.LoadBundleWithFile(s.Project, s.Preset, s.Bundle, file)
	}
	undoneValues, err := fileValues(res.File.Raw)
	if err != nil {
		return "", "", RemoteView{}, err
	}
	for _, e := range inverse.Edits {
		setPointer(undoneValues, e.Key, e.Value, e.Op == "unset")
	}
	resolved := func(cfg config.Config, ptr string) [3]string {
		if r := routeAt(cfg, ptr); r != nil {
			return [3]string{"route", r.Model, strings.TrimRight(r.BaseURL, "/")}
		}
		return [3]string{}
	}
	// A file the undo would write that does not load is refused below.
	if undone, err := load(undoneValues); err == nil {
		var wrote, was *config.Config
		for _, base := range []string{"/model", "/summarizer/model"} {
			for ptr := base; len(ptr) < len(base)+4*len("/fallback"); ptr += "/fallback" {
				now, after := resolved(res.Effective, ptr), resolved(undone, ptr)
				if now == after {
					continue
				}
				if wrote == nil {
					cfg, err := load(desired)
					if err != nil {
						return "", "", RemoteView{}, fmt.Errorf("this undo cannot tell where it would send the model, because the settings the command wrote no longer load here (%v); nothing was changed, so put those settings back by hand instead", err)
					}
					wrote = &cfg
					// The file before the command is compared when it still
					// loads here.
					if values, err := fileValues(beforeRaw); err == nil {
						if cfg, err := load(values); err == nil {
							was = &cfg
						}
					}
				}
				if now != resolved(*wrote, ptr) || was != nil && after != resolved(*was, ptr) {
					return "", "", RemoteView{}, &config.ErrConflict{ETag: res.File.ETag}
				}
			}
		}
	}
	raw, etag, view, err := s.prepare(inverse, grant, true, inForce)
	var conflict *config.ErrConflict
	if err != nil && !errors.As(err, &conflict) {
		err = fmt.Errorf("this undo would write settings that cannot be saved from here now (%v); nothing was changed, so put those settings back by hand instead", err)
	}
	return raw, etag, view, err
}

// isRoute says whether a pointer names a route: /model or
// /summarizer/model, or a fallback of one.
func isRoute(ptr string) bool {
	rest, ok := strings.CutPrefix(ptr, "/summarizer/model")
	if !ok {
		rest, ok = strings.CutPrefix(ptr, "/model")
	}
	for ok && rest != "" {
		rest, ok = strings.CutPrefix(rest, "/fallback")
	}
	return ok
}

// routeAt is the route of cfg a pointer names (/model or /summarizer/model,
// and their fallbacks), or nil. A summarizer with no route of its own is
// nil here, though it runs on the main route: that route is compared as
// /model, and reading it under the summarizer too would make a person's
// change of it a conflict for an undo that only gives the summarizer its
// own route back or takes it out.
func routeAt(cfg config.Config, ptr string) *config.ModelRoute {
	r := &cfg.Model
	rest, ok := strings.CutPrefix(ptr, "/summarizer/model")
	if ok {
		r = cfg.Summarizer.Model
	} else if rest, ok = strings.CutPrefix(ptr, "/model"); !ok {
		return nil
	}
	for rest != "" && r != nil {
		if rest, ok = strings.CutPrefix(rest, "/fallback"); !ok {
			return nil
		}
		r = r.Fallback
	}
	return r
}

// RoutePairs are the (address, key variable) pairs of a configuration's
// routes and their fallbacks, in order: what a command's journal records as
// in force when the command is applied.
func RoutePairs(cfg config.Config) [][2]string {
	var out [][2]string
	for _, a := range configRoutes(cfg) {
		for cur := &a; cur != nil; cur = cur.Fallback {
			if pair := [2]string{strings.TrimRight(cur.BaseURL, "/"), cur.APIKeyEnv}; !slices.Contains(out, pair) {
				out = append(out, pair)
			}
		}
	}
	slices.SortFunc(out, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	return out
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
