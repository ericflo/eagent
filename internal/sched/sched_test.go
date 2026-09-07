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
