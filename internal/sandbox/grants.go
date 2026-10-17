package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Grant records that an access-control entry for the sandbox identity was
// written into a directory on disk.
//
// Confinement is not free: to let a confined process use a project directory
// as its working directory, the backend has to add an inheritable ACE to that
// directory's DACL. That is a lasting change to the user's filesystem, made
// outside their view. Keeping the list lets settings show exactly what was
// changed and offer a one-click undo, which is the difference between a
// boundary the user consented to and one they can only discover with icacls.
type Grant struct {
	Path      string    `json:"path"`
	GrantedAt time.Time `json:"granted_at"`
}

// grantFilePath is a variable so tests can point the store at a temp file.
var grantFilePath = defaultGrantFilePath()

func defaultGrantFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "sandbox_grants.json")
}

var grantMu sync.Mutex

// grantStore mirrors the JSON file on disk.
type grantStore struct {
	Grants []Grant `json:"grants"`
}

// samePath compares two absolute paths the way the platform does. Windows
// paths are case-insensitive, so a grant recorded as C:\Work and revoked as
// c:\work must be the same entry, otherwise revocation silently no-ops.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// normalizeGrantPath canonicalises a path before it is stored. An empty result
// means the path could not be resolved and must not be recorded.
func normalizeGrantPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

func readGrants() (grantStore, error) {
	var st grantStore
	path := grantFilePath
	if path == "" {
		return st, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	if len(b) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		// A corrupt file must not block confinement. Losing the record is
		// bad but recoverable — the ACEs are still in the DACLs and the
		// next grant for the same path rewrites the entry.
		return grantStore{}, nil
	}
	return st, nil
}

func writeGrants(st grantStore) error {
	path := grantFilePath
	if path == "" {
		return errors.New("sandbox: cannot locate a home directory for the grant record")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("sandbox: create grant record directory: %w", err)
	}
	sort.Slice(st.Grants, func(i, j int) bool { return st.Grants[i].Path < st.Grants[j].Path })
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename: a crash mid-write would otherwise leave a truncated
	// file, and this record is the only evidence of what was changed.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("sandbox: write grant record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sandbox: replace grant record: %w", err)
	}
	return nil
}

// RecordGrant notes that dir now carries an ACE for the sandbox identity.
// Re-recording an existing path refreshes nothing but is not an error, so
// callers never have to check first.
func RecordGrant(dir string) error {
	path := normalizeGrantPath(dir)
	if path == "" {
		return nil
	}
	grantMu.Lock()
	defer grantMu.Unlock()
	st, err := readGrants()
	if err != nil {
		return err
	}
	for i := range st.Grants {
		if samePath(st.Grants[i].Path, path) {
			return nil
		}
	}
	st.Grants = append(st.Grants, Grant{Path: path, GrantedAt: time.Now().UTC()})
	return writeGrants(st)
}

// ForgetGrant drops dir from the record without touching its DACL. Callers
// pair it with a real revocation; on its own it would hide a live grant.
func ForgetGrant(dir string) error {
	path := normalizeGrantPath(dir)
	if path == "" {
		return nil
	}
	grantMu.Lock()
	defer grantMu.Unlock()
	st, err := readGrants()
	if err != nil {
		return err
	}
	kept := st.Grants[:0]
	for _, g := range st.Grants {
		if !samePath(g.Path, path) {
			kept = append(kept, g)
		}
	}
	if len(kept) == len(st.Grants) {
		return nil
	}
	st.Grants = kept
	return writeGrants(st)
}

// ListGrants returns the recorded grants, newest first. A missing record is an
// empty list, not an error: most users will never have granted anything.
func ListGrants() ([]Grant, error) {
	grantMu.Lock()
	defer grantMu.Unlock()
	st, err := readGrants()
	if err != nil {
		return nil, err
	}
	out := append([]Grant(nil), st.Grants...)
	sort.Slice(out, func(i, j int) bool { return out[i].GrantedAt.After(out[j].GrantedAt) })
	return out, nil
}

// recordFailure remembers why the grant record could not be written, so the
// settings page can say that a directory was granted but is not listed. The
// ACE is already in the DACL at that point; failing the command would punish
// the user for a bookkeeping error, so the failure is surfaced instead.
var recordFailure struct {
	mu  sync.Mutex
	msg string
}

func noteRecordFailure(dir string, err error) {
	recordFailure.mu.Lock()
	recordFailure.msg = fmt.Sprintf("granted sandbox access to %s but could not record it: %v", dir, err)
	recordFailure.mu.Unlock()
}

func recordFailureNotice() string {
	recordFailure.mu.Lock()
	defer recordFailure.mu.Unlock()
	return recordFailure.msg
}

// pathForgetter is implemented by backends that cache grants in memory. The
// cache exists to avoid re-reading a DACL on every command; it must be
// invalidated when the grant is revoked from outside that path.
type pathForgetter interface {
	forgetPath(string)
}

func forgetActivePath(path string) {
	if f, ok := Active().(pathForgetter); ok {
		f.forgetPath(path)
	}
}

// Revoke works out whether dir currently carries an ACE for the sandbox
// identity, removes it, and only then forgets the record. The order matters:
// dropping the record first would leave an unreported grant behind if the
// DACL update failed.
//
// Reports granted=false when there was nothing to remove, so the UI can say
// "already clean" instead of implying it changed something.
func Revoke(dir string) (granted bool, err error) {
	path := normalizeGrantPath(dir)
	if path == "" {
		return false, fmt.Errorf("sandbox: %q is not a usable path", dir)
	}
	// A directory that no longer exists took its ACE with it. Reporting an
	// error here would leave a permanent, unremovable row in settings — the
	// user deletes a project, and the record can never be cleaned up.
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		forgetActivePath(path)
		if ferr := ForgetGrant(path); ferr != nil {
			return false, ferr
		}
		return false, nil
	}
	had, err := revokePlatform(path)
	if err != nil {
		return false, err
	}
	// The in-process cache must forget the path too. Without this the runner
	// still believes it granted access, skips the DACL check on the next
	// command, and the command fails with a bare access-denied error.
	forgetActivePath(path)
	if err := ForgetGrant(path); err != nil {
		return had, err
	}
	return had, nil
}
