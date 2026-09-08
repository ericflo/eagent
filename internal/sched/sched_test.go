package sched

import (
	"testing"
	"time"
)

func TestParseLoopForms(t *testing.T) {
	for _, spec := range []string{"30s", "every 30s", "@every 30s", "5m", "1h30m"} {
		s, err := Parse(spec)
		if err != nil || s.Kind != Loop {
			t.Fatalf("%q: %v %v", spec, s, err)
		}
	}
	if _, err := Parse("0s"); err == nil {
		t.Fatal("zero interval accepted")
	}
	s, _ := Parse("1s")
	if got := s.Next(time.Unix(0, 0)); got.Sub(time.Unix(0, 0)) != MinInterval {
		t.Fatalf("min interval not enforced: %v", got)
	}
}

func TestParseOnce(t *testing.T) {
	s, err := Parse("in 10m")
	if err != nil || s.Kind != Once {
		t.Fatal(err)
	}
	if s.Next(time.Now()).IsZero() {
		t.Fatal("should fire once")
	}
	if !s.Next(time.Now().Add(time.Hour)).IsZero() {
		t.Fatal("should not fire after its time")
	}
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if s, err = Parse(at); err != nil || s.Kind != Once {
		t.Fatalf("rfc3339: %v", err)
	}
}

func TestCronNext(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 9, 6, 10, 7, 30, 0, loc) // Sunday
	cases := map[string]time.Time{
		"*/15 * * * *":   time.Date(2026, 9, 6, 10, 15, 0, 0, loc),
		"0 12 * * *":     time.Date(2026, 9, 6, 12, 0, 0, 0, loc),
		"30 9 * * mon":   time.Date(2026, 9, 7, 9, 30, 0, 0, loc),
		"0 0 1 * *":      time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
		"@hourly":        time.Date(2026, 9, 6, 11, 0, 0, 0, loc),
		"5 4 * * sun":    time.Date(2026, 9, 13, 4, 5, 0, 0, loc),
		"0 0 * * 7":      time.Date(2026, 9, 13, 0, 0, 0, 0, loc), // 7 == Sunday
		"0 9-17/4 * * *": time.Date(2026, 9, 6, 13, 0, 0, 0, loc),
		"0 0 15 * wed":   time.Date(2026, 9, 9, 0, 0, 0, 0, loc), // dom OR dow
		"0 0 29 feb *":   time.Date(2028, 2, 29, 0, 0, 0, 0, loc),
	}
	for spec, want := range cases {
		s, err := Parse(spec)
		if err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
		if got := s.Next(base); !got.Equal(want) {
			t.Errorf("%q: got %v want %v", spec, got, want)
		}
	}
}

func TestCronRejects(t *testing.T) {
	for _, bad := range []string{"60 * * * *", "* 24 * * *", "* * 32 * *", "* * * 13 *", "a b c d e", "1 2 3"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Daylight-saving transitions: the walk must terminate across a missing hour
// and fire a daily job once, not twice, across a repeated one.
func TestCronNextAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	daily6, _ := Parse("0 6 * * *")
	got := daily6.Next(time.Date(2026, 3, 7, 23, 0, 0, 0, ny)) // the night the clocks go forward
	if want := time.Date(2026, 3, 8, 6, 0, 0, 0, ny); !got.Equal(want) {
		t.Fatalf("spring forward: got %v want %v", got, want)
	}
	daily3, _ := Parse("0 3 * * *")
	if got := daily3.Next(time.Date(2026, 3, 8, 0, 30, 0, 0, ny)); !got.Equal(time.Date(2026, 3, 8, 3, 0, 0, 0, ny)) {
		t.Fatalf("03:00 after the gap: %v", got)
	}
	daily1, _ := Parse("0 1 * * *")
	first := daily1.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, ny)) // the night the clocks go back: 01:00 happens twice
	second := daily1.Next(first)
	if first.Day() != 1 || second.Day() != 2 || second.Sub(first) < 24*time.Hour {
		t.Fatalf("fall back: fired at %v then %v; a daily job must fire once a day", first, second)
	}
	hourly, _ := Parse("0 * * * *")
	a := hourly.Next(time.Date(2026, 11, 1, 0, 30, 0, 0, ny))
	b := hourly.Next(a)
	c := hourly.Next(b)
	// Civil hours advance monotonically; the repeated 01:00 is visited once,
	// so an hourly job skips one fire on fall-back rather than firing twice.
	if !(b.After(a) && c.After(b)) || c.Sub(a) > 3*time.Hour || b.Hour() != 2 || c.Hour() != 3 {
		t.Fatalf("hourly across fall back: %v %v %v", a, b, c)
	}
	havana, err := time.LoadLocation("America/Havana")
	if err == nil {
		// Local midnight does not exist on Havana's spring-forward day: the
		// job must never fire twice on one day, never fire early, and never
		// stall; the missing midnight itself is skipped.
		daily, _ := Parse("@daily")
		at := time.Date(2026, 3, 5, 12, 0, 0, 0, havana)
		lastDay := 0
		for i := 0; i < 5; i++ {
			next := daily.Next(at)
			if next.IsZero() || !next.After(at) {
				t.Fatalf("@daily in Havana stalled at %v", at)
			}
			if next.Hour() != 0 || next.Minute() != 0 {
				t.Fatalf("@daily in Havana fired at %v, not at a local midnight", next)
			}
			if next.YearDay() == lastDay {
				t.Fatalf("@daily in Havana fired twice on day %d (%v)", lastDay, next)
			}
			if gap := next.Sub(at); gap > 49*time.Hour {
				t.Fatalf("@daily in Havana gap of %v ending %v", gap, next)
			}
			lastDay = next.YearDay()
			at = next
		}
		ny2, _ := Parse("0 2 * * *")
		if got := ny2.Next(time.Date(2026, 3, 8, 0, 30, 0, 0, ny)); got.Day() != 9 || got.Hour() != 2 {
			t.Fatalf("02:00 does not exist on 8 March in New York; fired at %v", got)
		}
	}
}
