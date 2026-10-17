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

// revokePlatform reports that revocation is not implemented here. Returning a
// nil error would let the UI claim it removed a grant that never existed.
func revokePlatform(dir string) (bool, error) {
	return false, errors.New("sandbox: revoke is only implemented on Windows")
}

// RevokeWorkspace is the package-level form of revokePlatform.
func RevokeWorkspace(dir string) error {
	_, err := Revoke(dir)
	return err
}
