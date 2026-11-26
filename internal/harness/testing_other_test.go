//go:build !linux

package harness

// memoryDisk: tests use the usual temporary directory elsewhere.
func memoryDisk() string { return "" }
