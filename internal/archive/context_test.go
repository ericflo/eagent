package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
)

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
