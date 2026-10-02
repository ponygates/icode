package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/executil"
)

// projectMemoryPath returns the project-level memory file (ICODE.md) path.
func (t *TUI) projectMemoryPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "ICODE.md"
	}
	return filepath.Join(cwd, "ICODE.md")
}

// prCommentsCommand shows pull-request comments via the GitHub CLI (Claude
// Code's /pr_comments). Requires `gh` to be installed and authenticated.
func (t *TUI) prCommentsCommand(args []string) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.add(RoleSystem, "未检测到 GitHub CLI (gh)。请先安装并登录：https://cli.github.com")
		return
	}
	pr := ""
	if len(args) > 0 {
		pr = args[0]
	}
	var cmd *exec.Cmd
	if pr != "" {
		cmd = exec.Command("gh", "pr", "view", pr, "--comments", "--json", "title,comments")
	} else {
		cmd = exec.Command("gh", "pr", "view", "--comments", "--json", "title,comments")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.add(RoleError, "获取 PR 评论失败: "+string(out))
		return
	}
	t.add(RoleSystem, "PR 评论:\n"+string(out))
}

// releaseNotesCommand prints the latest release notes from CHANGELOG.md
// (Claude Code's /release-notes).
func (t *TUI) releaseNotesCommand() {
	candidates := []string{"CHANGELOG.md", filepath.Join(".icode", "CHANGELOG.md")}
	var data []byte
	for _, c := range candidates {
		if d, err := os.ReadFile(c); err == nil {
			data = d
			break
		}
	}
	if len(data) == 0 {
		t.add(RoleSystem, "未找到 CHANGELOG.md。")
		return
	}
	text := string(data)
	if i := strings.Index(text, "\n## "); i > 0 {
		rest := text[i+1:]
		if j := strings.Index(rest, "\n## "); j > 0 {
			text = text[:i+1+j]
		}
	}
	const max = 2000
	if len(text) > max {
		text = text[:max] + "\n…"
	}
	t.add(RoleSystem, "发布说明:\n"+text)
}

// bugCommand opens a pre-filled GitHub issue for bug reports (Claude Code's
// /bug).
func (t *TUI) bugCommand() {
	body := fmt.Sprintf("**环境**: iCode %s / %s / %s\n**复现步骤**:\n1. \n\n**预期**: \n**实际**: ",
		t.version, t.provider, t.model)
	u := "https://github.com/ponygates/icode/issues/new?title=%5Bbug%5D&body=" + url.QueryEscape(body)
	t.add(RoleSystem, "请在此提交 Bug 报告：\n"+u)
	openURL(u)
}

// openURL opens a URL in the default browser (cross-platform).
func openURL(u string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		_ = exec.Command("open", u).Start()
	default:
		_ = exec.Command("xdg-open", u).Start()
	}
}

// add appends a message and refreshes the screen.
func (t *TUI) add(role Role, content string) {
	t.AddMessage(role, content)
}

// ── Shell mode ───────────────────────────────────────────────────

func (t *TUI) execShell(cmdStr string) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return
	}
	t.add(RoleTool, "bash "+cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.add(RoleError, err.Error())
	}
	if len(output) > 0 {
		t.AppendToolResult(strings.TrimRight(string(output), "\n"))
	}
}

// ── Compact ──────────────────────────────────────────────────────

func (t *TUI) copyLastAssistant(args []string) {
	t.mu.Lock()
	var content string
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			count := 0
			for i := len(t.messages) - 1; i >= 0; i-- {
				if t.messages[i].Role == RoleAssistant {
					count++
					if count == n {
						content = t.messages[i].Content
						break
					}
				}
			}
		} else {
			t.mu.Unlock()
			t.add(RoleSystem, "用法: /copy [N]  — 复制倒数第 N 条助手回复（默认 1）")
			return
		}
	} else {
		for i := len(t.messages) - 1; i >= 0; i-- {
			if t.messages[i].Role == RoleAssistant {
				content = t.messages[i].Content
				break
			}
		}
	}
	t.mu.Unlock()
	if content == "" {
		t.add(RoleSystem, "没有助手回复可复制。")
		return
	}
	if err := writeClipboard(content); err != nil {
		t.add(RoleError, "复制到剪贴板失败: "+err.Error())
		return
	}
	t.add(RoleSystem, "✓ 已复制最近一条助手回复到剪贴板。")
}

func writeClipboard(text string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("clip")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
}

// copyLastReply copies the most recent assistant message to the system
// clipboard, triggered by Ctrl+Y.
func (t *TUI) copyLastReply() {
	t.mu.Lock()
	var content string
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Role == RoleAssistant {
			content = t.messages[i].Content
			break
		}
	}
	t.mu.Unlock()
	if content == "" {
		t.add(RoleSystem, "没有助手回复可复制。")
		return
	}
	if err := writeClipboard(content); err != nil {
		t.add(RoleError, "复制到剪贴板失败: "+err.Error())
		return
	}
	t.add(RoleSystem, "✓ 已复制最近一条助手回复到剪贴板 (Ctrl+Y)。")
}

// persistSetting loads the user config, applies fn, and saves it back to disk.
// Best-effort: failures are ignored (logged by config.Save on error).
func persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	fn(cfg)
	_ = cfg.Save(config.DefaultPath())
}
