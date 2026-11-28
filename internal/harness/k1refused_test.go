package harness

import (
	"sync"

	"github.com/ericflo/eagent/internal/integration"
)

// k1ResumingHost is the serve as the reply watcher drives it: it resumes
// the rig's stopped session with the reply, as the serve's Resume does.
type k1ResumingHost struct {
	g       *phoneRig
	mu      sync.Mutex
	hosted  bool
	resumed []integration.PhoneMessage
}

func (h *k1ResumingHost) Hosted(string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hosted
}

func (h *k1ResumingHost) Resume(_ string, msg integration.PhoneMessage) error {
	h.mu.Lock()
	h.hosted = true
	h.resumed = append(h.resumed, msg)
	h.mu.Unlock()
	h.g.resume(Options{Interactive: true, Prompt: msg.Text, PromptSource: "finalechat", PromptAttachments: msg.Attachments, PromptPhoneID: msg.ID, PromptRefused: msg.Refused})
	return nil
}
