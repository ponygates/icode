package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/spf13/cobra"
)

// icode mcp — Claude Code `claude mcp` parity: manage MCP servers from the
// shell instead of hand-editing config.yaml. list/add/remove round-trip the
// same MCPServerCfg entries the desktop settings UI and /mcp command use,
// and take effect on the next session (or immediately via the TUI's /mcp).

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "管理 MCP（Model Context Protocol）服务器",
	Long: `管理 MCP 服务器配置（Claude Code ` + "`claude mcp`" + ` 对等实现）。

示例:
  icode mcp list
  icode mcp add filesystem -- npx -y @modelcontextprotocol/server-filesystem /data
  icode mcp add github --url https://mcp.example.com/sse --trust readonly
  icode mcp remove filesystem

新增或删除后，新会话自动生效；运行中的交互会话可用 /mcp 动态刷新。`,
}

var mcpAddCmd = &cobra.Command{
	Use:   "add <name> [--url <url> | -- <command> [args...]]",
	Short: "添加或更新一个 MCP 服务器",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return fmt.Errorf("服务器名称不能为空")
		}
		url, _ := cmd.Flags().GetString("url")
		trust, _ := cmd.Flags().GetString("trust")
		m := config.MCPServerCfg{Name: name, Enabled: true, TrustMode: trust}

		if u := strings.TrimSpace(url); u != "" {
			m.Type = "sse"
			m.URL = u
		} else {
			m.Type = "stdio"
			command, cmdArgs, err := parseMCPAddArgs(args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}
			if containsShellMetachars(command) {
				return fmt.Errorf("启动命令不能包含 shell 元字符（请写可执行文件本身，参数走 args）: %s", command)
			}
			m.Command = command
			m.Args = cmdArgs
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		replaced := false
		for _, mc := range cfg.MCP {
			if mc.Name == name {
				replaced = true
				break
			}
		}
		cfg.UpsertMCP(m)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		if replaced {
			fmt.Printf("已更新 MCP 服务器 %s\n", name)
		} else {
			fmt.Printf("已添加 MCP 服务器 %s\n", name)
		}
		fmt.Println("新会话自动生效；运行中的会话可用 /mcp 动态刷新。")
		return nil
	},
}

var mcpRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "移除一个 MCP 服务器",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		found := false
		for _, mc := range cfg.MCP {
			if mc.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("未找到名为 %q 的 MCP 服务器（icode mcp list 查看已配置项）", name)
		}
		cfg.RemoveMCP(name)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("已移除 MCP 服务器 %s\n", name)
		return nil
	},
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出已配置的 MCP 服务器",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if len(cfg.MCP) == 0 {
			fmt.Println("尚未配置任何 MCP 服务器。示例: icode mcp add fs -- npx -y @modelcontextprotocol/server-filesystem /data")
			return nil
		}
		entries := append([]config.MCPServerCfg(nil), cfg.MCP...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		fmt.Printf("%-16s %-6s %-10s %-8s %s\n", "NAME", "TYPE", "ENABLED", "TRUST", "ENDPOINT")
		for _, mc := range entries {
			endpoint := mc.URL
			if endpoint == "" {
				endpoint = strings.Join(append([]string{mc.Command}, mc.Args...), " ")
			}
			enabled := "yes"
			if !mc.Enabled {
				enabled = "no"
			}
			trust := mc.TrustMode
			if trust == "" {
				trust = "all"
			}
			fmt.Printf("%-16s %-6s %-10s %-8s %s\n", mc.Name, mc.Type, enabled, trust, truncate(endpoint, 60))
		}
		return nil
	},
}

func init() {
	mcpAddCmd.Flags().String("url", "", "SSE 服务器端点（与 stdio 命令二选一）")
	mcpAddCmd.Flags().String("trust", "", "信任级别: ask（每次确认）| readonly（只读工具自动放行）| all（默认，不限制）")
	mcpCmd.AddCommand(mcpAddCmd, mcpRemoveCmd, mcpListCmd)
	rootCmd.AddCommand(mcpCmd)
}

// parseMCPAddArgs extracts the stdio launch command after the "--" separator:
//
//	icode mcp add fs -- npx -y @mcp/filesystem /data
//
// args holds the positional arguments and dash is cobra's ArgsLenAtDash()
// (the index into args where "--" appeared, or -1 when absent).
func parseMCPAddArgs(args []string, dash int) (command string, cmdArgs []string, err error) {
	if dash < 0 || dash >= len(args) {
		return "", nil, fmt.Errorf("stdio 服务器需要 '--' 后跟启动命令，例如:\n  icode mcp add fs -- npx -y @modelcontextprotocol/server-filesystem /data\n或改用 --url <endpoint> 添加 SSE 服务器")
	}
	rest := args[dash:]
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return "", nil, fmt.Errorf("'--' 后缺少启动命令")
	}
	command = rest[0]
	if len(rest) > 1 {
		cmdArgs = append([]string(nil), rest[1:]...)
	}
	return command, cmdArgs, nil
}

// containsShellMetachars rejects obvious shell injection in the stdio launch
// command: the command is exec'd DIRECTLY (no shell), so metacharacters are
// always a configuration mistake, not an exploit path.
func containsShellMetachars(command string) bool {
	return strings.ContainsAny(command, ";|&`$") ||
		strings.Contains(command, "\n") ||
		strings.Contains(command, "..")
}
