//go:build !windows && !linux && !darwin

package sandbox

// newPlatformRunner reports that no backend exists here. Returning (nil, nil,
// nil) makes Init treat the platform as unsupported, which is the honest
// answer for a platform this package has no verified implementation for.
func newPlatformRunner(cfg Config) (Runner, []string, error) {
	return nil, nil, nil
}

// revokePlatform reports that there is nothing to revoke. No backend here ever
// writes permission metadata, so no grant record is created in the first place;
// a leftover row can only come from a profile shared with another platform, and
// clearing it is the correct outcome rather than an error.
func revokePlatform(dir string) (bool, error) {
	return false, nil
}
