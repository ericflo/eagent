package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
)

// capsFromMeta prefers the eagent.client map and leaves behavior unchanged
// when no capability keys are present.
func TestCapsFromMeta(t *testing.T) {
	if got := capsFromMeta(nil); got != nil {
		t.Fatalf("nil meta = %+v, want nil", got)
	}
	if got := capsFromMeta(map[string]any{"eagent": "mirror"}); got != nil {
		t.Fatalf("plain meta = %+v, want nil", got)
	}
	got := capsFromMeta(map[string]any{
		"eagent.client": map[string]any{
			"timezone": "America/Los_Angeles",
			"locale":   "en-US",
			"device":   "phone",
			"app":      "finalechat/1.2",
			"supplies": []any{"tz", "locale"},
		},
		"eagent.tz": "ignored-when-map-present",
	})
	if got == nil {
		t.Fatal("map meta returned nil")
	}
	if got.Source != "finalechat" || got.Timezone != "America/Los_Angeles" || got.Locale != "en-US" || got.Device != "phone" || got.App != "finalechat/1.2" {
		t.Fatalf("map meta = %+v", got)
	}
	if !got.Declares("tz") || got.Declares("screen") {
		t.Fatalf("supplies = %+v", got.Supplies)
	}
	if line := got.Compact(); line != "finalechat · tz America/Los_Angeles · en-US · phone · finalechat/1.2" {
		t.Fatalf("compact = %q", line)
	}
	// Flat keys are the fallback.
	got = capsFromMeta(map[string]any{"eagent.tz": "Europe/Berlin", "eagent.device": "tablet"})
	if got == nil || got.Timezone != "Europe/Berlin" || got.Device != "tablet" || got.Source != "finalechat" {
		t.Fatalf("flat meta = %+v", got)
	}
	// An empty map declares nothing.
	if got := capsFromMeta(map[string]any{"eagent.client": map[string]any{}}); got != nil {
		t.Fatalf("empty map = %+v, want nil", got)
	}
}

// The inbox preserves the client capsule through its JSON file round trip,
// so handleInbox can thread it into the appended event.
func TestPostInboxPreservesClientCaps(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "sess")
	if err := PostInbox(sess, InboxMessage{
		Type:   "message",
		Text:   "hi",
		From:   "web",
		Client: &clientcaps.Caps{Source: "web", Timezone: "America/Los_Angeles", Locale: "en-US", Supplies: []string{"tz", "locale", "screen"}},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(InboxDir(sess))
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox = %+v, err = %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(InboxDir(sess), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var back InboxMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Client == nil || back.Client.Timezone != "America/Los_Angeles" || back.Client.Locale != "en-US" || !back.Client.Declares("screen") {
		t.Fatalf("round trip = %+v", back.Client)
	}
	var umd event.UserMessageData
	umd.Text, umd.Source, umd.Client = back.Text, back.From, back.Client
	if umd.Client.Compact() != "web · tz America/Los_Angeles · en-US" {
		t.Fatalf("compact = %q", umd.Client.Compact())
	}
}
