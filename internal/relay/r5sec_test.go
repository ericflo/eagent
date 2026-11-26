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

// privateTmux points the tmux host at a socket of the test's own.
func privateTmux(t *testing.T, tag string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	old := Socket
	Socket = fmt.Sprintf("eagent-r5sec-%s-%d", tag, os.Getpid())
	t.Cleanup(func() {
		KillSocket(Socket)
		Socket = old
	})
}

// eventually polls cond every 10ms for up to two seconds.
func eventually(cond func() bool) bool { return within(2*time.Second, cond) }

func within(d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// tricky are arguments tmux would read as its own syntax if they reached its
// command line unescaped: a lone ";" or any argument ending in ";" ends a
// tmux command, and what follows runs as another tmux command.
func tricky(marker string) []string {
	return []string{";", "run-shell", "touch " + marker, ";", "x;", `a\;`, `b\\;`, `\`, "{", "}", "-t", "#{pane_id}", "~", "--", "-"}
}

func TestTmuxStartRunsNoCommandHiddenInTheAgentsArguments(t *testing.T) {
	privateTmux(t, "attack")
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	// A profile's args, as a bundle or a hand-edited config could carry them.
	argv := []string{"sleep", "30", ";", "run-shell", "touch " + marker}
	name := Target("1", "attack")
	_ = (Tmux{}).Start(name, dir, argv, nil)
	// tmux waits for run-shell before its client returns; the window only
	// covers a slow machine.
	if within(100*time.Millisecond, func() bool { _, err := os.Stat(marker); return err == nil }) {
		t.Fatal("an argument ran as a tmux command: the marker file exists")
	}
}

func TestTmuxStartPassesEveryArgumentThroughUnchanged(t *testing.T) {
	privateTmux(t, "start")
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	out := filepath.Join(dir, "argv.txt")
	args := tricky(marker)
	argv := append([]string{"sh", "-c", `printf '%s\n' "$EAGENT_R5" "$@" > ` + out + `; sleep 30`, "sh"}, args...)
	name := Target("1", "inject")
	if err := (Tmux{}).Start(name, dir, argv, []string{"EAGENT_R5=v;"}); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { b, _ := os.ReadFile(out); return strings.HasSuffix(string(b), "\n") && len(b) > 0 }) {
		t.Fatal("the program never wrote its arguments")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran as a tmux command: the marker file exists")
	}
	got, _ := os.ReadFile(out)
	want := strings.Join(append([]string{"v;"}, args...), "\n") + "\n"
	if string(got) != want {
		t.Fatalf("the program got\n%q\nwant\n%q", got, want)
	}
	// The pane still stays when the program exits.
	if v, err := tmux(nil, "show-options", "-t", "="+name+":", "-v", "remain-on-exit"); err != nil || strings.TrimSpace(v) != "on" {
		t.Fatalf("remain-on-exit = %q, %v", v, err)
	}
}

func TestTmuxKeysTypesKeysThatLookLikeTmuxSyntaxLiterally(t *testing.T) {
	privateTmux(t, "keys")
	dir := t.TempDir()
	out := filepath.Join(dir, "typed.txt")
	name := Target("1", "keys")
	if err := (Tmux{}).Start(name, dir, []string{"sh", "-c", "stty -echo; cat > " + out}, nil); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { return (Tmux{}).Alive(name) }) {
		t.Fatal("not alive")
	}
	if err := (Tmux{}).Keys(name, "a;", ";", "-l", "-R", "Enter"); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { b, _ := os.ReadFile(out); return strings.HasSuffix(string(b), "\n") }) {
		b, _ := os.ReadFile(out)
		t.Fatalf("typed %q, want a line", b)
	}
	if b, _ := os.ReadFile(out); string(b) != "a;;-l-R\n" {
		t.Fatalf("typed %q, want %q", b, "a;;-l-R\n")
	}
}

func TestCheckPickRefusesModelsAndEffortsThatAreNotNames(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi", "antigravity", "custom-agent"} {
		p := config.RelayProfile{Agent: agent}
		for _, bad := range []string{";", "gpt;", "-x", "--dangerously-skip-permissions", "a\x1b[201~b", "a\x00b", "a\x7fb"} {
			if err := CheckPick(p, bad, ""); err == nil {
				t.Errorf("%s: model %q accepted", agent, bad)
			}
		}
		if AgentEfforts(agent) == nil {
			for _, bad := range []string{";", "-x", "a\x1bb", "high;"} {
				if err := CheckPick(p, "", bad); err == nil {
					t.Errorf("%s: effort %q accepted", agent, bad)
				}
			}
		}
	}
	// Real names still pass.
	for _, ok := range [][2]string{{"claude", "claude-opus-4-7[1m]"}, {"codex", "gpt-5.5"}, {"pi", "openai/gpt-5:high"}, {"custom-agent", "org/model-1.5"}} {
		if err := CheckPick(config.RelayProfile{Agent: ok[0]}, ok[1], ""); err != nil {
			t.Errorf("%s: %q refused: %v", ok[0], ok[1], err)
		}
	}
}
