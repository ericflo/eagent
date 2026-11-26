package harness

import (
	"strings"
	"testing"
	"time"
)

// "Until 10:00" is the next 10:00 on the user's clock. When today's 10:00
// has passed and a daylight-saving change falls before tomorrow's, adding
// 24 hours lands an hour off: 09:00 (autumn) or 11:00 (spring).
func TestAClockDeadlineAcrossADaylightSavingChangeIsThatClockTime(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		name string
		now  time.Time
	}{
		// Clocks go back at 02:00 on 1 November 2026.
		{"autumn", time.Date(2026, 10, 31, 23, 0, 0, 0, loc)},
		// Clocks go forward at 02:00 on 8 March 2026.
		{"spring", time.Date(2026, 3, 7, 23, 0, 0, 0, loc)},
	} {
		at, err := parseUntil("10:00", c.now)
		if err != nil {
			t.Fatal(err)
		}
		if h, m := at.In(loc).Hour(), at.In(loc).Minute(); h != 10 || m != 0 || at.In(loc).YearDay() != c.now.AddDate(0, 0, 1).YearDay() {
			t.Errorf("%s: \"10:00\" said at %s is %s, %02d:%02d on the user's clock", c.name, c.now.Format("Mon 15:04 MST"), at.In(loc).Format("Mon 15:04 MST"), h, m)
		}
	}
}

// On the change day itself a clock time can be missing (02:30 in spring)
// or happen twice (01:30 in autumn). Either way the deadline is later than
// now and within a day of it, never in the past or a day too far.
func TestAClockDeadlineOnTheChangeDayIsSoonAndLater(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		until string
		now   time.Time
	}{
		{"2:30", time.Date(2026, 3, 8, 0, 30, 0, 0, loc)},   // does not exist that night
		{"1:30", time.Date(2026, 11, 1, 0, 30, 0, 0, loc)},  // exists twice that night
		{"1:30", time.Date(2026, 10, 31, 23, 0, 0, 0, loc)}, // tomorrow's happens twice
		{"2:30", time.Date(2026, 3, 7, 23, 0, 0, 0, loc)},   // tomorrow's does not exist
	} {
		at, err := parseUntil(c.until, c.now)
		if err != nil {
			t.Fatal(err)
		}
		if !at.After(c.now) || at.Sub(c.now) > 25*time.Hour {
			t.Errorf("%q said at %s is %s", c.until, c.now.Format("Jan 2 15:04 MST"), at.In(loc).Format("Jan 2 15:04 MST"))
		}
	}
}

// Codex is given the task, hands over to Claude, which finishes a turn and
// hands over to pi. pi's brief lists "the user's instructions so far": the
// task the user gave Codex is one of them, whatever the agents are called.
func TestABriefAfterTwoSwitchesKeepsTheFirstAgentsTask(t *testing.T) {
	first, claude, pi := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three")
	useSeatList(t, first, claude, pi)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	r4CodexTurned(t, rt)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"}), "switch to Claude")
	relayTurned(t, rt, "claude", 1)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"}), "switch to pi")
	waitFor(t, "pi's brief typed", func() bool { return len(pi.sentTexts()) >= 1 })
	if brief := pi.sentTexts()[0]; !strings.Contains(brief, r4Task) {
		t.Errorf("pi's brief lost the task the user gave Codex:\n%s", brief)
	}
}

// Codex, Claude, Codex again, pi: what the user told Claude in between is
// part of the work too, quoted after the first Codex's task.
func TestASwitchBackKeepsWhatTheUserToldTheAgentInBetween(t *testing.T) {
	const between = "also keep the old CSV export"
	first, claude, back, pi := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three"), newFakeSeat("four")
	useSeatList(t, first, claude, back, pi)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	r4CodexTurned(t, rt)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"}), "switch to Claude")
	relayTurned(t, rt, "claude", 1)
	userSays(t, rt, between)
	relayTurned(t, rt, "claude", 2)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "codex", "relay": "claude"}), "switch back to Codex")
	relayTurned(t, rt, "codex", 1)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi", "relay": "codex"}), "switch to pi")
	waitFor(t, "pi's brief typed", func() bool { return len(pi.sentTexts()) >= 1 })
	brief := pi.sentTexts()[0]
	i, j := strings.Index(brief, r4Task), strings.Index(brief, between)
	if i < 0 || j < 0 || j < i {
		t.Errorf("pi's brief does not quote the task and then what the user told Claude:\n%s", brief)
	}
}
