package web

import (
	"encoding/json"
	"testing"
)

// postMessage/postAnswer accept the browser's client capsule in the body.
func TestMessageBodyAcceptsClientCaps(t *testing.T) {
	var b messageBody
	if err := json.Unmarshal([]byte(`{"text":"hi","client":{"source":"web","timezone":"America/Los_Angeles","locale":"en-US","device":"desktop","screen":"1512x982","supplies":["tz","locale","screen"]}}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Text != "hi" || b.Client == nil {
		t.Fatalf("body = %+v", b)
	}
	if b.Client.Source != "web" || b.Client.Timezone != "America/Los_Angeles" || b.Client.Locale != "en-US" || b.Client.Device != "desktop" || b.Client.Screen != "1512x982" {
		t.Fatalf("client = %+v", b.Client)
	}
	if !b.Client.Declares("tz") || !b.Client.Declares("screen") || b.Client.Declares("camera") {
		t.Fatalf("supplies = %+v", b.Client.Supplies)
	}
	if line := b.Client.Compact(); line != "web · tz America/Los_Angeles · en-US · desktop · 1512x982" {
		t.Fatalf("compact = %q", line)
	}
	// Old bodies without the field still decode with a nil capsule.
	var plain messageBody
	if err := json.Unmarshal([]byte(`{"text":"hi"}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Client != nil {
		t.Fatalf("plain body client = %+v", plain.Client)
	}
}
