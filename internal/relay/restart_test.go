package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

func writeRollout(t *testing.T, home string, day time.Time, id, cwd string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "rollout-"+day.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	meta, _ := json.Marshal(map[string]any{"timestamp": day.Format(time.RFC3339Nano), "type": "session_meta",
		"payload": map[string]any{"id": id, "timestamp": day.Format(time.RFC3339Nano), "cwd": cwd}})
	if err := os.WriteFile(p, append(meta, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func transcriptFound(s *Seat, d time.Duration) (string, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return "", false
			}
			if ev.Kind == KindInner {
				return ev.Transcript, true
			}
		case <-deadline:
			return "", false
		}
	}
}

// Restarting a Codex relay (restart, upgrade, a session resumed) follows the
// same rollout again; the old seat lets go of it however it stopped.
func TestRestartedCodexRelayFindsItsTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	work := t.TempDir()
	path := writeRollout(t, home, time.Now(), "inner-1", work)
	spec := Spec{Session: "S", Name: "codex", Dir: work, Profile: config.RelayProfile{Agent: "codex"}}
	h := &fakeHost{alive: true, screen: "Ask Codex to do anything"}
	target := Target("S", "codex")
	s1 := newSeat(spec, codexAgent{}, h, target, "inner-1")
	s1.launched = time.Now().Add(-time.Second)
	go s1.watch()
	if got, ok := transcriptFound(s1, 5*time.Second); !ok || got != path {
		t.Fatalf("first seat: %q", got)
	}
	_ = s1.Kill()
	if _, held := claimed.Load(path); held {
		t.Fatal("the stopped seat still holds the transcript")
	}
	launched := time.Now()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644) // codex resume appends
	_, _ = f.WriteString(`{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}` + "\n")
	f.Close()
	s2 := newSeat(spec, codexAgent{}, h, target, "inner-1")
	s2.launched, s2.since = launched, launched
	go s2.watch()
	defer s2.Close()
	if got, ok := transcriptFound(s2, 5*time.Second); !ok || got != path {
		t.Fatalf("restarted seat: %q", got)
	}
}

// Resuming a Codex session that began days ago finds its rollout by id.
func TestCodexResumeFindsAnOldRollout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	work := t.TempDir()
	path := writeRollout(t, home, time.Now().Add(-72*time.Hour), "inner-old", work)
	now := time.Now()
	_ = os.Chtimes(path, now, now) // appended to by the resume
	if got, _ := (codexAgent{}).locate(work, now.Add(-time.Second), "inner-old"); got != path {
		t.Fatalf("locate = %q, want %q", got, path)
	}
}
