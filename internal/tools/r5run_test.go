package tools

import (
	"regexp"
	"strings"
	"testing"
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
