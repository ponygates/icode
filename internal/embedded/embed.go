// Package embedded holds the compiled desktop frontend embedded in the Go binary.
// Both the CLI server mode and the desktop_launcher use this to serve the UI
// without needing disk files.
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
