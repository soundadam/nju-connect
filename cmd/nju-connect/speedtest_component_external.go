package main

import (
	"os"
	"path/filepath"
)

func externalSpeedtestHelperPath() string {
	if configured := os.Getenv("NJU_CONNECT_LIBRESPEED_CLI"); filepath.IsAbs(configured) {
		if info, err := os.Lstat(configured); err == nil && info.Mode().IsRegular() {
			return configured
		}
	}
	for _, prefix := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		candidate := filepath.Join(prefix, "opt", "librespeed-cli-nju-connect", "libexec", "librespeed-cli")
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}
