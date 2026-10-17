//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The macOS backend drives sandbox-exec with an SBPL profile.
//
// What this boundary can and cannot do is worth stating up front, because it
// is narrower than the Windows one:
//
//   - Writes are restricted. Only the workspace, the caches the toolchain
//     needs, and the user's own 0700 temp directory are writable.
//   - Network egress is denied by default.
//   - Reads are NOT restricted in general. sandbox-exec can deny reads, but
//     allowing the whole filesystem and then denying pieces is the only
//     arrangement that keeps ordinary tools working, so the profile denies a
//     short list of credential stores instead and says so in its notices.
//
// sandbox-exec is marked deprecated by Apple and has been for years; it is
// still present and functional, and it remains the only sandboxing interface
// available to an unprivileged process on this platform.

const sandboxExecPath = "/usr/bin/sandbox-exec"

func newPlatformRunner(cfg Config) (Runner, []string, error) {
	if _, err := os.Stat(sandboxExecPath); err != nil {
		return nil, nil, fmt.Errorf("sandbox: %s is not available: %w", sandboxExecPath, err)
	}
	r := &posixRunner{
		name:    "sandbox-exec",
		helper:  sandboxExecPath,
		network: cfg.Network,
		extra:   append([]string(nil), cfg.WritablePaths...),
	}
	r.wrap = wrapSandboxExec

	if err := probeSandboxExec(r); err != nil {
		return nil, nil, fmt.Errorf("sandbox: sandbox-exec self-test failed: %w", err)
	}

	notices := []string{
		"confining with sandbox-exec: writes are limited to the workspace and network access is blocked",
		"sandbox-exec cannot restrict reads generally; credential stores are denied individually",
	}
	return r, notices, nil
}

// wrapSandboxExec builds the sandbox-exec invocation.
func wrapSandboxExec(r *posixRunner, dir string, argv []string, spec Spec) ([]string, []string, error) {
	net := spec.Network
	if net == "" {
		net = r.network
	}
	writable := writableDirs(dir, spec, r)
	// macOS gives every user a private 0700 directory in $TMPDIR. Tools that
	// ask the system for a scratch path get that one regardless of the
	// environment, so it has to be writable or mktemp fails at random.
	if tmp := os.TempDir(); tmp != "" {
		writable = append(writable, tmp)
	}
	profile := sandboxProfile(writable, net)
	out := []string{r.helper, "-p", profile}
	out = append(out, argv...)
	// TMPDIR is left alone here: it already points at the per-user directory
	// the profile just made writable.
	return out, os.Environ(), nil
}

// sandboxProfile renders the SBPL text.
//
// Rule order matters: SBPL applies the last matching rule, so the read-denies
// for credential stores must come after the blanket read-allow. Writing them
// first is a common way to end up with a profile that looks strict and is not.
func sandboxProfile(writable []string, net NetworkPolicy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	// Process control, signals and these IPC mechanisms are prerequisites for
	// almost any binary to start at all; denying them produces failures that
	// look like the sandbox is broken rather than restrictive.
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow signal)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm)\n")
	b.WriteString("(allow file-read*)\n")

	if len(writable) > 0 {
		clauses := make([]string, 0, len(writable)+6)
		for _, w := range writable {
			if w == "" || w == "/" {
				continue
			}
			clauses = append(clauses, "(subpath "+sbplQuote(w)+")")
		}
		// The standard devices are not under any subpath above and writing to
		// them is how a shell prints.
		for _, dev := range []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/fd"} {
			clauses = append(clauses, "(literal "+sbplQuote(dev)+")")
		}
		b.WriteString("(allow file-write*\n  ")
		b.WriteString(strings.Join(clauses, "\n  "))
		b.WriteString(")\n")
	}

	for _, dir := range secretDirs() {
		b.WriteString("(deny file-read* (subpath " + sbplQuote(dir) + "))\n")
	}

	if net == NetworkAllow {
		b.WriteString("(allow network*)\n")
	} else {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// secretDirs lists credential stores that stay unreadable even though reads are
// otherwise allowed. Only existing directories are listed, so the profile does
// not carry lines for things this user never installed.
func secretDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{
		".ssh",
		".aws",
		".gnupg",
		".kube",
		".config/gh",
		".icode",
		"Library/Keychains",
	}
	var out []string
	for _, c := range candidates {
		p := filepath.Join(home, c)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// sbplQuote renders a path as an SBPL string literal.
func sbplQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// probeSandboxExec runs a harmless command through the real profile so a
// syntax error or a refused operation shows up here rather than on the user's
// first build.
func probeSandboxExec(r *posixRunner) error {
	dir, err := os.MkdirTemp("", "icode-sandbox-probe-")
	if err != nil {
		dir = os.TempDir()
	} else {
		defer os.RemoveAll(dir)
	}
	full, _, err := wrapSandboxExec(r, dir, []string{"/bin/sh", "-c", "exit 0"}, Spec{})
	if err != nil {
		return err
	}
	return runProbe(full[0], full[1:])
}

// revokePlatform reports that there is nothing to revoke: this backend never
// changes filesystem metadata.
func revokePlatform(dir string) (bool, error) {
	return false, nil
}
