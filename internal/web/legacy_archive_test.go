package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A saved old session says how it stopped, and the saved viewer shows it on
// the overview (which its Overview button opens again), shows the old
// session's task and process rows, and marks where the current eagent took
// an older session over.
func TestSavedOldSessionShowsHowItStopped(t *testing.T) {
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.LegacyOrchestrator: "a", event.LegacyTask: "b", event.LegacyNarrator: "c"}}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the parser"}),
		event.New(event.LegacyTaskCreate, event.LegacyOrchestrator, map[string]any{"id": "t1", "title": "Fix it", "kind": "work"}),
		event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: true, Reason: "fixed the parser"}),
	} {
		if _, err := session.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	exported, err := archive.Snapshot(context.Background(), project, session.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Close()
	raw, err := os.ReadFile(filepath.Join(exported.Dir, "derived", "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d["done"] != true || d["last_reason"] != "fixed the parser" {
		t.Fatalf("saved summary: done=%v last_reason=%v", d["done"], d["last_reason"])
	}
	if _, ok := d["schedules"].([]any); !ok {
		t.Fatalf("saved summary has no schedules list: %s", raw)
	}
	page, err := os.ReadFile(filepath.Join(exported.Dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<button data-view="overview">Overview</button>`,
		`detail.last_reason`,
		`took this older session over`,
		`showLegacyActivity: ARCHIVE ||`,
		`case 'task.end': return act(`,
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("saved viewer lacks %q", want)
		}
	}
}
