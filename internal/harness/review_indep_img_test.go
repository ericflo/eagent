package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A picture the user names by a path with a space in it (a GNOME
// screenshot: "Screenshot From 2026-11-25 10-00-00.png"), written in quotes
// as a terminal or file manager pastes it, is leave to look at it.
func TestIndepViewImageUserNamedPathWithSpaces(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	dir := filepath.Join(t.TempDir(), "Screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "Screenshot From 2026-11-25 10-00-00.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e what is in '" + outside + "'?")
	s.waitFor("the message", func() bool { return len(s.events(event.UserMessage)) == 1 })
	s.settled()
	calls := &turnCalls{}
	args, _ := json.Marshal(map[string]string{"path": outside})
	out, isErr, _ := s.rt.runTool(context.Background(), calls, tc("view_image", string(args)))
	if isErr || len(calls.images) != 1 {
		t.Fatalf("a picture the user named (path with spaces): error=%v images=%d: %s", isErr, len(calls.images), out)
	}
}
