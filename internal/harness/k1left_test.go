package harness

import (
	"sync"

	"github.com/ericflo/eagent/internal/integration"
)

// k1LeftHost is the serve, as the reply watcher sees it: it resumes the
// session (and hands each message over), and runs nothing.
type k1LeftHost struct {
	mu      sync.Mutex
	resumed []integration.PhoneMessage
	next    chan integration.PhoneMessage
}

func (h *k1LeftHost) Hosted(string) bool { return false }
func (h *k1LeftHost) Resume(_ string, msg integration.PhoneMessage) error {
	h.mu.Lock()
	h.resumed = append(h.resumed, msg)
	h.mu.Unlock()
	h.next <- msg
	return nil
}
