//go:build linux || darwin || freebsd || openbsd || netbsd

package integration

import (
	"os/exec"
	"syscall"
)

// detachProcess gives the child its own session so a hard exit of this
// process (which kills its own process group) leaves the new session running.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
