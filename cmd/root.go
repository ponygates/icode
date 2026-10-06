// Package cmd provides the Cobra CLI entry points for iCode.
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/privacy"
	"github.com/ponygates/icode/internal/update"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	appVersion string
	appBuild   string
	appCommit  string

	// weAllocatedConsole is true when Execute() created a fresh console for a
	// double-clicked GUI-subsystem binary. It lets us keep that console window
	// open (pause on exit) so the user can read any error instead of the window
	// vanishing the instant the process ends.
	weAllocatedConsole bool
)

// ExecuteDesktop launches the desktop-only build.
// version/build/commit come from the main package (set via ldflags) so the
// desktop binary reports the same version as the CLI instead of a stale
// hardcoded constant.
func ExecuteDesktop(version, build, commit string) (err error) {
	appVersion = version
	appBuild = build
	appCommit = commit
	// Safety net below bootDesktopBackend's own filtered redirect: if the
	// rotating writer fails to open, stdlib log still exits through redaction.
	log.SetOutput(privacy.NewRedactingWriter(os.Stderr))
	// The backend points os.Stderr at a raw *os.File (the runtime needs a real
	// descriptor for its own traceback writes), so an unrecovered panic would
	// append its message — which can carry an Authorization header from a
	// provider error — to desktop.log unredacted. Recovering here routes it
	// through the filtered logger instead.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[desktop] 未处理的内部错误: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("icode 桌面端内部错误: %v", r)
		}
	}()
	return runDesktop()
}

// Execute is the main entry point for the CLI.
func Execute(version, build, commit string) (err error) {
	appVersion = version
	appBuild = build
	appCommit = commit

	// Single choke point for the CLI's stdlib log: every log.Printf in
	// server/acp/engine/etc. exits through os.Stderr here, so a provider
	// error echoing an Authorization header can no longer land in a terminal
	// capture or redirected log file in the clear. (The desktop backend
	// overrides this with its own filtered rotating writer.)
	log.SetOutput(privacy.NewRedactingWriter(os.Stderr))

	// Keep a fresh double-click console window open on a fatal error/panic so
	// the user can read what went wrong instead of the window flashing away.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("icode 内部错误: %v", r)
			fmt.Fprintf(os.Stderr, "\n⚠ 发生了内部错误: %v\n", r)
		}
		// Flush the ICODE_TEE=1 byte-mirror (stdout_tee.go) before exit so
		// the trace tail survives; no-op when the tee is not active.
		teeDrain()
		if err != nil && weAllocatedConsole {
			fmt.Fprintln(os.Stderr, "\n按 Enter 键退出...")
			_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		}
	}()

	// The CLI binary links as a CONSOLE-subsystem app on Windows (see
	// build.bat): a GUI-subsystem binary breaks the console IME bridge —
	// raw pinyin letters leak into stdin and IME commits get echoed straight
	// into the screen buffer. setupConsoleIO is kept as a compatibility
	// shim: with inherited console handles (the normal console-subsystem
	// case) it returns true immediately; it only does real work for
	// GUI-subsystem builds (e.g. icode-desktop.exe sharing this package).
	consoleReady := setupConsoleIO()

	// No console AND no CLI arguments means a genuine Explorer double-click.
	// Allocate a fresh console and run the enhanced TUI (CLI). The desktop app
	// stays reachable via `icode desktop` / `icode-desktop.exe` (the
	// windows+desktop_only build). Only if allocation fails do we fall back to
	// the desktop app.
	if !consoleReady && len(os.Args) <= 1 {
		if allocConsole() {
			weAllocatedConsole = true
			fixConsoleCodepage()
			// Opt-in byte-level forensic mirror of everything this process
			// writes to the terminal (ICODE_TEE=1) — see stdout_tee.go.
			teeStdio()
			return rootCmd.Execute()
		}
		return runDesktop()
	}

	// Console available: run the TUI. (The desktop app is a separate binary —
	// see -tags desktop_only — so this CLI binary never auto-launches a GUI
	// window on double-click.)
	fixConsoleCodepage()
	teeStdio()
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "icode",
	Short: i18n.Tr("app.tagline"),
	Long: fmt.Sprintf(`%s — %s

多模型 AI 编程 Agent：
  • 14 家内置 LLM 提供商（DeepSeek / 智谱 / Kimi / Anthropic…60+ 模型），任意 OpenAI 兼容端点即插即用
  • 缓存优先 token 优化，最高节省 %.0f%%
  • 原生中文界面（zh-CN / zh-TW / en）
  • 终端 TUI + 桌面版（WebView2）+ VS Code 扩展三端统一

直接运行 'icode'（或双击可执行文件）即进入交互式对话；
'icode chat' 等效；'icode --help' 查看全部命令。
`, i18n.Tr("app.name"), i18n.Tr("app.tagline"), 94.0),
	Version: appVersion,
	// When launched with no subcommand — e.g. by double-clicking the
	// executable — drop straight into the chat so the app actually runs
	// instead of printing help and immediately closing the window.
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		// Non-interactive print mode (Claude Code `-p` parity): run a single
		// prompt to completion, print the result, exit. Scripts/CI friendly.
		prompt, _ := cmd.Flags().GetString("print")
		if cmd.Flags().Changed("print") {
			// Empty --print + piped stdin reads the prompt from the pipe
			// (Claude Code `claude -p < file` parity): `cat err.log | icode -P ""`.
			if strings.TrimSpace(prompt) == "" {
				if term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("--print 需要一个 prompt 参数，或通过管道提供（例如: cat err.log | icode -P \"\"）")
				}
				b, rerr := io.ReadAll(os.Stdin)
				if rerr != nil {
					return fmt.Errorf("read stdin: %w", rerr)
				}
				prompt = string(b)
			}
			if strings.TrimSpace(prompt) != "" {
				outFmt, _ := cmd.Flags().GetString("output-format")
				cont, _ := cmd.Flags().GetBool("continue")
				resumeID, _ := cmd.Flags().GetString("resume")
				mode, _ := cmd.Flags().GetString("mode")
				allowed, _ := cmd.Flags().GetString("allowedTools")
				disallowed, _ := cmd.Flags().GetString("disallowedTools")
				return runPrintMode(prompt, outFmt, cont, resumeID, provider, model, mode, allowed, disallowed)
			}
		}
		return startChat(provider, model, "")
	},
}

func init() {
	// Clean up the .old binary left by a previous `icode upgrade`.
	update.CleanupOldBinary()

	// Only flags the user actually typed join the cli settings layer; a flag
	// sitting on its default must not outrank the repository's own config.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		values := map[string]string{}
		for _, name := range []string{"lang", "provider", "model", "mode"} {
			if cmd.Flags().Changed(name) {
				if v, _ := cmd.Flags().GetString(name); v != "" {
					values[name] = v
				}
			}
		}
		config.SetCLILayer(config.CLIOverlayFromFlags(values))
		return nil
	}

	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(desktopCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(acpCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(upgradeCmd)

	// Persistent flags
	rootCmd.PersistentFlags().StringP("lang", "l", "zh-CN", "界面语言（zh-CN、zh-TW、en）")
	rootCmd.PersistentFlags().StringP("provider", "p", "", "默认 LLM 提供商")
	rootCmd.PersistentFlags().StringP("model", "m", "", "默认模型 ID")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "输出详细日志")
	// Print (non-interactive) mode — Claude Code `-p` parity. Note: -p is
	// already bound to --provider, so print uses -P / --print to avoid
	// breaking existing scripts.
	rootCmd.PersistentFlags().StringP("print", "P", "", "打印模式：非交互执行单次 prompt，输出结果后退出")
	rootCmd.PersistentFlags().String("output-format", "text", "打印模式输出格式：text | json | stream-json")
	rootCmd.PersistentFlags().BoolP("continue", "c", false, "打印模式下继续最近一次会话")
	rootCmd.PersistentFlags().String("resume", "", "打印模式下恢复指定会话 ID")
	rootCmd.PersistentFlags().String("mode", "", "权限模式：plan | agent | auto | yolo")
	rootCmd.PersistentFlags().String("allowedTools", "", "打印模式：允许使用的工具白名单（逗号分隔，支持前缀通配 mcp__*；白名单外的工具将被拒绝）")
	rootCmd.PersistentFlags().String("disallowedTools", "", "打印模式：禁用的工具黑名单（逗号分隔，支持前缀通配；优先级高于白名单）")
}
