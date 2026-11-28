package harness

import (
	"strings"
)

func gaveUp(s *session) bool {
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "went unanswered") {
			return true
		}
	}
	return false
}
