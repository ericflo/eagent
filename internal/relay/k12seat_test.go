package relay

import (
	"testing"
	"time"
)

// A pi at rest (its verdict settled on rest) shows one of its own dialogs
// for a moment; once it is answered pi's status file says it works (the
// answer started a run, its first entry not yet written). The look after
// the dialog sees pi at work, yet the seat says it is quiet: the settled
// verdict from before the dialog stands for that look, and one look is
// all quiet needs now.
func TestK12SR10QuietSaidOnALookThatSeesPiAtWork(t *testing.T) {
	s, status := piReadySeat(t, piFooterScreen, &piStatus{State: "idle"})
	s.state, s.stateAt = "idle", time.Now()
	s.mu.Lock()
	s.catchingUp = false
	s.mu.Unlock()
	s.sayQuiet()
	s.sayQuiet()
	if !s.Snapshot().Quiet {
		t.Fatalf("an idle pi at rest for two looks is not quiet")
	}
	_ = drain(s.events)
	// A dialog of pi's own, answered within a look.
	writePiStatus(t, status, piStatus{State: "idle", Prompt: "Pick a template"})
	s.reportDialog("Pick a template")
	s.sayQuiet()
	writePiStatus(t, status, piStatus{State: "working"})
	s.reportDialog("")
	s.sayQuiet()
	if s.Snapshot().Quiet {
		t.Errorf("quiet while pi's status file says it works (the only look since the dialog saw it at work)")
	}
	for _, ev := range drain(s.events) {
		if ev.Kind == KindQuiet {
			t.Errorf("KindQuiet said on a look that saw pi at work")
		}
	}
}

// A pi run held by one of its dialogs, answered, then quiet in its
// transcript for ten minutes while pi's status file says it works (a long
// build): blocked, but not stuck. The verdict settled while the dialog was
// up ("other": pi reports the dialog's prompt) must not stand in for what
// pi says now.
func TestK12SR10StaleVerdictAfterPiDialogCallsWorkingPiStuck(t *testing.T) {
	s, status := piReadySeat(t, piFooterScreen, &piStatus{State: "working"})
	s.state, s.stateAt = "working", time.Now()
	// pi asks something mid-run: the seat is blocked on it.
	writePiStatus(t, status, piStatus{State: "working", Prompt: "Allow rm -rf build?"})
	s.reportDialog("Allow rm -rf build?")
	// The screen check runs while it is blocked (every 1.5 s).
	s.checkScreen(time.Now())
	s.checkScreen(time.Now())
	// The user answers it with keys; pi runs on.
	writePiStatus(t, status, piStatus{State: "working"})
	s.reportDialog("")
	if st := s.Snapshot(); st.State != "working" {
		t.Fatalf("after the dialog: %s, want working", st.State)
	}
	_ = drain(s.events)
	// Ten quiet minutes later, pi's status still says working.
	s.mu.Lock()
	s.stateAt = time.Now().Add(-11 * time.Minute)
	s.mu.Unlock()
	s.checkScreen(time.Now())
	st := s.Snapshot()
	if st.State != "blocked" {
		t.Fatalf("after ten quiet minutes: %s, want blocked", st.State)
	}
	if st.Stuck {
		t.Errorf("pi's status file says it works, yet the seat says it is stuck (a waiting model change would restart it mid-run)")
	}
	for _, ev := range drain(s.events) {
		if ev.Kind == KindState && ev.Stuck {
			t.Errorf("event said stuck while pi works: %+v", ev)
		}
	}
}
