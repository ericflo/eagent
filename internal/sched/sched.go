// Package sched computes wake times for loops, cron expressions, and one-shot
// timers. It is pure: the harness owns the actual timers.
package sched

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind of schedule.
const (
	Loop = "loop"
	Cron = "cron"
	Once = "once"
)

// Schedule is a parsed wake specification.
type Schedule struct {
	Kind  string
	Spec  string
	every time.Duration
	cron  *cronExpr
	at    time.Time
}

// Parse accepts a loop interval ("30s", "5m", "1h30m", or "every 30s"), a
// 5-field cron expression, or an RFC3339 timestamp / relative delay prefixed
// with "in " ("in 10m").
func Parse(spec string) (*Schedule, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return nil, fmt.Errorf("empty schedule")
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "every ") {
		d, err := time.ParseDuration(strings.TrimSpace(s[6:]))
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("bad interval %q", s)
		}
		return &Schedule{Kind: Loop, Spec: s, every: d}, nil
	}
	if strings.HasPrefix(lower, "in ") {
		d, err := time.ParseDuration(strings.TrimSpace(s[3:]))
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("bad delay %q", s)
		}
		return &Schedule{Kind: Once, Spec: s, at: time.Now().Add(d)}, nil
	}
	if strings.HasPrefix(lower, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(s[7:]))
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("bad interval %q", s)
		}
		return &Schedule{Kind: Loop, Spec: s, every: d}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return nil, fmt.Errorf("interval must be positive: %q", s)
		}
		return &Schedule{Kind: Loop, Spec: s, every: d}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &Schedule{Kind: Once, Spec: s, at: t}, nil
	}
	if c, err := parseCron(s); err == nil {
		return &Schedule{Kind: Cron, Spec: s, cron: c}, nil
	} else if len(strings.Fields(s)) == 5 {
		return nil, err
	}
	return nil, fmt.Errorf("unrecognised schedule %q (use an interval like 30s, a 5-field cron expression, or an RFC3339 time)", s)
}

// ParseLoop parses an interval only.
func ParseLoop(spec string) (*Schedule, error) {
	s, err := Parse(spec)
	if err != nil {
		return nil, err
	}
	if s.Kind != Loop {
		return nil, fmt.Errorf("%q is not an interval", spec)
	}
	return s, nil
}

// MinInterval guards against runaway loops.
const MinInterval = 5 * time.Second

// Next returns the first fire time strictly after t, or zero if the schedule
// will never fire again.
func (s *Schedule) Next(t time.Time) time.Time {
	switch s.Kind {
	case Loop:
		return t.Add(max(s.every, MinInterval))
	case Once:
		if s.at.After(t) {
			return s.at
		}
		return time.Time{}
	case Cron:
		return s.cron.next(t)
	}
	return time.Time{}
}

// Repeats reports whether the schedule fires more than once.
func (s *Schedule) Repeats() bool { return s.Kind != Once }

// ---- cron ---------------------------------------------------------------

type cronExpr struct {
	minute, hour, dom, month, dow uint64 // bitsets
	domStar, dowStar              bool
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

var aliases = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

func parseCron(s string) (*cronExpr, error) {
	if a, ok := aliases[strings.ToLower(s)]; ok {
		s = a
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron needs 5 fields, got %d", len(fields))
	}
	var c cronExpr
	var err error
	if c.minute, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hour, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.dom, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	c.domStar = fields[2] == "*"
	if c.month, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dow, err = parseField(fields[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	c.dowStar = fields[4] == "*"
	if c.dow&(1<<7) != 0 { // 7 == Sunday
		c.dow |= 1
		c.dow &^= 1 << 7
	}
	return &c, nil
}

func parseField(f string, lo, hi int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(f, ",") {
		step := 1
		rng := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			var err error
			step, err = strconv.Atoi(part[i+1:])
			if err != nil || step <= 0 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			rng = part[:i]
		}
		start, end := lo, hi
		if rng != "*" {
			a, b, found := strings.Cut(rng, "-")
			var err error
			if start, err = atoiNamed(a, names); err != nil {
				return 0, err
			}
			if found {
				if end, err = atoiNamed(b, names); err != nil {
					return 0, err
				}
			} else if strings.Contains(part, "/") {
				end = hi
			} else {
				end = start
			}
		}
		if start < lo || end > hi || start > end {
			return 0, fmt.Errorf("value out of range in %q", part)
		}
		for v := start; v <= end; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func atoiNamed(s string, names map[string]int) (int, error) {
	if v, ok := names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return v, nil
}

func (c *cronExpr) matchesDay(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dow
	case c.dowStar:
		return dom
	default:
		return dom || dow // classic cron semantics
	}
}

func (c *cronExpr) next(from time.Time) time.Time {
	// Walk wall-clock ("civil") time in UTC, where every minute exists
	// exactly once and arithmetic always advances. Walking local time either
	// repeats a wall-clock hour (DST fall-back: a daily job fired twice) or
	// fails to advance at all (spring-forward: time.Date normalises the
	// missing hour backwards, and this loop used to spin forever). The
	// matching minute is turned back into a local instant only at the end.
	loc := from.Location()
	w := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute(), 0, 0, time.UTC).Add(time.Minute)
	limit := w.AddDate(5, 0, 0)
	for w.Before(limit) {
		if c.month&(1<<uint(w.Month())) == 0 {
			w = time.Date(w.Year(), w.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.matchesDay(w) {
			w = time.Date(w.Year(), w.Month(), w.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if c.hour&(1<<uint(w.Hour())) == 0 {
			w = time.Date(w.Year(), w.Month(), w.Day(), w.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if c.minute&(1<<uint(w.Minute())) == 0 {
			w = w.Add(time.Minute)
			continue
		}
		t := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, loc)
		// A wall-clock minute that does not exist locally (spring forward)
		// normalises to some other instant; it is skipped, not fired early.
		ty, tm, td := t.Date()
		exists := ty == w.Year() && tm == w.Month() && td == w.Day() && t.Hour() == w.Hour() && t.Minute() == w.Minute()
		if exists && t.After(from) {
			return t
		}
		w = w.Add(time.Minute) // a wall-clock minute a DST jump skipped or repeated
	}
	return time.Time{}
}
