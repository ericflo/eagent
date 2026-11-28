package web

import (
	"github.com/ericflo/eagent/internal/integration"
)

type sr12Host struct{ resumed chan integration.PhoneMessage }

func (h *sr12Host) Hosted(string) bool { return false }
func (h *sr12Host) Resume(_ string, m integration.PhoneMessage) error {
	select {
	case h.resumed <- m:
	default:
	}
	return nil
}
