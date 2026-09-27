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
	// The Formula was called librespeed-cli-soundconnect before 1.1.0; Homebrew
	// renames the keg on upgrade, but an old opt link may still be the only one.
	for _, formula := range []string{"librespeed-cli-nju-connect", "librespeed-cli-soundconnect"} {
		for _, prefix := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
			candidate := filepath.Join(prefix, "opt", formula, "libexec", "librespeed-cli")
			if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
	}
	return ""
}
