//go:build windows

package sandbox

import "testing"

// TestConfinedNullDeviceWorks guards the fix for a hard AppContainer limitation:
// the NUL device's DACL ships without an AppContainer entry, and the Go
// toolchain opens os.DevNull while computing build IDs. If this regresses,
// `go build` fails inside the boundary with "open NUL: Access is denied".
func TestConfinedNullDeviceWorks(t *testing.T) {
	r := newTestRunner(t)
	work := t.TempDir()

	res := run(t, r, work, "cmd.exe", "/c", "echo hi> NUL && echo nul-ok")
	if res.ExitCode != 0 {
		t.Fatalf("NUL unreachable inside the sandbox (exit=%d, output=%q); "+
			"the backend must add the container SID to the NUL device DACL",
			res.ExitCode, res.Output)
	}
}
