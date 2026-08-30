package update

import "testing"

// Asset names mirror .github/workflows/build.yml: icode-desktop-<os>-<arch>
// from native runners and icode-cli-<goos>-<goarch> from cross-compiles.
var ciAssets = []string{
	"icode-desktop-ubuntu-latest-amd64",
	"icode-desktop-macos-13-amd64",
	"icode-desktop-macos-14-arm64",
	"icode-desktop-windows-latest-amd64.exe",
	"icode-cli-linux-amd64",
	"icode-cli-linux-arm64",
	"icode-cli-darwin-amd64",
	"icode-cli-darwin-arm64",
	"icode-cli-windows-amd64.exe",
	"icode-cli-windows-arm64.exe",
	"icode-cli-freebsd-amd64",
}

func TestPickAssetCLIKinds(t *testing.T) {
	cases := []struct {
		goos, goarch, exeName, want string
	}{
		{"windows", "amd64", "icode-cli.exe", "icode-cli-windows-amd64.exe"},
		{"windows", "arm64", "icode-cli.exe", "icode-cli-windows-arm64.exe"},
		{"linux", "amd64", "icode-cli", "icode-cli-linux-amd64"},
		{"linux", "arm64", "icode-cli", "icode-cli-linux-arm64"},
		{"darwin", "amd64", "icode-cli", "icode-cli-darwin-amd64"},
		{"darwin", "arm64", "icode-cli", "icode-cli-darwin-arm64"},
		{"freebsd", "amd64", "icode-cli", "icode-cli-freebsd-amd64"},
	}
	origExecutable := executableNameFn
	t.Cleanup(func() { executableNameFn = origExecutable })
	for _, c := range cases {
		executableNameFn = func() string { return c.exeName }
		got, err := pickAssetFor(c.goos, c.goarch, ciAssets)
		if err != nil || got != c.want {
			t.Errorf("cli %s/%s: got %q err=%v, want %q", c.goos, c.goarch, got, err, c.want)
		}
	}
}

func TestPickAssetDesktopKinds(t *testing.T) {
	cases := []struct {
		goos, goarch, exeName, want string
	}{
		{"windows", "amd64", "icode-desktop.exe", "icode-desktop-windows-latest-amd64.exe"},
		{"darwin", "amd64", "icode-desktop", "icode-desktop-macos-13-amd64"},
		{"darwin", "arm64", "icode-desktop", "icode-desktop-macos-14-arm64"},
		{"linux", "amd64", "icode-desktop", "icode-desktop-ubuntu-latest-amd64"},
	}
	origExecutable := executableNameFn
	t.Cleanup(func() { executableNameFn = origExecutable })
	for _, c := range cases {
		executableNameFn = func() string { return c.exeName }
		got, err := pickAssetFor(c.goos, c.goarch, ciAssets)
		if err != nil || got != c.want {
			t.Errorf("desktop %s/%s: got %q err=%v, want %q", c.goos, c.goarch, got, err, c.want)
		}
	}
}

// A desktop binary with no matching desktop asset must NOT silently downgrade
// to the headless CLI build — error out instead.
func TestPickAssetDesktopNeverDowngradesToCLI(t *testing.T) {
	origExecutable := executableNameFn
	executableNameFn = func() string { return "icode-desktop.exe" }
	t.Cleanup(func() { executableNameFn = origExecutable })
	if _, err := pickAssetFor("windows", "arm64", ciAssets); err == nil {
		t.Error("windows/arm64 desktop should error (no desktop asset), got nil")
	}
}

// Legacy releases (pre-0.48) shipped a bare windows-amd64 icode.exe.
func TestPickAssetLegacyFallback(t *testing.T) {
	origExecutable := executableNameFn
	executableNameFn = func() string { return "icode.exe" }
	t.Cleanup(func() { executableNameFn = origExecutable })
	got, err := pickAssetFor("windows", "amd64", []string{"icode.exe"})
	if err != nil || got != "icode.exe" {
		t.Errorf("legacy fallback: got %q err=%v", got, err)
	}
}

func TestBytesMagicOK(t *testing.T) {
	if !bytesMagicOK([]byte("MZ\x00\x00"), "windows") {
		t.Error("windows PE magic rejected")
	}
	if !bytesMagicOK([]byte{0x7f, 'E', 'L', 'F'}, "linux") {
		t.Error("linux ELF magic rejected")
	}
	if !bytesMagicOK([]byte{0xcf, 0xfa, 0xed, 0xfe}, "darwin") {
		t.Error("darwin Mach-O magic rejected")
	}
	if bytesMagicOK([]byte("<ht"), "windows") {
		t.Error("HTML accepted as PE")
	}
}
