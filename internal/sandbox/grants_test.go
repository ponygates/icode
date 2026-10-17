package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// useTempGrantStore points the record at a throwaway file so tests never touch
// the real ~/.icode/sandbox_grants.json.
func useTempGrantStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandbox_grants.json")
	old := grantFilePath
	grantFilePath = path
	t.Cleanup(func() { grantFilePath = old })
	return path
}

func TestGrantRecordRoundTrip(t *testing.T) {
	useTempGrantStore(t)
	a := t.TempDir()
	b := t.TempDir()

	if err := RecordGrant(a); err != nil {
		t.Fatalf("record a: %v", err)
	}
	if err := RecordGrant(b); err != nil {
		t.Fatalf("record b: %v", err)
	}

	grants, err := ListGrants()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("got %d grants, want 2: %+v", len(grants), grants)
	}
	seen := map[string]bool{}
	for _, g := range grants {
		seen[g.Path] = true
		if g.GrantedAt.IsZero() {
			t.Fatalf("grant %q has no timestamp", g.Path)
		}
	}
	if !seen[a] || !seen[b] {
		t.Fatalf("recorded paths are missing from %+v", grants)
	}
}

// TestGrantRecordIsIdempotent matters because ensureAccess runs on every
// command: a record that grew on each call would turn the settings list into an
// unusable log.
func TestGrantRecordIsIdempotent(t *testing.T) {
	useTempGrantStore(t)
	dir := t.TempDir()

	for i := 0; i < 3; i++ {
		if err := RecordGrant(dir); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	grants, _ := ListGrants()
	if len(grants) != 1 {
		t.Fatalf("got %d grants, want 1", len(grants))
	}
}

func TestForgetGrantRemovesOnlyThatPath(t *testing.T) {
	useTempGrantStore(t)
	keep := t.TempDir()
	drop := t.TempDir()

	_ = RecordGrant(keep)
	_ = RecordGrant(drop)

	if err := ForgetGrant(drop); err != nil {
		t.Fatalf("forget: %v", err)
	}
	grants, _ := ListGrants()
	if len(grants) != 1 || grants[0].Path != keep {
		t.Fatalf("remaining grants = %+v, want only %q", grants, keep)
	}
}

// TestGrantPathMatchFollowsPlatform is the difference between a working undo
// button and one that silently no-ops because the user typed the path in a
// different case.
func TestGrantPathMatchFollowsPlatform(t *testing.T) {
	useTempGrantStore(t)
	dir := t.TempDir()

	if err := RecordGrant(dir); err != nil {
		t.Fatalf("record: %v", err)
	}
	other := dir
	if runtime.GOOS == "windows" {
		other = swapCase(dir)
	} else {
		other = dir + string(filepath.Separator) + "."
	}

	if err := ForgetGrant(other); err != nil {
		t.Fatalf("forget: %v", err)
	}
	grants, _ := ListGrants()
	if runtime.GOOS == "windows" {
		if len(grants) != 0 {
			t.Fatalf("case-different path did not match the recorded grant: %+v", grants)
		}
		return
	}
	if len(grants) != 1 {
		t.Fatalf("a different path removed the grant on %s: %+v", runtime.GOOS, grants)
	}
}

func swapCase(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= 'a' && r <= 'z' {
			b[i] = r - 32
			continue
		}
		if r >= 'A' && r <= 'Z' {
			b[i] = r + 32
		}
	}
	return string(b)
}

// TestCorruptGrantFileDoesNotBlockConfinement: the record is bookkeeping. If it
// cannot be parsed, commands must still run — losing the list is bad, refusing
// to work is worse.
func TestCorruptGrantFileDoesNotBlockConfinement(t *testing.T) {
	path := useTempGrantStore(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	grants, err := ListGrants()
	if err != nil {
		t.Fatalf("list over a corrupt file must not fail: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected an empty list, got %+v", grants)
	}
}

func TestMissingGrantFileIsAnEmptyList(t *testing.T) {
	useTempGrantStore(t)

	grants, err := ListGrants()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grants, got %+v", grants)
	}
}

func TestRevokeRejectsAnEmptyPath(t *testing.T) {
	useTempGrantStore(t)
	if _, err := Revoke("   "); err == nil {
		t.Fatalf("revoke accepted an empty path")
	}
}

// TestRevokeCleansUpADeletedDirectory: the ACE disappeared with the directory,
// so the record must be removable too. Failing here would leave a row in
// settings that no action can ever clear.
func TestRevokeCleansUpADeletedDirectory(t *testing.T) {
	useTempGrantStore(t)
	dir := t.TempDir()
	if err := RecordGrant(dir); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	revoked, err := Revoke(dir)
	if err != nil {
		t.Fatalf("revoke on a deleted directory must not fail: %v", err)
	}
	if revoked {
		t.Fatalf("revoke claimed it changed a DACL that no longer exists")
	}
	grants, _ := ListGrants()
	if len(grants) != 0 {
		t.Fatalf("the record for a deleted directory survived: %+v", grants)
	}
}
