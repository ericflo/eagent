package relay

import (
	"testing"
	"time"
)

// rv16: Claude empties its queue back into the input (an interrupt, or the
// person pulling queued messages up to edit): a popAll line. The parser's
// list keeps the words, so a later pasted send of the same words (its
// enqueue line Claude's placeholder) has its first prompt marked a second
// record.
func TestRV16PopAllLeavesWordsThatHideAPastedSendsArrival(t *testing.T) {
	long := "check the migration plan\nagainst the schema"
	k1Case{"popall_then_pasted_send",
		nil,
		[]string{k1Queue("enqueue", long), k1Queue("popAll", ""), k1TurnEnd()},
		[][2]string{{"r1", long}},
		"",
		[]string{"r1"},
	}.runRV15(t, []string{k1Queue("enqueue", "[Pasted text #1 +1 lines]"), k1TurnEnd(), k1Queue("dequeue", "")}, claudeUser(time.Now(), long))
}

// rv16: the same with a remove line that names no words.
func TestRV16BareRemoveLeavesWordsThatHideAPastedSendsArrival(t *testing.T) {
	long := "check the migration plan\nagainst the schema"
	k1Case{"bare_remove_then_pasted_send",
		nil,
		[]string{k1Queue("enqueue", long), k1Queue("remove", ""), k1TurnEnd()},
		[][2]string{{"r1", long}},
		"",
		[]string{"r1"},
	}.runRV15(t, []string{k1Queue("enqueue", "[Pasted text #1 +1 lines]"), k1TurnEnd(), k1Queue("dequeue", "")}, claudeUser(time.Now(), long))
}
