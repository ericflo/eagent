package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Artifact registration auto-creates an untitled server thread that later
// posts never backfill, so automatic publication must wait for the phone
// mirror's first Post (recorded as a phone.thread event) before registering.
func TestPublisherDefersRegistrationUntilMirrorPosts(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	if _, err := Publish(context.Background(), project, session.ID, "test", false); !errors.Is(err, ErrWaitingForMirror) {
		t.Fatalf("unmirrored Publish registered: %v", err)
	}
	f.mu.Lock()
	registrations, patches := f.registrations, f.patches
	f.mu.Unlock()
	if registrations != 0 {
		t.Fatalf("unmirrored Publish registered %d artifacts", registrations)
	}
	if patches != 0 {
		t.Fatalf("unmirrored Publish patched %d threads", patches)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir(project), session.ID, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "waiting-for-mirror" {
		t.Fatalf("status.json state = %q", status.State)
	}
	if _, err := session.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "fixture-thread", ExternalID: "eagent:" + session.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), project, session.ID, "test", false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registrations != 1 {
		t.Fatalf("mirrored Publish registered %d artifacts", f.registrations)
	}
	if f.patches != 1 {
		t.Fatalf("registration backfilled %d thread titles", f.patches)
	}
	if f.patchTitle != filepath.Base(project) || f.patchAgent != "eagent" {
		t.Fatalf("title backfill = %q/%q", f.patchTitle, f.patchAgent)
	}
}

// Explicit forced publication still registers without a mirror (and still
// backfills the title), so one-shot CLI use is never blocked.
func TestPublisherForcedPublishBackfillsTitleWithoutMirror(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registrations != 1 || f.patches != 1 {
		t.Fatalf("forced Publish registered=%d patched=%d", f.registrations, f.patches)
	}
	if f.patchTitle != filepath.Base(project) || f.patchAgent != "eagent" {
		t.Fatalf("title backfill = %q/%q", f.patchTitle, f.patchAgent)
	}
}
