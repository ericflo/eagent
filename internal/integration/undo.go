package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func journalInverse(j commandJournal) (control.Proposal, error) {
	if j.Resource != nil {
		return j.Resource.Inverse()
	}
	if j.Applied == nil || j.BeforeRaw == nil {
		return control.Proposal{}, fmt.Errorf("this command has no undo metadata")
	}
	return settings.ConfigInverse(*j.Applied, *j.BeforeRaw)
}

func inverseDigest(p control.Proposal) string {
	raw, _ := json.Marshal(p)
	return artifact.Digest(raw)
}

// undoOffer exposes a bounded review without publishing the private journal.
// Prompt/bundle bodies remain local; resource reviews identify the operation,
// name and previous content hash. Config reviews contain only affected fields.
func undoOffer(j commandJournal) map[string]any {
	inverse, err := journalInverse(j)
	if err != nil {
		return nil
	}
	inverseRaw, _ := json.Marshal(inverse)
	if len(inverseRaw) > control.MaxCommandBytes-1024 {
		return nil
	}
	offer := map[string]any{"format": "finalechat.settings-undo/v1", "command_id": j.CommandID, "restore_sha256": inverseDigest(inverse), "operation": inverse.Operation}
	if j.Resource != nil {
		if len(j.Resource.Before) > 32768 {
			return nil // Outside the current typed action's payload limit.
		}
		offer["resource"] = map[string]any{"kind": j.Resource.Kind, "name": j.Resource.Name, "restore_override": j.Resource.BeforeExists, "restore_sha256": artifact.Digest(j.Resource.Before)}
	} else {
		offer["edits"] = inverse.Edits
	}
	// Return the same JSON representation recovered from the durable journal.
	raw, _ := json.Marshal(offer)
	if len(raw) > control.MaxCommandBytes/2 {
		return nil // An optional review must not prevent the save acknowledgement.
	}
	_ = json.Unmarshal(raw, &offer)
	return offer
}

func prepareUndo(s *settings.Service, editor *config.Editor, grant control.Grant, p control.Proposal, journal *commandJournal) error {
	if _, err := s.ValidateAction(p, grant); err != nil {
		return err
	}
	id, _ := p.Parameters["command_id"].(string)
	if !artifact.ValidPath(id) || strings.Contains(id, "/") || len(id) > 80 || id == journal.CommandID {
		return fmt.Errorf("invalid command to undo")
	}
	path := filepath.Join(stateDir(s.Project), "commands", id+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return fmt.Errorf("the original command journal is unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var original commandJournal
	if json.Unmarshal(raw, &original) != nil || original.CommandID != id || original.ResourceKey != grant.Key || original.Status != "succeeded" {
		return fmt.Errorf("only a successful command for this resource can be undone")
	}
	inverse, err := journalInverse(original)
	if err != nil {
		return err
	}
	if p.Parameters["restore_sha256"] != inverseDigest(inverse) {
		return fmt.Errorf("the reviewed undo no longer matches its original command")
	}
	inverse.SchemaVersion, inverse.ExpectedVersion, inverse.Generation = p.SchemaVersion, p.ExpectedVersion, p.Generation
	journal.Applied = &inverse
	journal.Undoes = id
	if original.Resource != nil {
		_, after, err := original.Resource.State(editor)
		if err != nil {
			return err
		}
		if !after {
			return &config.ErrConflict{}
		}
		change, err := s.PrepareResource(editor, inverse, grant)
		if err != nil {
			return err
		}
		// The normal writer validated the reversal, including current policy.
		// Retain the exact former bytes instead of filling an old sparse bundle.
		change.After, change.AfterExists = original.Resource.Before, original.Resource.BeforeExists
		journal.Resource = &change
		return nil
	}
	desired, etag, _, err := s.PrepareUndo(inverse, original.Desired, grant)
	if err != nil {
		return err
	}
	journal.Desired, journal.BeforeETag, journal.DesiredETag = desired, etag, artifact.Digest([]byte(desired))
	before := config.Resolve(s.Project, s.Preset, s.Bundle).File.Raw
	if before == "" {
		before = "{}\n"
	}
	journal.BeforeRaw = &before
	return nil
}
