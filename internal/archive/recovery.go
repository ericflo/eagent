package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericflo/eagent/internal/protocol/artifact"
)

// Verify checks every file and chunk without executing any archive content.
func Verify(ctx context.Context, directory string) (artifact.Manifest, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return artifact.Manifest{}, err
	}
	defer root.Close()
	f, err := root.Open("manifest.json")
	if err != nil {
		return artifact.Manifest{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, artifact.MaxManifestBytes+1))
	f.Close()
	if err != nil || len(raw) > artifact.MaxManifestBytes {
		return artifact.Manifest{}, fmt.Errorf("invalid or oversized archive manifest")
	}
	var manifest artifact.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if err := manifest.Validate(); err != nil {
		return manifest, err
	}
	for _, file := range manifest.Files {
		if err := copyVerified(ctx, root, file, io.Discard); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func copyVerified(ctx context.Context, root *os.Root, record artifact.File, dst io.Writer) error {
	parts := strings.Split(record.Path, "/")
	for n := range parts {
		info, err := root.Lstat(strings.Join(parts[:n+1], "/"))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (n == len(parts)-1 && !info.Mode().IsRegular()) {
			return fmt.Errorf("archive path is not a regular file: %s", record.Path)
		}
	}
	f, err := root.Open(record.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != record.Size {
		return fmt.Errorf("archive file size changed: %s", record.Path)
	}
	hash := sha256.New()
	for _, chunk := range record.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw := make([]byte, int(chunk.Size))
		if _, err := io.ReadFull(f, raw); err != nil {
			return err
		}
		if artifact.Digest(raw) != chunk.SHA256 {
			return fmt.Errorf("corrupt archive chunk in %s", record.Path)
		}
		hash.Write(raw)
		if _, err := dst.Write(raw); err != nil {
			return err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
		return fmt.Errorf("corrupt archive file: %s", record.Path)
	}
	if n, err := f.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		return fmt.Errorf("archive file changed during verification: %s", record.Path)
	}
	return nil
}

// Recover preserves native source files in a new directory. It deliberately
// does not create process state, install configuration, or execute tool events.
func Recover(ctx context.Context, directory, destination string) (int, error) {
	manifest, err := Verify(ctx, directory)
	if err != nil {
		return 0, err
	}
	if manifest.Dataset["format"] != "eagent.session-jsonl/v1" {
		return 0, fmt.Errorf("this archive does not contain an eagent native session")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return 0, err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(destination)
		}
	}()
	root, err := os.OpenRoot(directory)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var recovered []artifact.File
	for _, record := range manifest.Files {
		if record.Role != "source" {
			continue
		}
		path := filepath.Join(destination, filepath.FromSlash(record.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return 0, err
		}
		err = copyVerified(ctx, root, record, f)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, closeErr
		}
		recovered = append(recovered, record)
	}
	raw, err := json.MarshalIndent(map[string]any{"format": "finalechat.source-recovery/v1", "dataset": manifest.Dataset, "files": recovered, "runtime_restored": false}, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(destination, "recovery.json"), append(raw, '\n'), 0o600); err != nil {
		return 0, err
	}
	ok = true
	return len(recovered), nil
}
