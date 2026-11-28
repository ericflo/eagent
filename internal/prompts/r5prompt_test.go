package prompts

import (
	"strings"
	"testing"
)

// relay_control keys returns the screen after the keys. The prompt says so
// and does not send the model to relay_screen for a second look, which costs
// a call each time a dialog is cleared.
func TestThePromptUsesTheScreenThatKeysReturn(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := set.Render("EAGENT.md", EagentData{Project: "/p"})
	if strings.Contains(p, "read the screen again") {
		t.Fatal("the prompt tells the model to read the screen again after relay_control keys")
	}
	if !strings.Contains(p, "check the screen it returns") {
		t.Fatal("the prompt does not say that relay_control keys returns the screen")
	}
}
