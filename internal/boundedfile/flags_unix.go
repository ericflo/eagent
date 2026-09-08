//go:build unix

package boundedfile

import "syscall"

const nonblock = syscall.O_NONBLOCK
