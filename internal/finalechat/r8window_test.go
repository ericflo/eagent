package finalechat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The reads for a caller with no message to read on from: by time, and the
// message just before a given one. An answer that is not a message the
// service lists before the one asked about (as from a service that does not
// know the request and answers with its newest message) is not taken for it.
func TestReadsWithNoMessageToReadOnFrom(t *testing.T) {
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var query string
	var answer []Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": answer})
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "fc_test"}
	ctx := context.Background()

	if _, err := c.MessagesSince(ctx, "ext:eagent:1", time.Time{}, "user", 100); err != nil || query != "after_time=1970-01-01T00%3A00%3A00Z&limit=100&sender=user" {
		t.Fatalf("from the thread's start: query %q, err %v", query, err)
	}
	if _, err := c.MessagesSince(ctx, "ext:eagent:1", at.Add(1500*time.Microsecond), "user", 100); err != nil || query != "after_time=2026-11-26T12%3A00%3A00.0015Z&limit=100&sender=user" {
		t.Fatalf("after a time: query %q, err %v", query, err)
	}

	m := Message{ID: "m5", CreatedAt: at}
	for _, tc := range []struct {
		name   string
		answer []Message
		want   string
	}{
		{"the one before", []Message{{ID: "m4", CreatedAt: at.Add(-time.Second)}}, "m4"},
		{"written in the same instant, listed before", []Message{{ID: "m4", CreatedAt: at}}, "m4"},
		{"none before", nil, ""},
		{"the message itself", []Message{{ID: "m5", CreatedAt: at}}, ""},
		{"a later one", []Message{{ID: "m9", CreatedAt: at.Add(time.Second)}}, ""},
		{"written in the same instant, listed after", []Message{{ID: "m9", CreatedAt: at}}, ""},
		{"more than was asked for", []Message{{ID: "m3", CreatedAt: at.Add(-2 * time.Second)}, {ID: "m4", CreatedAt: at.Add(-time.Second)}}, ""},
	} {
		answer = tc.answer
		got, err := c.MessageBefore(ctx, "ext:eagent:1", m, "user")
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q (err %v), want %q", tc.name, got, err, tc.want)
		}
		if query != "before=m5&limit=1&sender=user" {
			t.Fatalf("%s: query %q", tc.name, query)
		}
	}
}
