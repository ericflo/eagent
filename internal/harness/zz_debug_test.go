package harness

import "testing"

func dumpLogs(t *testing.T, s *session) {
	_, _, logs := s.ui.snapshot()
	for _, l := range logs {
		t.Log("LOG: " + l)
	}
}
