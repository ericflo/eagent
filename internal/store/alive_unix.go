//go:build linux || darwin || freebsd || openbsd || netbsd

package store

import (
	"os"
	"path/filepath"
	"syscall"
)

// Alive reports whether some process holds the session's lock, that is,
// whether the session is running somewhere on this machine.
func Alive(sessionPath string) bool {
	f, err := os.OpenFile(filepath.Join(sessionPath, ".lock"), os.O_RDWR, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	// A shared lock answers the question without contending with another
	// probe or with a runner trying to take the exclusive lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
