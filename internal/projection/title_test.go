package projection

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// Without any retitle the summary carries no title (the UI falls back to
// first_message) and keeps first_message for backwards compatibility.
func TestSummaryTitleFallback(t *testing.T) {
	st := state.New()
	st.Apply(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "paint the fence"}))
	sum := Summary(Metadata{}, st)
	if sum.Title != "" {
		t.Fatalf("title = %q, want empty until retitled", sum.Title)
	}
	if sum.Description != "" || sum.Summary != "" {
		t.Fatalf("description/summary = %q/%q, want empty", sum.Description, sum.Summary)
	}
	if sum.FirstMessage != "paint the fence" {
		t.Fatalf("first_message = %q", sum.FirstMessage)
	}
}

// The latest thread.title event wins; empty fields leave that side
// unchanged; replay preserves the title with no phone involved.
func TestSummaryTitleOverride(t *testing.T) {
	evs := []event.Event{
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "paint the fence"}),
		event.New(event.ThreadTitle, event.ActorOrchestrator, event.ThreadTitleData{Title: "Paint the fence", Description: "Repainting the fence blue."}),
		event.New(event.ThreadTitle, event.ActorOrchestrator, event.ThreadTitleData{Description: "Repainting the fence blue and covering it."}),
	}
	st := state.Replay(evs)
	sum := Summary(Metadata{}, st)
	if sum.Title != "Paint the fence" {
		t.Fatalf("title = %q", sum.Title)
	}
	if sum.Description != "Repainting the fence blue and covering it." {
		t.Fatalf("description = %q", sum.Description)
	}
	if sum.Summary != sum.Description {
		t.Fatalf("summary = %q, want alias of description %q", sum.Summary, sum.Description)
	}
	if sum.FirstMessage != "paint the fence" {
		t.Fatalf("first_message = %q", sum.FirstMessage)
	}
	if d := Detail(Metadata{}, st); d.Title != sum.Title || d.Description != sum.Description {
		t.Fatalf("detail did not inherit summary title: %+v", d.SessionSummary)
	}
}
