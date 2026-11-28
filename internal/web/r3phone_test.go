package web

import (
	"testing"

	"github.com/ericflo/eagent/internal/integration"
)

// A session resumed for a message from the phone is given what a running
// session reads off the message: its id, the composer's route and
// delivery, and the client's capabilities.
func TestPhoneResumeCarriesTheMessagesIDRouteDeliveryAndClient(t *testing.T) {
	s := New(t.TempDir(), nil)
	opts := s.phoneResumeOptions(integration.PhoneMessage{ID: "m1", Text: "/screen", Meta: map[string]any{
		"eagent.route": "agent", "eagent.delivery": "after_turn", "eagent.client": map[string]any{"timezone": "Asia/Manila"},
	}})
	if opts.Prompt != "/screen" || opts.PromptSource != "finalechat" || opts.PromptPhoneID != "m1" || opts.PromptRoute != "agent" || opts.PromptDelivery != "after_turn" {
		t.Fatalf("options %+v", opts)
	}
	if opts.PromptClient == nil || opts.PromptClient.Timezone != "Asia/Manila" {
		t.Fatalf("client %+v", opts.PromptClient)
	}
}
