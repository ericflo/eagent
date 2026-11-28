package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A relay's own directory is made when something is first written there,
// and for a Claude just started nothing has been: no message was held for
// the end of a turn yet. Its SessionStart hook is the first to write, and
// the session it names must be kept, or a /clear or /resume afterwards
// leaves the seat reading the transcript Claude has left.
func TestClaudesSessionIsKeptBeforeAnythingElseWasWrittenForTheRelay(t *testing.T) {
	spec := Spec{Profile: config.RelayProfile{Agent: "claude"}}
	dir := filepath.Join(t.TempDir(), "relays", "claude")
	if err := Prepare(&spec, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Skip("the relay's directory is made up front now; the hook has nothing to make")
	}
	in, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "new-id", "transcript_path": "/x/new-id.jsonl", "source": "clear"})
	var out bytes.Buffer
	if RunStopHook(spec.Spool, bytes.NewReader(in), &out) != 0 || out.Len() != 0 {
		t.Fatalf("the SessionStart hook said %q", out.String())
	}
	// Where prepare points the seat (a test binary has no hook command,
	// so the spec names no status file here).
	raw, err := os.ReadFile(claudeSessionFile(spec.Spool))
	var got claudeSession
	if err != nil || json.Unmarshal(raw, &got) != nil || got.ID != "new-id" {
		t.Fatalf("the session Claude is on was not kept: %v, %q", err, raw)
	}
}

// A read that began inside a line drops what is left of that line. The
// transcript is then replaced by a shorter one before any line ended: the
// new file is read from its top, where no line is cut, and its first line
// was dropped all the same (below the bar; a note of the review's).
func TestATranscriptReplacedBeforeItsFirstLineEndedIsReadFromItsFirstLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(`{"n":"a line that is still being written`), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := &tailer{path: path, offset: 10, skipPartial: true}
	if lines, _, err := tail.next(); err != nil || len(lines) != 0 {
		t.Fatalf("setup: %q, %v", lines, err)
	}
	if err := os.WriteFile(path, []byte("{\"n\":1}\n{\"n\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, starts, err := tail.next()
	if err != nil || len(lines) != 2 || string(lines[0]) != `{"n":1}` || starts[0] != 0 {
		t.Fatalf("the new transcript's lines: %q at %v, %v", lines, starts, err)
	}
}
