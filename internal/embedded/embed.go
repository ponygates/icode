//go:build !noembedded

// Package embedded holds the compiled desktop frontend embedded in the Go binary.
// Both the CLI server mode and the desktop_launcher use this to serve the UI
// without needing disk files.
//
// Build with -tags noembedded to compile WITHOUT the frontend (CLI-only
// builds, or when `vite build` failed and we refuse to bake a stale UI into
// the binary). embed_stub.go then replaces this file and Frontend() returns
// nil; both call sites (cmd/commands.go, cmd/desktop_common.go) already treat
// nil as "no embedded UI".
package embedded

import (
	"embed"
	"io/fs"
)

//go:embed dist
var frontend embed.FS

// Frontend returns the desktop dist/ as an fs.FS for serving.
func Frontend() fs.FS {
	sub, err := fs.Sub(frontend, "dist")
	if err != nil {
		return nil
	}
	return sub
}
