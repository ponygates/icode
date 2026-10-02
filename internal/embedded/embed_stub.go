//go:build noembedded

// noembedded variant of package embedded: the binary is compiled without the
// desktop frontend (see embed.go for the regular variant). This keeps
// `go build -tags noembedded` working when internal/embedded/dist is absent
// or stale — //go:embed dist would otherwise be an unconditional compile
// error (or silently embed outdated UI).
package embedded

import "io/fs"

// Frontend returns nil — this build carries no embedded frontend. Callers
// (cmd/commands.go, cmd/desktop_common.go) already handle nil as
// "no embedded UI" and fall back accordingly.
func Frontend() fs.FS { return nil }
