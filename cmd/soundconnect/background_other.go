//go:build !linux && !darwin

package main

import (
	"errors"
	"os/exec"
)

func configureBackgroundProcess(*exec.Cmd) error {
	return errors.New("background native runtime is not supported on this platform")
}
