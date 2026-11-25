package harness

import (
	"fmt"
	"strings"
	"testing"
)

const (
	r4Task       = "use only the archived temperature readings"
	r4Correction = "use Celsius units"
)

// r4Count counts the typed texts that contain s.
func r4Count(texts []string, s string) int {
	n := 0
	for _, t := range texts {
		n += strings.Count(t, s)
	}
	return n
}

// r4CodexTurned waits until Codex's reply to the task is on the record.
func r4CodexTurned(t *testing.T, rt *Runtime) {
	t.Helper()
	waitFor(t, "Codex's turn on the record", func() bool {
		n := 0
		rt.sync(func() { n = rt.st.Relays["codex"].Turns })
		return n == 1
	})
}

// r4Chain starts with Codex given the task, then runs steps (each a
// switch to a profile or a restart of the relay now running) whose agents
// never become ready, so none of their briefs is typed; the correction is
// sent while the first switch launches. It returns what the last agent was
// typed and what each switch tool said.
func r4Chain(t *testing.T, steps []string) (typed []string, said []string) {
	t.Helper()
	seats := []relaySeat{&lingerSeat{newFakeSeat("one")}}
	var stuck []*closingSeat
	for range steps {
		c := newClosingSeat("stuck")
		stuck = append(stuck, c)
		seats = append(seats, c)
	}
	final := newFakeSeat("done")
	seats[len(seats)-1] = final
	g := useGatedLaunches(t, seats, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	waitFor(t, "the task typed into Codex", func() bool {
		return len(seats[0].(*lingerSeat).sentTexts()) == 1
	})
	r4CodexTurned(t, rt)
	current := "codex"
	var outs []<-chan string
	for i, step := range steps {
		var args map[string]any
		if step == "restart" {
			args = map[string]any{"action": "restart", "relay": current}
		} else {
			args = map[string]any{"action": "switch", "profile": step, "relay": current}
			current = step
		}
		out := relayToolAsync(rt, args)
		outs = append(outs, out)
		g.waitEntered(t)
		if i == 0 {
			userSays(t, rt, r4Correction)
		}
		g.release <- struct{}{}
		if i < len(steps)-1 {
			// This agent is up and waits for its startup screens: what was
			// queued for it (the brief first) is never typed.
			stuck[i].waitEntered(t, "")
			if step == "restart" {
				recv(t, out, "restart")
			}
		}
	}
	for i, out := range outs {
		if steps[i] != "restart" || i == len(steps)-1 {
			said = append(said, recv(t, out, steps[i]))
		}
	}
	waitRelayStarts(t, rt)
	waitFor(t, "the last agent typed into", func() bool { return len(final.sentTexts()) >= 2 })
	return final.sentTexts(), said
}

func r4CheckFinal(t *testing.T, typed []string, intermediates ...string) {
	t.Helper()
	briefs := 0
	for _, s := range typed {
		if strings.HasPrefix(s, "You are taking over") {
			briefs++
		}
	}
	if briefs != 1 || !strings.HasPrefix(typed[0], "You are taking over") {
		t.Fatalf("want one handoff brief, typed first; got %q", firstWords(typed))
	}
	brief := typed[0]
	if !strings.Contains(brief, "from Codex") {
		t.Errorf("the brief does not hand over from Codex, the agent that did the work:\n%s", brief)
	}
	if !strings.Contains(brief, r4Task) {
		t.Errorf("the original task is not in the brief:\n%s", brief)
	}
	for _, who := range intermediates {
		if !strings.Contains(brief, who) {
			t.Errorf("the brief does not mention %s, which never got its brief:\n%s", who, brief)
		}
	}
	if !strings.Contains(brief, "Codex's last report:\none") {
		t.Errorf("the brief does not carry Codex's last report:\n%s", brief)
	}
	if n := r4Count(typed, r4Correction); n != 1 {
		t.Errorf("the correction was typed %d times, want once: %q", n, typed)
	}
	if n := r4Count(typed, r4Task); n != 1 {
		t.Errorf("the task was typed %d times, want once: %q", n, typed)
	}
	if len(typed) != 2 || typed[1] != r4Correction {
		t.Errorf("want the brief, then the correction; got %q", firstWords(typed))
	}
}

func r4CheckSaid(t *testing.T, said []string) {
	t.Helper()
	last := said[len(said)-1]
	if !strings.Contains(last, "Handed over") {
		t.Errorf("the last switch: %q", last)
	}
	for _, s := range said[:len(said)-1] {
		if !strings.Contains(s, "could not be typed") {
			t.Errorf("a switch whose brief was superseded said %q", s)
		}
	}
}

// The reviewer's scenario: Codex has the task, a switch to Claude, the
// correction while Claude starts, a switch to pi before Claude got its
// brief. pi gets one brief that hands over from Codex with the task, and
// the correction once, after it.
func TestTwoQuickSwitchesKeepTheTaskAndTypeTheCorrectionOnce(t *testing.T) {
	typed, said := r4Chain(t, []string{"claude", "pi"})
	r4CheckFinal(t, typed, "Claude")
	r4CheckSaid(t, said)
}

// Three switches, none of the middle agents ever briefed.
func TestThreeQuickSwitchesKeepTheTaskAndTypeTheCorrectionOnce(t *testing.T) {
	typed, said := r4Chain(t, []string{"claude", "pi", "antigravity"})
	r4CheckFinal(t, typed, "Claude", "Pi")
	r4CheckSaid(t, said)
}

// A switch, a restart of the new agent before its brief went in, then a
// switch on.
func TestSwitchRestartSwitchKeepsTheTaskAndTypesTheCorrectionOnce(t *testing.T) {
	typed, said := r4Chain(t, []string{"claude", "restart", "pi"})
	r4CheckFinal(t, typed, "Claude")
	r4CheckSaid(t, said)
}

// A plain switch with nothing waiting types the brief it always typed,
// word for word.
func TestPlainSwitchTypesTheSameBrief(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("Fixed the parser.")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	waitFor(t, "the task typed", func() bool { return len(old.sentTexts()) == 1 })
	r4CodexTurned(t, rt)
	var dir string
	rt.sync(func() { dir = rt.st.Relays["codex"].Dir })
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, out, "switch")
	waitFor(t, "the brief typed", func() bool { return len(next.sentTexts()) == 1 })
	want := fmt.Sprintf("You are taking over this work from Codex, which was working in %s.\n\nThe user's instructions so far, verbatim, oldest first:\n- %s\n\nCodex's last report:\nFixed the parser.\n\nCheck the working tree and recent commits, then continue where it left off.", dir, r4Task)
	if got := next.sentTexts()[0]; got != want {
		t.Fatalf("brief:\n%s\nwant:\n%s", got, want)
	}
}

// A switch while the old agent still had the task and the correction
// waiting to be typed (it was on its startup screens): the new agent gets
// each once, as its own sends after the brief, not also quoted in the
// brief as already said.
func TestSwitchTypesSendsThatWaitedOnTheOldAgentOnce(t *testing.T) {
	old, next := newClosingSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	old.waitEntered(t, r4Task)
	userSays(t, rt, r4Correction)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, out, "switch")
	waitFor(t, "three sends typed", func() bool { return len(next.sentTexts()) >= 3 })
	typed := next.sentTexts()
	if len(typed) != 3 || !strings.HasPrefix(typed[0], "You are taking over") || typed[1] != r4Task || typed[2] != r4Correction {
		t.Fatalf("want the brief, the task, the correction; got %q", firstWords(typed))
	}
	if r4Count(typed, r4Task) != 1 || r4Count(typed, r4Correction) != 1 {
		t.Fatalf("a send typed twice: %q", typed)
	}
}
