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
	"github.com/ericflo/eagent/internal/runtimecontrol"
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
	Endpoint    string            `json:"endpoint,omitempty"`
	Deleted     bool              `json:"remote_deleted,omitempty"`
	Recapture   bool              `json:"needs_recapture,omitempty"`
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
	return publish(ctx, project, session, version, force, false)
}

// RecreatePublication is only called for one explicitly named deleted archive.
// Ordinary forced publication is never permission to bypass deletion/history.
func RecreatePublication(ctx context.Context, project, session, version string) (string, error) {
	return publish(ctx, project, session, version, true, true)
}

func publish(ctx context.Context, project, session, version string, force, recreate bool) (_ string, resultErr error) {
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
	defer func() {
		status := "published"
		message := "Archive publication is current."
		if resultErr != nil {
			status, message = "pending", "Publication did not complete; its captured intent is retained for retry."
			if errors.Is(resultErr, ErrPublicationDeleted) {
				status, message = "remote_deleted", ErrPublicationDeleted.Error()
			}
			if errors.Is(resultErr, ErrPublicationConflict) {
				status, message = "conflicted", "Local source history must preserve both pending and published native records before publication can continue."
			}
		}
		_ = writeJSONAtomic(filepath.Join(directory, "status.json"), map[string]any{"state": status, "message": message, "at": time.Now().UTC()})
	}()
	path := filepath.Join(directory, "publication.json")
	var pending publication
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return "", fmt.Errorf("invalid publisher journal: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	endpoint := strings.TrimRight(client.BaseURL, "/")
	if pending.Endpoint != "" && pending.Endpoint != endpoint {
		return "", fmt.Errorf("publisher journal belongs to a different FinaleChat service; export the session and publish it as a separate artifact")
	}
	pending.Endpoint = endpoint
	getHead := func() (finalechat.ArtifactHead, error) {
		head, err := publicationHead(ctx, client, pending.ArtifactID)
		if errors.Is(err, ErrPublicationDeleted) {
			pending.Deleted = true
			if saveErr := writeJSONAtomic(path, pending); saveErr != nil {
				return head, saveErr
			}
		}
		return head, err
	}
	if recreate {
		if pending.ArtifactID == "" {
			return "", fmt.Errorf("this session has no deleted publication to recreate")
		}
		if _, err := getHead(); !errors.Is(err, ErrPublicationDeleted) {
			if err != nil {
				return "", err
			}
			return "", fmt.Errorf("the artifact still exists; recreation cannot bypass a history conflict")
		}
		if err := writeJSONAtomic(filepath.Join(directory, "retired-"+artifact.Digest([]byte(pending.ArtifactID))+".json"), pending); err != nil {
			return "", err
		}
		pending = publication{Endpoint: endpoint}
		if err := writeJSONAtomic(path, pending); err != nil {
			return "", err
		}
	}
	if pending.Deleted {
		return "", ErrPublicationDeleted
	}
	var capabilities finalechat.Me
	if err := client.Request(ctx, "GET", "/api/v1/me", nil, nil, &capabilities, 0); err != nil {
		return "", err
	}
	if !capabilities.Has("artifacts.v1") {
		return "", ErrArtifactsUnsupported
	}
	complete := func() error {
		if err := publicationStage(directory, pending.Directory); err != nil {
			return err
		}
		root, err := os.OpenRoot(pending.Directory)
		if err != nil {
			return err
		}
		defer root.Close()
		revision, err := client.CommitArtifact(ctx, pending.ArtifactID, pending.RevisionID, pending.ClientKey, pending.Manifest, func(name string) (io.ReadCloser, error) { return root.Open(filepath.FromSlash(name)) })
		if err != nil {
			var apiError *finalechat.Error
			if errors.As(err, &apiError) && (apiError.Status == 404 || apiError.Status == 409) {
				head, headErr := getHead()
				if headErr != nil {
					return headErr
				}
				if apiError.Status == 409 && !sameRevision(head.Artifact.CurrentRevisionID, pending.RevisionID) {
					return fmt.Errorf("%w: another publisher advanced the artifact; a fresh capture is required", ErrPublicationConflict)
				}
			}
			return err
		}
		if revision.ID == "" {
			return fmt.Errorf("artifact commit returned no revision identity; retained pending capture for retry")
		}
		oldDirectory := pending.Directory
		pending.RevisionID = &revision.ID
		pending.Completed = true
		pending.Recapture = false
		pending.Directory = ""
		if err := writeJSONAtomic(path, pending); err != nil {
			return err
		}
		_ = os.RemoveAll(oldDirectory)
		return nil
	}
	if pending.ArtifactID != "" && !pending.Completed && pending.Directory != "" && !pending.Recapture {
		if err := complete(); err != nil {
			if !errors.Is(err, ErrPublicationConflict) {
				return "", err
			}
			pending.Recapture = true
			if err := writeJSONAtomic(path, pending); err != nil {
				return "", err
			}
		}
	}
	var head finalechat.ArtifactHead
	if pending.ArtifactID != "" {
		head, err = getHead()
		if err != nil {
			return "", err
		}
	}
	signature, err := sourceSignature(project, info, version)
	if err != nil {
		return "", err
	}
	if pending.Completed && sameRevision(head.Artifact.CurrentRevisionID, pending.RevisionID) && pending.Signature == signature {
		return pending.ArtifactID, nil
	}
	snapshot, err := archive.SnapshotIn(ctx, project, info.ID, version, directory)
	if err != nil {
		return "", err
	}
	keepSnapshot := false
	defer func() {
		if !keepSnapshot {
			snapshot.Close()
		}
	}()
	if pending.Recapture {
		if err := publicationStage(directory, pending.Directory); err != nil {
			return "", err
		}
		if err := requireSourceExtension(ctx, snapshot.Dir, snapshot.Manifest, pending.Manifest); err != nil {
			return "", err
		}
	}
	if pending.ArtifactID == "" {
		a, err := client.Artifact(ctx, "ext:eagent:"+info.ID, "session", "Session explorer")
		if err != nil {
			return "", err
		}
		if a.ID == "" {
			return "", fmt.Errorf("artifact registration returned no identity")
		}
		pending.ArtifactID = a.ID
		// Persist identity before upload or source comparison. Once registered,
		// deletion can never make a retry silently create a replacement record.
		if err := writeJSONAtomic(path, pending); err != nil {
			return "", err
		}
		head, err = getHead()
		if err != nil {
			return "", err
		}
	}
	if head.Revision != nil {
		if err := requireSourceExtension(ctx, snapshot.Dir, snapshot.Manifest, head.Revision.Manifest); err != nil {
			return "", err
		}
	}
	nextFingerprint := fingerprint(snapshot.Manifest)
	if pending.Completed && sameRevision(head.Artifact.CurrentRevisionID, pending.RevisionID) && pending.Fingerprint == nextFingerprint {
		pending.Signature = signature
		if err := writeJSONAtomic(path, pending); err != nil {
			return "", err
		}
		return pending.ArtifactID, nil
	}
	// A distinct publisher may have advanced current. Only a newly captured
	// snapshot may adopt that parent; retries keep their recorded parent.
	oldDirectory := pending.Directory
	pending = publication{ArtifactID: head.Artifact.ID, RevisionID: head.Artifact.CurrentRevisionID, ClientKey: artifact.Digest([]byte(snapshot.Dir + snapshot.Manifest.CapturedAt.String())), Directory: snapshot.Dir, Manifest: snapshot.Manifest, Fingerprint: nextFingerprint, Signature: signature, Endpoint: endpoint}
	if err := writeJSONAtomic(path, pending); err != nil {
		return "", err
	}
	keepSnapshot = true
	if oldDirectory != "" && oldDirectory != snapshot.Dir && publicationStage(directory, oldDirectory) == nil {
		_ = os.RemoveAll(oldDirectory)
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
					if errors.Is(err, ErrPublicationDeleted) {
						logf("artifact %s: %v", info.ID, err)
					} else {
						logf("artifact %s: %v (will retry)", info.ID, err)
					}
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
	if status, err := runtimecontrol.ReadStatus(project, info.ID); err == nil {
		files["runtime_settings"] = []any{status.Generation, status.Values, status.AvailableUntil.After(time.Now())}
	}
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
	editorData, err := service.EditorData()
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{"files": files, "settings": view.Snapshot.Version, "producer": version, "viewer": archive.ViewerFingerprint(), "editor": editorData})
	return artifact.Digest(raw), nil
}
