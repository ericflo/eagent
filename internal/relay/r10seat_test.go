package relay

import (
	"slices"
	"testing"
	"time"
)

// Finding 11: on a reattach, Claude (and pi) stamp a transcript entry to
// the millisecond, the record stamps a send to the nanosecond. An entry in
// the same millisecond as a restored send is its arrival; one a millisecond
// earlier is still not.
func TestR10AnEntryInTheSendsMillisecondConfirmsARestoredSend(t *testing.T) {
	t.Parallel()
	sec := time.Now().Add(-time.Minute).Truncate(time.Second)
	sent := sec.Add(123456789 * time.Nanosecond)
	r := Restored{ID: "r1", Text: "count the chairs", Mode: "queue", At: sent}
	s := attachSeat(Spec{Restore: []Restored{r}}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", sec.Add(800*time.Millisecond))
	if ok := claudeLines(s, claudeUser(sec.Add(122*time.Millisecond), "count the chairs")); len(ok) != 0 {
		t.Fatalf("an entry a millisecond before r1 was recorded confirmed %v", ok)
	}
	if ok := claudeLines(s, claudeUser(sec.Add(123*time.Millisecond), "count the chairs")); !slices.Equal(ok, []string{"r1"}) {
		t.Fatalf("an entry in r1's millisecond confirmed %v, want r1", ok)
	}
}
