// Package clientcaps describes what a connected client can supply.
//
// A client (the web UI, the phone app) declares its capabilities once per
// message: its timezone, locale, device, and which capability tokens it can
// supply. The declaration travels with the message into the event log, so
// rendering stays a pure function of persisted data.
package clientcaps

import "strings"

// Caps declares what context a connected client can supply.
type Caps struct {
	Source   string   `json:"source,omitempty"`   // web | finalechat
	Timezone string   `json:"timezone,omitempty"` // IANA, e.g. America/Los_Angeles
	Locale   string   `json:"locale,omitempty"`   // e.g. en-US
	Device   string   `json:"device,omitempty"`   // desktop | phone | tablet | unknown
	App      string   `json:"app,omitempty"`      // client app/version freeform
	Screen   string   `json:"screen,omitempty"`   // e.g. 1512x982, web only, optional
	Supplies []string `json:"supplies,omitempty"` // capability tokens, e.g. tz, locale, screen
}

// Compact renders the capsule in one line for the orchestrator, skipping
// empties: "web · tz America/Los_Angeles · en-US". "" when nothing is set.
func (c *Caps) Compact() string {
	if c == nil {
		return ""
	}
	var parts []string
	if c.Source != "" {
		parts = append(parts, c.Source)
	}
	if c.Timezone != "" {
		parts = append(parts, "tz "+c.Timezone)
	}
	if c.Locale != "" {
		parts = append(parts, c.Locale)
	}
	if c.Device != "" && c.Device != "unknown" {
		parts = append(parts, c.Device)
	}
	if c.Screen != "" {
		parts = append(parts, c.Screen)
	}
	if c.App != "" {
		parts = append(parts, c.App)
	}
	return strings.Join(parts, " · ")
}

// Declares reports whether the client claims it can supply token.
func (c *Caps) Declares(token string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Supplies {
		if s == token {
			return true
		}
	}
	return false
}
