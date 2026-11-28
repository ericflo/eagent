package integration

import (
	"sync/atomic"
)

// k6Host hosts every session. Once armed, its next look (a sweep's) says so
// on entered and waits for release.
type k6Host struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (h *k6Host) Hosted(string) bool {
	if h.armed.Swap(false) {
		close(h.entered)
		<-h.release
	}
	return true
}

func (h *k6Host) Resume(string, PhoneMessage) error { return nil }
