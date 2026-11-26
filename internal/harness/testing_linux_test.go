//go:build linux

package harness

import "syscall"

// memoryDisk returns a directory on a memory filesystem with room to spare
// (/dev/shm with a gigabyte free), or "" when there is none. Every event a
// session records is fsynced; on a real disk that is a few milliseconds
// each, which is most of what many tests take, and nothing in the package
// tests the disk.
func memoryDisk() string {
	const tmpfsMagic = 0x01021994
	var st syscall.Statfs_t
	if err := syscall.Statfs("/dev/shm", &st); err != nil || st.Type != tmpfsMagic {
		return ""
	}
	if uint64(st.Bavail)*uint64(st.Bsize) < 1<<30 {
		return ""
	}
	return "/dev/shm"
}
