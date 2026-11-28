package harness

import (
	"errors"
	"fmt"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// sendMoved is the outcome of a send, seen from the agent it was first sent
// to, that waited for a switch and went to the agent that took over.
const sendMoved state.SendOutcome = "moved"

// sendVerdict is what is said and decided about one send of a user's
// message, from where it got to.
type sendVerdict struct {
	// listed is how the list of what the user sent an agent shows it.
	listed string
	// again says relay_forward may send the same message to the same agent
	// once more; refusal says why not, when it may not.
	again   bool
	refusal string
	// receipt is the phone's word for the user's message after this send.
	receipt string
}

// judgeSend is the one place a send's outcome is turned into words and a
// decision. who names the agent, id the send, to (for a moved send) the
// agent it went to, atts the user's attachment lines as the send carried
// them.
//
//	outcome      listed as                            forward again              receipt
//	waiting      not confirmed yet                    no, it is on its way       sent
//	held         held for the end of its turn         no, it is on its way       held
//	arrived      arrived                              no, it went as written     delivered
//	not_sent     did not go                           yes, word for word         failed
//	unconfirmed  typed, arrival never confirmed       no, look at its screen     sent
//	moved        went to <agent>                      no, it went there          (the send there decides)
//	"" (no id)   arrived                              no, it went as written     delivered
//
// Only a send that certainly did not go may go again: anything typed may
// be in the agent's hands, and sending it blind could have the agent do
// the work twice. For the same reason the phone does not call an
// unconfirmed send "failed": FinaleChat shows that as "Not delivered" with
// a Retry that sends the words again. "sent" says what is known (it was
// typed into the agent), and a late confirmation turns it into delivered.
//
// Older records: a success is arrived; a failure without an outcome is
// unconfirmed (state.DeliveryOutcome). The "" row is a send id the state
// never noted (a relay.send under a name with no relay.start), which reads
// as arrived, as every send not waiting did before outcomes existed.
func judgeSend(o state.SendOutcome, who, id, to, atts string) sendVerdict {
	switch o {
	case state.SendWaiting:
		return sendVerdict{listed: "not confirmed yet", receipt: "sent",
			refusal: fmt.Sprintf("that message is already on its way to %s as written (%s), and its transcript has not shown it yet; to add to it, use relay_send", who, id)}
	case state.SendHeld:
		return sendVerdict{listed: "held for the end of its turn", receipt: "held",
			refusal: fmt.Sprintf("that message is already held for %s to read when its current turn ends (%s); to add to it, use relay_send", who, id)}
	case state.SendNotSent:
		return sendVerdict{listed: "did not go; nothing of it reached the agent", again: true, receipt: "failed"}
	case state.SendUnconfirmed:
		v := sendVerdict{listed: "typed, but its arrival was never confirmed", receipt: "sent",
			refusal: fmt.Sprintf("that message was typed into %s (%s), but its arrival was never confirmed, so it may have reached it; relay_forward does not send it blind. Look at relay_screen first: if %s never got it, send the same words with relay_send", who, id, who)}
		if atts != "" {
			v.refusal += ", with these lines for the user's attachments:" + atts
		}
		return v
	case sendMoved:
		return sendVerdict{listed: "went to " + to,
			refusal: fmt.Sprintf("that message waited for the handover and went to %s instead (%s)", to, id)}
	}
	return sendVerdict{listed: "arrived", receipt: "delivered",
		refusal: fmt.Sprintf("that message already went to %s as written (%s); to add to it, use relay_send", who, id)}
}

// messageReceipt is the phone's one receipt for a user message, from the
// outcomes of all its sends (to however many agents, in any order; a send
// a switch moved counts by its outcome where it went):
//
//	sends                                           receipt
//	any arrived                                     delivered
//	else any waiting or typed and never confirmed   sent
//	else any held for the end of a turn             held
//	else (every one certainly did not go)           failed
//	none                                            ""
//
// A send id the state never noted counts as waiting.
//
// "failed" is the only receipt with a Retry on the phone, and Retry types
// the words into the agent again: it is offered only when no agent can
// have them or be about to.
func messageReceipt(outcomes []state.SendOutcome) string {
	rank := map[string]int{"failed": 1, "held": 2, "sent": 3, "delivered": 4}
	best := ""
	for _, o := range outcomes {
		if o == "" {
			// A send the state never noted (no relay under its name): it
			// was recorded and nothing is known of it since.
			o = state.SendWaiting
		}
		if r := judgeSend(o, "", "", "", "").receipt; rank[r] > rank[best] {
			best = r
		}
	}
	return best
}

// sendsReceipt is messageReceipt over every send of the user message at
// seq, found through sendSeq (send id to message seq).
func sendsReceipt(st *state.State, sendSeq map[string]int64, seq int64) string {
	var outcomes []state.SendOutcome
	for id, s := range sendSeq {
		if s == seq {
			outcomes = append(outcomes, st.SendOutcome(id))
		}
	}
	return messageReceipt(outcomes)
}

// sendErrOutcome is how sure a send that failed with err is not to have
// reached the agent: a seat fails only before typing, except while it types
// (relay.TypingError); every other failure (the agent not running, a
// switch that took over first, a start that failed, the session ending)
// comes before anything was typed.
func sendErrOutcome(err error) string {
	var te *relay.TypingError
	if errors.As(err, &te) {
		return event.DeliveryUnconfirmed
	}
	return event.DeliveryNotSent
}
