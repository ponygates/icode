package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pasteClipboardImage implements Ctrl+V (Claude Code parity): when the system
// clipboard holds an image, save it to a temp PNG and insert an @path reference
// into the input. On send the path is read, base64-encoded, and inlined as a
// true multimodal attachment (see consumePendingImages), so vision-capable
// models can actually see the picture. Falls back gracefully when the clipboard
// has no image or the platform's clipboard tool is unavailable (text paste is
// already handled by the terminal's bracketed-paste mode, so we never
// double-paste text). The downloaded image is left in the OS temp dir.
func (t *TUI) pasteClipboardImage() {
	var path string
	switch runtime.GOOS {
	case "windows":
		ps := `Add-Type -AssemblyName System.Windows.Forms; ` +
			`Add-Type -AssemblyName System.Drawing; ` +
			`if ([System.Windows.Forms.Clipboard]::ContainsImage()) { ` +
			`$img=[System.Windows.Forms.Clipboard]::GetImage(); ` +
			`$p=Join-Path $env:TEMP ('icode-clip-'+[datetime]::Now.ToString('yyyyMMddHHmmssffff')+'.png'); ` +
			`$img.Save($p,[System.Drawing.Imaging.ImageFormat]::Png); $p }`
		out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
		if err == nil {
			path = strings.TrimSpace(string(out))
		}
	case "darwin":
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("icode-clip-%d.png", time.Now().UnixNano()))
		cmd := exec.Command("bash", "-c",
			fmt.Sprintf("command -v pngpaste >/dev/null 2>&1 && pngpaste %q >/dev/null 2>&1 && echo %q", tmp, tmp))
		if out, err := cmd.Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p == tmp {
				path = p
			}
		}
	default: // linux / other
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("icode-clip-%d.png", time.Now().UnixNano()))
		cmd := exec.Command("bash", "-c",
			fmt.Sprintf("command -v xclip >/dev/null 2>&1 && xclip -selection clipboard -t image/png -o >%q 2>/dev/null && echo %q", tmp, tmp))
		if out, err := cmd.Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p == tmp {
				path = p
			}
		}
	}
	if path == "" {
		t.notice("剪贴板中没有图片（或当前环境无法读取剪贴板图片）")
		return
	}
	t.dismissWelcome()
	t.insertAtCursor("@" + path + " ")
	t.updateSuggestions()
	t.notice("📎 已粘贴剪贴板图片: " + filepath.Base(path) + "（发送时作为多模态附件）")
}

// readImageFile reads an image file and returns its base64 payload plus a MIME
// type, or ok=false when the file is unreadable, too large, or not a supported
// image format.
func readImageFile(path string) (b64, mime string, ok bool) {
	const maxImageBytes = 25 << 20 // 25 MB cap to keep memory / context bounded
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || len(b) > maxImageBytes {
		return "", "", false
	}
	mime = detectImageMIME(b, path)
	if mime == "" {
		return "", "", false
	}
	return base64.StdEncoding.EncodeToString(b), mime, true
}

// detectImageMIME identifies common image formats by magic bytes, falling back
// to the file extension.
func detectImageMIME(b []byte, path string) string {
	switch {
	case len(b) >= 8 && b[0] == 0x89 && string(b[1:4]) == "PNG":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 6 && (string(b[0:6]) == "GIF89a" || string(b[0:6]) == "GIF87a"):
		return "image/gif"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// ── Reverse history search (Ctrl+R, Claude Code style) ───────────
