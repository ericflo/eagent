package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/protocol/artifact"
)

var ErrPublicationDeleted = errors.New("the remote artifact was deleted; automatic publication is paused; explicitly publish this session with --recreate to create a new record")
var ErrPublicationConflict = errors.New("artifact source history conflict")
var ErrArtifactsUnsupported = errors.New("this FinaleChat deployment does not advertise artifacts.v1; ordinary chat mirroring remains available")

// ErrWaitingForMirror pauses artifact registration while the session's phone
// mirror has not completed its first Post: registering first would make the
// server auto-create an untitled thread that later posts never backfill.
var ErrWaitingForMirror = errors.New("phone mirror has not posted yet; artifact registration is deferred until the thread exists")

func publicationHead(ctx context.Context, client *finalechat.Client, id string) (finalechat.ArtifactHead, error) {
	head, err := client.ArtifactHead(ctx, id)
	var apiError *finalechat.Error
	if errors.As(err, &apiError) && apiError.Status == 404 {
		return head, ErrPublicationDeleted
	}
	return head, err
}

func sameRevision(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func publicationStage(parent, directory string) error {
	root, err := filepath.Abs(parent)
	if err != nil {
		return err
	}
	stage, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if directory == "" || filepath.Dir(stage) != root {
		return fmt.Errorf("publication journal points outside its private staging directory")
	}
	info, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("publication staging directory is not a regular directory")
	}
	return nil
}

// requireSourceExtension proves that every previously committed native source
// is a byte prefix of the new capture. It never downloads/replays remote tools.
func requireSourceExtension(ctx context.Context, directory string, next, previous artifact.Manifest) error {
	if err := previous.Validate(); err != nil {
		return fmt.Errorf("%w: invalid previous manifest: %v", ErrPublicationConflict, err)
	}
	for _, key := range []string{"format", "schema", "session_id"} {
		if !reflect.DeepEqual(next.Dataset[key], previous.Dataset[key]) {
			return fmt.Errorf("%w: the artifact belongs to a different native dataset", ErrPublicationConflict)
		}
	}
	files := map[string]artifact.File{}
	for _, file := range next.Files {
		if file.Role == "source" {
			files[file.Path] = file
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, old := range previous.Files {
		if old.Role != "source" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		file, ok := files[old.Path]
		if !ok || file.Size < old.Size {
			return fmt.Errorf("%w: the local capture is missing published records in %s", ErrPublicationConflict, old.Path)
		}
		f, err := root.Open(filepath.FromSlash(file.Path))
		if err != nil {
			return err
		}
		digest := sha256.New()
		remaining := old.Size
		buffer := make([]byte, artifact.MaxBlobBytes)
		for remaining > 0 {
			if err = ctx.Err(); err != nil {
				break
			}
			var n int
			n, err = io.ReadFull(f, buffer[:min(int64(len(buffer)), remaining)])
			if err != nil {
				break
			}
			_, _ = digest.Write(buffer[:n])
			remaining -= int64(n)
		}
		f.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(digest.Sum(nil)) != old.SHA256 {
			return fmt.Errorf("%w: local and published records diverged in %s; the published archive was left unchanged", ErrPublicationConflict, old.Path)
		}
	}
	return nil
}
