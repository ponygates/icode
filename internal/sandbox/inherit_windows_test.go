//go:build windows

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPreexistingFilesAreReadable pins a Windows behaviour the design depends
// on: an inheritable ACE is expected to cover children that already existed
// when the grant was applied. If a future Windows version stops propagating,
// every real project (whose files all predate the grant) would become
// unreadable inside the boundary and only new files would work.
func TestPreexistingFilesAreReadable(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()
	sub := filepath.Join(work, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "preexisting.txt"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := r.Start(ctx, []string{"cmd.exe", "/c", "type sub\\preexisting.txt"}, Spec{Dir: work})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, _ := h.Wait()
	if res.ExitCode != 0 {
		t.Fatalf("pre-existing file unreadable (exit=%d, output=%q): "+
			"ACL inheritance is not reaching existing children", res.ExitCode, res.Output)
	}
}
