package web

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var embeddedAssets embed.FS

func assetsFS() fs.FS {
	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		// If embedding is broken, return an empty fs; callers will 404.
		return emptyFS{}
	}
	return sub
}

// emptyFS is a minimal fs.FS implementation that always returns fs.ErrNotExist.
// Used as a safe fallback for embedded assets.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
