package relay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// TestLiveRelay drives the real Codex and Claude Code CLIs (cheap models,
// low effort) through a seat: launch, startup dialogs, transcript
// discovery, a typed message confirmed from the transcript, a finished
// turn, and a message held for the end of a turn. It spends a few cents of
// the account's usage, so it only runs with EAGENT_LIVE_RELAY=1.
func TestLiveRelay(t *testing.T) {
	if os.Getenv("EAGENT_LIVE_RELAY") != "1" {
		t.Skip("set EAGENT_LIVE_RELAY=1 to run against the real CLIs")
	}
	old := Socket
	Socket = fmt.Sprintf("eagent-live-%d", os.Getpid())
	defer func() {
		exec.Command("tmux", "-L", Socket, "kill-server").Run()
		Socket = old
	}()
	exe := filepath.Join(t.TempDir(), "eagent")
	if out, err := exec.Command("go", "build", "-o", exe, "../../cmd/eagent").CombinedOutput(); err != nil {
		t.Fatalf("build eagent: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name    string
		profile config.RelayProfile
	}{
		{"claude", config.RelayProfile{Agent: "claude", Model: "claude-haiku-5-5", Effort: "low", Args: []string{"--dangerously-skip-permissions"}, Host: "tmux"}},
		{"codex", config.RelayProfile{Agent: "codex", Model: "gpt-6-astra", Effort: "low", Args: []string{"--yolo"}, Host: "tmux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exec.Command("git", "-C", dir, "init", "-q").Run()
			spool := filepath.Join(t.TempDir(), "spool")
			spec := Spec{Session: "live", Name: tc.name, Profile: tc.profile, Dir: dir, Spool: spool,
				Hook: ShellQuote(exe) + " relay-hook " + ShellQuote(spool)}
			seat, started, err := Launch(spec, "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer seat.Kill()
			t.Logf("started %s in %s: %s", started.Target, started.Host, strings.Join(started.Command, " "))
			var log []string
			wait := func(what string, limit time.Duration, ok func(Event) bool) Event {
				deadline := time.After(limit)
				for {
					select {
					case ev, open := <-seat.Events():
						if !open {
							t.Fatalf("events closed while waiting for %s; saw %v", what, log)
						}
						log = append(log, ev.Kind+":"+ev.State+oneLine(ev.Text, 60))
						if ok(ev) {
							return ev
						}
					case <-deadline:
						screen, _ := seat.Screen(30)
						t.Fatalf("no %s within %s; saw %v; screen:\n%s", what, limit, log, screen)
					}
				}
			}
			msg := "Today's relay check — ✓\nReply with exactly the word ALPHA and nothing else."
			if _, err := seat.Send("r1", msg, "queue"); err != nil {
				screen, _ := seat.Screen(30)
				t.Fatalf("send: %v\n%s", err, screen)
			}
			wait("delivery of r1", 60*time.Second, func(ev Event) bool { return ev.Kind == KindDelivered && ev.SendID == "r1" && ev.OK })
			turn := wait("first turn", 120*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
			if !strings.Contains(turn.Text, "ALPHA") {
				t.Fatalf("turn text %q", turn.Text)
			}
			snap := seat.Snapshot()
			if snap.Transcript == "" || snap.InnerID == "" {
				t.Fatalf("transcript not found: %+v", snap)
			}
			// A long step, then a message held for the end of that turn.
			if _, err := seat.Send("r2", "Run the shell command `sleep 8`, then reply with exactly BRAVO.", "queue"); err != nil {
				t.Fatal(err)
			}
			wait("the second turn to start", 60*time.Second, func(ev Event) bool { return ev.Kind == KindState && ev.State == "working" })
			time.Sleep(2 * time.Second)
			how, err := seat.Send("r3", "Now reply with exactly the word CHARLIE.", "after_turn")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("after_turn: %s", how)
			var texts []string
			for len(texts) < 2 {
				ev := wait("BRAVO and CHARLIE", 180*time.Second, func(ev Event) bool { return ev.Kind == KindTurn || ev.Kind == KindDelivered && ev.SendID == "r3" })
				if ev.Kind == KindTurn {
					texts = append(texts, ev.Text)
				}
				if strings.Contains(strings.Join(texts, " "), "CHARLIE") {
					break
				}
			}
			joined := strings.Join(texts, " | ")
			if !strings.Contains(joined, "BRAVO") || !strings.Contains(joined, "CHARLIE") {
				t.Fatalf("turns %q", joined)
			}
			t.Logf("events: %v", log)
		})
	}
}
