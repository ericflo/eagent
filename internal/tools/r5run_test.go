package tools

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The place of a hit is the first match outside the provider's replay
// payload: one before the payload, else the first after it.
func TestTheFirstMatchOutsideTheReplayPayloadIsTheOneShown(t *testing.T) {
	re := regexp.MustCompile("(?i)needle")
	text := `aaa NEEDLE {"native":"needle needle"} bbb needle`
	from := strings.Index(text, `"needle needle"`)
	to := from + len(`"needle needle"`)
	if at := firstOutside(re, text, from, to); at != strings.Index(text, "NEEDLE") {
		t.Errorf("before the payload: %d", at)
	}
	text = `aaa {"native":"needle needle"} bbb needle`
	from = strings.Index(text, `"needle needle"`)
	to = from + len(`"needle needle"`)
	if at := firstOutside(re, text, from, to); at != strings.LastIndex(text, "needle") {
		t.Errorf("after the payload: %d, want %d", at, strings.LastIndex(text, "needle"))
	}
	if at := firstOutside(re, `{"native":"needle"}`, 10, 18); at != -1 {
		t.Errorf("only inside the payload: %d", at)
	}
	if at := firstOutside(re, "no match here", -1, -1); at != -1 {
		t.Errorf("no match: %d", at)
	}
	if at := firstOutside(re, "x needle", -1, -1); at != 2 {
		t.Errorf("no payload: %d", at)
	}
}

// relay_forward's seq is a number the conversation never shows; the
// description says where it is read (a raw line of the record), so the
// model does not guess one, which could name another of the user's
// messages and send that.
func TestRelayForwardSaysWhereAMessagesNumberIsRead(t *testing.T) {
	var desc string
	for _, tool := range All() {
		if tool.Name == RelayForward {
			var schema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				t.Fatal(err)
			}
			desc = schema.Properties["seq"].Description
		}
	}
	if !strings.Contains(desc, SessionRead) || !strings.Contains(desc, "raw=true") {
		t.Fatalf("relay_forward.seq says %q", desc)
	}
	ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "rebase it"})
	ev.Seq = 7
	if line := wholeLine(ev, true); !strings.Contains(line, `"seq":7`) {
		t.Fatalf("a raw line does not show its number: %s", line)
	}
}
