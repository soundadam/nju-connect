//go:build !linux && !darwin

package app

import (
	"errors"
	"os/exec"
)

func configureBackgroundProcess(*exec.Cmd) error {
	return errors.New("background native runtime is not supported on this platform")
}

func inheritedPipe(int) bool { return false }
