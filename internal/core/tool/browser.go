package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// browserTool renders dynamic web pages with a headless browser (Edge/Chrome
// --dump-dom), extracting the post-JS text that a plain HTTP fetch cannot see
// — ZCode Browser Use / Claude Code web-browsing parity, zero new
// dependencies (the browser binary is already installed on the OS).
type BrowserTool struct{}

func (t *BrowserTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "browser",
		Description: "用无头浏览器打开网页并提取渲染后的文本（支持 JS 动态页面）。" +
			"动作: dump <url> 提取正文文本（适用于 fetch 抓不到的动态页面）; " +
			"screenshot <url> <path> 保存整页截图。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"dump", "screenshot"}},
				"url":    map[string]any{"type": "string"},
				"path":   map[string]any{"type": "string"},
			},
			"required": []string{"action", "url"},
		},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var req struct {
		Action string `json:"action"`
		URL    string `json:"url"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		return &types.ToolResult{Success: false, Error: "browser: 参数解析失败: " + err.Error()}, nil
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.URL = strings.TrimSpace(req.URL)
	if req.Action == "" || req.URL == "" {
		return &types.ToolResult{Success: false, Error: "browser: 需要 action 与 url"}, nil
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		req.URL = "https://" + req.URL
	}
	bin := findBrowser()
	if bin == "" {
		return &types.ToolResult{Success: false, Error: "browser: 未找到 Edge/Chrome（需要安装任一浏览器）"}, nil
	}

	ctx2, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	switch req.Action {
	case "dump":
		out, err := runHeadless(ctx2, bin, "--dump-dom", req.URL)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "browser dump: " + err.Error()}, nil
		}
		text := htmlToText(out, 6000)
		if strings.TrimSpace(text) == "" {
			// Fallback: raw head of the DOM when extraction yields nothing.
			text = clipStr(out, 2000)
		}
		return &types.ToolResult{Success: true, Content: text}, nil

	case "screenshot":
		if req.Path == "" {
			req.Path = filepath.Join(os.TempDir(), fmt.Sprintf("browser-%d.png", time.Now().UnixNano()))
		}
		abs, err := filepath.Abs(req.Path)
		if err != nil {
			abs = req.Path
		}
		if _, err := runHeadless(ctx2, bin, "--screenshot="+abs, "--window-size=1280,800", req.URL); err != nil {
			return &types.ToolResult{Success: false, Error: "browser screenshot: " + err.Error()}, nil
		}
		if _, err := os.Stat(abs); err != nil {
			return &types.ToolResult{Success: false, Error: "browser screenshot: 未生成文件（页面可能拒绝了无头访问）"}, nil
		}
		return &types.ToolResult{Success: true, Content: "截图已保存: " + abs}, nil
	}
	return &types.ToolResult{Success: false, Error: "browser: 未知 action " + req.Action}, nil
}

// findBrowser locates Edge / Chrome / Chromium on common platforms.
func findBrowser() string {
	candidates := []string{}
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		candidates = []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
	}
	for _, c := range candidates {
		if strings.ContainsAny(c, `/\`) {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		} else if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

// runHeadless runs the browser in headless mode with the given extra args and
// returns combined output (--dump-dom prints the rendered DOM to stdout).
func runHeadless(ctx context.Context, bin string, extra ...string) (string, error) {
	args := []string{
		"--headless", "--disable-gpu", "--no-first-run", "--disable-extensions",
		"--user-data-dir=" + headlessProfileDir(),
	}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Non-zero exit still carries useful DOM on stdout in some builds.
		if len(out) > 0 {
			return string(out), nil
		}
		return "", fmt.Errorf("%v", err)
	}
	return string(out), nil
}

// headlessProfileDir gives every browser call a private temp profile so
// concurrent runs never fight over the default user-data-dir lock.
func headlessProfileDir() string {
	d := filepath.Join(os.TempDir(), "icode-browser")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// clipStr truncates s to n runes with an ellipsis.
func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
