package clientcaps

import (
	"encoding/json"
	"testing"
)

func TestCompactSkipsEmpties(t *testing.T) {
	cases := []struct {
		name string
		caps *Caps
		want string
	}{
		{"nil", nil, ""},
		{"empty", &Caps{}, ""},
		{"unknown device skipped", &Caps{Device: "unknown"}, ""},
		{"source only", &Caps{Source: "web"}, "web"},
		{"full web", &Caps{Source: "web", Timezone: "America/Los_Angeles", Locale: "en-US"}, "web · tz America/Los_Angeles · en-US"},
		{"phone with device and screen", &Caps{Source: "finalechat", Timezone: "Europe/Berlin", Device: "phone", Screen: "1512x982"}, "finalechat · tz Europe/Berlin · phone · 1512x982"},
		{"app appended", &Caps{Source: "web", App: "eagent-web/3"}, "web · eagent-web/3"},
	}
	for _, c := range cases {
		if got := c.caps.Compact(); got != c.want {
			t.Errorf("%s: Compact() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDeclares(t *testing.T) {
	var nilCaps *Caps
	if nilCaps.Declares("tz") {
		t.Error("nil caps must not declare anything")
	}
	c := &Caps{Supplies: []string{"tz", "locale", "screen"}}
	for _, tok := range []string{"tz", "locale", "screen"} {
		if !c.Declares(tok) {
			t.Errorf("expected Declares(%q) = true", tok)
		}
	}
	if c.Declares("camera") {
		t.Error("Declares(camera) = true, want false")
	}
}

func TestJSONOmitsEmpties(t *testing.T) {
	raw, err := json.Marshal(&Caps{Source: "web", Timezone: "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["source"] != "web" || m["timezone"] != "America/Los_Angeles" {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}
