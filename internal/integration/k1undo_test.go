package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

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
