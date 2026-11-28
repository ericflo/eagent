package relay

// The sixteenth review's fifth pass over the seat (sr5seat).

import (
	"os"
	"path/filepath"
	"testing"
)

// sr5ClaudeSeat is a Claude seat on session a-id (transcript a, holding
// lines), with the SessionStart hook's file naming a-id, watched at piLook.
func sr5ClaudeSeat(t *testing.T, h Host, name string, lines ...string) (s *Seat, spool, a, b string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b = filepath.Join(project, "a-id.jsonl"), filepath.Join(project, "b-id.jsonl")
	writeLines(t, a, lines...)
	state := t.TempDir()
	spool = filepath.Join(state, "after_turn")
	sr4ClaudeHook(t, spool, "a-id", a, "startup")
	s = newSeat(Spec{Spool: spool, StatusFile: filepath.Join(state, "claude-session.json"), Hook: "eagent relay-hook"}, claudeAgent{}, h, name+"-"+filepath.Base(state), "a-id")
	s.state, s.poll = "idle", piLook
	return s, spool, a, b
}
