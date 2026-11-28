package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// r16Look runs view_image and send_message's attachment check on a path
// outside the project, after the user wrote text in the session.
func r16Look(t *testing.T, text, path string) (viewErr bool, viewOut string, sendErr error) {
	t.Helper()
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.type_("@e " + text)
	s.waitFor("the message", func() bool { return len(s.events(event.UserMessage)) == 1 })
	s.settled()
	calls := &turnCalls{}
	args, _ := json.Marshal(map[string]string{"path": path})
	viewOut, viewErr, _ = s.rt.runTool(context.Background(), calls, tc("view_image", string(args)))
	_, sendErr = s.rt.messageAttachments([]any{path})
	return viewErr, viewOut, sendErr
}

// The user names a picture by a path with a space in it that goes through
// a symlink (on a Mac, anything under /tmp or the temporary folder). The
// path as written is leave to look at the picture and to send it: the
// text was compared only with the path the link resolves to.
func TestAPictureNamedThroughASymlinkWithASpaceMayBeLookedAt(t *testing.T) {
	real := filepath.Join(t.TempDir(), "Screen Shots")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "shots")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	if err := os.WriteFile(filepath.Join(real, "shot 1.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(link, "shot 1.png")
	viewErr, out, sendErr := r16Look(t, "what is in '"+written+"'?", written)
	if viewErr || sendErr != nil {
		t.Fatalf("view_image: error=%v %s; send_message: %v", viewErr, out, sendErr)
	}
}

// An agent or the user names a picture inside a Markdown link or image,
// or in the curved quotes a phone's keyboard types: it is named all the
// same.
func TestAPictureNamedInAMarkdownLinkMayBeLookedAt(t *testing.T) {
	for _, form := range []string{"![preview](%s)", "the shot is [here](%s).", "saved as out=%s", "the shot is \u201c%s\u201d, I think", "see \u2018%s\u2019"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "preview.png")
		if err := os.WriteFile(path, tinyPNG, 0o644); err != nil {
			t.Fatal(err)
		}
		viewErr, out, sendErr := r16Look(t, "look: "+fmt.Sprintf(form, path), path)
		if viewErr || sendErr != nil {
			t.Fatalf("%s: view_image: error=%v %s; send_message: %v", form, viewErr, out, sendErr)
		}
		// A longer path that only contains it names another file.
		other := filepath.Join(dir, "preview.png.bak")
		if err := os.WriteFile(other, tinyPNG, 0o644); err != nil {
			t.Fatal(err)
		}
		if viewErr, _, _ := r16Look(t, "look: "+fmt.Sprintf(form, other), path); !viewErr {
			t.Fatalf("%s: a longer path named the picture", form)
		}
	}
}
