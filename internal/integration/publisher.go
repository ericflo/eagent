// Package integration owns opt-in archive publication and the outbound
// settings connector. It never writes into an active session event log.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/filelock"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

type publication struct {
	ArtifactID  string            `json:"artifact_id"`
	RevisionID  *string           `json:"revision_id"`
	ClientKey   string            `json:"client_key"`
	Directory   string            `json:"directory"`
	Manifest    artifact.Manifest `json:"manifest"`
	Fingerprint string            `json:"fingerprint"`
	Signature   string            `json:"signature"`
	Completed   bool              `json:"completed"`
}

func stateDir(project string) string {
	return filepath.Join(project, ".agents/eagent/finalechat-state")
}

func writeJSONAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func fingerprint(m artifact.Manifest) string {
	// Capture timestamps are useful metadata, but should not create new
	// versions every interval when neither the session nor settings changed.
	files := []artifact.File{}
	for _, f := range m.Files {
		if f.Path != "context/provenance.json" {
			files = append(files, f)
		}
	}
	raw, _ := json.Marshal(map[string]any{"files": files, "dataset": m.Dataset, "producer": m.Producer})
	return artifact.Digest(raw)
}

// Publish resumes pending uploads before taking a fresh snapshot. force is
// for the explicit CLI command; it does not enable future publication.
func Publish(ctx context.Context, project, session, version string, force bool) (string, error) {
	if finalechat.Disabled() {
		return "", finalechat.ErrDisabled
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		return "", err
	}
	if !force && !cfg.Finalechat.Artifacts {
		return "", nil
	}
	client, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
	if !ok {
		return "", fmt.Errorf("artifact publication needs a FinaleChat API token")
	}
	info, err := store.Resolve(store.Root(project), session)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(stateDir(project), info.ID)
	unlock, err := filelock.Acquire(ctx, filepath.Join(directory, "publisher.lock"))
	if err != nil {
		return "", err
	}
	defer unlock()
	path := filepath.Join(directory, "publication.json")
	var pending publication
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return "", fmt.Errorf("invalid publisher journal: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	complete := func() error {
		root, err := os.OpenRoot(pending.Directory)
		if err != nil {
			return err
		}
		defer root.Close()
		revision, err := client.CommitArtifact(ctx, pending.ArtifactID, pending.RevisionID, pending.ClientKey, pending.Manifest, func(name string) (io.ReadCloser, error) { return root.Open(filepath.FromSlash(name)) })
		if err != nil {
			return err
		}
		pending.RevisionID = &revision.ID
		pending.Completed = true
		if err := writeJSONAtomic(path, pending); err != nil {
			return err
		}
		_ = os.RemoveAll(pending.Directory)
		return nil
	}
	if pending.ArtifactID != "" && !pending.Completed {
		if err := complete(); err != nil {
			return "", err
		}
	}
	signature, err := sourceSignature(project, info, version)
	if err != nil {
		return "", err
	}
	if pending.Completed && pending.Signature == signature {
		return pending.ArtifactID, nil
	}
	snapshot, err := archive.SnapshotIn(ctx, project, info.ID, version, directory)
	if err != nil {
		return "", err
	}
	nextFingerprint := fingerprint(snapshot.Manifest)
	if pending.Completed && pending.Fingerprint == nextFingerprint {
		snapshot.Close()
		pending.Signature = signature
		if err := writeJSONAtomic(path, pending); err != nil {
			return "", err
		}
		return pending.ArtifactID, nil
	}
	a, err := client.Artifact(ctx, "ext:eagent:"+info.ID, "session", "Session explorer")
	if err != nil {
		snapshot.Close()
		return "", err
	}
	// A distinct publisher may have advanced current. Only a newly captured
	// snapshot may adopt that parent; retries keep their recorded parent.
	pending = publication{ArtifactID: a.ID, RevisionID: a.CurrentRevisionID, ClientKey: artifact.Digest([]byte(snapshot.Dir + snapshot.Manifest.CapturedAt.String())), Directory: snapshot.Dir, Manifest: snapshot.Manifest, Fingerprint: nextFingerprint, Signature: signature}
	if err := writeJSONAtomic(path, pending); err != nil {
		snapshot.Close()
		return "", err
	}
	if err := complete(); err != nil {
		return "", err
	}
	return pending.ArtifactID, nil
}

func active(path string) bool {
	f, err := os.OpenFile(filepath.Join(path, ".lock"), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// StartPublisher runs independently of chat mirroring. Multiple runtimes and
// the web server can call it; a project lock elects one scanning process.
// Stop waits for cancellation and makes a bounded final attempt after the
// harness has recorded its closing events.
func StartPublisher(ctx context.Context, project, version string, logf func(string, ...any), sessions ...string) func() {
	if finalechat.Disabled() {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if logf == nil {
		logf = func(string, ...any) {}
	}
	sweep := func(ctx context.Context) {
		if finalechat.Disabled() {
			return
		}
		cfg, err := config.Load(project, "")
		if err != nil || !cfg.Finalechat.Artifacts {
			return
		}
		lockCtx, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		unlock, err := filelock.Acquire(lockCtx, filepath.Join(stateDir(project), "scan.lock"))
		stop()
		if err != nil {
			return
		}
		defer unlock()
		infos, err := store.List(store.Root(project))
		if err != nil {
			logf("artifact sessions: %v", err)
			return
		}
		for _, info := range infos {
			_, known := os.Stat(filepath.Join(stateDir(project), info.ID, "publication.json"))
			if !active(info.Path) && known != nil && !slices.Contains(sessions, info.ID) {
				continue
			}
			if _, err := Publish(ctx, project, info.ID, version, false); err != nil {
				if ctx.Err() == nil {
					logf("artifact %s: %v (will retry)", info.ID, err)
				}
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
	go func() {
		defer close(done)
		sweep(ctx)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep(ctx)
			}
		}
	}()
	return func() {
		cancel()
		<-done
		finalCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		sweep(finalCtx)
	}
}

// Export copies a staged archive to a new directory without overwriting an
// existing destination. Original sources and the standalone website travel together.
func Export(ctx context.Context, project, session, version, destination string) error {
	if destination == "" {
		return fmt.Errorf("export destination is required")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	snapshot, err := archive.SnapshotIn(ctx, project, session, version, filepath.Dir(absolute))
	if err != nil {
		return err
	}
	defer snapshot.Close()
	// Reserve a new destination atomically. A directory created by another
	// writer while the snapshot was built must never be silently replaced.
	if err := os.Mkdir(absolute, 0o700); err != nil {
		return err
	}
	if err := os.Rename(snapshot.Dir, absolute); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	return nil
}

func IsArtifactEnabled(project string) bool {
	cfg, err := config.Load(project, "")
	return err == nil && cfg.Finalechat.Artifacts
}

func sourceSignature(project string, info store.Info, version string) (string, error) {
	service := &settings.Service{Project: project}
	view, err := service.RemoteSnapshot(service.Grant())
	if err != nil {
		return "", err
	}
	files := map[string]any{}
	if stat, err := os.Stat(filepath.Join(project, ".agents/eagent/settings-audit.jsonl")); err == nil {
		files["settings_audit"] = []any{stat.Size(), stat.ModTime().UnixNano()}
	}
	err = filepath.WalkDir(info.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(info.Path, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != "." && rel != "outputs" && rel != "attachments" && !strings.HasPrefix(rel, "outputs"+string(filepath.Separator)) && !strings.HasPrefix(rel, "attachments"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(rel) != ".jsonl" && !strings.HasPrefix(rel, "outputs"+string(filepath.Separator)) && !strings.HasPrefix(rel, "attachments"+string(filepath.Separator)) {
			return nil
		}
		stat, err := d.Info()
		if err != nil {
			return err
		}
		files[rel] = []any{stat.Size(), stat.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{"files": files, "settings": view.Snapshot.Version, "producer": version})
	return artifact.Digest(raw), nil
}
