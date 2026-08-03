package main

import (
	"strings"

	"github.com/soundadam/soundconnect/internal/config"
)

func commandPaths(worktree string) (config.Paths, error) {
	if strings.TrimSpace(worktree) == "" {
		return config.DefaultPaths()
	}
	return config.LocalPaths(worktree)
}
