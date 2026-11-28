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
		KillSocket(Socket)
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
		{"antigravity", config.BuiltinRelayProfiles()["antigravity"].With("gemini-3.8-flash-low", "low")},
		{"claude", config.RelayProfile{Agent: "claude", Model: "claude-haiku-5-5", Effort: "low", Args: []string{"--dangerously-skip-permissions"}, Host: "tmux"}},
		{"codex", config.RelayProfile{Agent: "codex", Model: "gpt-6-astra", Effort: "low", Args: []string{"--yolo"}, Host: "tmux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exec.Command("git", "-C", dir, "init", "-q").Run()
			spool := filepath.Join(t.TempDir(), "spool")
			spec := Spec{Session: "live", Name: tc.name, Profile: tc.profile, Dir: dir, Spool: spool,
				Hook: ShellQuote(exe) + " relay-hook " + ShellQuote(spool)}
			if tc.name == "antigravity" {
				spec.StatusFile = filepath.Join(t.TempDir(), "status.json")
				spec.Hook = ShellQuote(exe) + " relay-agy-status " + ShellQuote(spec.StatusFile)
			}
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
			// Generous: the model itself is sometimes slow to answer even this.
			turn := wait("first turn", 240*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
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
			if tc.name == "antigravity" {
				if err := seat.Kill(); err != nil {
					t.Fatal(err)
				}
				spec.Profile = spec.Profile.With("gemini-3.8-flash-medium", "medium")
				seat, _, err = Launch(spec, snap.InnerID, true)
				if err != nil {
					t.Fatal(err)
				}
				defer seat.Kill()
				if _, err := seat.Send("r4", "Which single word did I ask you to say first in this conversation? Reply with just that word.", "queue"); err != nil {
					t.Fatal(err)
				}
				reply := wait("resumed reply", 180*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
				if !strings.Contains(reply.Text, "ALPHA") || seat.Snapshot().InnerID != snap.InnerID {
					t.Fatalf("resume lost conversation: %q", reply.Text)
				}
				status := antigravityStatus(spec.StatusFile)
				if !strings.Contains(status.Model.Name, "Flash (Medium)") {
					t.Fatalf("resumed model = %q", status.Model.Name)
				}
			}
			t.Logf("events: %v", log)
		})
	}
}

// TestLivePi drives the real pi CLI through a seat on a private tmux
// socket: launch with eagent's extension, a typed message confirmed from
// the transcript, the exact end of a turn, an interrupt, a message held for
// the end of a turn, and a resume of the same session. It spends a little
// of the account's usage, so it only runs with EAGENT_LIVE_PI=1.
// EAGENT_LIVE_PI_DIR (else a temporary directory) is where it works and
// keeps pi's session files; EAGENT_LIVE_PI_MODEL picks the model.
func TestLivePi(t *testing.T) {
	if os.Getenv("EAGENT_LIVE_PI") != "1" {
		t.Skip("set EAGENT_LIVE_PI=1 to run against the real pi")
	}
	old := Socket
	Socket = "pi-adapter-test"
	defer func() {
		KillSocket(Socket)
		Socket = old
	}()
	root := os.Getenv("EAGENT_LIVE_PI_DIR")
	if root == "" {
		root = t.TempDir()
	}
	root = filepath.Join(root, fmt.Sprintf("run-%d", time.Now().Unix()))
	dir, sessions, state := filepath.Join(root, "work"), filepath.Join(root, "sessions"), filepath.Join(root, "state")
	for _, d := range []string{dir, sessions, state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// pi's session files go to the test's own directory.
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", sessions)
	model := os.Getenv("EAGENT_LIVE_PI_MODEL")
	if model == "" {
		model = "anthropic/claude-haiku-5-5"
	}
	spec := Spec{Session: "live", Name: "pi", Profile: config.BuiltinRelayProfiles()["pi"].With(model, "low"), Dir: dir}
	spec.Profile.Host = "tmux"
	if err := Prepare(&spec, state); err != nil {
		t.Fatal(err)
	}
	spec.Env = append(spec.Env, "PI_CODING_AGENT_SESSION_DIR="+sessions)
	seat, started, err := Launch(spec, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { seat.Kill() }()
	t.Logf("started %s: %s", started.Target, strings.Join(started.Command, " "))
	var log []string
	wait := func(what string, limit time.Duration, ok func(Event) bool) Event {
		deadline := time.After(limit)
		for {
			select {
			case ev, open := <-seat.Events():
				if !open {
					t.Fatalf("events closed while waiting for %s; saw %v", what, log)
				}
				log = append(log, ev.Kind+":"+ev.State+":"+oneLine(ev.Text, 60))
				if ok(ev) {
					return ev
				}
			case <-deadline:
				screen, _ := seat.Screen(30)
				t.Fatalf("no %s within %s; saw %v; screen:\n%s", what, limit, log, screen)
			}
		}
	}
	// 1. A message, its arrival and the end of its turn.
	if _, err := seat.Send("r1", "Reply with the single word OK and nothing else.", "queue"); err != nil {
		screen, _ := seat.Screen(30)
		t.Fatalf("send: %v\n%s", err, screen)
	}
	wait("delivery of r1", 60*time.Second, func(ev Event) bool { return ev.Kind == KindDelivered && ev.SendID == "r1" && ev.OK })
	turn := wait("first turn", 120*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
	t.Logf("turn 1: %q interrupted=%v %dms", turn.Text, turn.Interrupted, turn.DurationMS)
	if strings.TrimSpace(turn.Text) != "OK" || turn.Interrupted {
		t.Fatalf("turn 1 = %+v", turn)
	}
	snap := seat.Snapshot()
	if snap.InnerID != started.InnerID || !strings.HasSuffix(snap.Transcript, "_"+started.InnerID+".jsonl") {
		t.Fatalf("transcript %+v", snap)
	}
	// 2. An interrupt.
	if _, err := seat.Send("r2", "Run the shell command `sleep 30`, then reply with exactly DELTA.", "queue"); err != nil {
		t.Fatal(err)
	}
	wait("a sleep to start", 60*time.Second, func(ev Event) bool { return ev.Kind == KindActivity && strings.Contains(ev.Text, "sleep 30") })
	time.Sleep(2 * time.Second)
	if err := seat.Interrupt(); err != nil {
		t.Fatal(err)
	}
	turn = wait("the interrupted turn", 30*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
	t.Logf("turn 2: %q interrupted=%v", turn.Text, turn.Interrupted)
	if !turn.Interrupted || strings.Contains(turn.Text, "DELTA") {
		t.Fatalf("turn 2 = %+v", turn)
	}
	// 3. A message held for the end of a turn.
	if _, err := seat.Send("r3", "Run the shell command `sleep 6`, then reply with exactly BRAVO.", "queue"); err != nil {
		t.Fatal(err)
	}
	wait("the third turn to start", 60*time.Second, func(ev Event) bool { return ev.Kind == KindState && ev.State == "working" })
	time.Sleep(2 * time.Second)
	how, err := seat.Send("r4", "Now reply with exactly the word CHARLIE.", "after_turn")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after_turn: %s", how)
	var texts []string
	delivered := false
	for len(texts) < 2 || !delivered {
		ev := wait("BRAVO, CHARLIE and r4's delivery", 120*time.Second, func(ev Event) bool {
			return ev.Kind == KindTurn || ev.Kind == KindDelivered && ev.SendID == "r4"
		})
		if ev.Kind == KindTurn {
			texts = append(texts, ev.Text)
		} else {
			delivered = ev.OK
			t.Logf("r4 delivered after %d turn(s)", len(texts))
		}
	}
	t.Logf("turns 3-4: %q", texts)
	if strings.TrimSpace(texts[0]) != "BRAVO" || strings.TrimSpace(texts[1]) != "CHARLIE" {
		t.Fatalf("turns %q", texts)
	}
	// 4. A resume continues the same session.
	if err := seat.Kill(); err != nil {
		t.Fatal(err)
	}
	seat, started, err = Launch(spec, snap.InnerID, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resumed: %s", strings.Join(started.Command, " "))
	if _, err := seat.Send("r5", "Which single word did I ask you to reply with first in this conversation? Reply with just that word.", "queue"); err != nil {
		screen, _ := seat.Screen(30)
		t.Fatalf("send after resume: %v\n%s", err, screen)
	}
	turn = wait("the resumed reply", 120*time.Second, func(ev Event) bool { return ev.Kind == KindTurn })
	t.Logf("resumed turn: %q, inner %s, transcript %s", turn.Text, seat.Snapshot().InnerID, seat.Snapshot().Transcript)
	if !strings.Contains(turn.Text, "OK") || seat.Snapshot().InnerID != snap.InnerID || seat.Snapshot().Transcript != snap.Transcript {
		t.Fatalf("resume lost the conversation: %q %+v", turn.Text, seat.Snapshot())
	}
	t.Logf("events: %v", log)
}
