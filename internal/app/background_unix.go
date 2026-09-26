//go:build linux || darwin

package app

import (
	"os/exec"
	"syscall"
)

func configureBackgroundProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

// inheritedPipe reports whether fd is an open pipe.
func inheritedPipe(fd int) bool {
	var status syscall.Stat_t
	return syscall.Fstat(fd, &status) == nil && status.Mode&syscall.S_IFMT == syscall.S_IFIFO
}
