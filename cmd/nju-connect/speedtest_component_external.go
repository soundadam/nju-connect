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
	for _, candidate := range []string{
		"/opt/homebrew/opt/librespeed-cli-soundconnect/libexec/librespeed-cli",
		"/usr/local/opt/librespeed-cli-soundconnect/libexec/librespeed-cli",
		"/home/linuxbrew/.linuxbrew/opt/librespeed-cli-soundconnect/libexec/librespeed-cli",
	} {
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}
