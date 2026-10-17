//go:build linux

package sandbox

import (
	"fmt"
	"os"
)

// The Linux backend drives bubblewrap. Landlock is the more precise mechanism
// but it can only restrict, never grant: a command started under it cannot be
// given write access to the workspace unless the process tree is already
// arranged that way. bubblewrap expresses both halves in one place — build a
// mount namespace where / is read-only and the workspace is bound read-write —
// which is what this backend needs.
//
// The dependency is deliberate and visible: bwrap is a small, packaged
// binary (Debian/Ubuntu: bubblewrap, Fedora: bubblewrap, Arch: bubblewrap). If
// it is absent the platform reports itself unsupported rather than silently
// running unconfined.

func newPlatformRunner(cfg Config) (Runner, []string, error) {
	bin, err := resolveHelper("bwrap")
	if err != nil {
		return nil, nil, err
	}
	r := &posixRunner{
		name:    "bwrap",
		helper:  bin,
		network: cfg.Network,
		extra:   append([]string(nil), cfg.WritablePaths...),
	}
	r.wrap = wrapBwrap

	// Test the exact invocation this backend will use, not a simplified one.
	// A probe that passes while real runs fail is worse than no probe: it
	// turns "sandbox unavailable" into a command that dies mid-way.
	if err := probeBwrap(r); err != nil {
		return nil, nil, fmt.Errorf("sandbox: bubblewrap self-test failed: %w", err)
	}

	notices := []string{
		"confining with bubblewrap: the workspace is writable, the rest of the filesystem is read-only",
	}
	if cfg.GrantWorkspace {
		// Say plainly that nothing was changed on disk. Users who saw the
		// Windows build may otherwise assume a DACL edit happened here too.
		notices = append(notices, "no filesystem permissions were changed: write access lives in the mount namespace")
	}
	return r, notices, nil
}

// wrapBwrap builds the bubblewrap invocation.
func wrapBwrap(r *posixRunner, dir string, argv []string, spec Spec) ([]string, []string, error) {
	net := spec.Network
	if net == "" {
		net = r.network
	}
	out := []string{
		r.helper,
		// Take the child down if this process dies, and detach it from the
		// controlling terminal so it cannot inject input back.
		"--die-with-parent",
		"--new-session",
		// Separate namespaces make the confinement real: a process cannot
		// see or signal anything outside, and an attempted escape has no
		// target to reach.
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
	}
	if net != NetworkAllow {
		out = append(out, "--unshare-net")
	}
	out = append(out,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		// A fresh tmpfs hides whatever other processes left in /tmp; TMPDIR
		// below points into it.
		"--tmpfs", "/tmp",
	)
	// Binds come after the tmpfs so a workspace that happens to live under
	// /tmp is restored rather than hidden.
	for _, w := range writableDirs(dir, spec, r) {
		out = append(out, "--bind", w, w)
	}
	out = append(out, "--chdir", dir, "--")
	out = append(out, argv...)
	return out, envWithTmp(os.Environ(), "/tmp"), nil
}

// probeBwrap runs a harmless command through the real wrapper construction.
func probeBwrap(r *posixRunner) error {
	dir, err := os.MkdirTemp("", "icode-sandbox-probe-")
	if err != nil {
		dir = "/"
	} else {
		defer os.RemoveAll(dir)
	}
	full, _, err := wrapBwrap(r, dir, []string{"/bin/sh", "-c", "exit 0"}, Spec{})
	if err != nil {
		return err
	}
	return runProbe(full[0], full[1:])
}

// revokePlatform reports that there is nothing to revoke. The Linux backend
// never writes permission metadata, so no grant record is ever created; a
// stale row can only come from a Windows profile sharing the same home
// directory, and clearing it is the correct answer.
func revokePlatform(dir string) (bool, error) {
	return false, nil
}
