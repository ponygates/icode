package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/plugins"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/core/voice"
	"github.com/ponygates/icode/internal/mesh"
	"github.com/ponygates/icode/internal/xgo"
)

// tryCustomSlash resolves `cmd` (e.g. "/changelog") against the user- and
// project-scoped command registry loaded from .icode/commands/*.md. When a
// match is found, its template is expanded and the result is submitted as a
// regular user message. Returns true if a custom command handled the input.
func (t *TUI) tryCustomSlash(cmd, argStr string) bool {
	reg := slashcmd.CachedLoad(slashcmd.DefaultDirs()...)
	c, ok := reg.Get(cmd)
	if !ok {
		return false
	}
	expanded, err := c.Expand(context.Background(), argStr)
	if err != nil {
		t.add(RoleError, fmt.Sprintf("展开 %s 失败: %v", cmd, err))
		return true
	}
	if strings.TrimSpace(expanded) == "" {
		t.add(RoleSystem, fmt.Sprintf("命令 %s 展开为空", cmd))
		return true
	}

	// Feed the expanded text into the normal submit flow so it is displayed
	// as a user message and streamed through the LLM. Bypass the leading
	// prefix scan (! # /) that the raw submit() runs — the expanded body
	// might legitimately start with any of those characters.
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: fmt.Sprintf("(%s) %s", cmd, argStr)})
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(expanded, nil)
		}()
	}
	t.ensureAnim()
	t.drainStream()
	return true
}

// ── Claude Code-parity command helpers ──────────────────────────

// mcpCommand manages MCP servers (Claude Code's /mcp has list/add/get/remove/
// restart subcommands). Configuration is persisted to the user config file.
func (t *TUI) mcpCommand(args []string) {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleSystem, "无法读取配置: "+err.Error())
		return
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
			b.WriteString("  （无。用 `/mcp add <name> <stdio|sse> <command> [args...]` 添加，\n   或编辑 ~/.icode/config.yaml 的 mcp 段）\n")
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
		t.add(RoleSystem, b.String())
	case "add":
		if len(args) < 4 {
			t.add(RoleSystem, "用法: /mcp add <name> <stdio|sse> <command> [args...]")
			return
		}
		name := args[1]
		typ := strings.ToLower(args[2])
		if typ != "stdio" && typ != "sse" {
			t.add(RoleSystem, "类型只能是 stdio 或 sse")
			return
		}
		mc := config.MCPServerCfg{Name: name, Type: typ, Command: args[3], Enabled: true}
		if len(args) > 4 {
			mc.Args = args[4:]
		}
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		for _, s := range cfg.MCP {
			if s.Name != name {
				filtered = append(filtered, s)
			}
		}
		cfg.MCP = append(filtered, mc)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			t.add(RoleError, "保存失败: "+err.Error())
			return
		}
		// Live-apply: connect in the shared pool and refresh the engine's
		// tool registry so the new server is usable immediately — no restart
		// (Claude Code parity).
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnMCPApply("add", name, mc))
			return
		}
		t.add(RoleSystem, fmt.Sprintf("[x] 已添加 MCP 服务器 %s（%s）。重启 iCode 后生效。", name, typ))
	case "remove":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp remove <name>")
			return
		}
		name := args[1]
		filtered := make([]config.MCPServerCfg, 0, len(cfg.MCP))
		found := false
		for _, s := range cfg.MCP {
			if s.Name != name {
				filtered = append(filtered, s)
			} else {
				found = true
			}
		}
		if !found {
			t.add(RoleSystem, "未找到 MCP 服务器: "+name)
			return
		}
		cfg.MCP = filtered
		if err := cfg.Save(config.DefaultPath()); err != nil {
			t.add(RoleError, "保存失败: "+err.Error())
			return
		}
		// Live-apply: disconnect from the shared pool and unregister its
		// tools immediately — no restart (Claude Code parity).
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnMCPApply("remove", name, config.MCPServerCfg{}))
			return
		}
		t.add(RoleSystem, fmt.Sprintf("[x] 已移除 MCP 服务器 %s。重启 iCode 后生效。", name))
	case "get":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp get <name>")
			return
		}
		name := args[1]
		for _, s := range cfg.MCP {
			if s.Name == name {
				var b strings.Builder
				fmt.Fprintf(&b, "MCP 服务器 %s:\n", name)
				fmt.Fprintf(&b, "  类型: %s\n", s.Type)
				fmt.Fprintf(&b, "  命令: %s\n", s.Command)
				if len(s.Args) > 0 {
					fmt.Fprintf(&b, "  参数: %s\n", strings.Join(s.Args, " "))
				}
				if s.URL != "" {
					fmt.Fprintf(&b, "  地址: %s\n", s.URL)
				}
				fmt.Fprintf(&b, "  启用: %v\n", s.Enabled)
				if s.TrustMode != "" {
					fmt.Fprintf(&b, "  信任模式: %s\n", s.TrustMode)
				}
				t.add(RoleSystem, b.String())
				return
			}
		}
		t.add(RoleSystem, "未找到 MCP 服务器: "+name)
	case "restart":
		if len(args) < 2 {
			t.add(RoleSystem, "用法: /mcp restart <name>")
			return
		}
		t.add(RoleSystem, fmt.Sprintf("已请求重启 %s。MCP 连接于启动时建立，请重启 iCode 使新配置生效。", args[1]))
	default:
		t.add(RoleSystem, "用法: /mcp [list] | add <name> <stdio|sse> <command> [args...] | remove <name> | get <name> | restart <name>")
	}
}

// agentsCommand renders the live agent panel: registered sub-agents with
// their capability flags, teams, and background sub-agent runs (elapsed at
// render time). Claude Code "claude agents" parity.
func (t *TUI) agentsCommand() {
	var b strings.Builder
	b.WriteString("子 agent 注册表:\n")
	reg := agent.Load(agent.AgentDefaultDirs()...)
	reg.RegisterDefaults()
	list := reg.List()
	if len(list) == 0 {
		b.WriteString("  （无）\n")
	}
	for _, d := range list {
		flags := ""
		if d.Fork {
			flags += " fork"
		}
		if d.Memory != "" {
			flags += " memory:" + d.Memory
		}
		if d.Isolation != "" {
			flags += " isolation:" + d.Isolation
		}
		if flags != "" {
			flags = "  [" + strings.TrimSpace(flags) + "]"
		}
		fmt.Fprintf(&b, "  %s — %s%s\n", d.Name, d.Description, t.paint("dim", flags))
	}

	if teams := agent.LoadTeams(agent.TeamDefaultDirs()...); len(teams) > 0 {
		b.WriteString("\n团队:\n")
		for _, tm := range teams {
			names := make([]string, 0, len(tm.Members))
			for _, m := range tm.Members {
				names = append(names, m.Name)
			}
			fmt.Fprintf(&b, "  %s (%d 成员: %s)\n", tm.Name, len(tm.Members), strings.Join(names, ", "))
		}
	}

	if lines := tool.ListAgentTaskLines(); len(lines) > 0 {
		b.WriteString("\n后台运行中:\n")
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}
	t.add(RoleSystem, b.String())
}

// skillEvalCommand runs trigger-accuracy evals: no args → every skill with
// evals.yaml; "<name>" → one skill; "<name> --scaffold" → create a starter
// suite from the skill's triggers.
func (t *TUI) skillEvalCommand(args []string) {
	reg := skills.Load(skills.DefaultDirs()...)
	scaffold := false
	var name string
	for _, a := range args {
		if strings.EqualFold(a, "--scaffold") {
			scaffold = true
		} else {
			name = strings.TrimSpace(a)
		}
	}

	if name == "" && scaffold {
		t.add(RoleSystem, "用法: /skill-eval <技能名> --scaffold")
		return
	}

	if name != "" {
		s, ok := reg.Get(name)
		if !ok {
			t.add(RoleError, fmt.Sprintf("未找到技能 %q（/skills 查看列表）", name))
			return
		}
		if scaffold {
			if err := skills.ScaffoldEval(s); err != nil {
				t.add(RoleError, "脚手架失败: "+err.Error())
				return
			}
			t.add(RoleSystem, fmt.Sprintf("已创建 %s\n编辑用例后运行 /skill-eval %s 验证。", s.EvalPath(), name))
			return
		}
		suite, exists := skills.LoadEval(s)
		if !exists || len(suite.Cases) == 0 {
			t.add(RoleSystem, fmt.Sprintf("技能 %q 还没有 evals.yaml。运行 /skill-eval %s --scaffold 生成模板。", name, name))
			return
		}
		t.add(RoleSystem, renderEvalReport(skills.RunEval(s, suite)))
		return
	}

	reports := skills.RunAllEvals(reg)
	if len(reports) == 0 {
		t.add(RoleSystem, "没有技能携带 evals.yaml。\n用 /skill-eval <名称> --scaffold 为技能生成触发测试模板。")
		return
	}
	var b strings.Builder
	b.WriteString("技能触发自测 (Skill Evals):\n\n")
	totalCases, totalPass := 0, 0
	for _, rep := range reports {
		mark := t.paint("green", "✓")
		if rep.PassRate() < 1 {
			if rep.PassRate() >= 0.5 {
				mark = t.paint("yellow", "!")
			} else {
				mark = t.paint("red", "✗")
			}
		}
		fmt.Fprintf(&b, "  %s %-16s %d/%d 通过\n", mark, rep.SkillName, rep.Passed, rep.Total)
		totalCases += rep.Total
		totalPass += rep.Passed
		for _, c := range rep.Cases {
			if !c.Pass {
				want := "漏触发" // wanted fire, didn't get it
				if !c.WantFire {
					want = "误触发" // didn't want fire, got it
				}
				fmt.Fprintf(&b, "      ✗ [%s] %q\n", want, c.Prompt)
			}
		}
	}
	pct := 100.0
	if totalCases > 0 {
		pct = float64(totalPass) * 100 / float64(totalCases)
	}
	fmt.Fprintf(&b, "\n总计: %d/%d (%.0f%%)\n失败用例旁标注了「应触发/误触发」，据此调整 SKILL.md 的 description 与 triggers 后重跑。", totalPass, totalCases, pct)
	t.add(RoleSystem, b.String())
}

// skillDoctorCommand runs the /skill-doctor health check (A4): frontmatter
// completeness, trigger coverage, name shadowing across interop dirs,
// orphaned user skills and eval-suite status — the Claude Code convention.
func (t *TUI) skillDoctorCommand() {
	reg := skills.Load(skills.DefaultDirs()...)
	rep := skills.Doctor(reg)

	var b strings.Builder
	b.WriteString("技能体检 (Skill Doctor):\n")
	if len(rep.Findings) == 0 {
		b.WriteString("  （无已加载技能。在 ~/.icode/skills/ 或 .icode/skills/ 下放置 SKILL.md 即可启用）\n")
		t.add(RoleSystem, b.String())
		return
	}
	b.WriteString("\n")
	for _, f := range rep.Findings {
		mark := t.paint("green", "✓")
		if f.Sev == skills.SevWarn {
			mark = t.paint("yellow", "!")
		} else if f.Sev == skills.SevError {
			mark = t.paint("red", "✗")
		}
		fmt.Fprintf(&b, "  %s %-22s %s\n", mark, f.Skill, f.Issue)
		if f.Hint != "" {
			fmt.Fprintf(&b, "      └ %s\n", f.Hint)
		}
	}
	if len(rep.Shadowed) > 0 {
		b.WriteString("\n遮蔽（同名技能，后者生效）:\n")
		for _, s := range rep.Shadowed {
			fmt.Fprintf(&b, "  ⚑ %s\n", s)
		}
	}
	if len(rep.Orphans) > 0 {
		fmt.Fprintf(&b, "\n孤儿技能（不在内置市场，来源：导入/远程/历史版本）: %s\n", strings.Join(rep.Orphans, ", "))
	}
	fmt.Fprintf(&b, "\n%s\n", rep.EvalSummary)
	if rep.CatalogNotInstalled > 0 {
		fmt.Fprintf(&b, "市场提示: 还有 %d 个内置技能未安装（桌面端技能市场可一键安装）。\n", rep.CatalogNotInstalled)
	}
	t.add(RoleSystem, b.String())
}

// renderEvalReport formats a single-skill eval run for the chat pane.
func renderEvalReport(rep skills.EvalReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "技能 %q 触发自测: %d/%d 通过 (%.0f%%)\n\n", rep.SkillName, rep.Passed, rep.Total, rep.PassRate()*100)
	for _, c := range rep.Cases {
		mark := "✓"
		if !c.Pass {
			mark = "✗"
		}
		want := "应触发"
		if !c.WantFire {
			want = "不触发"
		}
		got := ""
		if !c.Pass {
			if c.GotFire && !c.WantFire {
				got = " （实际：误触发）"
			} else if !c.GotFire && c.WantFire {
				got = " （实际：未触发）"
			}
		}
		fmt.Fprintf(&b, "  %s [%s] %q%s\n", mark, want, c.Prompt, got)
	}
	return b.String()
}

// pluginCommand manages bundled plugins: list (default), install <dir|.zip>
// [--force], remove <name>.
func (t *TUI) pluginCommand(args []string) {
	if len(args) == 0 || args[0] == "list" {
		list := plugins.List()
		if len(list) == 0 {
			t.add(RoleSystem, "没有已安装插件。安装：/plugin install <目录或.zip>\n插件可捆绑 skills/ commands/ agents/ teams/，一次安装全部生效。")
			return
		}
		var b strings.Builder
		b.WriteString("已安装插件:\n")
		for _, m := range list {
			v := m.Version
			if v == "" {
				v = "-"
			}
			fmt.Fprintf(&b, "  %s  v%s  %s\n", m.Name, v, m.Description)
		}
		b.WriteString("\n卸载：/plugin remove <名称>")
		t.add(RoleSystem, b.String())
		return
	}

	switch strings.ToLower(args[0]) {
	case "install":
		if len(args) < 2 {
			t.add(RoleError, "用法: /plugin install <目录|zip> [--force]")
			return
		}
		force := false
		for _, a := range args[2:] {
			if strings.EqualFold(a, "--force") {
				force = true
			}
		}
		m, err := plugins.Install(args[1], force)
		if err != nil {
			t.add(RoleError, "安装失败: "+err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 插件 %q 已安装。其 skills/commands/agents 立即可用（新会话加载完整清单）。", m.Name))

	case "remove", "uninstall":
		if len(args) < 2 {
			t.add(RoleError, "用法: /plugin remove <名称>")
			return
		}
		if err := plugins.Remove(args[1]); err != nil {
			t.add(RoleError, err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 插件 %q 已卸载。", args[1]))

	default:
		t.add(RoleError, "用法: /plugin [list | install <目录|.zip> [--force] | remove <名称>]")
	}
}

// meshCommand manages cross-machine peers: list (default), add <name> <url>
// [token], remove <name>, token (show local shared secret).
func (t *TUI) meshCommand(args []string) {
	if len(args) == 0 || args[0] == "list" {
		peers, _ := mesh.LoadPeers()
		if len(peers) == 0 {
			tok, _ := mesh.EnsureToken()
			t.add(RoleSystem, fmt.Sprintf("没有已配置的远程机器。\n\n本机 mesh token（复制到对端 /mesh add）:\n%s\n\n添加对端: /mesh add <名称> http://<ip>:<端口> <对端token>\n之后发消息给 \"<名称>/<会话ID>\" 即跨机投递。", tok))
			return
		}
		var b strings.Builder
		b.WriteString("已配置的远程机器:\n")
		b.WriteString(mesh.RenderPeerStatuses(mesh.PingAll(context.Background())))
		b.WriteString("\n发送: 让模型调用 send_message，to 写 \"<名称>/<会话ID>\"；每 3 秒自动转发。")
		b.WriteString("\n接收方需在 config.toml 配置 [server] mesh_listen = \"0.0.0.0:8788\" 开放入站（token 门禁）。")
		t.add(RoleSystem, b.String())
		return
	}

	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 3 {
			t.add(RoleError, "用法: /mesh add <名称> http://<ip>:<端口> [对端token]")
			return
		}
		tok := ""
		if len(args) >= 4 {
			tok = args[3]
		}
		if err := mesh.UpsertPeer(mesh.Peer{Name: args[1], URL: args[2], Token: tok}); err != nil {
			t.add(RoleError, "保存失败: "+err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 对端 %q 已保存。若其 token 留空，请在对方运行 /mesh token 获取后补填。", args[1]))

	case "remove":
		if len(args) < 2 {
			t.add(RoleError, "用法: /mesh remove <名称>")
			return
		}
		ok, err := mesh.RemovePeer(args[1])
		if err != nil {
			t.add(RoleError, err.Error())
			return
		}
		if !ok {
			t.add(RoleError, fmt.Sprintf("对端 %q 不存在", args[1]))
			return
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 对端 %q 已移除。", args[1]))

	case "token":
		tok, err := mesh.EnsureToken()
		if err != nil {
			t.add(RoleError, err.Error())
			return
		}
		// The mesh token is a static shared secret (no rotation mechanism yet);
		// point operators at the manual replacement path so a suspected leak
		// has a documented remedy.
		t.add(RoleSystem, "本机 mesh token（交给对端配置）:\n"+tok+
			"\n\n提示：该 token 为静态共享密钥，不会自动轮换。若怀疑泄漏，直接替换 ~/.icode/mesh.token 并同步更新所有对端的 /mesh add 配置。")

	default:
		t.add(RoleError, "用法: /mesh [list | add <名> <url> [token] | remove <名> | token]")
	}
}

// toggleVoiceRecording starts/stops mic capture from the CLI. First /voice
// begins recording (status bar shows the live indicator); a second /voice
// stops, transcribes via Zhipu GLM-ASR in the background, and drops the
// recognised text into the input buffer for editing before sending.
func (t *TUI) toggleVoiceRecording() {
	t.mu.Lock()
	rec := t.voiceRec
	if rec == nil {
		rec = voice.NewRecorder()
		t.voiceRec = rec
	}
	t.mu.Unlock()

	if rec.Recording() {
		audio, err := rec.Stop()
		if err != nil {
			t.add(RoleError, "停止录音失败: "+err.Error())
			return
		}
		t.add(RoleSystem, fmt.Sprintf("⏹ 录音结束（%d KB），正在转写…", len(audio)/1024))
		xgo.GoSafe("tui.voice.transcribe", func() {
			cfg, err := config.Load()
			if err != nil {
				t.add(RoleError, "配置读取失败: "+err.Error())
				return
			}
			zp := cfg.Providers["zhipu"]
			text, terr := voice.TranscribeZhipu(context.Background(), zp.APIKey, audio, "icode-voice.wav")
			if terr != nil {
				t.add(RoleError, "语音识别失败: "+terr.Error())
				return
			}
			t.mu.Lock()
			t.inputBuf = strings.TrimRight(t.inputBuf, " ") + text + " "
			t.cursor = len([]rune(t.inputBuf))
			t.mu.Unlock()
			t.add(RoleSystem, "🎤 已转写并填入输入框（可编辑后回车发送）:\n"+text)
		})
		return
	}

	if err := rec.Start(); err != nil {
		t.add(RoleError, "无法开始录音: "+err.Error()+"（语音输入目前仅 Windows 支持，桌面端麦克风按钮不受限）")
		return
	}
	t.statusNotice = t.paint("red", "● 录音中") + t.paint("dim", " — 再次输入 /voice 结束并转写")
}
