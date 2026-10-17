//go:build !windows

package sandbox

import "errors"

// newPlatformRunner is the placeholder for the Linux (bubblewrap / Landlock) and
// macOS (sandbox-exec) backends. Returning (nil, nil, nil) makes Init treat the
// platform as unsupported, which is the honest answer until those backends are
// implemented and verified on their own machines.
func newPlatformRunner(cfg Config) (Runner, []string, error) {
	return nil, nil, nil
}

// RevokeWorkspace is a no-op outside Windows.
func RevokeWorkspace(dir string) error {
	return errors.New("sandbox: revoke is only implemented on Windows")
}
