package relay

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// attachTarget is the session an attach command names, written either as
// "-t NAME" or "-tNAME".
func attachTarget(argv []string) string {
	for i, a := range argv {
		if a == "-t" && i+1 < len(argv) {
			return argv[i+1]
		}
		if t, ok := strings.CutPrefix(a, "-t"); ok && t != "" {
			return t
		}
	}
	return ""
}

func TestTmuxAttachNamesOnlyItsOwnSession(t *testing.T) {
	privateTmux(t, "attach")
	h := Tmux{}
	sibling := Target("1", "pi-2")
	if err := h.Start(sibling, t.TempDir(), []string{"sleep", "30"}, nil); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { return h.Alive(sibling) }) {
		t.Fatal("sibling not alive")
	}
	// has-session resolves a target-session as attach-session does,
	// without needing a terminal.
	gone := Target("1", "pi")
	if out, err := tmux(nil, "has-session", "-t", attachTarget(h.Attach(gone))); err == nil {
		t.Fatalf("attach to %s would open its sibling %s (%s)", gone, sibling, out)
	}
	if _, err := tmux(nil, "has-session", "-t", attachTarget(h.Attach(sibling))); err != nil {
		t.Fatalf("attach to %s does not find it: %v", sibling, err)
	}
}

// The command is shown to a person, who pastes it into a shell: each word
// must reach tmux as it is, in bash and in zsh (where a word starting with
// "=" is replaced by a command's path).
func TestTmuxAttachCommandSurvivesBeingPastedIntoAShell(t *testing.T) {
	argv := Tmux{}.Attach(Target("1", "pi"))
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		out, err := exec.Command(sh, "-c", `printf '%s\n' `+strings.Join(argv, " ")).CombinedOutput()
		if got := strings.Split(strings.TrimRight(string(out), "\n"), "\n"); err != nil || strings.Join(got, " ") != strings.Join(argv, " ") {
			t.Errorf("%s reads %q as %q (%v)", sh, strings.Join(argv, " "), out, err)
		}
	}
}

func TestScreenAttachNamesOnlyItsOwnSession(t *testing.T) {
	privateScreen(t)
	h := Screen{}
	sibling := Target("1", "pi-2")
	if err := h.Start(sibling, t.TempDir(), []string{"sleep", "30"}, nil); err != nil {
		t.Fatal(err)
	}
	pid := 0
	if !eventually(func() bool { pid = h.PID(sibling); return pid > 0 }) {
		t.Fatal("sibling not alive")
	}
	if got, want := strings.Join(h.Attach(sibling), " "), "screen -r "+strconv.Itoa(pid)+"."+sibling; got != want {
		t.Fatalf("attach to the live session is %q, want %q", got, want)
	}
	argv := h.Attach(Target("1", "pi"))
	if argv[0] == "screen" {
		t.Fatalf("attach to a session that is gone runs %q, which screen reads as the start of %s", argv, sibling)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not running") {
		t.Fatalf("attach to a gone session: %q, %v; want it to say so and fail", out, err)
	}
}
