package web

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend
var embeddedFrontend embed.FS

func frontendFS() fs.FS {
	sub, err := fs.Sub(embeddedFrontend, "frontend")
	if err != nil {
		return emptyFS{}
	}
	return sub
}

// emptyFS is a minimal fs.FS implementation that always returns fs.ErrNotExist.
// Used as a safe fallback when embedded static files are unavailable.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
