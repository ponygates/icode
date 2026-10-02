package tui

import (
	"os"
	"testing"
)

// TestMain keeps the package's tests away from the user's real
// ~/.icode/cli.log: several tests deliberately trigger diagnostic dumps
// (input-trace on drain timeouts, render-panic recovery) that would
// otherwise append test noise to the very file we later ask a user to
// submit as forensic evidence. (This actually happened: a full `go test`
// run filled cli.log with all-zero trace dumps.)
func TestMain(m *testing.M) {
	cliLogSuppress = true
	os.Exit(m.Run())
}
