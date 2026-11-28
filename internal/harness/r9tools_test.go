package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 5 of the ninth review: a model that answers "@e remind me ..."
// with a schedule call alone, wanting to read the timer's id and time
// before it tells the user, is called again with the result, as it was on
// main and as it is after relay_start; the user is then told. The same for
// cancel_schedule.
func TestR9ATimerSetOrCancelledAloneIsReportedToTheUser(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		last := msgs[len(msgs)-1]["role"]
		switch {
		case last == "tool" && strings.Contains(all, "timer s1 cancelled") && !strings.Contains(all, "Cancelled s1."):
			return say("Cancelled s1.")
		case last == "tool" && strings.Contains(all, "timer s1 set") && !strings.Contains(all, "Set s1."):
			return say("Set s1.")
		case last == "tool":
			return hold()
		case strings.Contains(all, "cancel that timer") && !strings.Contains(all, "timer s1 cancelled"):
			return reply{calls: []event.ToolCall{tc("cancel_schedule", `{"schedule":"s1"}`)}}
		case strings.Contains(all, "remind me in 30m") && !strings.Contains(all, "timer s1 set"):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"in 30m","note":"Remind the user to deploy."}`)}}
		}
		return hold()
	}, nil)
	s.settled()
	s.type_("@e remind me in 30m to deploy")
	s.waitFor("the user to be told the timer is set", func() bool { return strings.Contains(strings.Join(s.ui.said(), "\n"), "Set s1.") })
	s.settled()
	s.type_("@e cancel that timer")
	s.waitFor("the user to be told the timer is cancelled", func() bool { return strings.Contains(strings.Join(s.ui.said(), "\n"), "Cancelled s1.") })
}
