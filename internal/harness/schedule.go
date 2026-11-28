package harness

import (
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/sched"
)

// Timers eagent sets for itself with the schedule tool: "at 9:00 tell the
// agent to deploy", "check back in 30 minutes". When one comes due it is
// recorded, and that wakes eagent with the note it left. A session keeps
// running while one is set. The timers map and everything here belong to
// the loop goroutine.

// rearmSchedules starts the timers of a resumed session.
func (r *Runtime) rearmSchedules() {
	now := time.Now()
	for _, sc := range r.st.ActiveSchedules() {
		next := sc.Next
		if next.IsZero() || next.Before(now) {
			// Missed while eagent was not running: it comes due soon, once.
			next = now.Add(2 * time.Second)
		}
		r.armSchedule(sc.ID, next)
	}
}

// maxTimerHop bounds one time.AfterFunc. Go timers run on the monotonic
// clock, which stops while the machine sleeps, so a single long hop would
// hold a deadline that no longer matches the timer's wall-clock time.
// Hopping re-reads the wall clock every minute, so the error after a
// resume or a clock step is at most one hop.
const maxTimerHop = time.Minute

func (r *Runtime) armSchedule(id string, at time.Time) {
	if r.timers == nil {
		r.timers = map[string]*time.Timer{}
	}
	if t := r.timers[id]; t != nil {
		t.Stop()
	}
	d := time.Until(at)
	if d < 0 {
		d = 0
	}
	if d > maxTimerHop {
		var t *time.Timer
		t = time.AfterFunc(maxTimerHop, func() {
			r.post(func() {
				if r.timers[id] == t { // still ours: not cancelled or re-armed meanwhile
					r.armSchedule(id, at)
				}
			})
		})
		r.timers[id] = t
		return
	}
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		r.post(func() {
			// A timer that was cancelled or set again after this one
			// had already gone off must not fire in its place.
			if r.timers[id] == t {
				r.fireSchedule(id)
			}
		})
	})
	r.timers[id] = t
}

func (r *Runtime) fireSchedule(id string) {
	sc := r.st.Schedules[id]
	if sc == nil || sc.Cancelled || r.ending {
		return
	}
	now := time.Now()
	var next time.Time
	if sc.Kind != sched.Once {
		// Relative specs ("in 10m") must not be parsed again: a one-shot
		// comes due once.
		s, err := sched.Parse(sc.Spec)
		if err != nil {
			return
		}
		// Counted from when it was due when the wall clock reads earlier:
		// the timer runs on the steady clock, and a clock set back or
		// slewed would otherwise give this same time again, and a second
		// firing.
		from := now
		if sc.Next.After(from) {
			from = sc.Next
		}
		next = s.Next(from)
	}
	r.append(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: id, Note: sc.Note, Next: next}))
	if !next.IsZero() {
		r.armSchedule(id, next)
	} else {
		delete(r.timers, id)
	}
}

// listSchedules prints the timers that are set (/schedules).
func (r *Runtime) listSchedules() {
	active := r.st.ActiveSchedules()
	for _, s := range active {
		r.ui.Log("%s %s %q next %s: %s", s.ID, s.Kind, s.Spec, s.Next.In(r.st.Location()).Format("15:04:05"), s.Note)
	}
	if len(active) == 0 {
		r.ui.Log("no timers")
	}
}
