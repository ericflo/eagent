package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The review an undo offers is what it writes: a fallback the file had
// turned off with null, which a remote edit wrote into, is reviewed as a set
// of the fallback to null, and the undo writes that null.
func TestK2TheReviewShowsAFallbackPutBackToNull(t *testing.T) {
	s, g, _ := fixtureCommand(t)
	if _, err := config.SaveRaw(s.Project, `{"preset":"openai-high","model":{"fallback":null}}`, "", true); err != nil {
		t.Fatal(err)
	}
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}})
	first := executeExpected(t, s, g, q, "succeeded")
	offer, _ := first.Result["undo"].(map[string]any)
	raw, _ := json.Marshal(offer["edits"])
	if string(raw) != `[{"key":"/model/fallback","op":"set","value":null}]` {
		t.Fatalf("review %s, want the fallback set to null", raw)
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, first), "succeeded")
	var file map[string]map[string]json.RawMessage
	b, _ := os.ReadFile(config.File(s.Project))
	_ = json.Unmarshal(b, &file)
	if string(file["model"]["fallback"]) != "null" {
		t.Fatalf("undo wrote\n%s", b)
	}
}

// A command's journal records the (address, key variable) pairs in force
// when it is applied, so the undo of a remote /preset change over a route
// with a key variable of its own puts that variable back, which today's
// choices no longer offer. A journal without the record (written by an
// earlier eagent) is refused plainly, and nothing is written.
func TestK1UndoUsesThePairsInForceAtTheCommand(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		name := "recorded"
		if !recorded {
			name = "earlier journal"
		}
		t.Run(name, func(t *testing.T) {
			s, g, _ := fixtureCommand(t)
			before := `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
			if _, err := config.SaveRaw(s.Project, before, "", true); err != nil {
				t.Fatal(err)
			}
			q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}})
			first := executeExpected(t, s, g, q, "succeeded")
			path := filepath.Join(stateDir(s.Project), "commands", first.CommandID+".json")
			raw, _ := os.ReadFile(path)
			var j commandJournal
			if err := json.Unmarshal(raw, &j); err != nil || len(j.InForce) == 0 {
				t.Fatalf("journal records nothing in force: %v\n%s", err, raw)
			}
			if !recorded {
				j.InForce = nil
				raw, _ = json.Marshal(j)
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wrote, _ := os.ReadFile(config.File(s.Project))
			if !recorded {
				res := executeExpected(t, s, g, reviewedUndo(t, s, g, first), "rejected")
				if msg, _ := res.Result["message"].(string); !strings.Contains(msg, "nothing was changed") || !strings.Contains(msg, "by hand") {
					t.Fatalf("refusal %q", msg)
				}
				if now, _ := os.ReadFile(config.File(s.Project)); string(now) != string(wrote) {
					t.Fatalf("a refused undo wrote the file:\n%s", now)
				}
				return
			}
			executeExpected(t, s, g, reviewedUndo(t, s, g, first), "succeeded")
			got := config.Resolve(s.Project, "", "").Effective
			if got.Preset != "openai-high" || got.Model.APIKeyEnv != "OPENAI_API_KEY_WORK" {
				t.Fatalf("after undo %s %+v", got.Preset, got.Model)
			}
		})
	}
}

// An undo reviewed before eagent computed reversals from what a command
// changed (one offered for a model pick put back every pick field) no
// longer matches the reversal of that command. It is refused, nothing is
// written, and the reason says so and what to do instead.
func TestK1AnUndoReviewedByAnEarlierEagentIsRefusedPlainly(t *testing.T) {
	s, g, _ := fixtureCommand(t)
	if _, err := config.SaveRaw(s.Project, `{"preset":"openai-high"}`, "", true); err != nil {
		t.Fatal(err)
	}
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/model/model", Value: "gpt-6.1-sol"}}})
	first := executeExpected(t, s, g, q, "succeeded")
	if offer, _ := first.Result["undo"].(map[string]any); offer == nil {
		t.Fatalf("no undo offered: %#v", first.Result)
	}
	// The reversal an earlier eagent offered for this command.
	var old control.Proposal
	old.Operation = "settings.apply"
	for _, key := range []string{"/model/model", "/model/protocol", "/model/api_key_env", "/model/reasoning_effort", "/model/context_tokens"} {
		old.Edits = append(old.Edits, control.Edit{Op: "unset", Key: key})
	}
	if inverseDigest(old) == first.Result["undo"].(map[string]any)["restore_sha256"] {
		t.Fatal("setup: the earlier reversal is the one offered now")
	}
	wrote, _ := os.ReadFile(config.File(s.Project))
	undo := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.undo", Parameters: map[string]any{"command_id": first.CommandID, "restore_sha256": inverseDigest(old)}})
	j := executeExpected(t, s, g, undo, "rejected")
	msg, _ := j.Result["message"].(string)
	for _, want := range []string{"nothing was changed", "by hand"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q does not say %q", msg, want)
		}
	}
	if now, _ := os.ReadFile(config.File(s.Project)); string(now) != string(wrote) {
		t.Fatalf("a refused undo wrote the file:\n%s", now)
	}
}
