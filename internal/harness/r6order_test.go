package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// r6Seq is the record's first event of type ty naming the app message id
// (phone_id or message_id), or -1.
func r6Seq(g *phoneRig, ty, id string) int64 {
	for _, ev := range g.log() {
		var d struct {
			PhoneID   string `json:"phone_id"`
			MessageID string `json:"message_id"`
		}
		if ev.Type == ty && ev.Decode(&d) == nil && (d.PhoneID == id || d.MessageID == id) {
			return ev.Seq
		}
	}
	return -1
}

// A phone message taken as the answer to the open question names its app
// message on the answer itself: one write, no ledger entry beside it.
func TestAPhoneAnswerNamesItsMessageInTheOneWrite(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "paint it", true, r5AskBrain(), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	fp.userReply("red")
	id := fp.lastMessageID()
	waitFor(t, "the answer", func() bool { return r6Seq(g, event.UserAnswer, id) > 0 })
	g.rt.sync(func() {})
	if s := r6Seq(g, event.PhoneRead, id); s > 0 {
		t.Fatalf("a phone.read (seq %d) beside the answer that names the message", s)
	}
}

// A command from the phone is recorded as taken before it runs: what it
// does comes after its ledger entry.
func TestAPhoneCommandIsRecordedAsTakenBeforeItRuns(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "paint it", true, r5AskBrain(), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	fp.userReply("/dismiss")
	id := fp.lastMessageID()
	waitFor(t, "the dismissal", func() bool { return len(g.events(event.UserDismiss)) == 1 })
	read := r6Seq(g, event.PhoneRead, id)
	dismissed := g.events(event.UserDismiss)[0].Seq
	if read < 0 || read > dismissed {
		r5takenDump(g)
		t.Fatalf("the command's ledger entry (seq %d) is not before what it did (seq %d)", read, dismissed)
	}
}
