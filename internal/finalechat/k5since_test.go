package finalechat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A FinaleChat that does not know after_time ignores it and answers with
// its newest page, newest first. MessagesSince promises the messages
// written after since, oldest first, and keeps to it: what was written
// before since is left out, and the rest is put in order.
func TestK5MessagesSinceFromAServiceIgnoringAfterTime(t *testing.T) {
	since := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	newest := []Message{
		{ID: "m5", CreatedAt: since.Add(2 * time.Minute)},
		{ID: "m4", CreatedAt: since.Add(time.Minute)},
		{ID: "m3", CreatedAt: since},
		{ID: "m2", CreatedAt: since.Add(-2 * time.Minute)},
		{ID: "m1", CreatedAt: since.Add(-3 * time.Minute)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": newest}) // after_time ignored
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "fc_test"}
	msgs, err := c.MessagesSince(context.Background(), "ext:eagent:1", since, "user", 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if len(ids) != 2 || ids[0] != "m4" || ids[1] != "m5" {
		t.Fatalf("MessagesSince(%v) gave %v; want m4 then m5 (after since, oldest first)", since, ids)
	}
	// From the thread's start: all of the page, oldest first.
	if msgs, err = c.MessagesSince(context.Background(), "ext:eagent:1", time.Time{}, "user", 100); err != nil || len(msgs) != 5 || msgs[0].ID != "m1" || msgs[4].ID != "m5" {
		t.Fatalf("from the start: %v, %v", msgs, err)
	}
}
