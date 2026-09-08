package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/filelock"
)

// Editor owns the project configuration lock. Read, validate and compare
// versions while holding it, then save through this object before Close.
type Editor struct {
	project string
	unlock  func()
}

func LockEditor(ctx context.Context, project string) (*Editor, error) {
	absolute, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		absolute = canonical
	}
	unlock, err := filelock.Acquire(ctx, filepath.Join(absolute, ".agents/eagent/settings.lock"))
	if err != nil {
		return nil, err
	}
	return &Editor{project: absolute, unlock: unlock}, nil
}
func (e *Editor) Close() {
	if e.unlock != nil {
		e.unlock()
		e.unlock = nil
	}
}
func (e *Editor) SaveFile(edits map[string]json.RawMessage, ifMatch string, checkMatch bool) (SaveResult, error) {
	return saveFile(e.project, edits, ifMatch, checkMatch)
}
func (e *Editor) SaveRaw(raw, ifMatch string, checkMatch bool) (SaveResult, error) {
	return saveRaw(e.project, raw, ifMatch, checkMatch)
}

// RelatedPath resolves only the project's registered bundle and prompt
// resources. Wire protocols never accept an arbitrary local destination.
func (e *Editor) RelatedPath(kind, name string) (string, error) {
	switch kind {
	case "bundle":
		if validName(name) {
			return BundlePath(e.project, name), nil
		}
	case "prompt":
		if slices.Contains([]string{"ORCHESTRATOR.md", "TASK-WORKER.md", "NARRATOR.md", "PERSONA.md", "COMPACTION-DOSSIER.md"}, name) {
			return filepath.Join(e.project, ".agents/eagent/prompts", name), nil
		}
	}
	return "", fmt.Errorf("unknown settings resource")
}

// WriteRelated shares the same project transaction and atomic replacement as
// config.json. The service validates bundle configuration or prompt templates.
func (e *Editor) WriteRelated(kind, name string, raw []byte, remove bool) (string, error) {
	path, err := e.RelatedPath(kind, name)
	if err != nil {
		return "", err
	}
	if len(raw) > 1<<20 {
		return "", fmt.Errorf("settings resource exceeds 1 MiB")
	}
	rel, err := filepath.Rel(e.project, path)
	if err != nil {
		return "", err
	}
	current := e.project
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("settings symlinks must be managed locally")
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	if remove {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if directory, err := os.Open(filepath.Dir(path)); err == nil {
			err = directory.Sync()
			directory.Close()
			if err != nil {
				return "", err
			}
		}
		return path, nil
	}
	previous, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, writeAtomic(path, raw, previous)
}

func (e *Editor) SaveBundle(name, description string, cfg Config) (string, error) {
	raw, err := BundleBytes(name, description, cfg)
	if err != nil {
		return "", err
	}
	return e.WriteRelated("bundle", name, raw, false)
}
