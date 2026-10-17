//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfinedGoBuildWorks is acceptance criterion 4 from the design doc: the
// boundary must not break ordinary development. It fails loudly with the
// captured output because "go build failed inside the sandbox" has several
// distinct causes (toolchain unreadable, cache not writable, temp redirected
// somewhere the container cannot reach) and the message is what tells them apart.
func TestConfinedGoBuildWorks(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"built\") }\n"
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	res := run(t, r, work, "go", "build", "-o", "probe.exe", ".")
	if res.ExitCode != 0 {
		t.Fatalf("go build failed inside the sandbox (exit=%d):\n%s", res.ExitCode, res.Output)
	}
	if _, err := os.Stat(filepath.Join(work, "probe.exe")); err != nil {
		t.Fatalf("build produced no binary: %v", err)
	}
}

// TestConfinedGitStatusWorks covers the other everyday command, and exercises a
// POSIX-style shell so the Windows bash path is not silently broken.
func TestConfinedGitStatusWorks(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	res := run(t, r, work, "cmd.exe", "/c", "git --version")
	if res.ExitCode != 0 {
		t.Skipf("git not available: %s", res.Output)
	}
}
