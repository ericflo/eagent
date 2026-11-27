package relay

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// codex queue's parser (clap) reads "--message -x" as an unknown flag and
// refuses it; "--message=-x" passes the text whole, leading dash and all.
func TestCodexQueuePassesAMessageThatStartsWithADash(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"-x is wrong", "--help me", "plain"} {
		argv := codexQueueArgs("0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11", text)
		if !slices.Contains(argv, "--message="+text) || !slices.Contains(argv, "--thread=0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11") {
			t.Fatalf("argv %q", argv)
		}
		for _, a := range argv {
			if a == text {
				t.Fatalf("the message is a separate argument in %q", argv)
			}
		}
	}
}

// A relay's state directory given as a relative path that begins with "-"
// never reaches an agent's command line as something that looks like a
// flag: the hook, status file and spool paths are absolute.
func TestRelayPathsOnCommandLinesAreAbsolute(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{"claude", "pi", "antigravity"} {
		spec := Spec{Profile: config.RelayProfile{Agent: agent}}
		if err := Prepare(&spec, "-odd/state"); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{spec.Spool, spec.StatusFile} {
			if p != "" && !filepath.IsAbs(p) {
				t.Fatalf("%s: path %q is relative", agent, p)
			}
		}
		argv := AgentByName(agent).command(spec.Profile, "0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11", false, spec.Hook)
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-odd") {
				t.Fatalf("%s: %q passes %q", agent, argv, a)
			}
		}
	}
}

// An Antigravity status line with a state name the seat does not know: a
// relay blocked because it went quiet stays blocked (the name may mean it
// works), while one blocked because it was slow to start goes idle once
// its prompt shows, whatever name agy prints there.
func TestAntigravityUnknownStateNameFallsTheSafeWay(t *testing.T) {
	t.Parallel()
	screen := "> \nAntigravity relay · pondering · Gemini 3.8"
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	start := time.Now()
	s.state, s.stateAt, s.lastLine = "working", start.Add(-11*time.Minute), start.Add(-11*time.Minute)
	s.checkScreen(start)
	s.checkScreen(start.Add(1500 * time.Millisecond))
	if st := s.Snapshot().State; st != "blocked" {
		t.Fatalf("a quiet agent with an unknown state name is %s", st)
	}
	starting := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, &fakeHost{alive: true, screen: "loading"}, "y", "")
	starting.stateAt = start.Add(-30 * time.Second)
	starting.checkScreen(start)
	if st := starting.Snapshot().State; st != "blocked" {
		t.Fatalf("slow start: %s", st)
	}
	starting.host.(*fakeHost).screen = screen
	starting.checkScreen(start.Add(1500 * time.Millisecond))
	starting.checkScreen(start.Add(3000 * time.Millisecond)) // two checks in a row agree
	if st := starting.Snapshot().State; st != "idle" {
		t.Fatalf("at its prompt after a slow start, with an unknown state name, the seat is %s", st)
	}
	// The name is read as agy writes it, in any case.
	if (antigravityAgent{}).busy("Antigravity relay · Idle · m") || !(antigravityAgent{}).busy("Antigravity relay · running · m") {
		t.Fatal("state names misread")
	}
}
