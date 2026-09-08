package archive

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
)

// Capture the complete audit prefix with bounded memory and confined reads.
// The caller holds the cooperating settings writers' lock.
func (e *Export) captureAudit(ctx context.Context, project string) error {
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer root.Close()
	const path = ".agents/eagent/settings-audit.jsonl"
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > artifact.MaxFileBytes {
		return fmt.Errorf("settings audit must be a bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return e.add("context/settings-audit.jsonl", "source", "application/x-ndjson", func(w io.Writer) error {
		scan := bufio.NewScanner(io.LimitReader(f, info.Size()))
		scan.Buffer(make([]byte, 65536), 16<<20)
		scan.Split(committedLines)
		for scan.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := w.Write(scan.Bytes()); err != nil {
				return err
			}
		}
		return scan.Err()
	})
}

type attachmentReference struct {
	Path        string `json:"original_path"`
	ArchivePath string `json:"archive_path,omitempty"`
	Sequence    int64  `json:"first_event"`
	Status      string `json:"status"`
}

// Native attachment paths identify files the session claims to carry. Missing
// references are explicit; an export never guesses a file outside the session.
func (e *Export) captureReferences(sessionPath string, events []event.Event) error {
	files := map[string]artifact.File{}
	for _, file := range e.Manifest.Files {
		files[file.Path] = file
	}
	seen := map[string]bool{}
	refs := []attachmentReference{}
	missing := 0
	for _, ev := range events {
		var payload struct {
			Attachments []event.Attachment `json:"attachments"`
			Images      []event.Attachment `json:"images"`
		}
		if json.Unmarshal(ev.Data, &payload) != nil {
			continue
		}
		for _, att := range append(payload.Attachments, payload.Images...) {
			if seen[att.Path] {
				continue
			}
			seen[att.Path] = true
			ref := attachmentReference{Path: att.Path, Sequence: ev.Seq, Status: "unavailable"}
			rel := filepath.Clean(att.Path)
			if filepath.IsAbs(rel) {
				var err error
				rel, err = filepath.Rel(sessionPath, rel)
				if err != nil {
					rel = ""
				}
			}
			rel = filepath.ToSlash(rel)
			if artifact.ValidPath(rel) && (strings.HasPrefix(rel, "attachments/") || strings.HasPrefix(rel, "outputs/")) {
				ref.ArchivePath = rel
				if file, ok := files[rel]; ok {
					ref.Status = "included"
					if att.Size > 0 && file.Size != att.Size {
						ref.Status = "recorded_size_mismatch"
					}
				}
			}
			if ref.Status != "included" {
				missing++
			}
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	e.Manifest.Dataset["capture_complete"] = missing == 0
	e.Manifest.Dataset["unavailable_attachment_references"] = missing
	e.Manifest.Dataset["completeness_scope"] = "Complete committed JSONL prefix and declared session attachments at the captured file lengths; project files, running processes, credentials and unrecorded tool effects are excluded."
	return e.addJSON("context/attachments.json", "context", refs)
}
