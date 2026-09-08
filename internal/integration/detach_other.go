//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package integration

import "os/exec"

func detachProcess(*exec.Cmd) {}
