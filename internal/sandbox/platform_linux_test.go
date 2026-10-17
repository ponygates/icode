//go:build linux

package sandbox

import (
	"slices"
	"strings"
	"testing"
)

func testLinuxRunner(t *testing.T) *posixRunner {
	t.Helper()
	r := &posixRunner{
		name:    "bwrap",
		helper:  "/usr/bin/bwrap",
		network: NetworkDeny,
	}
	r.wrap = wrapBwrap
	return r
}

func TestBwrapKeepsTheFilesystemReadOnlyApartFromTheWorkspace(t *testing.T) {
	r := testLinuxRunner(t)
	dir := t.TempDir()
	argv, _, err := wrapBwrap(r, dir, []string{"/bin/sh", "-c", "true"}, Spec{})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !hasArgPair(argv, "--ro-bind", "/") {
		t.Fatalf("the root filesystem is not mounted read-only: %v", argv)
	}
	if !hasArgPair(argv, "--bind", dir) {
		t.Fatalf("the workspace is not writable: %v", argv)
	}
	if i, j := slices.Index(argv, "--chdir"), slices.Index(argv, dir); i < 0 || j < 0 || j < i {
		t.Fatalf("the command does not start in the workspace: %v", argv)
	}
	// Everything after "--" is the command; a wrap that puts its own options
	// there would hand them to the shell instead.
	sep := slices.Index(argv, "--")
	if sep < 0 || sep != len(argv)-4 {
		t.Fatalf("command separator is misplaced: %v", argv)
	}
}

func TestBwrapBlocksTheNetworkByDefault(t *testing.T) {
	r := testLinuxRunner(t)
	argv, _, _ := wrapBwrap(r, t.TempDir(), []string{"/bin/sh"}, Spec{})
	if !slices.Contains(argv, "--unshare-net") {
		t.Fatalf("network namespace is not isolated: %v", argv)
	}
}

func TestBwrapHonoursAnExplicitNetworkGrant(t *testing.T) {
	r := testLinuxRunner(t)
	argv, _, _ := wrapBwrap(r, t.TempDir(), []string{"/bin/sh"}, Spec{Network: NetworkAllow})
	if slices.Contains(argv, "--unshare-net") {
		t.Fatalf("network grant was ignored: %v", argv)
	}
}

func TestBwrapPointsTmpdirAtThePrivateTmpfs(t *testing.T) {
	r := testLinuxRunner(t)
	_, env, _ := wrapBwrap(r, t.TempDir(), []string{"/bin/sh"}, Spec{})
	var found bool
	for _, e := range env {
		if strings.HasPrefix(e, "TMPDIR=") || strings.HasPrefix(e, "TMP=") || strings.HasPrefix(e, "TEMP=") {
			if e != "TMPDIR=/tmp" {
				t.Fatalf("temp directory escapes the private tmpfs: %s", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no TMPDIR was set; writes would go to a path the sandbox hides")
	}
}

// A writable entry that does not exist makes bwrap refuse the whole run, so the
// builder has to drop it before it becomes an argument. Configuration can name
// directories that were deleted, which is exactly how this happens.
func TestBwrapDropsWritablePathsThatDoNotExist(t *testing.T) {
	r := testLinuxRunner(t)
	missing := t.TempDir() + "/gone"
	argv, _, err := wrapBwrap(r, t.TempDir(), []string{"/bin/sh"}, Spec{Writable: []string{missing}})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if slices.Contains(argv, missing) {
		t.Fatalf("a missing directory reached the argument list: %v", argv)
	}
}

// hasArgPair reports whether argv contains flag immediately followed by value.
func hasArgPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}
