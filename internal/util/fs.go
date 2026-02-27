package util

import (
	"errors"
	"os"
	"path/filepath"
)

// EnsureDir makes sure a directory exists (mkdir -p).
func EnsureDir(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	return os.MkdirAll(path, 0o755)
}

// WorkspaceDataDir returns the absolute path to the workspace data directory.
// All user-generated configuration and data must be stored under this directory.
func WorkspaceDataDir() string {
	// Use relative ./data so it stays inside the workspace and is easy to move.
	p, err := filepath.Abs("data")
	if err != nil {
		return "data"
	}
	return p
}
