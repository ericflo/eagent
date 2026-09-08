//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package store

// Alive cannot probe the lock without flock; report not running.
func Alive(string) bool { return false }
