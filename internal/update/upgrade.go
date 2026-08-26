// Self-upgrade — Claude Code `claude update` / opencode `upgrade` parity.
// Downloads the latest Windows amd64 release asset from GitHub, sanity-checks
// it, and atomically swaps the running binary (rename-based, safe on Windows
// where a running exe cannot be overwritten but CAN be renamed).
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// pickAsset chooses the windows-amd64 executable from a release asset list.
func pickAsset(names []string) (string, error) {
	var fallback string
	for _, n := range names {
		l := strings.ToLower(n)
		if !strings.HasSuffix(l, ".exe") {
			continue
		}
		if strings.Contains(l, "windows") && (strings.Contains(l, "amd64") || strings.Contains(l, "x64")) {
			return n, nil
		}
		if strings.EqualFold(n, "icode.exe") && fallback == "" {
			fallback = n
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("release assets 中未找到 Windows amd64 可执行文件，请到发布页手动下载")
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
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exePath, _ = filepath.Abs(exePath)
	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".icode-upgrade-*.exe")
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

	// Sanity check: must look like a Windows PE ("MZ" magic).
	if f, err := os.Open(tmpName); err == nil {
		magic := make([]byte, 2)
		_, _ = f.Read(magic)
		f.Close()
		if string(magic) != "MZ" {
			return nil, fmt.Errorf("downloaded file is not a Windows executable, aborting")
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
