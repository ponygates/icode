//go:build windows && !nogui

package cmd

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// maxWebViewDataDirSize caps how large the WebView2 user-data directory may
// grow before it is treated as bloated/corrupt. The runtime writes cookies,
// caches and GPU shaders into this folder without bound — a dir that balloons
// into the hundreds of MB is far more likely to be corrupt or to contain stale
// lock files, and it measurably slows (or hangs) runtime creation on the boot
// path. Such a dir is wiped before the window is created so every launch
// starts from a clean, fast slate.
const maxWebViewDataDirSize = 100 * 1024 * 1024 // 100 MB

// healWebViewDataDir inspects a WebView2 user-data dir before window creation
// and wipes it when it is oversized or unreadable (locked / half-deleted).
// Returns true when the dir was reset. It deliberately runs BEFORE
// tryInitWebView so a corrupt dir never gets the chance to block boot.
func healWebViewDataDir(dataPath string) bool {
	if strings.TrimSpace(dataPath) == "" {
		return false
	}
	size, err := webViewDataDirSize(dataPath)
	if err != nil {
		// Exists but can't be walked (locked / deleted underneath us) — treat
		// as corrupt and reset so creation never hangs waiting on it.
		log.Printf("[desktop] webview heal: scan %s failed: %v; resetting", dataPath, err)
		resetWebViewDataDir(dataPath)
		return true
	}
	if size > maxWebViewDataDirSize {
		log.Printf("[desktop] webview heal: %s is %.1f MB (> %d MB); resetting",
			dataPath, float64(size)/(1024*1024), maxWebViewDataDirSize/(1024*1024))
		resetWebViewDataDir(dataPath)
		return true
	}
	return false
}

// webViewDataDirSize returns the total size in bytes of all files under
// dataPath. A dir that does not exist yet yields (0, nil) — nothing to heal.
func webViewDataDirSize(dataPath string) (int64, error) {
	var total int64
	err := filepath.Walk(dataPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if p == dataPath && os.IsNotExist(err) {
				return nil // dir does not exist yet — nothing to heal
			}
			if p == dataPath {
				return err // root inaccessible → treat as corrupt
			}
			return nil // skip unreadable sub-entries
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// resetWebViewDataDir removes a WebView2 user-data dir so the next runtime
// creation starts from an empty folder. Zombie msedgewebview2.exe processes
// that would hold the dir lock are killed first. The runtime recreates the
// directory implicitly on the next NewWithOptions, so no mkdir is needed here.
func resetWebViewDataDir(dataPath string) {
	if strings.TrimSpace(dataPath) == "" {
		return
	}
	killStaleWebViewProcesses(dataPath)
	if err := os.RemoveAll(dataPath); err != nil {
		log.Printf("[desktop] webview reset: RemoveAll(%s) failed: %v", dataPath, err)
		return
	}
	log.Printf("[desktop] webview reset: wiped %s", dataPath)
}
