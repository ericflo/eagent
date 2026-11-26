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
//	unconfirmed  typed, arrival never confirmed       no, look at its screen     failed
//	moved        went to <agent>                      no, it went there          (the send there decides)
//
// Only a send that certainly did not go may go again: anything typed may
// be in the agent's hands, and sending it blind could have the agent do
// the work twice. A record with no outcome (older than outcomes) is read
// as arrived, as it always was.
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
		v := sendVerdict{listed: "typed, but its arrival was never confirmed", receipt: "failed",
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
