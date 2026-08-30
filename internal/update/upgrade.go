// Self-upgrade — Claude Code `claude update` / opencode `upgrade` parity.
// Downloads the release asset matching the running platform (GOOS/GOARCH and
// desktop vs CLI kind) from GitHub, sanity-checks it, and atomically swaps the
// running binary (rename-based, safe on Windows where a running exe cannot be
// overwritten but CAN be renamed).
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UpgradeResult reports what an Upgrade call did.
type UpgradeResult struct {
	From    string // version upgraded from
	To      string // version upgraded to
	Path    string // binary path that was replaced
	Rolled  bool   // true when the user must restart to run the new version
	Skipped string // non-empty when no upgrade was needed (reason)
}

// pickAsset chooses the release asset matching the running platform: the CI
// publishes icode-desktop-<os>-<arch> (native runners) and icode-cli-<goos>-<goarch>
// (cross-compiles, e.g. icode-cli-darwin-arm64, icode-cli-windows-arm64.exe).
// The binary kind (desktop vs cli) is detected from the executable's own name
// so a GUI build never "upgrades" into a headless one.
func pickAsset(names []string) (string, error) {
	return pickAssetFor(runtime.GOOS, runtime.GOARCH, names)
}

// executableNameFn is overridable in tests.
var executableNameFn = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}

func pickAssetFor(goos, goarch string, names []string) (string, error) {
	kind := "cli"
	if strings.Contains(strings.ToLower(executableNameFn()), "desktop") {
		kind = "desktop"
	}
	osTokens := []string{goos}
	if goos == "darwin" {
		osTokens = append(osTokens, "macos") // desktop assets use macos-13/macos-14
	}
	if goos == "linux" {
		osTokens = append(osTokens, "ubuntu") // desktop assets use ubuntu-latest
	}
	archTokens := []string{goarch}
	if goarch == "amd64" {
		archTokens = append(archTokens, "x64")
	}
	hasAny := func(l string, toks []string) bool {
		for _, t := range toks {
			if strings.Contains(l, t) {
				return true
			}
		}
		return false
	}

	// Legacy fallback: the pre-0.48 asset was a bare windows-amd64 icode.exe.
	var legacy string
	for _, n := range names {
		l := strings.ToLower(n)
		if goos == "windows" && !strings.HasSuffix(l, ".exe") {
			continue
		}
		if strings.HasPrefix(l, "icode-"+kind+"-") && hasAny(l, osTokens) && hasAny(l, archTokens) {
			return n, nil
		}
		if legacy == "" && (l == "icode.exe" ||
			(strings.Contains(l, "windows") && strings.HasSuffix(l, ".exe") && hasAny(l, archTokens))) {
			legacy = n
		}
	}
	if legacy != "" && goos == "windows" && goarch == "amd64" {
		return legacy, nil
	}
	return "", fmt.Errorf("release assets 中未找到 %s/%s 的 %s 可执行文件，请到发布页手动下载", goos, goarch, kind)
}

// fetchJSON GETs url and decodes JSON into v.
func fetchJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "iCode-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github: status %d", resp.StatusCode)
	}
	return jsonDecode(resp.Body, v)
}

// Upgrade checks for a newer release and, when available, downloads and
// swaps the running binary. The new version activates on next launch.
func Upgrade(ctx context.Context, current string) (*UpgradeResult, error) {
	info, err := Check(current)
	if err != nil {
		return nil, err
	}
	if !info.Available {
		return &UpgradeResult{From: info.Current, To: info.Latest, Skipped: "已是最新版本 " + info.Current}, nil
	}

	// Resolve the release's asset list.
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	releaseAPI := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	if err := fetchJSON(ctx, releaseAPI, &rel); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rel.Assets))
	byName := map[string]string{}
	for _, a := range rel.Assets {
		names = append(names, a.Name)
		byName[a.Name] = a.URL
	}
	assetName, err := pickAsset(names)
	if err != nil {
		return nil, fmt.Errorf("%w\n手动下载：%s", err, info.HTMLURL)
	}

	// Download to a temp file next to the target (same volume → atomic rename).
	goos := runtime.GOOS
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exePath, _ = filepath.Abs(exePath)
	tmpPattern := ".icode-upgrade-*"
	if goos == "windows" {
		tmpPattern += ".exe"
	}
	tmp, err := os.CreateTemp(filepath.Dir(exePath), tmpPattern)
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, byName[assetName], nil)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	req.Header.Set("User-Agent", "iCode-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tmp.Close()
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return nil, fmt.Errorf("download: status %d", resp.StatusCode)
	}

	written, err := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if written < 1<<20 { // a real iCode build is multiple MB — refuse junk
		return nil, fmt.Errorf("downloaded file too small (%d bytes), aborting", written)
	}

	// Sanity check: the downloaded binary must carry the platform's executable
	// magic (Windows PE "MZ", Linux ELF, macOS Mach-O) — refuses HTML error
	// pages or truncated downloads from being swapped in.
	if magic, err := execMagic(tmpName); err == nil && len(magic) > 0 {
		if !bytesMagicOK(magic, goos) {
			return nil, fmt.Errorf("downloaded file is not a %s executable, aborting", goos)
		}
	}

	old := exePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return nil, fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		// Roll back so the installation stays runnable.
		_ = os.Rename(old, exePath)
		return nil, fmt.Errorf("swap binary: %w", err)
	}

	return &UpgradeResult{
		From:   info.Current,
		To:     info.Latest,
		Path:   exePath,
		Rolled: true,
	}, nil
}

// CleanupOldBinary removes the .old backup left by a previous upgrade.
// Best-effort, called on startup.
func CleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(strings.TrimSuffix(exePath, filepath.Ext(exePath)) + ".old")
	_ = os.Remove(exePath + ".old")
}

// execMagic reads the first 4 bytes of a file.
func execMagic(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	magic := make([]byte, 4)
	_, err = io.ReadFull(f, magic)
	if err != nil {
		return nil, err
	}
	return magic, nil
}

// bytesMagicOK reports whether the magic matches the platform's executable format.
func bytesMagicOK(magic []byte, goos string) bool {
	switch goos {
	case "windows":
		return len(magic) >= 2 && string(magic[:2]) == "MZ"
	case "linux":
		return len(magic) >= 4 && magic[0] == 0x7f && string(magic[1:4]) == "ELF"
	case "darwin":
		// Mach-O: 32/64-bit, either endianness.
		return len(magic) >= 4 && (string(magic) == "\xcf\xfa\xed\xfe" || string(magic) == "\xce\xfa\xed\xfe" ||
			string(magic) == "\xfe\xed\xfa\xce" || string(magic) == "\xfe\xed\xfa\xcf")
	default:
		return true // no known magic — accept
	}
}
