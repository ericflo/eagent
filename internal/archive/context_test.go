package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

func TestArchiveCapturesRuntimeContextWithoutControlState(t *testing.T) {
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	values := runtimecontrol.FromConfig(config.Defaults())
	values.TaskConcurrency, values.Revision = 7, 3
	c, err := runtimecontrol.New(project, session.ID, values, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousHash := ""
	for _, available := range []bool{true, true, false} {
		if !available {
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		}
		exported, err := Snapshot(context.Background(), project, session.ID, "test")
		if err != nil {
			t.Fatal(err)
		}
		defer exported.Close()
		raw, err := os.ReadFile(filepath.Join(exported.Dir, "context/runtime-settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if available {
			hash := artifact.Digest(raw)
			if previousHash != "" && previousHash != hash {
				t.Fatal("unchanged runtime status introduced a capture-time-only revision")
			}
			previousHash = hash
		}
		var captured struct {
			View settings.RemoteView `json:"view"`
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatal(err)
		}
		if captured.View.Generation != c.Generation || captured.View.Snapshot.RuntimeKnown != available || captured.View.Snapshot.Saved["/task_concurrency"] != float64(7) {
			t.Fatalf("lost runtime context: %+v", captured.View)
		}
		if exported.Manifest.Dataset["runtime_settings_version"] != captured.View.Snapshot.Version {
			t.Fatal("missing runtime version")
		}
		for _, file := range exported.Manifest.Files {
			if strings.Contains(file.Path, "runtime-control/") || strings.Contains(file.Path, "runtime-connectors/") {
				t.Fatal("private control state was archived")
			}
		}
	}
}

func TestCaptureAuditPreservesCompletePrefixAndRejectsUnboundedOrEscapingFiles(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, ".agents/eagent/settings-audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\"command\":\"one\"}\n\n{\"torn\":")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	exported := &Export{Dir: t.TempDir()}
	if err := exported.captureAudit(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(exported.Dir, "context/settings-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw[:bytes.LastIndexByte(raw, '\n')+1]) {
		t.Fatal("audit bytes or committed boundary changed")
	}
	if err := os.Truncate(path, artifact.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := (&Export{Dir: t.TempDir()}).captureAudit(context.Background(), project); err == nil {
		t.Fatal("oversize audit was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "private.jsonl"), path); err != nil {
		t.Fatal(err)
	}
	if err := (&Export{Dir: t.TempDir()}).captureAudit(context.Background(), project); err == nil {
		t.Fatal("symlink audit was accepted")
	}
}

func TestAttachmentReferencesExplicitlyReportMissingAndChangedFiles(t *testing.T) {
	sessionPath := t.TempDir()
	exported := &Export{Dir: t.TempDir(), Manifest: artifact.Manifest{Dataset: map[string]any{}, Files: []artifact.File{{Path: "attachments/present.png", Role: "asset", Size: 12}, {Path: "attachments/changed.png", Role: "asset", Size: 15}}}}
	ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Attachments: []event.Attachment{
		{Path: filepath.Join(sessionPath, "attachments/present.png"), Size: 12},
		{Path: filepath.Join(sessionPath, "attachments/missing.png"), Size: 12},
		{Path: filepath.Join(sessionPath, "attachments/changed.png"), Size: 12},
		{Path: filepath.Join(t.TempDir(), "outside.png"), Size: 12},
	}})
	ev.Seq = 10
	if err := exported.captureReferences(sessionPath, []event.Event{ev}); err != nil {
		t.Fatal(err)
	}
	if exported.Manifest.Dataset["capture_complete"] != false || exported.Manifest.Dataset["unavailable_attachment_references"] != 3 {
		t.Fatal("incomplete capture presented as complete")
	}
	raw, _ := os.ReadFile(filepath.Join(exported.Dir, "context/attachments.json"))
	var refs []attachmentReference
	if err := json.Unmarshal(raw, &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 4 {
		t.Fatal("lost attachment references")
	}
	for _, ref := range refs {
		if ref.Sequence != 10 {
			t.Fatal("lost source anchor")
		}
		if ref.ArchivePath == "attachments/changed.png" && ref.Status != "recorded_size_mismatch" {
			t.Fatal("lost size mismatch")
		}
	}
}
