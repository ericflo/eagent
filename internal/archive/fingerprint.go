package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sync"

	webstatic "github.com/ericflo/eagent/internal/web/static"
)

// ViewerFingerprint changes when any shipped renderer, SDK, or replay runtime
// changes, including development builds whose human version remains "dev".
func ViewerFingerprint() string { return viewerFingerprint() }

var viewerFingerprint = sync.OnceValue(func() string {
	hash := sha256.New()
	for index, tree := range []fs.FS{assets, webstatic.Files} {
		err := fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			raw, err := fs.ReadFile(tree, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(hash, "%d:%d:%s:%d:", index, len(path), path, len(raw))
			_, _ = hash.Write(raw)
			return nil
		})
		if err != nil {
			panic(err)
		} // embedded files cannot disappear at runtime
	}
	return hex.EncodeToString(hash.Sum(nil))
})
