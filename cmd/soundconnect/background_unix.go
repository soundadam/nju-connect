//go:build linux || darwin

package main

import (
	"os/exec"
	"syscall"
)

func configureBackgroundProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
