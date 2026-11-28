package prompts

import (
	"strings"
	"testing"
)

// A session with no terminal and no phone lasts as long as its agent, and
// nobody can tell it the work is over. Only that session is told to stop
// the agent once the asked work is done; anywhere a user can still write,
// the agent stays until they are finished with it.
func TestOnlyASessionNobodyCanWriteToIsToldToStopItsAgentWhenDone(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const told = "once what the user asked for is done, tell them how it ended and stop the agent"
	batch := set.Render("EAGENT.md", EagentData{Project: "/p"})
	if !strings.Contains(batch, told) {
		t.Fatalf("a session with no terminal and no phone is not told to stop its agent when done:\n%s", batch)
	}
	for name, d := range map[string]EagentData{
		"at a terminal": {Project: "/p", Interactive: true},
		"on the phone":  {Project: "/p", Phone: "The user reads this on their phone."},
	} {
		if strings.Contains(set.Render("EAGENT.md", d), told) {
			t.Fatalf("a session %s is told to stop its agent when the work is done", name)
		}
	}
}
