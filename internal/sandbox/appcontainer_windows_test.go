//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func aceCount(sddl string) int { return strings.Count(sddl, "(A;") }

// sidPattern pulls trustee SIDs out of an SDDL string so a test can assert that
// a DACL edit preserved everyone who had access before.
var sidPattern = regexp.MustCompile(`S-1-(?:15-2|5|1)-\d[\d-]*`)

// TestGrantACEKeepsExistingEntries is the guard against the worst possible bug
// in this package: replacing a project directory's DACL wholesale instead of
// merging into it. Losing the owner's own ACE would lock the user out of their
// own files.
func TestGrantACEKeepsExistingEntries(t *testing.T) {
	dir := t.TempDir()
	sid, _, err := ensureProfile(profileName)
	if err != nil {
		t.Skipf("AppContainer unavailable: %v", err)
	}

	before, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read DACL: %v", err)
	}
	beforeSDDL := before.String()

	if err := grantACE(dir, sid); err != nil {
		t.Fatalf("grantACE: %v", err)
	}

	after, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("re-read DACL: %v", err)
	}
	afterSDDL := after.String()

	if !strings.Contains(afterSDDL, sid.String()) {
		t.Fatalf("granted SID missing from DACL:\n before=%s\n after =%s", beforeSDDL, afterSDDL)
	}
	// Windows emits two entries for an inheritable grant on a directory (one
	// effective on the directory, one inherit-only for children), so the count
	// grows by up to two — but it must never shrink.
	if got, want := aceCount(afterSDDL), aceCount(beforeSDDL)+1; got < want {
		t.Fatalf("ACE count = %d, want at least %d (existing entries must survive)\n before=%s\n after =%s",
			got, want, beforeSDDL, afterSDDL)
	}
	// The precise property: every trustee that had rights before still has
	// them afterwards.
	for _, s := range sidPattern.FindAllString(beforeSDDL, -1) {
		if !strings.Contains(afterSDDL, s) {
			t.Fatalf("trustee %s lost its access after grant\n before=%s\n after =%s", s, beforeSDDL, afterSDDL)
		}
	}

	// The current user must still be able to use the directory.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory became inaccessible to its owner: %v", err)
	}

	if err := revokeACE(dir, sid); err != nil {
		t.Fatalf("revokeACE: %v", err)
	}
	revoked, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read DACL after revoke: %v", err)
	}
	if strings.Contains(revoked.String(), sid.String()) {
		t.Fatalf("SID still present after revoke: %s", revoked.String())
	}
	for _, s := range sidPattern.FindAllString(beforeSDDL, -1) {
		if !strings.Contains(revoked.String(), s) {
			t.Fatalf("revoke removed unrelated trustee %s: %s", s, revoked.String())
		}
	}
}

func newTestRunner(t *testing.T) *acRunner {
	t.Helper()
	// Point the grant record at a temp file: a test that writes ACEs must not
	// leave entries in the user's real ~/.icode/sandbox_grants.json.
	oldGrantPath := grantFilePath
	grantFilePath = filepath.Join(t.TempDir(), "sandbox_grants.json")
	t.Cleanup(func() { grantFilePath = oldGrantPath })

	raw, _, err := newPlatformRunner(Config{
		GrantWorkspace: true,
		Network:        NetworkDeny,
		WritablePaths:  defaultWritablePaths(),
	})
	if err != nil {
		t.Skipf("AppContainer unavailable: %v", err)
	}
	r, ok := raw.(*acRunner)
	if !ok || !r.Available() {
		t.Skip("AppContainer backend reports unavailable")
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func run(t *testing.T, r *acRunner, dir string, argv ...string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := r.Start(ctx, argv, Spec{Dir: dir})
	if err != nil {
		t.Fatalf("start %v: %v", argv, err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("wait %v: %v", argv, err)
	}
	return res
}

// TestRevokeRemovesGrantAndCache exercises the whole undo path: the ACE is
// recorded when written, removed from the DACL on revoke, dropped from the
// record, and — the part that is easy to miss — evicted from the runner's
// in-memory cache so the next command re-grants instead of failing with a bare
// access-denied error.
func TestRevokeRemovesGrantAndCache(t *testing.T) {
	r := newTestRunner(t)
	SetActive(r)
	t.Cleanup(func() { SetActive(nil) })

	work := t.TempDir()
	if res := run(t, r, work, "cmd.exe", "/c", "echo one> a.txt"); res.ExitCode != 0 {
		t.Fatalf("initial run failed: exit=%d output=%q", res.ExitCode, res.Output)
	}

	grants, err := ListGrants()
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	found := false
	for _, g := range grants {
		if samePath(g.Path, work) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the workspace was granted but not recorded: %+v", grants)
	}

	revoked, err := Revoke(work)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked {
		t.Fatalf("revoke reported nothing to remove, but the ACE was just written")
	}
	if has, err := hasACE(work, r.sidString); err != nil || has {
		t.Fatalf("container SID still in the DACL after revoke (has=%v err=%v)", has, err)
	}
	if grants, _ := ListGrants(); len(grants) != 0 {
		t.Fatalf("grant record survived the revoke: %+v", grants)
	}

	// The cache must have been invalidated: the next command has to re-grant
	// rather than trust the stale "already granted" answer.
	if res := run(t, r, work, "cmd.exe", "/c", "echo two> b.txt"); res.ExitCode != 0 {
		t.Fatalf("run after revoke failed: exit=%d output=%q", res.ExitCode, res.Output)
	}
	if has, _ := hasACE(work, r.sidString); !has {
		t.Fatalf("the workspace was not re-granted on the next command")
	}
}

// TestRevokeReportsNothingToRemove keeps the UI honest: revoking a directory
// that never carried the entry must not be reported as a successful undo.
func TestRevokeReportsNothingToRemove(t *testing.T) {
	newTestRunner(t)
	work := t.TempDir()

	revoked, err := Revoke(work)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Fatalf("revoke claimed it removed an entry that was never there")
	}
}

// TestConfinedProcessCannotReadUserProfile proves the boundary is enforced by
// the kernel rather than by a rule that inspects the command string.
func TestConfinedProcessCannotReadUserProfile(t *testing.T) {
	r := newTestRunner(t)
	home := os.Getenv("USERPROFILE")
	if home == "" {
		t.Skip("USERPROFILE unset")
	}

	probe := filepath.Join(home, fmt.Sprintf("icode-sandbox-probe-%d.txt", os.Getpid()))
	if err := os.WriteFile(probe, []byte("secret"), 0o600); err != nil {
		t.Skipf("cannot create probe file: %v", err)
	}
	defer os.Remove(probe)

	work := t.TempDir()
	res := run(t, r, work, "cmd.exe", "/c", "type", probe)
	if res.ExitCode == 0 {
		t.Fatalf("reading a file in the user profile must be denied, got exit=0 output=%q", res.Output)
	}
}

// TestConfinedProcessCanUseWorkspace covers the other half: confinement must
// not break ordinary work in the project directory.
func TestConfinedProcessCanUseWorkspace(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	res := run(t, r, work, "cmd.exe", "/c", "echo sandbox-ok> out.txt && type out.txt")
	if res.ExitCode != 0 {
		t.Fatalf("write in workspace failed: exit=%d output=%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(string(res.Output), "sandbox-ok") {
		t.Fatalf("unexpected output: %q", res.Output)
	}
	if _, err := os.Stat(filepath.Join(work, "out.txt")); err != nil {
		t.Fatalf("file written inside the workspace is not visible to the user: %v", err)
	}
}

// TestConfinedProcessOutputIsStreamed checks that OnOutput sees the same bytes
// that Wait returns, since the progress UI depends on it.
func TestConfinedProcessOutputIsStreamed(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	var streamed []byte
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := r.Start(ctx, []string{"cmd.exe", "/c", "echo streamed"}, Spec{
		Dir:      work,
		OnOutput: func(p []byte) { streamed = append(streamed, p...) },
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if string(streamed) != string(res.Output) {
		t.Fatalf("streamed %q != collected %q", streamed, res.Output)
	}
}

// TestKillStopsConfinedProcess covers cancellation: the engine aborts a turn by
// killing the handle, and a leaked confined process would keep holding the
// workspace.
func TestKillStopsConfinedProcess(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := r.Start(ctx, []string{"cmd.exe", "/c", "ping -n 30 127.0.0.1 > NUL"}, Spec{Dir: work})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := h.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	done := make(chan struct{})
	go func() { h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("process did not exit after Kill")
	}
}
