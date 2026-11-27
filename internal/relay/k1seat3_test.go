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

// rv16: two waiting sends of the same words, one queued (the 3c5fe26 case),
// after a popAll earlier in the session: the dequeue takes the stale words,
// and the queued message's prompt confirms the second send too.
func TestRV16PopAllShiftsTheQueueAndTheSecondRecordConfirms(t *testing.T) {
	k1Case{"popall_then_one_queued_two_waiting",
		nil,
		[]string{k1Queue("enqueue", "look again"), k1Queue("popAll", ""), k1TurnEnd()},
		[][2]string{{"r1", "continue"}, {"r2", "continue"}},
		"",
		[]string{"r1"},
	}.runRV15(t, []string{k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", "")}, claudeUser(time.Now(), "continue"))
}

// The reader's suspicion: Claude writes other lines (a file-history
// snapshot, an attachment) between a dequeue and the prompt it runs. They
// are no prompt: the prompt after them is still the queued message's
// second record.
func TestK1ADequeuedPromptAfterASnapshotLineIsStillTheSecondRecord(t *testing.T) {
	ts := stamp(time.Now())
	k1Case{"dequeue_snapshot_attachment_prompt",
		[][2]string{{"r1", "continue"}, {"r2", "continue"}},
		[]string{k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", ""),
			`{"type":"file-history-snapshot","messageId":"m1","snapshot":{"trackedFileBackups":{}},"isSnapshotUpdate":false}`,
			`{"type":"attachment","timestamp":"` + ts + `","attachment":{"type":"queued_command"}}`},
		nil, claudeUser(time.Now(), "continue"), []string{"r1"},
	}.run(t)
}

// Claude takes the most urgent queued command first, not the oldest: the
// prompt after a dequeue says which it took.
func TestK1ADequeueTakesTheMessageItsPromptShows(t *testing.T) {
	k1Case{"the_newer_runs_first",
		[][2]string{{"r1", "continue"}, {"r2", "continue"}},
		[]string{k1Queue("enqueue", "<task-notification>build done</task-notification>"), k1Queue("enqueue", "continue"), k1TurnEnd(), k1Queue("dequeue", "")},
		nil, claudeUser(time.Now(), "continue"), []string{"r1"},
	}.run(t)
}
