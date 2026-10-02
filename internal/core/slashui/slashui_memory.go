package slashui

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/executil"
)

func cmdMCP(args []string) Result {
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "", "list":
		var b strings.Builder
		b.WriteString("MCP 服务器:\n")
		if len(cfg.MCP) == 0 {
			b.WriteString("  （无。用 `/mcp add <name> <stdio|sse> <command> [args...]` 添加）\n")
		}
		for _, s := range cfg.MCP {
			en := "✓"
			if !s.Enabled {
				en = "·"
			}
			line := fmt.Sprintf("  %s %s [%s] %s", en, s.Name, s.Type, s.Command)
			if s.URL != "" {
				line += " " + s.URL
			}
			b.WriteString(line + "\n")
		}
		return ok(b.String())
	case "add":
		if len(args) < 4 {
			return ok("用法: /mcp add <name> <stdio|sse> <command> [args...]")
		}
		typ := strings.ToLower(args[2])
		if typ != "stdio" && typ != "sse" {
			return errf("类型只能是 stdio 或 sse，收到: %s", args[2])
		}
		mc := config.MCPServerCfg{Name: args[1], Type: typ, Command: args[3], Enabled: true}
		if len(args) > 4 {
			mc.Args = args[4:]
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		for _, s := range cfg.MCP {
			if s.Name != args[1] {
				filtered = append(filtered, s)
			}
		}
		cfg.MCP = append(filtered, mc)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存失败: %v", err)
		}
		return ok(fmt.Sprintf("已添加 MCP 服务器 %s（%s）。重启 iCode 后生效。", args[1], typ))
	case "remove":
		if len(args) < 2 {
			return ok("用法: /mcp remove <name>")
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		found := false
		for _, s := range cfg.MCP {
			if s.Name != args[1] {
				filtered = append(filtered, s)
			} else {
				found = true
			}
		}
		if !found {
			return errf("未找到 MCP 服务器: %s", args[1])
		}
		cfg.MCP = filtered
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return errf("保存失败: %v", err)
		}
		return ok(fmt.Sprintf("已移除 MCP 服务器 %s。重启 iCode 后生效。", args[1]))
	case "get":
		if len(args) < 2 {
			return ok("用法: /mcp get <name>")
		}
		for _, s := range cfg.MCP {
			if s.Name == args[1] {
				var b strings.Builder
				fmt.Fprintf(&b, "MCP 服务器 %s:\n", args[1])
				fmt.Fprintf(&b, "  类型: %s\n", s.Type)
				fmt.Fprintf(&b, "  命令: %s\n", s.Command)
				if len(s.Args) > 0 {
					fmt.Fprintf(&b, "  参数: %s\n", strings.Join(s.Args, " "))
				}
				if s.URL != "" {
					fmt.Fprintf(&b, "  地址: %s\n", s.URL)
				}
				fmt.Fprintf(&b, "  启用: %v\n", s.Enabled)
				return ok(b.String())
			}
		}
		return errf("未找到 MCP 服务器: %s", args[1])
	case "restart":
		if len(args) < 2 {
			return ok("用法: /mcp restart <name>")
		}
		return ok(fmt.Sprintf("已请求重启 %s。MCP 连接于启动时建立，请重启 iCode 使新配置生效。", args[1]))
	default:
		return ok("用法: /mcp [list] | add <name> <stdio|sse> <command> [args...] | remove <name> | get <name> | restart <name>")
	}
}

func cmdMemory(b *Backend, args []string) Result {
	proj := projectMemoryPath()
	user, _ := projectcontext.UserMemoryPath()
	if len(args) == 0 {
		return memoryOverview(b, proj, user)
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "edit":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		cmd := executil.Command(editor, proj)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return errf("打开编辑器失败: %v", err)
		}
		return ok(fmt.Sprintf("已用 %s 打开项目记忆 %s", editor, proj))
	case "prefs", "preference", "preferences":
		return memoryPrefs(b, args[1:])
	case "list":
		return memoryPrefs(b, args[1:])
	case "forget", "rm":
		return memoryForget(b, args[1:])
	case "clear":
		return memoryClear(b, args[1:])
	default:
		// Unknown subcommand — treat as a raw append to the project memory file
		// (legacy behavior) so `# <content>`-style calls keep working.
		return memoryOverview(b, proj, user)
	}
}

// memoryOverview shows both the ICODE.md memory files and the remembered
// preference store.
func memoryOverview(b *Backend, proj, user string) Result {
	var bld strings.Builder
	bld.WriteString(fmt.Sprintf("记忆文件:\n  项目级: %s\n  用户级: %s\n\n", proj, user))
	if data, err := os.ReadFile(proj); err == nil && len(data) > 0 {
		bld.WriteString("── 项目记忆 (ICODE.md) ──\n" + string(data) + "\n")
	} else {
		bld.WriteString("（项目记忆为空，用 `# <内容>` 追加，或 `/memory edit` 编辑）\n")
	}
	prefs := memoryPrefs(b, nil)
	if strings.TrimSpace(prefs.Output) != "" {
		bld.WriteString("\n" + prefs.Output)
	}
	return ok(bld.String())
}

// memoryPrefs lists the remembered USER PREFERENCES (prefmem). Unlike the
// ICODE.md files these are learned automatically and only ever hold short
// preference statements — never code.
func memoryPrefs(b *Backend, _ []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	entries := b.Engine.PreferenceMemory().Snapshot()
	if len(entries) == 0 {
		return ok("（暂无已记忆的用户偏好。说“以后都用…/优先用…”会自动记住。）")
	}
	var bld strings.Builder
	bld.WriteString("已记忆的用户偏好 (prefmem):\n")
	for _, e := range entries {
		seen := ""
		if e.Seen > 1 {
			seen = fmt.Sprintf("  (提到 %d 次)", e.Seen)
		}
		bld.WriteString(fmt.Sprintf("  · %s%s\n", e.Text, seen))
	}
	bld.WriteString("\n管理: /memory forget <内容>  清除单条；/memory clear 清空全部；/memory list 查看。")
	return ok(bld.String())
}

// memoryForget removes a single remembered preference matching the argument
// text (substring match).
func memoryForget(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	if len(args) == 0 {
		return errf("用法: /memory forget <偏好内容片段>")
	}
	needle := strings.Join(args, " ")
	store := b.Engine.PreferenceMemory()
	removed := false
	for _, e := range store.Snapshot() {
		if strings.Contains(e.Text, needle) {
			store.Forget(e.Text)
			removed = true
		}
	}
	if removed {
		_ = store.SaveFile(prefmem.DefaultPath())
		return ok(fmt.Sprintf("已遗忘 %d 条与 “%s” 相关的偏好。", 1, needle))
	}
	return ok(fmt.Sprintf("没有找到与 “%s” 匹配的偏好。用 /memory list 查看。", needle))
}

// memoryClear wipes all remembered preferences (both memory and the persisted
// file). Pass "yes" to skip the confirmation prompt.
func memoryClear(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil || b.Engine.PreferenceMemory() == nil {
		return ok("偏好记忆未启用。")
	}
	confirm := ""
	if len(args) > 0 {
		confirm = strings.ToLower(args[0])
	}
	if confirm != "yes" && confirm != "y" {
		return ok("确认清空全部偏好记忆？执行 /memory clear yes")
	}
	n := b.Engine.PreferenceMemory().Purge()
	_ = b.Engine.PreferenceMemory().SaveFile(prefmem.DefaultPath())
	return ok(fmt.Sprintf("已清空 %d 条偏好记忆。", n))
}
