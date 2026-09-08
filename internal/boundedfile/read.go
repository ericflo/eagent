// Package boundedfile reads small local metadata without waiting on pipes or
// allocating according to an untrusted file size. Callers select their limit.
package boundedfile

import (
	"fmt"
	"io"
	"os"
)

func Read(path string, limit int64) ([]byte, error) {
	return read(path, limit, os.Stat, os.OpenFile)
}

// ReadRoot additionally confines symlinks and directory traversal to root.
func ReadRoot(root *os.Root, path string, limit int64) ([]byte, error) {
	return read(path, limit, root.Stat, root.OpenFile)
}

func read(path string, limit int64, stat func(string) (os.FileInfo, error), open func(string, int, os.FileMode) (*os.File, error)) ([]byte, error) {
	before, err := stat(path)
	if err != nil {
		return nil, err
	}
	if limit < 0 || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("%s is not a regular file within the %d byte limit", path, limit)
	}
	f, err := open(path, os.O_RDONLY|nonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || opened.Size() > limit || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("%s changed before reading", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit || after.Size() != opened.Size() || int64(len(raw)) != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, fmt.Errorf("%s changed while reading or exceeds %d bytes", path, limit)
	}
	return raw, nil
}
