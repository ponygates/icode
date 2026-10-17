//go:build darwin

package sandbox

import (
	"os"
	"strings"
	"testing"
)

func TestProfileDeniesEverythingItDoesNotName(t *testing.T) {
	p := sandboxProfile([]string{"/work/project"}, NetworkDeny)
	if !strings.Contains(p, "(deny default)") {
		t.Fatalf("profile does not start from a deny:\n%s", p)
	}
	if !strings.Contains(p, `(subpath "/work/project")`) {
		t.Fatalf("the workspace is not writable:\n%s", p)
	}
}

// SBPL applies the LAST matching rule, so a credential deny written before the
// blanket read-allow is silently discarded and the profile reads as strict
// while leaking. This asserts the order, not just the presence.
func TestCredentialDeniesComeAfterTheReadAllow(t *testing.T) {
	p := sandboxProfile([]string{"/work/project"}, NetworkDeny)
	allow := strings.Index(p, "(allow file-read*)")
	deny := strings.Index(p, "(deny file-read*")
	if deny < 0 {
		t.Skip("no credential store exists for this user; nothing to deny")
	}
	if allow < 0 || deny < allow {
		t.Fatalf("read-deny precedes the blanket read-allow, so it has no effect:\n%s", p)
	}
}

func TestProfileBlocksTheNetworkByDefault(t *testing.T) {
	p := sandboxProfile([]string{"/work"}, NetworkDeny)
	if !strings.Contains(p, "(deny network*)") {
		t.Fatalf("network is not denied:\n%s", p)
	}
	if strings.Contains(p, "(allow network*)") {
		t.Fatalf("profile both allows and denies the network:\n%s", p)
	}
}

func TestProfileHonoursAnExplicitNetworkGrant(t *testing.T) {
	p := sandboxProfile([]string{"/work"}, NetworkAllow)
	if !strings.Contains(p, "(allow network*)") {
		t.Fatalf("network grant was ignored:\n%s", p)
	}
}

// A path containing a quote would otherwise terminate the SBPL string and turn
// the rest of the path into syntax — either a parse error or, worse, a rule
// nobody wrote.
func TestProfileQuotesPathsSafely(t *testing.T) {
	got := sbplQuote(`/tmp/we"ird\dir`)
	want := `"/tmp/we\"ird\\dir"`
	if got != want {
		t.Fatalf("quoting: got %s want %s", got, want)
	}
}

func TestSecretDirsOnlyListsDirectoriesThatExist(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for _, d := range secretDirs() {
		if !strings.HasPrefix(d, home) {
			t.Fatalf("%q is outside the home directory", d)
		}
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Fatalf("%q was listed but does not exist", d)
		}
	}
}
