package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Claude Code's /clear starts a new session (a new id, a new transcript
// file); its SessionStart hook says so. The seat follows it as it follows
// pi: the move is recorded (KindInner, its log once the file is there), a
// send that arrives in the new session is confirmed, and its turn reported.
func TestK5ClaudeSeatFollowsTheSessionClearStarts(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	old, next := filepath.Join(project, "old-id.jsonl"), filepath.Join(project, "new-id.jsonl")
	writeLines(t, old, claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	state := t.TempDir()
	spool := filepath.Join(state, "after_turn")
	// The hook keeps the session next to the spool.
	status := filepath.Join(state, "claude-session.json")
	// What Claude's SessionStart hook runs.
	hook := func(id, path string) {
		in, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": id, "transcript_path": path, "source": "clear"})
		var out bytes.Buffer
		if RunStopHook(spool, bytes.NewReader(in), &out) != 0 || out.Len() != 0 {
			t.Fatalf("the SessionStart hook said %q", out.String())
		}
	}
	hook("old-id", old)
	s := newSeat(Spec{Spool: spool, StatusFile: status, Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true}, "k5-clear-"+filepath.Base(state), "old-id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	var seen []string
	deadline := time.After(5 * time.Second)
	until := func(match func(Event) bool) {
		t.Helper()
		for {
			select {
			case ev := <-s.events:
				if ev.Kind == KindInner {
					seen = append(seen, ev.InnerID+" "+filepath.Base(ev.Transcript))
				}
				if match(ev) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out; moves recorded %q", seen)
			}
		}
	}
	until(func(ev Event) bool { return ev.Kind == KindInner && ev.Transcript == old })
	hook("new-id", next) // /clear: Claude writes no file until the next prompt
	until(func(ev Event) bool { return ev.Kind == KindInner && ev.InnerID == "new-id" })
	s.track("r1", "run the tests", "queue")
	writeLines(t, next, claudeTurnLines("run the tests", "Ran them.", time.Now())...)
	var confirmed bool
	until(func(ev Event) bool {
		confirmed = confirmed || ev.Kind == KindDelivered && ev.OK && ev.SendID == "r1"
		return ev.Kind == KindTurn && ev.Text == "Ran them."
	})
	if !confirmed || !strings.Contains(strings.Join(seen, ","), "new-id new-id.jsonl") {
		t.Fatalf("confirmed %v, moves recorded %q; want r1 confirmed and the new session's log recorded", confirmed, seen)
	}
}

// The hook command eagent gives Claude runs at its SessionStart too, where
// it records the session and hands nothing over from the spool; at its
// Stop it hands the spool over as before.
func TestK5ClaudeHookOnSessionStartRecordsTheSession(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	if err := spoolMessage(spool, "r1", "then commit"); err != nil {
		t.Fatal(err)
	}
	in := `{"hook_event_name":"SessionStart","session_id":"s2","transcript_path":"/p/s2.jsonl","source":"clear"}`
	var out bytes.Buffer
	if code := RunStopHook(spool, strings.NewReader(in), &out); code != 0 || out.Len() != 0 || len(spoolNames(spool)) != 1 {
		t.Fatalf("SessionStart: code %d, said %q, spool %v", code, out.String(), spoolNames(spool))
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(spool), "claude-session.json"))
	var st struct {
		ID         string `json:"session_id"`
		Transcript string `json:"transcript_path"`
	}
	if json.Unmarshal(raw, &st) != nil || st.ID != "s2" || st.Transcript != "/p/s2.jsonl" {
		t.Fatalf("recorded %q", raw)
	}
	argv := strings.Join(claudeAgent{}.command(config.BuiltinRelayProfiles()["claude"], "u-1", false, "/bin/eagent relay-hook /spool"), " ")
	if !strings.Contains(argv, `"SessionStart"`) || !strings.Contains(argv, `"Stop"`) {
		t.Fatalf("claude argv %q", argv)
	}
}
