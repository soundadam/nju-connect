package main

import (
	"os"
	"path/filepath"

	"github.com/soundadam/soundconnect/internal/speedtest"
)

func bundledSpeedtestComponentPath(asset speedtest.ComponentAsset) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	directory := filepath.Dir(executable)
	relativeComponent := filepath.Join("campus-speed", asset.Version, asset.Architecture, speedtest.HelperName)
	candidates := []string{
		filepath.Clean(filepath.Join(directory, "..", "Resources", relativeComponent+".component")),
		filepath.Clean(filepath.Join(directory, "..", "Resources", relativeComponent)),
		filepath.Join(directory, relativeComponent),
		filepath.Clean(filepath.Join(directory, "..", "libexec", relativeComponent)),
	}
	for _, candidate := range candidates {
		info, statErr := os.Lstat(candidate)
		if statErr == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}
