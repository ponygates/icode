package slashui

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/privacy"
	"github.com/ponygates/icode/internal/mesh"
)

func cmdKeys() Result {
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	var b strings.Builder
	b.WriteString("API 密钥状态:\n")
	for name, pc := range cfg.Providers {
		st := "未配置"
		if pc.APIKey != "" {
			st = "已配置"
		}
		b.WriteString(fmt.Sprintf("  %-14s %s\n", name, st))
	}
	b.WriteString("\n用 `icode config key <provider> <key>` 或 设置 → 提供商 配置。")
	return ok(b.String())
}

func cmdSecurity(b *Backend, st *State, args []string) Result {
	valid := map[string]config.SecurityLevel{
		"local":        config.SecLocal,
		"desensitize":  config.SecDesensitize,
		"local-llm":    config.SecLocalLLM,
		"foreign-llm":  config.SecForeignLLM,
		"unrestricted": config.SecUnrestricted,
	}
	if len(args) == 0 {
		sec := st.Security
		if sec == "" {
			if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
				sec = string(c.SecurityLevel)
			} else {
				sec = "local"
			}
		}
		return ok(fmt.Sprintf("当前安全等级: %s\n用法: /security [local|desensitize|local-llm|foreign-llm|unrestricted]",
			permission.SecurityLabel(config.SecurityLevel(sec))))
	}
	newLevel := strings.ToLower(args[0])
	level, validLevel := valid[newLevel]
	if !validLevel {
		return errf("无效安全等级: %s", args[0])
	}
	persistSetting(func(c *config.Config) { c.SecurityLevel = level })
	return Result{Output: "安全等级已设为 " + permission.SecurityLabel(level), Security: newLevel}
}

func cmdPermissions(b *Backend, st *State, args []string) Result {
	// /permissions reload — re-read permission rules from disk and push them
	// into the live gate so changes take effect mid-turn (Claude Code parity:
	// permission changes apply to the rest of the current turn).
	if len(args) > 0 && strings.EqualFold(args[0], "reload") {
		if b == nil || b.Gate == nil {
			return ok("权限门未初始化。")
		}
		cfg, _ := config.Load()
		if cfg == nil {
			return ok("无可用配置。")
		}
		var rules []permission.ParamRule
		for _, r := range cfg.Permission.Rules {
			d := permission.Decision(strings.ToLower(strings.TrimSpace(r.Decision)))
			if d != permission.DecisionAllow && d != permission.DecisionDeny && d != permission.DecisionAsk {
				continue
			}
			rules = append(rules, permission.ParamRule{Pattern: r.Pattern, Decision: d})
		}
		b.Gate.SetParamRules(rules)
		if wd, err := os.Getwd(); err == nil {
			b.Gate.SetClaudeSettings(permission.LoadClaudeSettings(wd))
		}
		b.Gate.SetAllowedPaths(cfg.Tools.AllowedPaths)
		b.Gate.SetDeniedCommands(cfg.Tools.DeniedCommands)
		b.Gate.SetMode(permission.Mode(cfg.Defaults.Mode))
		return ok(fmt.Sprintf("权限规则已重载并即时生效（%d 条参数规则，安全等级 %s）。",
			len(rules), permission.SecurityLabel(cfg.SecurityLevel)))
	}
	cfg, _ := config.Load()
	var sb strings.Builder
	sb.WriteString("权限 / 安全:\n")
	sec := st.Security
	if sec == "" {
		if cfg != nil && cfg.SecurityLevel != "" {
			sec = string(cfg.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	sb.WriteString(fmt.Sprintf("  当前安全等级: %s\n", permission.SecurityLabel(config.SecurityLevel(sec))))
	if cfg != nil {
		if len(cfg.Hooks) > 0 {
			sb.WriteString("  生命周期钩子:\n")
			for ev, rules := range cfg.Hooks {
				for _, r := range rules {
					sb.WriteString(fmt.Sprintf("    %-12s %s\n", ev, r.Command))
				}
			}
		}
	}
	sb.WriteString("\n切换安全等级: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n工具级允许/拒绝请在 设置 → 工具权限 中配置。")
	return ok(sb.String())
}

func cmdAddDir(b *Backend, args []string) Result {
	if len(args) == 0 {
		var list []string
		if c, err := config.Load(); err == nil {
			list = c.Defaults.ExtraDirs
		}
		if len(list) == 0 {
			return ok("用法: /add-dir <目录> — 添加额外工作目录。当前无额外目录。")
		}
		return ok("当前额外工作目录:\n  " + strings.Join(list, "\n  "))
	}
	dir := args[0]
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errf("解析路径失败: %v", err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return errf("目录不存在或不是文件夹: %s", abs)
	}
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	for _, d := range cfg.Defaults.ExtraDirs {
		if d == abs {
			return ok("该目录已在工作目录列表中: " + abs)
		}
	}
	cfg.Defaults.ExtraDirs = append(cfg.Defaults.ExtraDirs, abs)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存配置失败: %v", err)
	}
	if b != nil && b.Engine != nil {
		b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	}
	return ok(fmt.Sprintf("已添加工作目录: %s（共 %d 个，已注入上下文）", abs, len(cfg.Defaults.ExtraDirs)))
}

func cmdUpdate(b *Backend) Result {
	if b == nil || b.RefreshModels == nil {
		return errf("模型刷新服务未初始化。")
	}
	updates, err := b.RefreshModels(context.Background())
	if err != nil && len(updates) == 0 {
		return errf("刷新失败: %v", err)
	}
	var sb strings.Builder
	sb.WriteString("模型目录刷新结果:\n")
	okN, fail := 0, 0
	for _, u := range updates {
		if u.Success {
			okN++
			sb.WriteString(fmt.Sprintf("  ✓ %-14s %d 个模型（%s）\n", u.Name, u.Count, u.Source))
		} else {
			fail++
			msg := u.Error
			if msg == "" {
				msg = "未知错误"
			}
			sb.WriteString(fmt.Sprintf("  ✗ %-14s %s\n", u.Name, msg))
		}
	}
	sb.WriteString(fmt.Sprintf("成功 %d · 失败 %d", okN, fail))
	return ok(sb.String())
}

func cmdDoctor(b *Backend, st *State) Result {
	var sb strings.Builder
	sb.WriteString("iCode 诊断:\n")
	sb.WriteString(fmt.Sprintf("  版本:       %s\n", shortStr(st.Version, "dev")))
	sb.WriteString(fmt.Sprintf("  模型:       %s\n", shortStr(st.Model, "未设置")))
	sb.WriteString(fmt.Sprintf("  提供商:     %s\n", shortStr(st.Provider, "未设置")))
	sec := st.Security
	if sec == "" {
		if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
			sec = string(c.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	sb.WriteString(fmt.Sprintf("  安全等级:   %s\n", permission.SecurityLabel(config.SecurityLevel(sec))))
	cfg, err := config.Load()
	if err != nil {
		sb.WriteString("  配置:       读取失败 " + err.Error() + "\n")
	} else {
		configured := 0
		for _, pc := range cfg.Providers {
			if pc.APIKey != "" {
				configured++
			}
		}
		sb.WriteString(fmt.Sprintf("  已配置 Key: %d 个提供商\n", configured))
	}
	if proj := projectMemoryPath(); proj != "" {
		sb.WriteString("  项目记忆:   " + proj + "\n")
	}
	if user, err := projectcontext.UserMemoryPath(); err == nil {
		sb.WriteString("  用户记忆:   " + user + "\n")
	}
	return ok(sb.String())
}

func cmdAdmin(b *Backend, args []string) Result {
	if len(args) == 0 || (len(args) > 0 && strings.ToLower(args[0]) != "off") {
		if b != nil && b.Gate != nil {
			b.Gate.SetMode(permission.Mode("yolo"))
		}
		return Result{Output: "管理员模式开启（yolo 模式，不再逐一询问工具权限）", Mode: "yolo"}
	}
	if b != nil && b.Gate != nil {
		b.Gate.SetMode(permission.Mode("ask"))
	}
	persistSetting(func(c *config.Config) { c.Defaults.Mode = "ask" })
	return Result{Output: "管理员模式已关闭，恢复 ask 模式", Mode: "ask"}
}

// ── Helpers ───────────────────────────────────────────────────────

// cmdMesh manages cross-machine message peers (server/desktop surface).
func cmdMesh(args []string, ctx context.Context) Result {
	if len(args) == 0 || args[0] == "list" {
		peers, _ := mesh.LoadPeers()
		if len(peers) == 0 {
			tok, _ := mesh.EnsureToken()
			return ok(fmt.Sprintf("没有已配置的远程机器。\n本机 mesh token:\n%s\n\n添加对端: /mesh add <名称> http://<ip>:<端口> <对端token>", tok))
		}
		var b strings.Builder
		b.WriteString("已配置的远程机器:\n")
		b.WriteString(mesh.RenderPeerStatuses(mesh.PingAll(ctx)))
		b.WriteString("\n发送: to 写 \"<名称>/<会话ID>\"；接收方需配置 [server] mesh_listen。")
		return ok(b.String())
	}
	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 3 {
			return errf("用法: /mesh add <名称> http://<ip>:<端口> [对端token]")
		}
		tok := ""
		if len(args) >= 4 {
			tok = args[3]
		}
		if err := mesh.UpsertPeer(mesh.Peer{Name: args[1], URL: args[2], Token: tok}); err != nil {
			return errf("保存失败: %v", err)
		}
		return ok("✓ 对端 " + args[1] + " 已保存。发消息 to 写 \"<名称>/<会话ID>\" 即跨机投递。")
	case "remove":
		if len(args) < 2 {
			return errf("用法: /mesh remove <名称>")
		}
		found, err := mesh.RemovePeer(args[1])
		if err != nil {
			return errf("%v", err)
		}
		if !found {
			return errf("对端 %q 不存在", args[1])
		}
		return ok("✓ 对端 " + args[1] + " 已移除。")
	case "token":
		tok, err := mesh.EnsureToken()
		if err != nil {
			return errf("%v", err)
		}
		return ok("本机 mesh token（交给对端配置）:\n" + tok)
	default:
		return errf("用法: /mesh [list | add <名> <url> [token] | remove <名> | token]")
	}
}

// cmdBugZip assembles a sanitized diagnostics bundle: version, recent logs,
// config with every key/secret masked, OS info — one zip to attach to an issue.
func cmdBugZip(st *State) Result {
	name := fmt.Sprintf("icode-diagnostics-%s.zip", time.Now().Format("20060102-150405"))
	zf, err := os.Create(name)
	if err != nil {
		return errf("创建诊断包失败: %v", err)
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	defer zw.Close()

	add := func(name string, data []byte) {
		f, err := zw.Create(name)
		if err != nil {
			return
		}
		_, _ = f.Write(data)
	}

	// 1. version + environment
	ver := "unknown"
	if st != nil {
		ver = st.Version
	}
	add("version.txt", []byte(fmt.Sprintf("version: %s\nos: %s/%s\ntime: %s\n",
		ver, runtime.GOOS, runtime.GOARCH, time.Now().Format(time.RFC3339))))

	// 2. config (sanitized: every field whose name hints at a secret is masked)
	if data, err := os.ReadFile(config.DefaultPath()); err == nil {
		add("config.sanitized.yaml", []byte(sanitizeConfig(string(data))))
	}

	// 3. recent logs (last 200 lines each)
	home, _ := os.UserHomeDir()
	for _, logName := range []string{"desktop.log"} {
		if data, err := os.ReadFile(filepath.Join(home, ".icode", logName)); err == nil {
			// Redact again on the way out: logs written before the log-pipe
			// redaction existed may still hold plaintext keys on disk.
			add("logs/"+logName, []byte(privacy.RedactSecrets(tailLines(string(data), 200))))
		}
	}

	abs, _ := filepath.Abs(name)
	return ok(fmt.Sprintf("✓ 诊断包已生成: %s\n已脱敏（API key 全部打码）；直接附到 GitHub issue 即可。", abs))
}
