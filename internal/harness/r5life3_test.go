package harness

import (
	"testing"
	"time"
)

// "Until 1:30" is the next 1:30 on the user's clock. In the hour the clocks
// go back, said at 01:15 the second time (PST) the next 1:30 is 15 minutes
// away (01:30 PST); said at 01:45 PDT it is 45 minutes away. Today's 1:30
// built by time.Date is the first pass (PDT), already gone: going to
// tomorrow's would be a day too far.
func TestAClockDeadlineInTheRepeatedHourIsTheNextOnTheClock(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		now  time.Time
		want time.Duration
	}{
		{time.Date(2026, 11, 1, 0, 15, 0, 0, loc).Add(2 * time.Hour), 15 * time.Minute}, // 01:15 PST
		{time.Date(2026, 11, 1, 1, 45, 0, 0, loc), 45 * time.Minute},                    // 01:45 PDT
		{time.Date(2026, 11, 1, 1, 15, 0, 0, loc), 15 * time.Minute},                    // 01:15 PDT: the first pass is next
	} {
		at, err := parseUntil("1:30", c.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := at.Sub(c.now); got != c.want || at.In(loc).Format("15:04") != "01:30" {
			t.Errorf("\"1:30\" said at %s is %s, %v later; want 01:30, %v later", c.now.Format("Jan 2 15:04 MST"), at.In(loc).Format("Jan 2 15:04 MST"), got, c.want)
		}
	}
}

// On the night the clocks jump from 02:00 to 03:00, 02:30 does not exist.
// "Until 2:30" then means as long after midnight as 2:30 is on any other
// night: 03:30 on the clock, two hours after 00:30. Said after the jump,
// it is tomorrow's 02:30.
func TestAClockDeadlineTheClocksSkipIsAsLongAfterMidnightAsUsual(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	now := time.Date(2026, 3, 8, 0, 30, 0, 0, loc) // 00:30 PST
	at, err := parseUntil("2:30", now)
	if err != nil {
		t.Fatal(err)
	}
	if at.Sub(now) != 2*time.Hour || at.In(loc).Format("15:04 MST") != "03:30 PDT" {
		t.Errorf("\"2:30\" said at 00:30 on the spring night is %s, %v later; want 03:30 PDT, 2h later", at.In(loc).Format("Jan 2 15:04 MST"), at.Sub(now))
	}
	now = time.Date(2026, 3, 8, 4, 0, 0, 0, loc) // 04:00 PDT
	if at, _ = parseUntil("2:30", now); at.In(loc).Format("Jan 2 15:04") != "Mar 9 02:30" {
		t.Errorf("\"2:30\" said at 04:00 after the jump is %s, want tomorrow's 02:30", at.In(loc).Format("Jan 2 15:04 MST"))
	}
}
