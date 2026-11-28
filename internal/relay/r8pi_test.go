package relay

import (
	"path/filepath"
	"testing"
	"time"
)

// After pi's /new the status file names the new session before pi writes
// any file for it (that waits for its first prompt). The seat reports the
// move at once, with no transcript, so a restart in that window opens the
// new session and not the one the user left; once the file appears it is
// reported with its path and its entries are news.
func TestR8PiNewSessionReportedBeforeItsFile(t *testing.T) {
	isolatePi(t)
	w := newPiSwitchSeat(t, time.Time{}, []string{piHeaderLine("first", time.Now())}, entries(0))
	w.until(5*time.Second, innerIs(w.first))
	second := filepath.Join(w.dir, "second.jsonl")
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
	got := w.until(looks(8), func(ev Event) bool { return ev.Kind == KindInner })
	if ev := got[len(got)-1]; ev.InnerID != "second" || ev.Transcript != "" {
		t.Fatalf("move reported as %+v, want the new id and no transcript", ev)
	}
	writeLines(t, second, append([]string{piHeaderLine("second", time.Now()), piModelLine(time.Now())}, piTurnLines("k", "go", "First reply.", time.Now().Add(stampAhead))...)...)
	got = w.until(5*time.Second, isTurn)
	if !innerSeen(got, second, "second") {
		t.Fatalf("the new file was not reported with its path: %+v", got)
	}
	if turns := turnsIn(got); len(turns) != 1 || turns[0] != "First reply." {
		t.Fatalf("turns %q", turns)
	}
}
