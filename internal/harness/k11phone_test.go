package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every stopping poll notes the thread for the reply watcher (e3639c3).
// When the note cannot be written, the session's log said a message left
// for the next run could not be noted, though nothing was sent. The poll
// cannot know as it stops that nothing was left, so the log says what may
// follow, never that a message was left.
func TestK11NoMessageLeftIsSaidWhenTheWatcherNoteCannotBeWritten(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	// The watcher's kept file cannot be written: a folder stands where it goes.
	if err := os.MkdirAll(filepath.Join(srPhoneKept(g.project), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.interrupt()
	said := false
	for _, l := range g.ui.logged() {
		if strings.Contains(l, "a message left for the next run") {
			t.Fatalf("nothing was sent from the phone, and the log says %q", l)
		}
		said = said || strings.Contains(l, "may wait unread")
	}
	if !said {
		t.Fatalf("the note that could not be written is not said: %q", g.ui.logged())
	}
}
