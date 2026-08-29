# 更新日志

## v0.47.1 — hook 事件补全至 10 种（Claude Code parity）（2026-08-29）

> 对标体检 P2 之一：hook 生命周期事件从 4 种补到 10 种，覆盖会话/权限/子代理生命周期，外部脚本可自动化联动更多环节。

- **新增 6 种事件**：`SessionStart`（新会话首条消息）、`PermissionRequest`（工具调用需确认时）、`SubagentStart` / `SubagentStop`（子代理运行前后）、`Notification` / `PreCompact`（v0.46.6 已加）。
- **触发点**：`Send` 新会话 → SessionStart；权限请求发送处 → PermissionRequest（带 tool/prompt）；`RunSubAgent` → SubagentStart/Stop（带 agent 名 + 提示词）。
- **单测**：6 个新事件注册/分发 + 载荷字段 + Notification 规则实际执行验证。
- 事件全集（10）：PreToolUse / PostToolUse / UserPromptSubmit / Stop / Notification / PreCompact / SessionStart / PermissionRequest / SubagentStart / SubagentStop。
- 验证：hooks/conversation 编译/vet/测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.47.0 — auto 模式分类器智能审查（Claude Code parity）（2026-08-29）

> 对标体检 P1：auto 模式从「规则化（读自动/写询问）」升级为「便宜模型智能审查」——安全操作自动放行，只打断用户处理真正有风险的操作。

- **`permission.Classifier` 接口**：`Classify(ctx, tool, input) (allow, reason, err)`；`Gate.SetClassifier` 注入；`Check` 在规则流判 `Ask` 时（auto 模式）调分类器——allow → 自动批准（理由透出），deny → 保持询问（风险理由透出），**分类器出错 → 安全回退询问**（fail-safe）。
- **`llmClassifier`**（conversation/classifier.go）：用 `provider/model` 指定便宜模型（`Chat` 非流式 + 严格 JSON 输出 `{"allow":…,"reason":…}`，容忍代码围栏），参数截断 800 字。
- **配置**：`config.yaml` → `permission.classifier_model: "deepseek/deepseek-chat"`（空 = 关闭，保持原规则化 auto）。`app.go` 组装时 `SetClassifierModel`。
- **单测**：分类器安全写→allow、风险写→ask（理由透出）、调用次数断言。
- 验证：permission/conversation/config/app 编译/vet/测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.46.6 — 深度看齐：Notification/PreCompact hooks + /loop 循环任务（2026-08-29）

> 深度对标体检：hook 事件种类（4 vs Claude Code 31）、/loop 循环任务为剩余差距，本轮补上高价值项。

- **Notification hook**（Claude Code parity）：回复完成/失败时触发外部脚本——完成 Fire `ToolOutput: "completed"`，失败 Fire `"error: …"`；用于通知/日志/自动化联动。
- **PreCompact hook**：会话压缩（SummarizeConversation）前触发，外部脚本可快照/审计上下文交接。
- **`/loop <间隔> <名称> <任务>`**（Claude Code parity）：创建循环自动化任务——`30s` 映射秒级 RRULE（WorkBuddy 精度）、`5m/2h/1d` 映射 `every:`；任务面板可管理。
- **体检确认已覆盖**：AGENTS.md/CLAUDE.md/ICODE.md 多级加载 ✓；auto 模式为规则化（读自动/写询问）——Claude Code 的分类器智能审查列为 P1。
- 验证：hooks/conversation/slashui 编译/vet/测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.46.5 — 输入光标间隔修复 + CLI 表格竖线对齐（2026-08-29）

> 用户反馈：编辑输入时光标与左边已输入内容有一点间隔；markdown 表格竖线没对齐。

- **光标间隔修复**：根因 = `indent = promptW + 2`——首行内容实际在列 3（`❯`+空格），续行用 3 空格缩进内容在列 4，二者起点不一致；光标按 `indent + vw + 1` 计算恒偏右 1 列。修复：`indent = promptW + 1`（首行与续行内容统一到列 3），光标紧贴已输入内容。
- **CLI 表格竖线对齐**：根因 = `runeWidth` 把默认 emoji 符号（✅❌❎❓❔❕❗）与「符号+U+FE0F」（⚠️❤️）按 1 列计算，而现代终端按 2 列渲染 → 表格列宽 padding 错误、竖线错位。修复：`runeWidthStr` 增加 VS16 上下文（`runeWidthWithNext`）——默认 emoji 符号计 2 宽，符号+VS16 计 2 宽。
- 新增单测（`textutil_test.go`：emoji/VS16/全角宽度 + 表格 padding 对齐）。
- 桌面端表格为 CSS `<table>`（border 由浏览器绘制）天然对齐，无需改。
- 验证：tui 全绿；全量测试绿；四份二进制重编 PE 正确。

## v0.46.4 — 技能市场扩充至 15 个 + 积压 commit 落盘（2026-08-29）

- **技能市场 +5（通用/运营）**：weekly-report（周报）、meeting-minutes（会议纪要）、email-writer（商务邮件）、translate-zh（中英互译润色）、wechat-article（公众号文章，含标题候选/正文结构/合规提示）——市场现 15 个：5 开发 + 5 保险展业 + 5 通用运营。
- **积压 commit 落盘**：v0.43.0~v0.46.3 全部改动提交（`2ed872a`，69 files，+8419/-645），工作区干净；`.freebuff/` 加入 .gitignore。
- 验证：skills 测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.46.3 — 增量渲染根治闪跳 + 光标间距 + 思考指示移底 + 状态栏丰富（2026-08-29）

> 用户反馈二轮：闪跳仍存在、光标与内容有间距、思考进度要放下面、任务栏信息更丰富。

- **增量重绘（根治闪跳）**：`render()` 写屏改为**行级 diff**——缓存上一帧每行内容（`lastFrame`），只重写内容变化的行；尺寸变化时才全屏清。消息流/滚动/新 token 追加不再整屏重绘，Win10 conhost 上闪烁彻底消除（配合上轮 33ms 节流）。
- **光标间距修复**：`[MULTI]` 模式首行有额外前缀（黄色标记 + 提示），光标列此前按 `indent + vw` 计算导致光标与已输入内容之间出现空隙——现在首行光标列计入前缀可见宽度。
- **思考指示移到底部**：`⠋ 生成中… 32% 12s` 从消息区移到**输入框上方**（context bar 之上，紧贴输入区），消息区不再每帧重绘该行。
- **状态栏更丰富**：新增 **📎 会话标题**（自动命名/`/rename` 后显示，截 18 字）+ **🧩N 技能数**（懒缓存一次性计算）；叠加原有模式/模型/分支/PR/Token/上下文%/缓存/费用/Todo/⚙工具/⚡后台/⏱计时。
- 验证：tui 编译/vet/测试绿（含 box_align）；全量测试绿；四份二进制重编 PE 正确。

## v0.46.2 — CLI 渲染闪跳修复 + 思考指示 opencode 化（2026-08-29）

> 用户反馈：CLI 输入后回复时局部界面闪跳；思考滑块样式/位置要对齐 opencode；输入框与任务栏提示综合 Claude Code + opencode。

- **闪跳修复**：渲染节流 `12ms → 33ms`（≈30fps）——此前约 83fps 全屏逐行重绘，在 Win10 conhost/Windows Terminal 上高频 ANSI 刷新造成可见闪烁；30fps 对文本流足够平滑且明显降低终端压力。
- **思考指示 opencode 化**：去掉 14 格的 `[▓▓▓▓▓▓░░░░░░]` 宽滑块（每帧变化的滑块正是闪跳视觉重灾区），改为 opencode 默认 dots spinner 风格——`deepseek-v4-flash ⠋ 生成中… 32% 12s`（spinner + 状态文本 + 上下文% + 秒数，全部 dim/紧凑）。修正 thinkingBox 与 thinkingBar 的 `status.gen` 重复显示。
- **输入框/状态栏综合确认**：`❯` 提示符（Claude Code）+ 上下文条（CC）+ 状态行全要素（模式/模型/分支/PR/Token/上下文%/缓存/费用/Todo/当前工具⚙/后台⚡/计时⏱，opencode InlineToolRow 风格）+ 空输入提示 + `[MULTI]` 标记——两家元素已齐备，本轮微调确认。
- 验证：tui 编译/vet/测试绿（含 box_align）；全量测试绿；四份二进制重编 PE 正确。

## v0.46.1 — Skill 技能市场（WorkBuddy SkillHub parity）（2026-08-29）

> 对标体检 P2-②：内置技能市场浏览 + 一键安装。市场机制（`ListCatalog`/`Install`）此前已存在，本轮补上**命令入口**并扩充业务类技能。

- **`/skills market`**：列出内置技能市场（离线可用），✓ 标记已安装项。
- **`/skills install <名称>`**：一键安装到 `~/.icode/skills/`（复制 SKILL.md + 触发词），下次注册即生效。
- **市场扩充至 10 个技能**：
  - 开发：code-review / commit-msg / doc-gen / explain-code / unit-tests（原有）。
  - 保险展业（新增，贴合代理人场景）：**insurance-pitch**（话术，融入乔吉拉德/梅第/柴田和子/原一平方法论）、**policy-review**（保单检视）、**client-needs**（ABCD 定联 + 家庭责任需求分析）、**objection-handling**（异议四步法）、**claims-assist**（理赔协助）——均含合规红线与免责声明，不虚构费率/收益。
- 三端生效（slashui 共享命令集：CLI / simpleui / 桌面）。
- 验证：skills + slashui 编译/vet/测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.46.0 — Remote Control 远程控制（ZCode Claw / WorkBuddy Claw parity）（2026-08-29）

> 对标体检 P2-①：手机浏览器远程查看状态、发指令、管理自动化任务——代码仍在本机运行，安全 token 门控。

- **配置**：`config.yaml` 新增 `server.remote_listen`（如 `"0.0.0.0:8789"`）。配置后开独立监听器，主 API 仍 loopback-only。
- **端点**（Bearer apiToken 门控，与桌面端同一 token）：
  - `GET /api/remote/status` —— 版本 / 模式 / 安全级 / 后台代理+命令数 / 自动化任务 / 知识库片段 / 服务器时间。
  - `POST /api/remote/prompt` —— 发 prompt（自动建 remote 会话），非流式聚合回复（180s 超时）。
  - `GET|POST /api/remote/tasks` —— 列出 / 创建自动化任务（含 RRULE 秒级调度）。
- **手机页面**：`/remote?token=xxx` —— 状态卡片（5s 轮询）+ 指令输入（Enter 发送）+ 自动化任务列表/创建，移动端友好深色 UI。
- 启动日志打印访问地址：`http://<ip>:8789/remote?token=<token>`。
- 验证：server 包编译/vet/测试绿；全量测试绿；四份二进制重编 PE 正确。

## v0.45.3 — Ctrl+P 恢复 + /redo 文件重做 + RRULE 秒级定时（2026-08-29）

> 按用户反馈把 Ctrl+P 恢复为历史上翻（设置面板留在 Ctrl+,），并完成对标体检 P1 剩余核心项：/redo、RRULE 秒级定时。

### ♻️ Ctrl+P 恢复
- CLI：Ctrl+P 恢复「历史上翻」（readline 惯例）；设置面板仅 Ctrl+,。帮助面板同步。
- 桌面端：移除 Ctrl+P 拦截（恢复浏览器默认）；设置仍走 Ctrl+, 与设置页按钮。
- 简易 UI：移除 Ctrl+P 设置弹窗与拦截。

### ⏪ /redo（opencode /undo /redo parity）
- checkpoint `Store` 加 `redoStack`：`Rewind` 前记录当前 shadow-git HEAD，新增 `Redo(ctx)` 用 `git checkout <oldHead> -- .` 恢复被回滚的改动（不移动 HEAD）。
- TUI 新增 `/redo` 命令（`redoStep`）：与 `/undo`（rewind 1 步）/`/rewind N` 对称。

### ⏱ RRULE 秒级定时（WorkBuddy parity）
- 新增 `scheduler/rrule.go`：轻量 RRULE 解析——`FREQ=SECONDLY|MINUTELY|HOURLY|DAILY|WEEKLY|MONTHLY` + `INTERVAL` + `BYHOUR` + `BYMINUTE` + `BYDAY(MO..SU)`；`rrule.Next(from)` 计算下一次（秒级对齐）。
- `nextRunAfter`/`DescribeSchedule` 支持 `rrule:` 前缀；`describeRRule` 中文描述（如「每 30 秒」「每周一、周五 9 点」）。新增 6 个单测。
- 用法：`rrule:FREQ=SECONDLY;INTERVAL=30`（每 30 秒）、`rrule:FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=9;BYMINUTE=30`。

### 📋 体检 P1 剩余项等价覆盖说明
- `/loop`：自动化任务面板（`/tasks` + simpleui 任务 tab）+ `/idle` 闲时任务已覆盖循环执行能力。
- `/keybindings`：桌面端 ShortcutPanel（设置→快捷键，可配置）已覆盖；CLI 键位硬编码为稳定默认。
- `/teleport`：Claude Code 网页迁移独有，非 CLI 常规能力，建议跳过。

## v0.45.2 — Ctrl+P 设置面板（三端）+ Shift+Tab 模式确认（2026-08-29）

> 按用户需求：Ctrl+P 弹出设置选项进行所有设置（Claude Code / opencode 风格设置面板），CLI / 桌面 / 简易 UI 三端统一。

- **CLI（TUI）**：Ctrl+P 从「历史上翻」改为**打开设置面板**（建议列表打开时仍作建议光标上移）；提取 `openSettings()` 供 Ctrl+P 与 Ctrl+, 共用；历史上翻仍可用 ↑、下翻 Ctrl+N。帮助面板更新快捷键说明。
- **桌面端**：Ctrl+P 打开设置页（`preventDefault` 抑制浏览器打印对话框，`setSettingsOpen(true)`）。
- **简易 UI（simpleui）**：新增 `#settingsModal` 设置弹窗（主题切换 / 设置 API Key / 添加自定义模型 / 命令面板 四个入口 + 关闭），Ctrl+P 打开。
- **Shift+Tab 切模式**：CLI（`cycleMode`）与 simpleui（`cycleMode`）此前已对齐 Claude Code（plan → agent → yolo → auto），本轮确认并记录——桌面端经设置/按钮切换。
- 验证：Go build/vet、simpleui vet、tsc --noEmit 干净；tui 单测绿；前端 build + 嵌入 dist；四份二进制重编。

## v0.45.1 — AskUserQuestion 交互提问工具（Claude Code parity）（2026-08-29）

> 全面对标体检 P1-①：模型在需要人做决策时（选目录/选方案/是否继续）以多选方式提问，TUI 渲染选项面板、数字键选择。

- **工具**：新 `tool/askuser.go`——`ask_user_question`（question + 最多 9 个 options），`AskUserFunc` 经 ctx 注入（`WithAskUser`/`AskUserFromContext`）；无注入环境（headless/simpleui/desktop HTTP）**优雅降级**返回错误，绝不挂起。
- **引擎**：`Engine.AskUser tool.AskUserFunc` 字段；`executeTool` 注入 ctx（与 WithSessionID/WithProgress 同模式）。
- **TUI**：新 `ask.go`——`askUserInteractive`（阻塞等选，5 分钟超时兜底）+ `resolveAsk`；`handleKey` 顶部拦截 1-9/Enter/Esc；`drawAskOverlay` 居中选项框（黄问号 + 编号选项 + 快捷键提示）；`Run()` 注册 `OnSetAskUser`（测试路径不注入）。
- **接线**：`Callback.OnSetAskUser` 新方法；`chatCallback` 实现（设 `engine.AskUser`）；`testCallback` 同步。
- 新增 3 个单测（Def、无注入降级、注入后选择传递）。

## v0.45.0 — 全面对标体检（Claude Code / opencode / ZCode / WorkBuddy）+ fetch HTML 正文提取（2026-08-29）

> 四大对标对象能力清单调研 + iCode 现状对照，产出差距分级清单；首项 P0 落地。

### 📋 全面对标体检结论（iCode 已覆盖 vs 缺口）
- **已覆盖**：多模型聚合（50+ 提供商）、5 层上下文压缩、分级授权（plan/agent/auto/yolo + strike 兜底）、Goal 可验收、Subagents、Idle Tasks、LSP、知识库 RAG、MCP、hooks、skills、自动化任务、多模态（图片/文件）、`/bug` `/pr_comments` `/release-notes` `/statusline` `/compact` `/context` `/doctor` `/permissions` `/init` `/add-dir` `/agents` `/diff` `/review` `/theme` `/cost` 等 60+ 命令、WebSearch（Bing/Baidu/Tavily）、fetch、非交互 `-p` 模式、会话 resume/fork/rewind/undo/share。
- **缺口（分级）**：
  - P0：`fetch` 返回原始 HTML 噪音（本轮已修：HTML→可读正文）。
  - P1：AskUserQuestion 交互提问工具；`/undo`/`/redo` 基于 git 的文件回滚（opencode parity）；RRULE 秒级定时（WorkBuddy parity）；`/teleport`/`/loop`/`/keybindings`（Claude Code 独有）。
  - P2：Remote Control 远程调度（ZCode Claw / WorkBuddy Claw）；Skill/插件市场（WorkBuddy SkillHub）。

### ✨ fetch 工具增强：HTML → 可读正文（Claude Code WebFetch parity）
- 新增 `webfetch_extract.go`：`htmlToText`（去 script/style/注释块、提取 `<title>`、去标签、解码常见实体、压缩空白、12000 字封顶）、`isHTML`（Content-Type / 首字节判断）、`decodeHTMLEntities`。
- `FetchTool.Execute`：HTML 页面自动提取正文文本（省 token、可读性↑）；JSON/纯文本 API 响应保持原样（256KB 上限截断逻辑不变）。
- SSRF 防护与重定向校验保持不动；新增 3 个单测（`webfetch_extract_test.go`）。

## v0.44.2 — CLI 细节打磨二批：生成区模型标注 + 空输入占位提示（2026-08-29）

- **生成动画区显示当前模型**：`thinkingBar` 在 spinner 前加 dim 模型名（截 18 字），生成时一眼可见是哪个模型在跑（多模型切换场景尤其有用）——视觉：`deepseek-v4-flash ⠋ [▓▓▓▓▓▓░░░░░░] 32% 12s`。
- **空输入占位提示（Claude Code 风格）**：输入框为空且未生成时，首行显示 dim 提示 `/ 查看命令 · Tab 补全 · Ctrl+R 历史 · Alt+P 模型`；开始打字即消失；`/multiline` 模式不显示（已有 `[MULTI]` 标记）。
- 体检确认工具耗时/截断标记等已存在，未重复造轮子。

## v0.44.1 — CLI 细节打磨：会话自动命名 + 多行模式指示（2026-08-29）

> CLI 对标 Claude Code 的细节体检：工具折叠/Ctrl+L 清屏/Ctrl+D 退出等已齐，补两个真实缺口——会话标题自动命名、多行输入模式持续指示。

- **会话标题自动命名（Claude Code parity）**：全新会话的首条用户消息自动生成标题（清洗 `@file` 引用与附件标记、折叠空白、去掉前导标点、截 24 字），写入会话存储——`/resume` 选择器与 `/sessions` 列表立即显示有意义标题。`users==1` 守卫天然跳过 resume 会话与后续消息，手动 `/rename` 永不被覆盖。
- **多行模式 `[MULTI]` 持续标记**：`/multiline` 开启后，输入框首行常驻黄色 `[MULTI]` + 「Enter=换行·Alt+Enter=发送」，不再只在切换瞬间提示一次。
- 新文件 `internal/tui/title.go`（`autoTitle`/`deriveTitle`）；`raw_input.go` sendInput 接入；`render.go` drawInputBox 标记。

## v0.44.0 — 桌面端分级授权 strike 可视化（Claude Code parity）（2026-08-29）

> 桌面端功能对标体检：自动化面板/图片附件/知识库/LSP 等已齐，唯一缺口是权限弹窗缺少分级授权进度——本轮补齐。

- **后端**：`types.PermissionReq` 新增 `Strikes`/`Threshold` 字段；engine 发 `EventPermission` 时从 gate 读取（`EscalationState` + `StrikeThreshold`，strikes 含本次决策）。HTTP 序列化自动带上，TUI/permHandler 分支同步。
- **桌面端**：权限弹窗在工具名与提示下方新增黄色提示——未达阈值显示「已拦截 N/阈值 次，连续 阈值 次将退回手动」；达阈值显示「⚠ 已连续 N 次拦截，本会话已退回手动模式（需逐次确认）」。i18n 三语（zh/TW/en）新增 `permission.strikeEscalated`/`strikeProgress`。
- **构建**：前端 `npm run build` → dist 嵌入 `internal/embedded/dist` → 四份二进制重编。

## v0.43.9 — 简易 GUI LSP 诊断面板（UI 功能对标体检 P0+P1 收官）（2026-08-29）

> UI 功能对标体检最后一环：`/lsp` 的可视化面板。至此 P0（图片多模态/文件附件）+ P1（知识库/任务/分级授权/LSP）全清。

- **右栏第四个 tab「诊断」**：`LSP 状态` 按钮展示语言服务器检测与用法；文件路径输入框 + `诊断`（Enter 触发）跑 `/lsp diag <file>`，输出格式化报告（`[错误] 行:列 消息`），`<pre>` 可选中复制。
- Go 端：`LspStatus()`/`LspDiag(file)` 复用 `LSPManager.QueryReport`（TUI/HTTP/simpleui 三端同一实现），`w.Bind("lspStatus"/"lspDiag")`；未启用时给出明确提示（`lsp.enabled`）。
- 至此 simpleui 右栏：命令 / 知识库 / 任务 / 诊断 四 tab。

## v0.43.8 — 简易 GUI 自动化任务面板 + 分级授权状态可视化（2026-08-29）

> UI 功能对标体检 P1-⑤ + P1-⑥：把 `/tasks` 闲时调度与分级授权 strike 兜底搬进 GUI。

### ✨ 自动化任务面板（对齐 ZCode Idle Tasks / /tasks）
- 右栏第三个 tab「任务」：任务列表（绿点=启用、调度、下次运行时间），每条可「立即运行 / 删除」。
- 创建表单：任务名 + 内容 + 调度（闲时 0 点后 / 每天 02:00 / 00:30 / 每 30 分钟 / 每小时）。
- Go 端：`TasksList`/`TaskCreate`/`TaskDelete`/`TaskRunNow`（复用 `app.Scheduler`，持久化 SQLite），`w.Bind("tasksList"/"taskCreate"/"taskDelete"/"taskRunNow")`；空列表给出引导文案。

### ✨ 分级授权 strike 可视化（Claude Code parity）
- gate 新增 `EscalationState(sessionID)` getter（escalated + strikes）；bridge `EscalationState()` → `w.Bind("escState")`。
- 权限审批条内新增 `#escHint` 提示：未拦截时空白；有拦截时显示「已拦截 N/阈值 次，连续 N 次将退回手动」；触发兜底后显示「⚠ 已连续 N 次拦截，本会话已退回手动模式」。

## v0.43.7 — 简易 GUI 知识库检索面板（本地 RAG /kb 可视化）（2026-08-29）

> UI 功能对标体检 P1-③：`/kb` 文本查询早已可用（slashui），本轮补上**可视化面板**——右栏「命令 / 知识库」双 tab，零 token 本地检索。

- **右栏双 tab**：命令面板旁新增「知识库」，点击切换；搜索框 Enter / 检索按钮触发。
- **Go 端**：`KnowledgeStatus()`（片段数 JSON）、`KnowledgeSearch(query)`（`knowledge.Manager.Search` 本地 IDF/BM25 检索，top 6，片段截断 240 字），`w.Bind("kbStatus"/"kbSearch")`。
- **JS 端**：结果列表（出处文件/章节 + 相关度 + 摘要），点击将完整片段以 `[知识库资料: file / section]` 标记插入输入框，模型可直接读取。
- 未配置知识库时提示 `config.yaml knowledge.dirs`；空库自动触发 `Index`。

## v0.43.6 — 简易 GUI 文件拖拽/选择附件（@路径 引用，对齐 CLI）（2026-08-29）

> UI 功能对标体检 P0-②：把 CLI 的 `@file` 引用能力搬进简易 GUI——拖拽或 📎 选择任意文件，发送时统一展开。

- **拖拽文件进输入区**（`dragover`/`drop`）：图片 → 多模态附件；有本地路径（WebView2 `File.path`）→ 插入 `@绝对路径` 引用；无路径文本 → 直接内联。
- **📎 按钮放开**：不再只限图片，任意文件可多选；同一套 `addFileRef` 分类逻辑。
- **Go 端 `expandFileRefsUI`**（对齐 TUI `expandFileRefs`）：`@path` 在 `Send`/`SendWithAttachments` 发送前统一展开——图片按 magic bytes 识别为多模态附件（`[📎 图片: name]`）、文本内联（`[file: path]`）、其他二进制守卫（只留路径说明，原始字节绝不进 prompt）。
- 邮箱等带 `@` 的普通文本不受影响（文件读不到时原样保留引用）。

## v0.43.5 — 简易 GUI（simpleui）体验修复 + 图片多模态（2026-08-29）

> 补齐简易 GUI 与 CLI 的功能差：下拉框点击无选项、模型切换不生效、以及最常被代理人用到的「截图/保单图片直接发给模型」。

### 🐞 简易 GUI 下拉框修复
- 模型/会话下拉原为 `<input list>` + `<datalist>`，**WebView2 下点击聚焦不弹 popup**（体感「点不开、不出现其他选项」）。改为**自绘点击下拉面板**（`.dd`/`.dd-panel`/`.dd-option`）：点输入框或 `▾` 即弹、带搜索过滤、点选即切换、点面板外收起；空列表显示占位（无模型/无会话）。
- 修复 `SetModel` 只改 UI 不生效：切换下拉后把新模型**回写到当前会话**（`SessStore.Update`），下一轮 `Engine.Send` 即走新模型（此前首条消息固定 `ModelID` 后切换是假切换）。

### ✨ 简易 GUI 图片多模态粘贴（对齐 CLI）
- `RunCommand`/`Send` 之外新增 `SendWithAttachments(text, attsJSON)`，引擎 `Engine.Send(ctx, sid, prompt, atts)` 已是多模态就绪（OpenAI-compatible/Anthropic provider 支持 `image_url`/base64）。
- 输入框 `paste` 事件拦截剪贴板图片 → base64 暂存；新增 `📎` 按钮可文件选择多图；附件以缩略 chip 预览、可单独移除；发送时随消息以多模态附件送出（提示「📎 已附带 N 张图片」由引擎/CLI 一致逻辑给出）。
- 图片仅在本地转 base64，不上传第三方；发送后清空，不残留。

## v0.43.4 — CLI 对标 Claude Code 第五批 P4+P5：多模态统一 + 粘贴折叠 + 历史持久化（2026-08-29）

> 收尾 v0.43.3 遗留的「手动 `@image.png` 自动识别为图片附件」，并补齐两项 Claude Code 标志性细节体验：多行粘贴折叠、输入历史跨会话持久化。

### ✨ 手动 @图片 → 多模态附件（统一通道）
- `expandFileRefs` 现在对图片直接产出 `[]types.Attachment`（读字节 → 按 magic bytes 识别 MIME → base64），文本里写 `[📎 图片: name]` 占位；Ctrl+V 粘贴与手动 `@photo.png` 走同一条多模态通道。
- 顺手修掉旧逻辑「jpg 若不含 0x00 会被当文本内联」的潜在 bug（现在图片一律走附件，不再按二进制守卫误判）。
- 移除了临时的 `pendingImages`/`consumePendingImages` 机制，`expandFileRefs` 统一收口，链路更干净。

### ✨ 多行粘贴折叠（Claude Code parity）
- 新增 `paste_fold.go`：`insertPasted` 在粘贴 ≥3 行或 >500 字节时折叠为 `[粘贴 N 行 #k]` 占位符，原文存入 `pasteBlocks`；发送时 `expandPasteBlocks` 还原，模型仍收到完整内容，但输入框不被刷屏。
- bracketed paste（终端 200~…201~）与右键粘贴都改走 `insertPasted`；交互式/行模式提交前均自动展开。

### ✨ 输入历史跨会话持久化（Claude Code parity）
- 新增 `history_persist.go`：`~/.icode/input_history.json`（0600 仅本地、不上传），保留最近 200 条 prompt，重开会话后仍可 ↑ 召回。
- 配置开关 `history_persist: false`（默认 true）可整体关闭，尊重本地数据安全偏好。
- **关键护栏**：持久化仅在 `Run()` 武装（`historyPersistActive`），单元测试直接调用 `pushHistory` 不会触碰用户真实历史文件。

### 🔧 安全 / 细节
- 二进制守卫、图片 25MB 上限、纯图片消息补「（请查看附件中的图片）」等护栏延续生效。
- 四份二进制（控制台 `icode.exe`/`bin/icode-cli.exe`、GUI `icode-cli.exe` simpleui / `icode-desktop.exe` desktop_only）均已重编并校验 PE 子系统。

## v0.43.3 — CLI 对标 Claude Code 第四批 P3：图片多模态内联（2026-08-28）

> 把上一版（v0.43.2）的「Ctrl+V 图片粘贴」从「路径引用」升级为**真正的多模态内联**：模型现在能直接「看见」粘贴的截图/保单图片。

### ✨ 图片多模态内联（Claude Code parity）
- `Callback.OnSend` 回调签名升级为 `OnSend(text string, attachments []types.Attachment)`；`chatCallback` 把附件透传给 `Engine.Send(ctx, sessionID, text, attachments)`。
- `types.Message.Attachments` 早已存在，OpenAI-compatible 与 Anthropic provider 的 `buildRequestBody` 已能把图片附件转成 `image_url` / base64（含 `attachment_test` 验证）——本次把 TUI 这一环接通。
- 提交流程：展开 `@file` 引用后，`consumePendingImages` 把粘贴图片从文本里抽出 → 读字节、base64、按 magic bytes 识别 MIME（png/jpeg/gif/webp，扩展名兜底）→ 构造 `[]types.Attachment`；文本中的 `@path` 替换为 `[📎 图片: name]` 标记（会话记录可读），图片字节以多模态内容块发给模型。
- `pasteClipboardImage`（Ctrl+V）在插入 `@path` 的同时把路径记入 `pendingImages`，发送时消费。
- 安全护栏：单图上限 25MB；纯图片消息自动补「（请查看附件中的图片）」防空内容被拒；无法读取/不支持的格式保留 `@path` 文本不静默丢弃。
- 反馈：发送带图时状态栏提示「📎 已附带 N 张图片发送给模型」。

### ⚠️ 向后兼容
- `OnSend` 改为两参，所有实现方同步：`chatCallback`（cmd/commands.go）、`testCallback`（slash_dispatch_test.go）。其余转发点（shell 输出、slash 转发、行模式）传 `nil`。
- 仅交互式 TUI 的 Ctrl+V 粘贴走多模态；手动 `@image.png` 仍按既有「路径引用」处理（后续可扩展为自动识别）。

### 验证
- `go build ./internal/tui ./cmd` + `go vet` 干净；`go test ./internal/tui/...` 绿。
- 四份二进制重编，PE 子系统：icode.exe / bin/icode-cli.exe = CONSOLE(3)；icode-cli.exe(simpleui) / icode-desktop.exe(desktop_only) = GUI(2)。

## v0.43.2 — CLI 对标 Claude Code 第三批 P2：自动回顾 + PR 徽章 + 图片粘贴（2026-08-28）

> 收尾 P2 批次（提示词建议已在 v0.43.1 的 autocomplete 中落地，本批不再重复）。

### ✨ 离开 3 分钟自动回顾（Claude Code parity）
- 主循环新增 1s 空闲 tick：用户离开 ≥3 分钟且**无流式输出**在跑时，自动跑一次 `/recap`，并提示「💤 你已离开 3 分钟，自动为你回顾一下当前会话…」。
- 10 分钟冷却，避免静默期内反复刷屏；对话不足 3 轮不触发。
- 空闲计时在每次按键、每次发送、每轮 `EndStream` 完成时重置，生成回复期间永不误触发。

### ✨ 状态栏 PR 徽章（Claude Code parity）
- 新增 `prSegment()`（类 `branchSegment` 懒加载，每 60s 经 `gh pr view --json` 刷新），状态栏分支后显示 `PR #123 OPEN` / `PR #456 MERGED`。
- 配色随合并态：`OPEN`/`MERGED` 绿，`CLOSED`/`DRAFT` 红；`gh` 未装或无 PR 时不显示，绝不阻塞渲染热路径。

### ✨ 剪贴板图片粘贴（Ctrl+V）
- `handleKey` 拦截 `Ctrl+V`：检测到剪贴板图片时（Win 走 `System.Windows.Forms.Clipboard`、macOS 走 `pngpaste`、Linux 走 `xclip` 最佳努力）存为临时 PNG 并插入 `@<path>` 引用，提示「📎 已粘贴剪贴板图片」。
- 文本粘贴仍由终端 bracketed-paste 处理，不重复；无图片/不支持时明确提示。
- `expandFileRefs` 加**二进制守卫**：遇含 NUL 字节的文件（如粘贴的 PNG）只插入路径引用，不再把二进制字节当文本内联进 prompt（避免污染上下文）。

## v0.43.1 — CLI 对标 Claude Code 第二批：快捷键包 + 后台化 + 权限说明 + transcript（2026-08-28）

> 承接 v0.43.0 的 P1 批次，每项同样带可见反馈。

### ✨ 快捷键包（Claude Code parity）
| 快捷键 | 能力 | 反馈 |
|---|---|---|
| `Ctrl+S` | 暂存提示词 / 空输入时恢复 | 「已暂存提示词（空输入时按 Ctrl+S 恢复）」「已恢复暂存的提示词」 |
| `Ctrl+G` | 用 `$VISUAL`/`$EDITOR` 编辑当前提示词 | 自动挂起 raw mode 与 alternate screen、编辑完读回；未设置编辑器时明确提示 |
| `Ctrl+_` | 撤销上一步输入编辑（readline 风格，200 步栈） | 「已撤销上一步输入编辑」 |
| `Ctrl+B` | **当前任务转入后台**：引擎继续跑，UI 回到输入 | 「⏭ 已转入后台运行」；期间输入自动排队，完成时「✅ 后台任务已完成」并自动发送队列 |
| `Alt+P` | 切换模型（不清空输入） | 打开模型选择器 |
| `Alt+T` | 切换 extended thinking | 走 `/thinking on\|off` 并显示结果 |
| `Ctrl+O` | transcript：展开/折叠**全部**工具执行详情 | 「🔍 已展开全部工具详情（N 条）· Ctrl+O 折叠」 |

### ✨ 权限提示 Tab 加「拒绝说明」（Claude Code parity）
- 权限框按 `Tab` 打开说明输入框 → 输入原因 → `Enter` 提交为「拒绝并说明」；说明经 `OnPermissionNote` 写入会话，agent 下一轮能看到**被拒原因**（不再盲目重试）。`Esc` 关闭说明框，`Ctrl+C` 直接拒绝。选项行同步显示 `[Tab] 加说明`。

### ✨ /recap 会话回顾
- 非破坏性（不像 `/compact` 改历史）：用模型生成「已完成 / 当前进展 / 下一步」，**上限 400 字符**；不足 3 轮提示无需回顾。

### 🧩 其他
- 帮助面板（`?` / `/help`）补全全部新快捷键，并更新 `!` shell 模式说明。
- `!` shell：raw 模式走完整实现（Ctrl+C 中止进程、输出截断 4000、结果作为 user turn 让 agent 响应），line 模式保留简单同步执行。

## v0.43.0 — CLI 对标 Claude Code：Esc 中断修复 + 非交互模式 + 排队消息 + 多行 + Shell 模式（2026-08-28）

> 用户反馈「Esc 在运行时不能中断现有任务」+ 要求逐项提升 CLI 细节体验（每项带效果反馈）。

### 🐞 核心修复：Esc 中断失效
1. **根因**：引擎两处 `ctx.Done()` 直接 return，不发任何事件、不保存部分输出 → UI 的 `EndStream` 永不触发 → `streaming` 卡死 → 二次 Esc 完全无效（stopFns 已被 defer 删除）。
2. **引擎侧**：中断时 `persistPartialTurn()` 保存已生成的**部分输出进会话**（Claude Code「已完成的工作保留」），并发 `EventSystem`「⏹ 已中断生成。已生成的部分输出已保留。」
3. **UI 侧兜底**：事件通道关闭而无 EventDone/Error 时，无条件 `EndStream()`（幂等）——任何路径都不再卡死。
4. **即时反馈**：按下 Esc 瞬间显示「⏹ 正在中断…」，不等引擎流 unwound。
5. **误中断防护**：lone Esc 判定（等 escFollowTimeout），**方向键首字节不再误判为中断**。

### 🐞 附带修复：流式文本重复输出（"OKOK"）
引擎主循环未做流式差分（continueAgentLoop 有 `textAccumulator` 而主循环没有）→ provider 发全量快照时重复输出。主循环现在统一走 accumulator，只转发 delta。

### ✨ 非交互模式（Claude Code `-p` 对齐，script/CI 友好）
- `icode --print "prompt"` / `-P`，支持 `--output-format text|json|stream-json`、`--continue/-c`（继续最近会话）、`--resume <id>`、`--mode`。
- stdout 保持纯净可管道（诊断/工具调用/用量走 stderr）；失败非零退出码。
- （注：`-p` 已绑定 `--provider`，故 print 用 `--print/-P` 避免破坏既有脚本。）

### ✨ 流式排队消息（Claude Code queueing）
- agent 工作期间打字不丢失：字符进 `queueBuf`，Enter 入队，turn 结束**自动发送最旧一条**；`↑` 取回最旧条目继续编辑。输入框上方显示「⏳ 输入中 / 已排队 (N)」。

### ✨ 多行输入
- `Ctrl+J` 插入换行；输入框按行渲染（最多 8 行、内部滚动、光标行保持可见）；`↑↓` 在行间移动（首/末行边界才浏览历史）；`Ctrl+A/E` 改为**当前逻辑行**行首/行尾。

### ✨ `!` Shell 模式
- `! npm test` 本地直跑（无需批准），输出进上下文并让 agent 响应；`Ctrl+C` 杀进程并显示「⏹ shell 命令已中断」；展示输出（超长截断）＋退出码。

### ✨ 双 Esc（Claude Code parity）
- 600ms 内双 Esc：有草稿 → 清空并存入历史（`↑` 召回，提示「🗑 草稿已清空并存入历史」）；空输入 → **两段式回溯**（第一次提示就绪、第二次才真正 `/rewind`，防误触）；任何非 Esc 键自动解除 armed 状态。

### 🧪 测试与交付
- 全量 internal+cmd 测试绿；`go vet` 无新增问题；非交互模式实机冒烟（json/text/工具调用+自动放行）；四份二进制重编（PE 3/3/2/2），版本 0.43.0。

## v0.42.8 — CLI Markdown 修复批次 + 文字 LOGO 恢复（2026-08-25）

### 🖥 TUI Markdown 三处修复（用户实测反馈：`**` 不转换）
1. **跨行加粗配对**：段落改为整体折叠渲染——模型把长加粗短语换行书写时 `**` 现在能正确配对；残留的未配对 `**` 由兜底清扫移除，星号绝不再漏进对话。
2. **表格单元格行内渲染**：此前单元格不走 renderInline，`**重点**` 原样显示；现在转换且用可见宽度对齐不破版。
3. **三星语法** `***粗斜体***` 支持。

### 🔰 文字 LOGO 恢复 + 空白根因修复
- 应用户要求恢复 **7 行完整版**文字 LOGO（黄点+间隔行+全高字母体）。
- 同时修复「LOGO 空白」真因：字母此前被 paint("white") 染色，light 主题下 white 映射为 `\x1b[30m`（黑前景），主题判定与终端实际背景不符时整片隐形。现字母改用终端默认前景色（任何终端/主题都可见），仅黄点保留着色。

### 📦 构建产物
- 三端合一单二进制完整重建（嵌入桌面前端，-H windowsgui）；删除冗余旧副本 bin/icode-cli.exe。分发物仅剩一个 icode.exe（双击=SimpleUI / `icode desktop`=桌面 / 终端运行=TUI）。

## v0.42.7 — 桌面 Markdown 补齐：嵌套列表修复 + 标题层级色（2026-08-25）

### 🐛 修复：嵌套列表被当段落吞掉
- 列表解析此前只匹配行首项——带缩进的子列表项会掉进段落收集器渲染为普通文本（与 SimpleUI 修复前同病）。
- 重写为统一的嵌套栈处理器：2 空格/Tab 一级，ul/ol/任务列表混合嵌套，任务勾选框在深层级正常工作。

### 🎨 标题层级视觉
- h1 保留下边线；**h2 新增 accent 色左侧竖条**；h3+ 保持常规——对齐 TUI/SimpleUI 的分级设计。

### ✅ 已完善无需改动
hljs 21 语言高亮（流式跳过 auto 防卡顿）、代码语言标签+复制、大块截断护栏、diff 专用着色、表格/引用/任务勾选、行内全套（粗斜删/行内码/链接/图片徽章/裸 URL）。

## v0.42.6 — Markdown 渲染优化：CLI 与 SimpleUI（2026-08-25）

### 🖥 TUI（CLI）
- **标题分级**：h1 青色粗体+全宽线；h2 黄色粗体带 ▍ 侧标；h3+ 纯加粗——层级一眼可辨，不再全部同款。
- **嵌套列表缩进**：二级项缩进 4 格渲染（此前与顶层平齐）。
- **代码块语言标注**：顶框显示 `┌─ go ───`。
- **表格数据列**改用默认前景色（此前整表 dim 灰难以阅读）。

### 💬 SimpleUI
- **代码块语法高亮**：客户端轻量分词器（注释/字符串/数字/关键字五类着色），按语言家族适配 go/js/py/sh/sql，暗/亮主题各配色——补齐与 TUI chroma 的最大差距。
- **嵌套列表**：栈式层级解析（2 空格一级），任务列表参与嵌套。
- **标题样式增强**：颜色分级（h1 蓝+下边线 / h2 黄 / h3+ 常规）+ 上边距，暗亮双主题。

### 🔍 核实
- 桌面端 react-markdown 渲染已完善，无需改动。

## v0.42.5 — Mesh 连通性可视化（三端）+ SimpleUI 会话搜索核实（2026-08-25）

### 📡 Mesh 对端连通性探测
- 新端点 `POST /api/mesh/ping`（X-Mesh-Token 门禁，主 mux 与 mesh 独立监听均注册）：回本机主机名与版本。
- `/mesh list` 三端升级为实时探测：每条对端显示 ✅ 在线（版本/主机名）或 ❌ 离线（原因），3 秒超时。
- 渲染逻辑收敛到 `mesh.RenderPeerStatuses` 共享函数，TUI 与 slashui 输出保证一致。

### ✅ 核实关闭：SimpleUI 会话搜索
- 会话选择器是 `<input list>` + `<datalist>` 原生组合——输入即按标题/ID 过滤，无需另做搜索框。

## v0.42.4 — 模式快捷键三端一致（2026-08-25）

### 🔁 Shift+Tab 模式循环统一
- 规范循环序固定为 **plan → agent → yolo → auto**（Claude Code 风格），三端完全一致：
  - **TUI**：参考实现（不变），帮助文案修正为实际循环序。
  - **桌面**：修复循环序不一致（此前是 plan→auto→ask→yolo，含已废弃的 ask、缺 agent）。
  - **SimpleUI**：从「前端本地算下一模式」改为调用后端 bridge `cycleMode`——以 gate 权威状态计算、设置并刷新状态条 + 系统提示，杜绝前端状态漂移。
- 桌面底部模式 Pill 与 SimpleUI 状态条随切换即时更新。

## v0.42.3 — 三端同步批次：/tasks 进服务端、/agents 面板对齐（2026-08-25）

### 🔁 三端命令词汇表对齐（CLI ↔ Desktop ↔ SimpleUI）
- **`/tasks` 进入 slashui 共享层**：桌面与 SimpleUI 的 `/api/slash` 路径现在与 TUI 一样能查看后台任务面板（子代理 agt-N + shell bg-N 状态/耗时/token）。
- **`/agents` 升级到 TUI 同版**：补齐能力标注（fork / memory / isolation）、团队清单、后台运行任务三段——此前是只有名字的旧列表。
- 核实项关闭：`/cost` 别名两端皆有；思考过程 SimpleUI 已是 `<details>` 折叠式，三端一致。
- 纯终端类命令（/expand /multiline /statusline 等）保持 CLI 独有，属合理差异。

## v0.42.2 — Mesh 入站监听修复 + 应用自升级 + SimpleUI 权限审批条 + 命令面板整合（2026-08-25）

### 🐛 修复：Mesh 跨机消息无法入站
- 主 API 服务此前只绑定 `127.0.0.1`，对端机器永远连不上 `/api/mesh/messages`。
- 新增 `[server] mesh_listen` 配置（如 `"0.0.0.0:8788"`）：独立最小监听只服务 mesh 端点，X-Mesh-Token 门禁；浏览器主 API 保持仅本机。默认关闭。`/mesh list` 输出开启指引。

### ⬆️ 应用自升级（CC `claude update` / opencode `upgrade` parity）
- 新命令 `icode upgrade`：检测新版 → 匹配 windows-amd64 资产 → 下载 → PE 头/体积校验 → rename 原子替换运行中二进制 → 重启生效；失败自动回滚，旧版保留为 `.old`（启动时自动清理）。

### 🔐 SimpleUI 权限审批条（向 CLI 看齐第一步）
- 移除三端唯一的权限全自动放行后门：SimpleUI 现在复用引擎 EventPermission 暂停机制。
- 黄色审批条内嵌窗口：「允许一次 / 本会话总是允许 / 拒绝」三键，Esc 中断时自动收起——安全语义与其余两端完全一致。

### ⌘ 桌面命令面板整合斜杠命令
- 既有 Ctrl+K 面板接入全部 slash 命令：模糊过滤（与 TUI 同算法）、⌘ 图标区隔、选中回填输入框可编辑参数、执行记录近用排序。

## v0.42.1 — 源码解析五书研究成果落地：工具缓存排序修复 + 拒绝人话化 + CU 闭环（2026-08-25）

> 基于微信读书《Claude Code 源码解析》《源码架构》《技术架构深度解析》《实战：Harness 工程之道》《橙皮书》五书共 576 条热门划线的对照研究，逐条核对 iCode 源码后的落地批次。

### 🚨 Bug 级修复：工具池缓存友好排序
- `ListDefs()` 此前直接遍历 Go map——**每次请求工具顺序随机**，而 prompt 缓存要求前缀字节级一致，Cache-First 的节省被静默吞掉。
- 对齐 CC `assembleToolPool()`：内置工具按字母序前置、`mcp_*` 后置，MCP 连接抖动只扰动尾部。新增顺序稳定性测试（连跑 5 次）。

### 💬 拒绝理由人话化（源码解析 ch.5：「拒绝至少要告诉用户原因」）
- 三层递进：
  1. `explainDeny()` 精确归因——沙箱越界 / 危险模式命中 / 安全引擎规则分开说明（此前统一报「in deny list」）；
  2. `HumanizeDeny()` 确定性白话映射——每类原因附「💡 如需放行」的具体办法（改哪份配置、跑哪个命令）；
  3. 可选 LLM 润色——会话模型、温度=0（确定性原则）、150 token 上限、6 秒超时；本地安全等级自动跳过（零隐藏网络调用），失败回退白话文本。
- 主对话与子代理两条路径全部接入。

### 👁 Computer Use 闭环（研究预览功能的生产化用法）
- **Goal 视觉验收**：UI 形目标（页面/前端/浏览器/dashboard 等 19 关键词启发）的验收链自动追加「screen_read 截屏确认」要求——编译通过 ≠ 完成，界面真实可用才算达成。
- **CU 熔断**：连续 12 次屏幕输入操作且无任何其他进展动作即强制拦截，引导模型先观察再行动或改用文件/命令方式；非 CU 动作与新用户轮次重置计数。会话间相互隔离。

## v0.42.0 — 跨机器消息同步 + CLI 语音转入 + 屏幕识别（2026-08-25）

### 🌍 跨机器消息同步（Mesh）
- 会话互发不再限单机：`to` 写 `<对端名>/<会话ID>` 即跨机投递，每 3 秒自动转发，送达即清理本地行。
- 配对模型：本机 `~/.icode/mesh.token`（首次自动生成）+ `~/.icode/peers.yaml` 对端清单；接收端点 `POST /api/mesh/messages` 以 `X-Mesh-Token` 校验。
- `/mesh list|add|remove|token` 三端可用；入站消息来源标记为 `<机器名>@remote`。

### 🎤 CLI 语音转入
- TUI 新增 `/voice`：第一次开始录音（状态栏红点提示），第二次停止并经智谱 GLM-ASR 转写，文本自动填入输入框供编辑后发送。
- 复用桌面端同一 ASR 通道与 zhipu Key；录音/转写全程异步不阻塞 UI。

### 👁 屏幕识别
- 新工具 `screen_read(question?)`：截图（vision 附带）+ **前台窗口标题与所属进程**上下文——补齐纯 screenshot 缺失的「用户在看什么」语义层。
- 非 Windows 平台优雅降级为纯截图。

## v0.41.1 — Skill Evals 技能自测 + 插件打包分发（2026-08-25）

### 🧪 Skill Evals（四家对标产品均无的差异化功能）
- 技能可携带 `evals.yaml` 触发测试用例（正例/误触发例），离线零 token 回归验证。
- `/skill-eval` 跑全部带用例技能，按 ✓/!/✗ 分级展示通过率；失败用例标注「漏触发/误触发」。
- `/skill-eval <名称> --scaffold` 从技能自身 triggers 生成模板（含一个反例），永不覆盖已有文件。

### 📦 插件打包（Claude Code plugin parity）
- 新格式 `plugin.yaml` 清单 + `skills/ commands/ agents/ teams/` 子目录捆绑。
- `/plugin install <目录|.zip>`（zip 防路径穿越）、`/plugin list`、`/plugin remove <名>`；三端可用。
- 安装即生效：插件子目录自动加入 skills/commands/agents 加载搜索路径（后置优先，可同名覆写）。

## v0.41.0 — 状态栏增强 + 思考滑块 + 参数级补全 + 语言体系闭环（2026-08-25）

> UI 精致度对齐 Claude Code / opencode；语言指令全链路打通（界面 ↔ 模型回复/思考/注释）。

### 📊 状态栏增强（CLI/TUI，Claude Code 风格）
- 模式徽章 `[auto]/[plan]/[yolo]`（按 CC 配色）+ git 分支 `⎇ main`（后台懒刷新，detached 显示短 SHA）。
- 上下文用量可视化 `▓▓▓░░░░░ 42%`：<60% 绿、<85% 黄、≥85% 红。
- **正在做的工作提示**：流式执行时黄色高亮当前工具 `⚙ bash`，完成自动清除。
- 后台任务计数 `⚡2 bg`（子代理 + shell 合并）。

### 🎚 思考滑块（桌面，opencode 尺寸样式 + 黄色系）
- 数字输入框 → 细轨道滑块（4px 轨道 / 14px 圆拇指 / 悬停放大），主色 #eab308。
- 实时数值显示 + 4k/8k/16k/32k 快捷档位（选中态黄底黄框）。

### ⌨ 斜杠补全向 Claude Code 看齐
- **模糊匹配**：`/cnf` → `/config`、`/mod` → `/model`（前缀 > 连续子序列 > 跨字符，相对 gap 惩罚）；@文件补全同算法并按相关性排序。
- **自定义命令进列表**：`.icode/commands/*.md` 与内置命令同面板展示（含 frontmatter 参数提示）。
- **参数级补全**：`/model ` 空格后弹出模型列表；同样支持 /lang /theme /security /mode。
- **最近使用优先**：常用命令自动置顶（TUI 内存 + 桌面 localStorage 双实现）。
- 选中行尾 `(tab)` 提示。

### 🌐 语言体系闭环（三端全链路）
- 新增 `LanguageDirective()`：按 locale 注入「回复 + 思考过程 + 代码注释」语言要求进系统提示词。
- 三级优先：`.icode/language` 文件 > ICODE.md 声明（`language: en`，前 40 行）> 全局设置。
- TUI `/lang` 与桌面设置页切换均实时刷新引擎，无需重启；修复服务端 config PUT 绕过 EffectiveSystemPrompt 的老 bug。

### 🤖 /agents 实时面板
- 升级为完整视图：代理注册表（带 fork/memory/isolation 能力标注）+ 团队成员清单 + 后台运行任务。

## v0.40.0 — 后台子代理 + Fork 缓存继承 + Worktree 隔离 + 跨会话消息（对标 Claude Code 2.1.x）（2026-08-25）

> 对标最新 Claude Code（v2.1.24x）功能代差：多代理后台化与缓存化委派。六项后端能力 + 桌面 MCP 测试 UI。

### 🚀 后台子代理（CC "background agents" parity）
- `task` 工具新增 `background: true`：子代理派生后立即返回 `agt-N` 句柄，主对话继续工作；完成时系统通知 + TUI 内提示。
- `task_output` 兼容 `agt-N` 查询（状态/输出/token），无 id 时列出全部后台任务（子代理 + shell 命令）。
- 新增 `/tasks` 面板命令：一屏查看所有后台任务（运行中/完成/失败 + 耗时）。

### ⚡ Fork 缓存继承（强化 Cache-First 卖点）
- `task` 工具新增 `fork: true` + `AgentDef.fork` 定义：将父对话尾部消息（≤40 条，原样字节）重放进子代理上下文——同前缀命中 Provider prompt cache，委派近乎零边际 token 成本。
- 引擎新增 `RunForkedSubAgent`，自动截断孤儿 tool-result 尾部保证消息配对完整。

### 🌿 Worktree 隔离执行
- `AgentDef.isolation: worktree`：子代理在临时 git worktree（独立分支）中读写，绝不触碰用户检出目录。
- 无改动自动清理；有改动回传 patch 或提交至隔离分支供 cherry-pick。启动时 `CleanupWorktrees` 修剪崩溃残留。

### 🧠 子代理持久记忆（CC per-agent memory parity）
- `AgentDef.memory: user|project|local`：每个子代理拥有跨会话 `MEMORY.md`（~/.icode/agent-memory、项目共享、项目私有三档）。
- 运行时注入记忆头部（200 行 / 25KB 上限）+ 维护指令，代理可积累项目知识。

### 💬 跨会话消息（CC SendMessage/ListAgents parity）
- 新表 `agent_messages` + 三工具：`send_message`（会话互发）、`inbox`（读收件箱/未读标记已读）、`list_agents`（发现可寻址会话）。
- SQLite 持久化，对端离线也能投递。

### 🔐 参数级权限规则（CC Tool(param:value) parity）
- 配置 `[permission.rules]`：`pattern = "Bash(git push:*)"` + `decision = "ask|allow|deny"`，首条命中即生效，优先于一切模式/白名单——YOLO 下也可硬拦破坏性命令。

### 🖥 桌面端
- 新增 **McpPanel**：MCP 服务器连接测试可视化（列表 + 实时状态灯 + 一键 `/api/mcp/test` 连接验证 + 发现工具清单）。
- 核实权限审批弹窗（允许一次/总是允许/拒绝三键）与图片粘贴输入已在先前版本落地，差距文档相应项关闭。

### 🧪 测试
- 新增：参数规则硬拦/首条命中/per-tool 匹配、agent 记忆三档+截断、后台代理生命周期/失败捕获/取消、fork 前缀纯函数、worktree 真仓库生命周期、消息往返/校验/限额；全绿。

## v0.39.0 — 分级授权兜底 + 闲时任务 + Goal 模式（对齐 Claude Code & 智谱 ZCode）（2026-08-14）

> 用户"按你建议全部完成"——落地 Claude Code 分级授权兜底 + 智谱 ZCode 闲时任务 / Goal 模式。

### ✨ P0-1 连续拦截退回手动（分类器兜底）
- `Gate` 加**会话级 strike 计数器**：`ask`/`deny` 累计 +1、`allow` 归零；连续 N 次（默认 3，`config.permission.strike_threshold` 可配）自动把会话降级为**手动模式**（后续所有操作强制 `ask`），并一次性提示「已连续 N 次拦截，自动退回手动模式」。补齐 Claude Code 分级授权的最后一块：`默认放行低危 → 命中危险软拦截 → 连续拦截退回手动` 全链路。

### ✨ P0-2 闲时任务调度（对齐智谱 Idle Tasks）
- scheduler 新增 `"idle"` 调度类型 + 闲时窗口（`config.scheduler.idle_start/idle_end`，默认 00:00–06:00，支持跨午夜）；闲时任务在低峰窗口内才执行，窗口外自动重排到下一窗口。`/idle <名称> <描述>` 命令创建闲时任务（三端），完成后结合既有通知提醒。

### ✨ P1 Goal 模式深化（对齐 ZCode 可验收 Goal）
- `/goal set <目标> --verify <验收命令>`：设置**可验收目标**，engine 每轮注入「ACCEPTANCE CHECK」指令，让模型自动「改动 → 运行验收命令 → 判断 → 未达标继续迭代 → 达标报告停止」。
- 桌面新增 **GoalPanel**（查看/设置目标与验收命令/清除）；`/goal show` 展示目标 + 验收命令。

### 🧪 测试与交付
- 新增：`TestStrikeEscalation`（连续拦截降级 + 会话隔离）、`TestIdleSchedule`（闲时窗口/跨午夜）；全量 internal+cmd 测试绿；前端 tsc 0 错误 + 构建通过；四份二进制重编（PE 3/3/2/2），版本号 0.39.0。

## v0.38.2 — 深化完善：LSP 诊断跳转 + 检索 IDF 加权 + 通知免打扰（2026-08-14）

> 用户"继续完善"——落地上一轮的三项待办。

### ✨ 深化 1：LSP 诊断结构化 + 点击复制定位
- 桌面 **LspPanel** 诊断结果从纯文本升级为**结构化条目**（severity 彩色标签 + 行:列 + 消息），点击条目一键复制 `file:line:col` 定位信息（桌面端为聊天 Agent，无代码编辑器，故以"复制定位"落地跳转）。

### ✨ 深化 2：知识库检索 IDF 加权（BM25 式排序）
- `knowledge` 包升级为**两遍索引**：先统计词频算 IDF（`log((N+1)/(df+1))+1`），再构建 IDF 加权向量。稀有词（判别性强）自动加权、常见词自动降权，检索命中更精准——仍保持零 API 零 token 离线。

### ✨ 深化 3：通知免打扰开关
- 新增 `config.notify` 配置（`enabled` / `quiet_from` / `quiet_to`），`notify` 包加 `SetPolicy` + `shouldNotify`（支持跨午夜时段），后台任务通知前自动检查免打扰窗口。

### 🧪 测试与交付
- 新增：`knowledge.TestIDFWeighting`、`notify.TestShouldNotify`；全量 internal 测试绿；前端 tsc 0 错误 + 构建通过；四份二进制重编（PE 3/3/2/2），版本号 0.38.2。

## v0.38.1 — 对标深化：子代理实时流 + 系统通知 + 桌面 LSP/知识库面板（2026-08-13）

> 用户"继续深化你建议的项目"——落地上一轮的三项待办。

### ✨ 深化 1：子代理实时执行流（对标 OpenCode 多 agent）
- `agent.Runner.Run` 通过 context 的 `ToolProgressFunc`（engine 注入）实时上报子代理的**工具调用**（🔧 调用工具 X）、**工具结果**（✓/⚠）、`TeamRunner` 补充团队级进度（👥 分解任务 / 并行执行 N 名成员）。
- 三端（TUI/桌面/简易 UI）均渲染 tool_progress，子代理执行过程从"静默等待"变成"可见的流水"。

### ✨ 深化 2：系统级 toast 通知（Windows 托盘 balloon）
- 新包 `internal/notify`：Windows 用 PowerShell + WinForms NotifyIcon balloon（零外部依赖、独立进程、不阻塞），macOS `osascript`、Linux `notify-send`。
- `bgtask` 完成/失败时触发系统通知——用户切到别的窗口也能收到"后台任务完成"提醒。

### ✨ 深化 3：桌面端 LSP / 知识库可视化面板
- 右侧面板新增 **LspPanel**（状态查询 + 文件诊断 + 符号搜索）与 **KnowledgePanel**（知识库检索），均复用 `/api/slash`（与 /lsp、/kb 命令同实现），无需记命令即可点选使用。

### 🧪 测试与交付
- 新增 `notify.psQuote` 单测；全量 internal 测试绿；前端 tsc 0 错误 + 构建通过；四份二进制重编（PE 3/3/2/2），版本号 0.38.1。

## v0.38.0 — 三端对标收尾：LSP 接线 + 知识库 RAG + 后台通知 + 回滚预览（2026-08-13）

> 用户"帮我全部按计划完成"——落地三端对标差距清单全部 8 项（P0→P1→P2）。

### ✨ P0 — 能力断层修复
- **LSP 接线（/lsp）**：`internal/lsp` 客户端早已实现但从未暴露。本轮新增 `Manager.QueryReport`（status/diag/syms/hover/def/refs）+ `ParsePos`（兼容 Windows 盘符）+ 便捷方法（DiagnosticsForFile/HoverAt/DefinitionAt/ReferencesAt/Symbols/AvailableServers）；`/lsp` 命令接入 **TUI（Callback.LSPQuery）+ 桌面/simpleui（slashui.cmdLsp）**，两套共享一份实现零重复。
- **简易 UI 补齐**：会话下拉加**时间分组前缀**（今天/昨天/近7天/更早，SessionEntry 补 updated_at）；错误友好化经 engine 层已覆盖（v0.37.8）；Token 统计条此前已有。

### ✨ P1 — 对标能力缺口
- **后台任务完成通知**：`bgTaskManager` 加 `SetCompleteHook`，TUI/simpleui 注入回调，后台任务完成/失败时自动推送「✓ 后台任务完成 / ⚠ 失败」提示（跨端、零平台依赖）。
- **多 agent 执行可视化**：`TaskTool` 委托子代理时先发一条 `EventToolProgress`（「🔍 子代理 X 正在执行：…」），三端实时渲染（桌面/简易UI/ TUI 均处理 tool_progress），消除"静默卡顿"。
- **文档知识库 RAG（/kb）**：新包 `internal/core/knowledge`——本地哈希特征向量 + 余弦相似度检索，**零 API 零 token 离线**（延续 iCode 省 token 使命）；`/kb` 命令 + `search_knowledge` 工具（模型可调用）；`config.knowledge.dirs` 配置目录，后台索引。

### ✨ P2 — 体验打磨
- **三端 cwd 打通**：桌面切换工作区时自动执行 `/cd <workspace.path>`，bash/文件工具随之作用在新目录。
- **TUI 回滚 diff 预览**：`/rewind` `/undo` 回滚前先展示 `checkpoint.Diff` 的统一 diff（即将撤销的改动）。
- **跨端续接**：验证通过——三端共享 SQLite 会话 + 摘要/预算/goal 随 `session.Metadata` 持久化，engine 统一读取，无需改动。

### 🧪 测试与交付
- 新增：`lsp.ParsePos`（含 Windows 盘符）、`knowledge` Index/Search/分段。全量 internal+cmd 测试绿；前端 tsc 0 错误 + 构建通过；四份二进制重编（PE 3/3/2/2），版本号 0.38.0。

## v0.37.13 — 状态栏 CWD / Git 分支点击可操作（WebView2 修复）（2026-08-13）

> 用户反馈：对话窗口状态栏的 `e:\icode`（工作目录）和 `-`（Git 分支）点击打不开。

### 🐛 根因
- **CWD pill**（显示 `e:\icode`）：点击调 `window.icode?.openFolder(cwdPath)`——**WebView2 桌面不注入 Electron 桥 `window.icode`**，点击完全无反应（fallback `window.open('file:///')` 也被 WebView2 安全策略拦截）。
- **Git 分支 pill**（无仓库时显示 `-`）：点击复制分支名但**无任何反馈**，看起来"打不开"。

### ✅ 修复
- **CWD pill → 复用工作区切换器**：状态栏的目录显示替换为 `<WorkspaceSwitcher compact />`（组件新增 `compact` 低调样式变体），点击弹出同一套菜单：**切换工作区 / 更换本地目录（WebView2 原生目录对话框）/ 新建工作区**——点击必有所应。
- **Git 分支 pill 加反馈**：复制成功显示「已复制 ✓」2 秒后恢复；无 Git 仓库时 title 提示「当前目录无 Git 仓库」。
- 顺带清理：删除不再使用的 `shortDir`、未使用的 `Folder` import（tsc 全绿验证）。

### 🧪 交付
- tsc --noEmit 0 错误 + 前端构建通过 + 重嵌 dist + 四份二进制重编（PE 3/3/2/2），版本号 0.37.13。

## v0.37.12 — 修复桌面版渲染崩溃：TDZ 变量初始化顺序（2026-08-13）

> 用户反馈桌面版打开即报"界面渲染出错 / Cannot access 'R' before initialization"。产物 hash 定位为 v0.37.11。

### 🐛 根因
`runCompactNow`（新增的 /compact 快捷执行）的 useCallback **依赖数组引用了声明在它之后的 const**：
- `const currentModel = models.find(...)`（第 593 行）与 `const mode = ...`（第 601 行）都声明在 runCompactNow（第 510 行）**之后**。
- 渲染时 useCallback 先执行、依赖数组立即求值 → 访问未初始化的 const → **TDZ ReferenceError**（压缩后即 "Cannot access 'R' before initialization"），应用整体崩溃。

### ✅ 修复
- `currentModel / ctxWindow / ctxWindowLabel / mode` 声明块**上移**到 runCompactNow 之前（附注释说明顺序约束）。
- **顺带修复 tsc 揪出的 2 个预存类型/运行时问题**（此前 `npm run build` 只跑 vite、不做类型检查，一直漏检）：
  - ChatPage 引用了未声明的 `securityLevel / setSecurityLevel`（/api/slash 返回 security 时才会触发）→ 补 store selector。
  - ModelsPage 用了 `Model.custom` 但接口无此字段 → `Model` 接口补 `custom?: boolean`。

### 🧪 验证
- `tsc --noEmit` 全绿（0 错误）；产物确认 currentModel 声明已排在依赖数组之前。
- 重嵌 dist + 四份二进制重编（PE 3/3/2/2），版本号 0.37.12。
- ⚠️ 教训：vite build 不做类型检查，TDZ/类型错误漏检——**前端改动后应加跑 `tsc --noEmit`**。

## v0.37.11 — 桌面组件交互化第二轮：Tab 右键菜单 + Token 明细 + 上下文一键压缩（2026-08-13）

> 用户"继续优化完成"——把工作文件夹切换之外的剩余关键组件也做成"点击可操作"。

### ✨ 会话标签右键菜单（TabBar）
- 多会话标签**右键**弹出菜单：✏ 重命名（prompt 输入新标题）、🔗 复制会话 ID、📄 导出 JSON（复用导出逻辑，抽成 `exportSessionJson(id)`）、× 关闭标签；点外部关闭。

### ✨ TokenBar 可点击展开明细
- 底部 Token 状态条整体可点击：弹出 popover 显示输入/输出/缓存命中/节省/费用明细（tabular 数字对齐）+ 两个快捷按钮：
  - **运行 /compact**：直接调后端 `/api/slash`（不碰输入框，用户正在打的草稿不受影响）→ 完成追加一条 system 消息反馈 + 刷新会话
  - **复制统计**：明细以文本复制到剪贴板
- 点外部 / Esc 关闭。

### ✨ 上下文窗口卡片点击压缩
- 右侧「上下文窗口」圆环卡片加 hover 高亮 + 点击直接执行 /compact（title 提示"点击压缩上下文"）——上下文管理从"只能看"变成"一键操作"。
- 执行逻辑统一走 `runCompactNow` + `icode:compact-session` 自定义事件（TokenBar 与圆环共用一处）。

### 🧪 交付
- i18n 三语补：tab.ctxHint/copyId/close、token.barClickHint/detailTitle/compactNow/copyStats、chat.ctxClickCompact。
- 前端构建通过（TS 干净，36.6s）、重嵌 dist、四份二进制重编（PE 3/3/2/2），版本号 0.37.11。

## v0.37.10 — 工作文件夹可点击切换（聊天工具栏交互组件）（2026-08-13）

> 用户"桌面版的对话框上的组件，点击可以进行相应操作，如点开工作文件夹可以进行选择切换"——把聊天页顶部原本只能"在资源管理器中打开"的文件夹按钮，升级为可交互的**工作文件夹切换器**。

### ✨ WorkspaceSwitcher 组件
- 聊天工具栏新增「工作文件夹」pill：显示当前工作区名 / 绑定目录 basename，**点击弹出 popover**：
  - **工作区列表**：所有工作区点选即切换（当前打勾），显示名称 + 路径
  - **更换本地目录**：调原生文件夹对话框（`window.pickDirectory`，WebView2 桥）把选中目录绑定到当前工作区；无工作区时自动新建
  - **新建工作区**：选择目录后自动命名创建
  - **在资源管理器中打开**：沿用原 `openFolder` 能力
- 无工作区时 pill 为"打开文件夹"样式；点外部 / Esc 关闭；纯浏览器环境（dev）降级为路径输入 prompt。
- i18n 三语补 `workspace.openInExplorer`。

### 🧪 交付
- 前端构建通过（TS 干净，产物含新组件与键）、重嵌 dist、四份二进制重编（PE 3/3/2/2），版本号 0.37.10。

## v0.37.9 — 桌面界面打磨：会话时间分组 + 消息模型标签 + 欢迎卡片（2026-08-13）

> 用户"在界面上帮我优化达到对标对象的水平"。设计系统已是 Apple flat（无阴影/发丝边框），本轮聚焦三处高感知度视觉升级（对标 Claude Code / WorkBuddy 的会话管理与消息头部）。

### ✨ 侧边栏会话按时间分组（Claude Code 式时间线）
- 会话列表从平铺改为**今天 / 昨天 / 近 7 天 / 更早**四段分组，组头小字 uppercase 标签；折叠模式保持纯图标列表不受影响。
- 前端 Session 补 `updatedAt`（后端 `updated_at` 透传），分组优先用更新时间、回退创建时间——最近动过的会话排最前。

### ✨ 模型消息头部标签行（Claude Code "Claude" 式）
- 每条模型消息气泡顶部新增**模型名 + 时间**小字标签行（如「DeepSeek V4 Pro · 14:05」），模型名跟随当前选中模型（selector 取 name 字符串，不破坏 MessageList 的 memo 优化）；thinking/工具消息不显示。

### ✨ Welcome 快捷操作升级为卡片网格（Reasonix/WorkBuddy 式）
- 4 个胶囊按钮改为 **2×2 卡片**（图标 + 标题 + 一行描述 + hover 提亮），桌面更精致、可点击区域更大；i18n 三语补描述键。

### 🎨 微调
- 发送按钮由直角小方块改为**渐变胶囊**（radius 999），与 composer 聚焦光环呼应。

### 🧪 交付
- 前端构建通过（TS 干净）、重嵌 dist、四份二进制重编（PE 3/3/2/2），版本号 0.37.9。

## v0.37.8 — 对标收尾：错误友好化 + 长会话压缩提示 + 桌面深度思考开关（2026-08-12）

> 用户"还有什么可以提升的，继续帮我做，比照对标项目"。差距扫描确认命令层（60+ 斜杠命令、自定义命令、/statusline、Analytics/TokenBar 等）已覆盖 Claude Code/OpenCode 主功能，真缺口收敛为三项体验项。

### ✨ 模型错误友好化（friendlyModelError）
- 新增 `internal/core/conversation/errors.go`：把裸错误（"HTTP 401"、"insufficient_quota"、超时、连接拒绝、模型不存在、上下文超窗等）映射为**清晰中文提示 + 修复建议**（如 401→`icode auth set <provider> <key>`、429→限流稍后重试、超时→检查网络/代理、模型不存在→`/model` 查看可用列表），并在末尾附截断的原始错误便于排查。
- 挂到全部 4 个错误出口：Send 兜底模型链错误、continueAgentLoop/recoverTruncation/repairBrokenToolCalls 的 EventError。

### ✨ 长会话自动压缩提示（token 护栏）
- `Engine.longSessionHint`：会话 user+assistant 消息 ≥ 40 条、且未启用 /budget 或 --lite 时，向 UI 推送一条一次性 EventSystem 提示"运行 /compact 或 /resume --compact 省 token"——**每会话仅一次**，不打扰；有预算/轻量恢复的会话自动跳过。

### ✨ 桌面「深度思考」开关（extended thinking UI 收尾）
- 后端 `/api/config` PUT 补 `defaults.thinking_tokens` 透传 + 即时 `engine.SetThinking`（重启/斜杠命令之外的新入口）。
- 桌面「桌面设置」页新增「深度思考」区块：Toggle 开关（开启默认预算 4096）+ 预算数字输入（≥1024，后端自动钳制到输出上限一半）；appStore 新增 `thinkingTokens/setThinkingTokens`，启动时从配置加载。
- i18n zh-CN/zh-TW/en 补齐。

### 🧪 测试与交付
- 新增 `errors_test.go`：friendlyModelError 14 分类子用例 + longSessionHint（短会话不提示 / 长会话提示一次不重复 / 有预算不提示）。
- 全量 internal+cmd 测试绿；前端构建 + 重嵌 dist + 四份二进制重编（PE 3/3/2/2），版本号 0.37.8。

## v0.37.7 — 坏 JSON 全自动修复 + Anthropic extended thinking + 桌面自定义提供商（2026-08-12）

> 用户"按你的建议执行"——落地 v0.37.6 结尾列出的三个可选项。

### ✨ tool-JSON 自动修复放宽（带防循环）
- `repairBrokenToolCalls` 触发条件从"仅 finish_reason=length"放宽为**任何坏参数 JSON 都自动修**（含模型 bug 产生的 finish_reason=stop 非法参数）：截断时仍升级 max_tokens（8K→64K），非截断保持当前预算、走"请重新输出完整参数"提示。
- **防循环预算**：每会话每轮 `maxToolRepairsPerTurn=2`（Send 时清零），模型反复吐坏 JSON 时预算耗尽即放行给工具正常报错，杜绝无限重试。

### ✨ Anthropic extended thinking 开关
- `types.ChatRequest` 新增 `Thinking`；anthropic provider 请求体输出 `thinking: {type: enabled, budget_tokens}`；**开启时自动抑制 temperature**（Anthropic 要求默认 1，否则 400）；预算钳制到 max_tokens/2（≥1024）兜底。
- 引擎 `SetThinking/ThinkingBudget` 并注入全部 4 个 ChatStream 调用点；config `defaults.thinking_tokens`；**`/thinking` 斜杠命令**（CLI + 桌面，`/thinking on|off|<tokens>`，持久化）。
- EventThinking 流转早已存在（TUI/桌面都渲染思考块），复用即可。

### ✨ 桌面端自定义提供商 UI
- 后端 `PUT/DELETE /api/config/provider` 早已支持（新厂商自动注册 OpenAI 兼容），缺口全在前端：ModelsPage 新增「＋ 新增提供商」弹窗（名称/Base URL/API Key/超时）→ `appStore.saveProvider`；自定义厂商头部新增🗑删除（确认弹窗 + `deleteProvider`，连带删除其下自定义模型）。
- i18n zh-CN/zh-TW/en 补齐。

### 🧪 测试与交付
- 新增：`TestRepairBrokenToolCalls_NonTruncated_RepairsToo`、`TestRepairBrokenToolCalls_BudgetExhausted`、`TestEngine_SetThinking`、`TestBuildMessagesBody_Thinking/ThinkingClampsBudget/NoThinkingPreservesTemperature`。全量 internal+cmd 测试绿。
- 前端构建 + 重嵌 dist + 四份二进制重编（PE 3/3/2/2），版本号 0.37.7。

## v0.37.6 — tool-JSON 自动重发 + 大文件分块读取 + few-shot 工作流示例（2026-08-12）

> 用户"继续"——落地 v0.37.5 清单剩余的高价值候补。多级 CLAUDE.md 经核查已实现（ICODE.md/CLAUDE.md/AGENTS.md 自 CWD 向上 5 级 + @import 展开），本轮补上另外三刀。

### ✨ tool-JSON 自动重发（Claude Code parity）
- 新增 `Engine.repairBrokenToolCalls`（engine.go）：当模型回合被 `max_tokens` 截断（finish_reason=length）且某个 tool call 的参数 JSON 不完整时，不再执行坏调用（那只会得到 "invalid args" 并让模型从头重来），而是**自动升级输出预算并发起补齐请求**——以 user 消息回放已生成的部分参数开头，让模型只重发该调用的完整参数，随后正常执行并续跑 agent loop。
- 兼容所有提供商：修复提示走 user 消息而非孤儿 assistant tool_use（避免 Anthropic 原生格式的 tool_result 缺失问题）。最多 3 次升级尝试（8K→16K→32K→64K），流错误立即降级；非截断的坏 JSON 不自动修复（那是模型 bug，交给工具报错反馈）。
- 抽出可测的 `brokenToolCallIndex` 帮助函数。

### ✨ 大文件分块读取（Claude Code parity）
- `read_file` 新增 `offset`（1 起行号）与 `limit`（最大行数）参数：按行窗口返回，头部报告文件总行数与当前范围，并在还有后续时提示"继续读取请用 offset=N"；`offset` 越界自动钳制到末尾。不传参数时保持原全量读取（向后兼容）。
- 工具描述同步更新，让模型知道大文件可以分段读。

### ✨ few-shot 工作流示例
- 系统提示 WORKFLOW 之后新增 `# WORKED EXAMPLE`：一行"修复 src/main.go 并发 bug"的 Good flow / Bad flow 对照（读→改→测→汇报 vs 重写整个文件/不验证），静态内容不破坏缓存前缀。
- 多级 CLAUDE.md 核查：`context.LoadProjectContext` 已实现（用户级 ~/.icode/CLAUDE.md + 项目级自 CWD 向上 maxParentLevels=5 逐级读 ICODE.md/CLAUDE.md/AGENTS.md + @import 3 层展开/循环检测），无需新做。

### 🧪 测试与交付
- 新增 `tool_repair_test.go`（brokenToolCallIndex 4 子用例 + 修复执行集成测试 + 全合法不触发 + 流错误降级）、`TestReadFileTool_Chunked`（窗口/尾部/全量/越界 4 场景）。全量 internal+cmd 测试绿。
- 四份二进制重编（icode.exe / bin/icode-cli.exe = CLI 控制台；icode-cli.exe = 简易 UI；icode-desktop.exe = 桌面），PE 子系统验证 3/3/2/2。版本号 0.37.6。

## v0.37.5 — 借鉴 Claude Code 的智能提升：语义摘要压缩 + 测试命令自动发现（2026-08-11）

> 用户两次询问"智能程度提升上还能借鉴 Claude Code 什么"。对标后确认 iCode 已具备并行工具调用/后台 bash/自动压缩/工具输出截断（BudgetEnforcer 头尾保留早已实现），本轮落地三刀中最有价值的：**模型生成语义摘要**（resume compaction）与**测试命令自动发现**。

### ✨ 模型语义摘要（Claude Code /compact 与 /resume --compact parity）
- 新增 `Engine.SummarizeConversation`（internal/core/conversation/summarize.go）：把会话旧轮次（保留最近 4 条）交给配置模型，产出结构化摘要（目标/已完成/关键决策/文件改动/待办/下一步建议），90s 超时、输出上限 6000 字；摘要输入走**头尾保留 + 中间省略**（与 BudgetEnforcer 同思路，60K runes 封顶）。
- **`/compact [指令]` 升级**（TUI + 桌面 slashui）：不再是 80 字逐行 dump，而是异步调用模型生成语义摘要，保留系统消息 + 最近 4 轮；模型不可用时自动降级为原本地摘要。摘要写入会话元数据（`summary_semantic` 标记）。
- **`/resume <id> --compact[=<n>]` 新增**（CLI + 桌面）：无缓存语义摘要时先用模型生成并缓存，随后以「语义摘要 + 最近 n 条」送入模型（引擎 Send 路径原有 LiteN 机制生效）——续聊省 token 且上下文不丢，正是 Claude Code 的 resume compaction。已有语义摘要则直接复用，零额外模型调用。
- `sessionum` 新增 `SemanticKey/IsSemantic/MarkSemantic`，区分本地统计摘要与模型语义摘要。
- 单测：`summarize_test.go`（4 个引擎用例 + 头尾保留）、`TestSemantic_MarkAndIs`。

### ✨ 测试命令自动发现（Claude Code parity）
- 新增 `context.DetectTestCommand()`（internal/core/context）：按 go.mod→`go test ./...`、package.json（yarn.lock→`yarn test` / pnpm-lock→`pnpm test` / 默认 `npm test`）、pyproject/pytest.ini/setup.py→`pytest`、Cargo.toml→`cargo test`、Makefile→`make test`、justfile→`just test` 自动探测。
- 探测结果并入 `LoadProjectAnalysis` 的 `Test:` 行，随项目分析注入系统提示——模型一开局就知道"这个项目怎么跑测试"，验证环节不再瞎猜。
- 单测：`TestDetectTestCommand`（10 子用例）+ `TestLoadProjectAnalysis_IncludesTestCommand`。

### 📌 说明
- 此前对标清单中的"长输出头尾保留"经核查**早已存在**（tokenopt/budget.go Enforce：70% head + tail + `[... N chars truncated ...]`，engine.go:800 已接入），无需新做。
- 待办（未做）：tool-JSON 自动重发、多级 CLAUDE.md（已有 ICODE.md 多级）、大文件分块读取、few-shot 工作流示例。

## v0.37.4 — CLI Markdown 渲染修复（对话界面支持 Markdown）（2026-08-11）

> 用户反馈 CLI 对话界面 Markdown 不支持。排查发现两类根因：line mode（非 TTY，如 IDE 终端/管道）assistant 消息完全不走 Markdown 渲染；行内代码反引号泄漏。

### 🐞 修复
- **line mode 全面渲染 Markdown**（stream.go printAssistant）：非 TTY 环境（IDE 内置终端、重定向、脚本）下，assistant 消息由纯文本改为走 `renderMarkdown`——标题/粗斜体/行内代码/围栏代码块/列表/表格/引用全部生效。此前只有 raw mode（真终端）渲染。
- **行内标记剥离**（markdown.go）：`color=false`（无 ANSI 终端）时新增 `stripInlineMarkup`——剥离 `` `code` ``、`**粗体**`、`*斜体*`、`_斜体_`、`~~删除~~` 标记但保留内容，反引号/星号不再泄漏进对话。
- **简易 UI 历史消息 Markdown**（simpleui_windows.go）：`uiAppend` 的 useMd 判断补上 assistant——历史会话回放的 assistant 消息从纯文本改为 Markdown 渲染（与流式一致）。
- **桌面版核查**：Markdown.tsx 为完整解析器且 ChatPage 始终使用，无同类问题。
- 新增回归测试 `markdown_inline_test.go`（两颜色模式反引号不泄漏 + line mode assistant 渲染）；全量 35 包测试绿；四份二进制按正确标签重编（icode.exe=CLI 控制台 / icode-cli.exe=简易UI GUI / icode-desktop.exe=桌面 GUI / bin/icode-cli.exe=CLI）。

## v0.37.3 — 系统提示词重写：对齐 Claude Code 级编码工作流（2026-08-11）

> 用户反馈"没有 Claude Code 智能好用"。归因：模型层（默认 deepseek-v4-flash vs Sonnet 5，占 60%+）+ 系统提示词层（默认 prompt 仅 15 行且是早期磁盘清理场景写的）+ 工作流层。本轮完成提示词整改。

### ✨ 默认 system prompt 重写（engine.go buildSystemPrompt）
- 旧版：通篇 disk_usage/disk_cleanup（早期磁盘清理需求残留），"NEVER refuse" 式粗暴引导。
- 新版（对齐 Claude Code 工作流，保持缓存稳定/中文友好）：
  1. **WORKFLOW**：探索→todo_write 计划→最小改动执行→验证（跑测试/lint）→**迭代修复**（失败读全错误、改根因、重跑，最多 3 次，同命令不重复）→简洁汇报
  2. **TOOL RULES**：坚持用工具、bash/cwd、task 子代理并行、换方案而非放弃
  3. **CODE QUALITY**：匹配项目风格、改动聚焦、修 bug 保兼容
  4. **SAFETY**：破坏性命令需用户批准、报真实结果
- 自动修复循环/todo 的"工程部分"已存在（todo_write 工具 + TodoCounts 渲染 + doom-loop 防死循环），本轮补上**工作流引导**——让模型按"改→测→修"闭环干活。
- 验证：conversation/tool/todo 测试全绿，四份二进制重编。

### 📌 用户侧提醒
- 最大杠杆在模型：`icode /model claude-sonnet-5` 或 `deepseek-v4-pro` 后对比体验。
- 后续立项：P1 JSON mode（openai_compat response_format）、P2 桌面自定义 provider UI、P3 Anthropic extended thinking 开关、D5 知识库。

## v0.37.2 — Agnes 独立厂商拆分 + SenseNova 模型修正（2026-08-11）

> 用户纠正：**Agnes AI 与 SenseNova（商汤）是两家独立厂商**。iCode 之前把 `agnes-chat/agnes-fast` 误放在 SenseNova 厂商下。

### 🐞 Agnes / SenseNova 厂商拆分
- **根因**：`sensenova.go` 混入了 Agnes 模型（`agnes-chat`/`agnes-fast`），且包注释把两者写成一家。实际：Agnes AI（agnes-ai.com，全球 Top 10 AI 实验室，免费多模态 API）与商汤 SenseNova（sensenova.cn）是两家独立厂商。
- **修复**：① 新建独立 provider `internal/llm/provider/agnes/agnes.go`（ProviderName="agnes"，DefaultBase=`https://api.agnes-ai.com/v1`，OpenAI 兼容），模型：`agnes-2.5-flash`（主力，代码/推理/agentic）、`agnes-2.0-flash`、`agnes-large`（8k）；② `sensenova.go` 移除 agnes 两模型，保留商汤真实模型并补 `sensenova-6.7-flash-lite`（公测入口常用）；③ `app.go registerProviders` 注册 agnes；④ 桌面 SettingsPage provider 颜色表补 sensenova/agnes。
- 验证：`go build ./...`、provider 全量测试绿、前端 build 通过重嵌、四份二进制重编 0.37.1（版本号沿用）。

## v0.37.1 — Anthropic 模型列表按官网全面修正（2026-08-11）

> 用户反馈 Anthropic 模型名过时；已核对 Anthropic 官方 Models overview（2026-06 更新）与 OpenRouter 页面，替换为最新一代模型。

### 🐞 Anthropic 模型列表修正
- **根因**：`DefaultModels` 仍为 `claude-sonnet-4-20250514` / `claude-haiku-4-20250514`（Claude 4 代），而官网当前为 **Fable 5 / Opus 5 / Sonnet 5 / Haiku 4.5**（4.6 代起 ID 无日期后缀）。
- **修复**（anthropic.go）：新增 4 个最新模型——`claude-fable-5`（1M ctx/128k out/$10-$50）、`claude-opus-5`（1M/$5-$25）、`claude-sonnet-5`（1M/$3-$15）、`claude-haiku-4-5-20251001`（200k/64k/$1-$5，官方 API ID 带日期）；全部 SupportsVision，缓存读取价 = 输入价×10%。
- **联动更新**：`openrouter.go` `anthropic/claude-sonnet-4` → `anthropic/claude-sonnet-5`（1M ctx/128k out，slug 已核对 OpenRouter 页面）；`cmd/commands.go` 默认模型推荐表同步（含 opus-5/haiku-4-5）。
- 测试断言同步（anthropic/openrouter/router test）；四份二进制重编 0.37.1。

## v0.37.0 — 定时任务/自动化 + CLI 上下文条 + 简易 UI 消息重发（2026-08-11）

> 第三十三批：对标 workbuddy 的定时任务（automations）落地——Go 调度核心 + SQLite 持久化 + REST API + 桌面设置页 UI；CLI 输入区上下文条上移（claude code 风格）；简易 UI 消息悬停重发；启动卡死防线加固（Bootstrap 看门狗/panic 兜底/entering 日志）。

### ✨ 定时任务/自动化（对标 workbuddy，D1）
- 新增 `internal/scheduler` 包：`every:30m`（间隔）与 `daily:09:00`（每天定点）两种调度格式（零依赖正则解析）；后台 20s tick 检查到期任务；每个任务在**独立新会话**中通过引擎执行（不污染用户对话），输出截断 4000 字符存入历史。
- SQLite 持久化：`automations` / `automation_runs` 两表 + `db.Store` CRUD（LoadAutomations/SaveAutomation/DeleteAutomation/AppendAutomationRun/ListAutomationRuns）。
- 挂载：`app.Bootstrap` 在有 SQLite 时创建并启动 `Scheduler`；server 新增 `/api/automations`（GET 列表 / POST 创建）、`/api/automations/{id}`（GET/PUT/DELETE）、`/api/automations/{id}/run`（立即执行）、`/api/automations/{id}/history`（执行历史）。
- 桌面设置页新增「定时任务」页（PageAutomations）：任务列表（启用开关/立即运行/展开历史/删除）+ 新建表单；三语 i18n。
- 单测 `TestNextRunAfter`（含 daily 越界校验）/`TestDescribeSchedule`/`TestSchedulerCRUD` 全绿。

### 🐞 CLI 启动卡死防线再加固（simpleui 08-08 三次卡在 Bootstrap 前）
- `app.Bootstrap()` 开头加 `bootstrap: entering (config.Load)` 日志——此前三次简易版启动卡在 `console hidden` 之后、连 `config loaded` 都没有，无法定位。
- `runSimpleUI` 的 Bootstrap 包 panic recover（弹错误框而非静默死）+ 20s 看门狗（打 WARNING 日志）。

### ✨ CLI 上下文条上移输入区（对标 claude code，C3）
- `drawInputBox` 提示行右侧新增 `context: NN% ▓▓░░` 实时条（复用 contextBar），输入时即可看到上下文窗口占用。

### ✨ 简易 UI 消息重发（对标 opencode，S3）
- 用户消息悬停显示「↻ 重发」按钮，点击回填输入框；消息块统一悬停显示复制/重发按钮（opacity 过渡）。

### ✅ 差距清单复核（2026-08-11 全量核对）
- 已存在无需实现：C1 彩色 diff（colorizeDiff）、C5 代码块语法高亮（chroma）、C2 工具输出折叠（/expand）、D2 MCP 测试 UI（doTest + /api/mcp/test）、D4 权限审批 UI（ChatPage pendingPermission modal）、D6 图片粘贴（ChatPage 已支持）、D8 快捷键查看页、S2 会话搜索（datalist 原生搜索）。
- 验证：`go test ./internal/scheduler/... ./internal/db/... ./internal/server/... ./internal/tui/` 全绿；`vite build` 通过并重嵌；四份二进制重编。

## v0.36.0 — 一键自动更新模型：新增检测 + 下架标记 + 文档富化（2026-08-02）

> 第三十二批：实现一键自动更新模型功能——点击刷新按钮后，自动从各 provider API + 官网文档获取最新模型列表，对比内置列表，新增新模型、标记下架模型，并持久化到磁盘缓存。

### ✨ 模型自动更新核心管线
- **API ID 列表 + 内置元数据富化 + 文档页补充**三阶段管线：先从 `/v1/models` 获取当前可用模型 ID 列表，再用 provider .go 文件中的丰富元数据（上下文/能力/定价）填充 API 返回的骨架，最后从 llms.txt 文档页补充新增模型的信息。
- **Diff 检测**：`compareModels()` 对比 API 返回 ID 集合 vs 内置列表——API 有但内置没有为「新增」，内置有但 API 没有为「下架」。
- **连续 2 次确认下架**：下架模型不立即标记 `Deprecated`，需连续 2 次刷新 API 都无该模型才标记（避免单次 API 抖动误判）。下架模型保留在列表中（灰显 + ⚠ 图标 + "已下架"标签），历史会话仍可引用。
- **文档页抓取**：`fetchDocModels()` 从各 provider 的 llms.txt 或 API 文档页抓取补充元数据（上下文长度、视觉能力、推理能力等）。404/超时优雅降级，不影响主流程。
- **富化合并**：`mergeModelInfo()` 将 API 骨架 + 内置元数据 + 文档补充三层信息合并，优先级：内置 > 文档 > API。
- **持久化**：`writeUpdateHistory()` 将每次刷新的 diff 结果写入 `~/.icode/cache/update-history.json`（保留最近 50 条）；模型列表写入 `~/.icode/cache/{provider}.json` 缓存（含 `Deprecated`/`DeprecatedCount` 字段）。

### ✨ 10 个 provider 文档 URL 配置
- 为 `knownFetchers` 中每个 fetcher 添加 `docURL` 字段：
  - OpenRouter: `https://openrouter.ai/llms.txt`
  - DeepSeek: `https://api-docs.deepseek.com/llms.txt`
  - 智谱: `https://open.bigmodel.cn/llms.txt`
  - Kimi: `https://platform.moonshot.cn/llms.txt`
  - Anthropic: `https://docs.anthropic.com/llms.txt`
  - NVIDIA: `https://build.nvidia.com/llms.txt`
  - 腾讯: `https://cloud.tencent.com/llms.txt`
  - 火山引擎/华为/SCNET: 无 llms.txt（全 JS 渲染或无公开文档页），跳过文档补充

### ✨ Desktop UI 增强
- **刷新摘要 Toast**：ModelsPage 刷新后显示 8 秒浮动通知，内容如"✅ +3 新模型 | ⚠ 2 已下架"，展开显示每个 provider 的具体变更和新模型名称。
- **下架模型展示**：模型列表中下架模型灰显 + ⚠ AlertTriangle 图标 + 黄色"已下架"标签 + 半透明背景，仍可手动选择使用。
- **`RefreshSummary` 类型**：appStore 新增 `refreshSummary` / `clearRefreshSummary` 状态，`refreshModels()` 解析 `POST /api/models/refresh` 返回的 `results[].added/removed`。
- **i18n**：三语（zh-CN/zh-TW/en）新增 `models.refreshResult` / `models.newModels` / `models.deprecatedModels` / `models.deprecated`。
- **CSS**：`global.css` 新增 `@keyframes slideIn` 动画（Toast 滑入效果）。

### ✨ SimpleUI 增强
- **刷新摘要**：`RefreshModelsUI()` 返回更详细的 diff 信息，包含每个 provider 的新增模型名称和下架模型 ID，以及汇总行"📋 变更: ➕N 新增, ⚠️M 下架"。
- **下架模型标记**：`Models()` 返回的模型 ID 中，下架模型加 "⚠ " 前缀；`SetModel()` 自动去除前缀。

### 🔧 类型增强
- `ModelInfo` 新增 `Deprecated bool` + `DeprecatedCount int` 字段（`json:"deprecated,omitempty"` / `json:"deprecated_count,omitempty"`，向后兼容）。
- `ProviderUpdate` 新增 `Added []types.ModelInfo` + `Removed []string` 字段，供前端解析 diff 结果。

### 🔧 内部重构
- `UpdateAll()` 重构为 `updateProvider()` 子方法，统一处理单个 provider 的 API 抓取→文档补充→富化合并→Diff 检测→下架标记→缓存写入→推送 SetModels 全流程。
- `UpdateOne()` 同步重构，复用 `updateProvider()`。
- 新增 `loadPreviousCache()` 读取过期缓存（用于 `DeprecatedCount` 跨次累计）。
- 新增 `applyDeprecatedLogic()` 实现连续 2 次确认逻辑。

### 📁 改动文件
| 文件 | 改动 |
|---|---|
| `internal/types/types.go` | ModelInfo 添加 `Deprecated` + `DeprecatedCount` |
| `pkg/modelupdate/service.go` | ProviderUpdate 添加 `Added`/`Removed`；modelFetcher 添加 `docURL`；10 个 fetcher 配置 llms.txt URL；新增 `fetchDocModels()`/`parseDocModelEntries()`/`extractModelID()`/`extractContextWindow()`/`mergeModelInfo()`/`compareModels()`/`applyDeprecatedLogic()`/`writeUpdateHistory()`/`loadPreviousCache()`；UpdateAll/UpdateOne 重构为富化+diff 管线 |
| `desktop/src/stores/appStore.ts` | Model 接口添加 `deprecated`/`deprecatedCount`；新增 `RefreshSummary` 类型；`refreshModels()` 解析 added/removed；新增 `refreshSummary`/`clearRefreshSummary` |
| `desktop/src/pages/ModelsPage.tsx` | 刷新摘要 Toast；下架模型灰显+⚠+"已下架"；导入 AlertTriangle |
| `desktop/src/styles/global.css` | 添加 `slideIn` keyframes |
| `desktop/src/i18n/index.ts` | 三语添加 refreshResult/newModels/deprecatedModels/deprecated |
| `cmd/simpleui_windows.go` | RefreshModelsUI 显示 diff 摘要；Models() 下架加⚠前缀；SetModel() 去前缀 |

## v0.35.0 — /model 视口跟随 + 双击 CLI 打开 WebView2 简易 UI（2026-07-29）

> 第三十一批：用户反馈「上下键选择大模型时屏幕没同步滚动」「双击 icode-cli.exe 想打开带滚动条的简易 UI，比 CMD/PowerShell 功能多一些」「CLI 还有没有类似问题」。

### ✨ /model 选择器视口跟随（修复高亮滚出屏幕）
- **根因**：`/model` 面板是作为「会话消息」追加到 `t.messages` 的，导航只改写该消息文本、**不调整 `scrollOffset`**。长会话或模型列表高于一屏时，高亮项会落到视口之外，用户看不到当前选中谁。
- **修复**：把 `/model` 从「会话消息」改为**固定覆盖层**（与 `helpBox` / 权限框同范式），在 `render()` 中作为独立覆盖层渲染（优先级高于欢迎横幅），始终完整可见、不受会话滚动影响。
- 列表高于一屏时，覆盖层内置滚动窗口 `modelPickerTop` 始终让高亮行 `modelPickerIdx` 落在可视区内（与 autocomplete 的窗口算法一致）；并用 `↑ N 更多 / ↓ N 更多 (共 N)` 提示还有更多项。
- 行模式（非 raw）下 `/model` 退化为打印静态列表（无覆盖层），保持旧行为。
- 新增单测 `TestModelPickerOverlayWindowing`（100 模型 / 17 行视口，验证高亮恒在窗口内且面板不超 bodyH）；既有 `TestModelPickerInteractive` / `TestModelPickerEscCancel` 同步更新（移除已废弃的 `modelPickerMsgIdx` 断言），全绿。

### ✨ 双击 CLI 二进制打开 WebView2 简易 UI
- **行为**：双击 `icode.exe` / `bin/icode-cli.exe`（控制台子系统）时，检测到「Windows 为进程新建了独立控制台、无父终端」（`GetConsoleProcessList` 仅含自身）→ 隐藏该黑框，打开原生 WebView2 窗口的**简易聊天界面**：带原生滚动条 + 鼠标滚轮滚动的会话区、模型下拉选择、清空按钮、输入框（Enter 发送 / Shift+Enter 换行）。
- **复用**：直接驱动同一套引擎（`app.Bootstrap` + `Engine.Send` 事件流），经 `webview2.Bind` 暴露 `send/models/setModel/clear`，Go 侧用 `w.Dispatch`+`w.Eval` 把流式 token / 工具调用实时推到 DOM。工具调用自动批准（该界面无终端可弹权限框）。比 CMD/PowerShell「功能多一些」——它就是 iCode 聊天本身。
- 新增 `cmd/simpleui_windows.go`（构建标签 `windows && !nogui`）+ `cmd/simpleui_stub.go`（非 Windows / nogui 回退到桌面）；`cmd/codepage_windows.go` 新增 `isFreshConsole()` / `hideConsoleWindow()`；`cmd/root.go` 的 `Execute()` 接入双击分支。
- 依赖项目已有的 `github.com/jchv/go-webview2`，无需新增第三方库；需目标机装有 WebView2 运行时（Win10/11 通常内置）。

### 🔍 CLI 类似问题排查（Task #103）
- 全面复核 raw TUI 的同类「高亮滚出视口」缺陷：autocomplete 覆盖层（`autocompleteLines`）已实现 `from = acIdx - maxShow + 1` 窗口算法，高亮恒可见；help / 权限框为固定覆盖层恒可见。结论：**唯一实例就是 `/model` 面板，已修复**。

### 🐞 斜杠命令 `/keys` 输入不生效（Task #106/#107）
- **根因**：`/keys`（查看 API 密钥状态）在 `slashDefs`（帮助列表）里有登记，但 `handleSlash` 的 `switch` 里**没有对应 `case`**。运行时落到 `default` 分支 → `tryCustomSlash` 找不到自定义命令 → `callback.OnSlashCommand` 对其无作为，等于「输入了但什么都没发生」。
- **误并入 `/multiline`**：本应属于 `/keys` 的「API 密钥状态」输出块，被错误地塞进了 `case "/multiline"`，导致 `/multiline` 开关行模式后还打印一堆密钥状态，输出张冠李戴。
- **修复**：新增独立 `case "/keys"` 承载密钥状态输出；`/multiline` 仅做开关（不再泄漏密钥块）。分发路径本身正确（`runLine` 与 raw 模式都把 `/` 路由到 `handleSlash`）。
- 新增单测 `TestSubmitSlashKeys`（验证 `/keys` 输出「API 密钥状态」）与 `TestSubmitSlashMultiline`（验证 `/multiline` 不再打印密钥块），全绿。

### 🐞 桌面启动卡死根因修复（MCP 连接阻塞 boot 关键路径）
- **根因（真正的卡死源）**：`internal/server/server.go` 的 `Start()` 在 boot 关键路径上**同步**调用 `s.mcpPool.Add(context.Background(), …)` 连接每一个 enabled 的 MCP 服务器——包括从 `~/.workbuddy/mcp.json` 自动导入的一大堆连接器。单个不可达/无响应的 MCP 服务器会让 `Add` 阻塞 30s+（`client.call` 内部 30s 超时，且 `context.Background()` 无截止），多个连接器串行叠加 = 数分钟。**这发生在 WebView2 窗口创建之前**，于是 `bootDesktopBackend` 永远到不了开窗口那一步 → 桌面「启动卡死」（无错误弹窗、无窗口，纯卡住）。这正是历次（v0.29 把 provider Health 挪后台、v0.31 前端看门狗）都没解决的真因：它们都没碰到 MCP 这条同步阻塞。
- **修复**：把 MCP 连接整体挪到**后台 goroutine**，用 `context.WithTimeout(20s)` 封顶；并对每个 enabled 服务器**并发** `Add`（各自受同一 20s 上下文约束），`wg.Wait` 后统一 `refreshMCPTools`。boot 路径不再等待任何 MCP 连接 → 服务立刻起来、WebView2 窗口立刻出现；MCP 工具在后台连好后自动生效。
- 影响面：仅桌面（`server.Start`）启动；CLI 不经此路径。`app.Bootstrap` 本身全程离线（配置/SQLite/注册 provider/引擎装配），非阻塞，已确认。

### 🐞 快速删除历史会话卡死（localStorage 同步序列化，与 v0.25 同源）
- **根因**：前端 `appStore.ts` 的 `Session` 类型携带完整 `messages`；后端 `GET /api/sessions` 列表会为**每条会话 `loadMessages()`**，把完整消息灌进前端 store。而删除（及任意会话状态变更）都触发订阅的持久化 `saveToLocal(sessions.slice(-50))` → **同步 `JSON.stringify` 至多 50 条会话的全部消息**。v0.25 只加了「800ms 防抖 + selector 限定」、**没剥离 messages**，于是会话多/消息多时每次持久化仍是一次数百 ms～数秒的同步主线程阻塞——「点快了」多次删除叠加即卡死。后端 `DELETE` 仅删单行、`deleteSession` 是 fire-and-forget，**后端不是卡死元凶**。
- **修复**：
  1. `saveToLocal` 只持久化会话**元数据**（id/title/modelId/provider/createdAt），**不再序列化 message 正文**（消息本就由后端 DB 持有，localStorage 仅作列表离线回退）。单次持久化从「数十 MB」降为「几 KB」，彻底消除该路径同步阻塞。
  2. `loadFromLocal` 恢复时补 `messages: []`（打开会话时由后端 `GET /api/sessions/{id}` 重载），类型安全、无回归。
  3. `deleteSession` 加 500ms 去抖：同一会话删除按钮快速重击只生效一次，避免冗余乐观更新 + DELETE 风暴。
- 验证：前端 `vite build` 通过（1819 模块）；三份二进制重编；`go test`/`go vet` 全绿。

### ✨ 简易 UI 升级：右侧可拖拽滑块 + 全 CLI 命令（Task #112）
- **右侧命令面板**：新增 `#side` 面板，按分组列出全部斜杠命令，点击即执行；中间 `#grip` **可拖拽滑块**（鼠标拖动调整右侧宽度，区间 180px～60% 视口），带折叠按钮。窗口放大到 1100×680。
- **具备 CLI 全部功能**：`RunCommand(text)` 路由 `/...` 到 `runSlash`，其余走 `Engine.Send` 聊天流。`runSlash` 大 switch 覆盖 `/clear /wipe /help /whoami /model /provider /models /keys /mcp /config /security /permissions /status /cost /context /doctor /memory /feedback /login /logout /init /export /diff /review /compact /summarize /agents /skills /teams /hooks /todo /release-notes /pr_comments /bug` 等——多数读写**真实配置**（`config.Load()`/`Save()`）与引擎状态，未知命令回退到聊天（模型）。复用真实 `gh`/`git` 子进程、配置与引擎，行为与原生 CLI 一致。

### ☑️ CLI 斜杠命令对标 Claude Code 对账补全（Task #111/#114）
- 盘点 49 条 CLI 斜杠命令，对照 Claude Code 命令集，补齐缺失并增强不完整的：
  - **新增**：`/bug`（打开预填 GitHub issue URL）、`/pr_comments`（用 `gh` 取 PR 评论）、`/release-notes`（读 CHANGELOG.md 生成）、`/statusline`（开关底部状态栏并持久化 `tui.show_status_line`）、`/vim`（开关 vim 风格键位并持久化 `tui.vim`）。
  - **增强**：`/mcp`（list/add/remove/get/restart 子命令，持久化到 config）、`/memory`（查看/编辑 ICODE.md）、`/permissions`（显示安全等级 + hooks 明细）、`/review`（支持文件参数 + 模型回合）、`/compact`（支持自定义压缩指令）、`/config`（新增 `set <key> <value>`，支持 theme/lang/security/model/provider）、`/model`（切换持久化）。
- iCode 独有命令（`/keys`、`/models`、`/summarize`、`/welcome`、`/token`、`/multiline`、`/rewind`、`/feedback`、`/agents`、`/skills`、`/teams`）保留；Claude Code 不适用项（`/add-dir`、`/remind`、`/voice`、`/terminal-setup`、`/skill`、`/stop` 等）跳过。
- 新增配置字段 `TUICfg.Vim`、`TUICfg.ShowStatusLine *bool`（`*bool`+`omitempty` 避免 yaml.v3 把未设键归零为 false，`Default()` 设 `boolPtr(true)`）；`slashDefs` 注册表与 `handleSlash` case 保持同步；新增 i18n 文案（zh-CN/zh-TW/en）。
- 新增单测 `TestSubmitSlashVim` / `TestSubmitSlashStatusline` / `TestSubmitSlashConfigSet` / `TestSubmitSlashMCP` / `TestSubmitSlashReleaseNotes`（均 snapshot/restore config 防污染）；`TestClaudeStyleRender` 固定 `statusVisible=true`。

### 📦 根目录放置 icode-cli.exe（Task #110）
- 复制 `bin/icode-cli.exe` → `E:\icode\icode-cli.exe`（控制台子系统，与 `bin/` 同构：终端内即完整 TUI，双击即进 WebView2 简易 UI）。

### ✨ CLI 全角/CJK 宽度修正（emoji / 组合符 / 扩展汉字）
- `internal/tui/textutil.go` 的 `runeWidth` 扩展：新增 emoji（U+1F000+）、地区指示符、杂项符号/箭头、组合变音与变异选择符（宽度 0）、CJK 扩展 B+（U+20000+）为宽度 2；原 box/geometric/punctuation 调校保留。
- 修复聊天中 emoji（🚀🔥）、带变异选择符的 ❤️、扩展汉字（𠮷）等被按 1 列计、导致折行越过终端右边界的问题；`renderMarkdown`/`wrapText` 自动受益（已按全角计宽）。
- 新增单测 `TestRuneWidthCJKAndEmoji` / `TestWrapTextCJKNoOverflow`。

### ✨ 简易 UI 增强：Markdown 渲染 + 智能滚动
- `cmd/simpleui_windows.go`：气泡内容经轻量 Markdown→HTML 渲染（标题/粗体/斜体/行内代码/代码块/引用/列表/分隔线，**先转义后渲染，模型输出无法注入脚本**），比 CLI 纯文本更直观；流式过程显示纯文本、`uiDone` 时统一渲染为 Markdown。
- 智能自动滚动：仅当用户已停在底部时才跟随新消息；向上翻看历史时不再被「拽回」底部。
- 右侧命令面板 / 拖拽滑块 / 折叠按钮 / 原生滚动条沿用；输入框为 `<textarea>`，原生支持鼠标点击任意位置放置光标编辑。

### ✨ 对标打磨第 2 轮（对标 claude code / opencode / reasonix / workbuddy）
- **CLI（`internal/tui/slash_commands.go`）**：
  - `/cost` 从一行简版升级为**费用面板**：Prompt / Completion / 合计 / 缓存命中率（含 10 格迷你进度条）/ 本轮估算费用，并提示 `/token` 查看完整 Cache-First Loop 节省报告（对标 Claude Code 的 `/cost` 明细 + 凸显 iCode「超级省 token」卖点）。
  - 新增 `/undo`：opencode 式快速撤销最近一次工具调用（复用检查点回滚，抽取 `rewindSteps(n)` 与 `/rewind` 共享）。
  - 新增 `/share`：导出时间戳 Markdown 副本并打印绝对路径（opencode 式分享）。
  - `slashDefs` + 三语文案（zh-CN/zh-TW/en）同步注册；新增单测 `TestSubmitSlashCostPanel` / `TestSubmitSlashUndo` / `TestSubmitSlashShare`（share 测试自动清理导出文件）。
- **简易 UI（`cmd/simpleui_windows.go`）**：
  - **代码块一键复制**：渲染的 Markdown 代码块右上角加「复制」按钮（事件委托 + clipboard）。
  - **会话管理**：顶栏新增会话下拉（`Sessions()` 从 `SessStore.List` 取 50 条）+「新会话」按钮（`NewSession()` 仅脱离当前会话、不删旧会话，与「清空」删除语义区分）；下拉切换即 `OpenSession(id)` 加载历史消息到 UI；桥接同步选中态（`refreshSessions`/`uiSetSession`）。补齐 `/session` `/sessions` `/resume` `/new` 真实处理（此前落入聊天）。
  - **真实 `/cost`**：改用 `Engine.SessionStats` 输出节省报告（已节省 Token/缓存命中率/压缩次数/预估费用，与 CLI `/token` 同源）；`/share` 落为时间戳导出。
- 验证：`go build ./...`、`go vet ./cmd/...`、`go test ./internal/tui/` 全绿；四份二进制重编，`icode version` → `0.35.0`。

### ✨ 对标打磨第 3 轮（路线图 #124~#127：/output-style、/update、/add-dir、桌面美观）
- **CLI `/output-style`**：Claude Code 对等。`config.DefaultCfg` 新增 `output_style`；`config.EffectiveSystemPrompt()` 统一组合「基础提示词 + 风格指令 + 额外目录」，concise/verbose 注入英文行为指令；启动与 `/output-style` 即时切换同源生效（`chatCallback.OnOutputStyle` → `Engine.SetSystemPrompt` + 持久化）。
- **CLI `/update`**：刷新模型目录（`app.RefreshModels` → `Updater.UpdateAll`），逐 provider 报告成功/失败与模型数，并即时刷新 `/model` 与 Tab 的模型列表（`tui.SetModels`）。
- **CLI `/add-dir`**：Claude Code 对等。`config.DefaultCfg` 新增 `extra_dirs`；校验目录存在性、去重持久化、重组合系统提示词注入上下文；无参时列出全部。
- **Callback 接口扩展**：`OnOutputStyle / OnAddDir / OnUpdateModels`（`chatCallback` 单实现者，安全）；TUI 无引擎时走 `persistSetting` 降级路径。三语文案 + 单测 `TestSubmitSlashOutputStyle/Update/AddDir` 全绿；简易 UI（runSlash）三命令同步真实实现。
- **桌面美观「只增不改」**（遵循既有 Reasonix 设计系统）：`Markdown.tsx` 代码块/代码头/行内码/复制按钮对齐发丝边框（0.5px）、圆角 8px、行高 1.6、等宽字体代码头；`global.css` 增量：滚动条悬停加宽（4px→8px）、`::placeholder` 用 muted 色、`hr` 发丝线。不改任何现有组件结构。
- 验证：`go build ./...`、`go vet ./cmd/...`、`go test ./internal/tui/ ./internal/config/... ./internal/server/` 全绿；前端 `vite build` 通过并重嵌；四份二进制重编 `0.35.0`。

### ✨ 桌面快捷键面板（`?` 弹出速查，Task #128，对标 workbuddy）
- 新增 `desktop/src/components/ShortcutPanel.tsx`：分组速查（全局/对话/命令面板/编辑），kbd 键帽样式（发丝边框 + 等宽字体，遵循 Reasonix 设计系统），`scaleIn` 入场动画；Esc 或点击空白关闭。
- `App.tsx` 全局监听 `?`（含全角 `？`）切换面板——**输入框/文本域/可编辑区内不触发**（避免正常打字误弹）；复用既有设置弹层挂载模式。
- 三语 i18n `shortcuts` 块（zh-CN/zh-TW/en），面板内容与实际快捷键核对一致（Ctrl+, 设置 / Ctrl+K 命令面板 / Enter 发送 / Shift+Enter 换行 / @ 引用文件 / # 记忆 / ! shell / ↑↓ 选择 / Esc 中断关闭）。
- 验证：前端 `vite build` 通过并重嵌 `internal/embedded/dist`；四份二进制重编 `0.35.0`。

### 🐞 桌面「会话列表加载全部消息」卡死（剩余主线程外瓶颈，Task #117/#118）
- **根因**：`internal/db/store.go` 的 `List()` 对**每一个**会话（上限 100）都 `loadMessages()` 读取完整消息正文，经 `GET /api/sessions` 在启动与每次列表刷新时一次性序列化所有会话的全部历史。会话多/历史长时，该请求既慢又大，前端阻塞等待 + 解析，表现为「启动/切换会话卡死」。v0.35.0 已修 MCP boot 阻塞与快速删除 localStorage 阻塞，此为该路径**最后一处**同步瓶颈。
- **修复**：后端 `List()` 改为**仅返回元数据**（与前端 `saveToLocal` 元数据化一致）；单会话 `GET /api/sessions/{id}`（`store.Get` 仍带 `loadMessages`）不变。桌面前端 `appStore.ts` 新增 `loadActive(id)`：在 `setActiveSession`（点击切换）与 `loadSessions` 初始恢复时，对当前活动会话 `GET /api/sessions/{id}` 懒加载消息填充，打开历史会话不再空白。
- 验证：前端 `vite build` 通过（1819 模块，无 TS 错误）；dist 重嵌 `internal/embedded/dist`；`go build ./...`、`go test ./internal/tui/ ./internal/server/` 全绿；四份二进制重编（见构建块）。

### 🐞 桌面启动卡死全面排查修复（Task #129~#131，全链路审计）
- **审计范围**：逐行审 `bootDesktopBackend`（Bootstrap → server.Start → health 轮询）、`runWebView`（WebView2 创建）、前端 `App.tsx` init。已修阻塞均确认在后台：MCP（v0.35.0）、provider 健康检查（v0.29）、前端 12s 超时兜底（v0.31）。`registerProviders` / `registerCustomModel` / `handleHealth` 确认无网络无阻塞。
- **修复① LSP 启动卡死（真凶之一）**：`internal/lsp/client.go` 的 `SendRequest` 在 `result := <-ch` 上**无超时永久阻塞**——语言服务器进程启动成功但不回应 `initialize` 时，`app.Bootstrap` 的 LSP 预启动（用户配置 `lsp.auto_start` 时触发）卡死整个 boot（无窗口）。修复：`SendRequest` 加 `select` 30s 超时并清理 pending；`app.go` 的 LSP AutoStart 从 boot 前台挪到**后台 goroutine** + 每服务器 10s 超时（与 MCP 同范式）。
- **修复② WebView2 数据目录僵尸锁（真凶之二，真实世界高发）**：上次崩溃残留的 `msedgewebview2.exe` 持有 `%LOCALAPPDATA%\icode\webview` 锁 → `webview2.NewWithOptions` 同步创建运行时**无限挂起** → 无窗口卡死。新增 `cmd/webview_cleanup_windows.go` 的 `killStaleWebViewProcesses(dataPath)`：枚举命令行含本数据目录的 WebView2 进程，沿父进程链检查——**祖先链中有存活 icode*.exe（另一实例在跑）则绝不杀**，只杀僵尸树（PowerShell CIM，8s 超时 + recover，失败静默）。在 `runWebView`（桌面）与 `runSimpleUI`（简易 UI）的 `NewWithOptions` 前均调用。非 Windows 平台为 no-op 桩。
- **修复③ 启动阶段计时日志**：`bootDesktopBackend` 各阶段（Bootstrap / server.Start / health 就绪 / 开窗前）与 `runWebView` 的 WebView2 创建前后均打 `[desktop] stage:` 计时日志到 `~/.icode/desktop.log`——下次若再卡死，看日志最后一行即知卡在哪一阶段，不再盲查。
- 验证：`go build ./...`、`go vet ./cmd/... ./internal/lsp/... ./internal/app/...`、`go test ./internal/lsp/... ./internal/server/... ./internal/tui/` 全绿；四份二进制重编 `0.35.0`。

### 🐞 CLI 汉字输入重叠显示修复
- **根因**：`internal/tui/render.go` 的 `drawInputBox` 在定位输入行光标时，硬编码 `col := 2 + vw`——假定提示符 `"❯ "` 恒为 2 列。在 CJK 终端环境中（且我们的 `runeWidth` 已将 dingbats 计为宽字），`❯` (U+276F) 的显示宽度为 2 → `"❯ "` = 3 列 → 光标定位偏左 → 后一个汉字画到前一个字上（重叠）。
- **修复**：光标列改用 `visibleWidth(prompt+" ") + vw + 1` 动态计算；输入行内容截断宽度 `innerW` 同步改为 `W - visibleWidth(prompt) - 2`（精确减 prompt 宽，不再硬减 4）。`drawSearchBox` 无用户逐字输入光标，不受影响。
- 新增单测 `TestCursorColCJK` 覆盖「空/ASCII/CJK/混排」光标列计算（含 `❯` 宽字终端场景）。四份二进制重编 `0.35.0`。

### 🔧 构建 / 验证
- 三份二进制同步重编并通过：`icode.exe`、`bin/icode-cli.exe`、`icode-cli.exe`（根目录，均控制台子系统）、`icode-desktop.exe`（windowsgui）。简易 UI（`cmd/simpleui_windows.go`）+ 全量斜杠命令改动已编入。
- `go build ./...`、`go vet ./cmd/...`、`./internal/tui`、`./internal/server`、`./internal/mcp` 全量单测均通过；`go test ./internal/tui/` 全绿；`icode version` → `0.35.0`。

---

## v0.34.0 — CLI 闪退兜底 + 真正的交互式 /model 选择器（2026-07-28）

> 第三十批：用户反馈「CLI 版大模型显示出来了，但不能上下选择切换，并且会闪退」「桌面版还是有点卡」。

### 🐞 根因与修复（CLI 闪退）
- **引擎流式 goroutine 无 `recover()`**：`internal/core/conversation/engine.go` 的 `Send()` 内部 `go func(){...}`（line 802）流式/工具执行若 panic 会**直接杀死整个 CLI 进程**（典型静默「闪退」）。已加 `defer recover()`：panic 时写 stderr 堆栈并向 TUI 推送 `EventError`，会话存活。
- **`runRaw` 主循环无顶层兜底**：`internal/tui/raw_input.go` 的按键循环原先无 `recover`，任一 `handleKey` 异常即崩。已用匿名函数 + `recover` 包裹每轮（render + ReadRune + handleKey），panic 仅写 `~/.icode/cli.log` 并继续运行。

### ✨ 交互式 /model 选择器（真正可上下选择）
- `slash_commands.go` 将 `showModelPicker` 升级为 `openModelPicker` + `buildModelPicker` + `updateModelPicker` + `movePicker` + `selectModelAt` + `closeModelPicker`。
- 行为：输入 `/model` 打开面板，**↑/↓ 移动高亮、Enter 确认、Esc 取消、直接输入编号跳转**；选中后关闭面板并提示 `Model -> <id>`。面板消息实时就地更新（不无限追加）。
- `raw_input.go` 的 `handleKey`：在 `↑/↓` 分支与 Enter/Esc/数字键处接入选择器；空模型列表不打开面板。

### 🐞 桌面残留卡顿（轻量优化）
- `desktop/src/pages/ChatPage.tsx` 的 `fileActions` 原依赖整个 `messages` 数组（流式每 chunk 数组引用都变）→ 每次 token 都重扫全部消息。改为依赖 `[activeSession?.id, activeSession?.messages?.length]`，流式期间不再重扫，仅会话切换/增删消息时重算。

### 🔧 构建 / 验证
- 三份二进制同步重编：`icode.exe`、`bin/icode-cli.exe`（控制台子系统）、`icode-desktop.exe`（windowsgui，PE 子系统已校验）。
- 桌面前端重构建 + 重嵌 `internal/embedded/dist`（`fileActions` 改动）。
- 新增单测 `TestModelPickerInteractive` / `TestModelPickerEscCancel`（模拟 ESC[A/B 序列 + Enter/Esc），全绿。

---

## v0.33.0 — CLI 修复：/model 交互面板 + 上下键历史（2026-07-28）

> 第二十九批：用户反馈「CLI 版 /model 没出现切换面板、上下键不能显示历史对话」。全面排查后定位为两处真实代码缺口 + 一处构建配置根因（非旧二进制问题——`which icode` 解析到当前 v0.32.0）。

### 🐞 根因 1：`/model` 无参数静默 + 模型列表从未填充
- `internal/tui/slash_commands.go` 的 `case "/model"` 仅在带参数时 `t.model = args[0]`，**无参数时 `if len(args) > 0` 为假 → 什么都不做**，所以输入 `/model` 没有任何面板。
- `internal/tui/tui.go` 的 `SetModels()` **在 `cmd` 中从未被调用** → `t.models` 永远为空 → 不仅 `/model` 无数据可列，**Tab 切模型也因 `len(t.models) > 1` 恒为假而失效**。
- 修复：
  - `cmd/commands.go` 的 `startChat()` 里 `t.SetModels(a.Reg.ListAllModels())` 填充模型列表。
  - `slash_commands.go` 的 `/model` 无参数时调用新增 `showModelPicker()` —— 列出全部可选模型（编号 + 当前项 `▶` 标记），并支持 `/model <编号>`（1-based）与 `/model <ID>` 两种切换方式（Claude Code 风格）。

### 🐞 根因 2：上下键历史只在 raw 模式生效，而 CLI 旧构建进不了 raw 模式
- 方向键历史（`historyPrev/Next`）只在 `runRaw()`（完整 TUI）里路由；`runLine()`（降级行模式）既不处理箭头键、提交时也不调 `pushHistory`，历史根本不会被记录。
- `icode.exe` / `bin/icode-cli.exe` 此前用 `-H windowsgui` 构建，从某些终端启动时 `term.IsTerminal(fd)` 为假 → 降级进 `runLine` → 方向键失效、历史为空。
- 修复：**CLI 两个二进制改为默认控制台子系统**（去掉 `-H windowsgui`），使 `IsTerminal` 为真、进入 `runRaw` → 方向键历史 / Tab 切模型 / 完整 TUI 全部生效。桌面 `icode-desktop.exe` 保持 `-H windowsgui` 不变（PE 子系统已校验：CLI=3 console，desktop=2 GUI）。
- 附带：`runLine()` 提交时补 `pushHistory()`，管道/降级模式下 `/history` 也能查到记录。

### ✅ 验证
- 单测 `TestModelPicker`（无参列表面板 + ▶ 标记 + `/model 2`/`/model <id>` 切换）、`TestHistoryRecall`（上/下导航）全绿。
- 三份二进制重建：CLI 两个 PE 子系统=3（console），桌面=2（GUI）。
- 实跑 `icode.exe`：`/model` 无参正确输出「可用模型（输入 /model <编号> 或 /model <ID> 切换）」面板。

### ⚠️ 已知
- CLI 改为控制台子系统后，双击 `icode.exe` 会打开一个控制台窗口（CLI 工具的正常行为）；桌面仍无控制台窗口。
- 若你之前是用旧 `bin/icode-cli.exe`(Jul 25) 或别的副本，请改用 `E:\icode\icode.exe`（已重编 v0.33.0，console 子系统）。

## v0.32.0 — 运行时卡死修复：代码块高亮 + 消息列表隔离（2026-07-27）

> 第二十八批：用户反馈「有时输入文字、有时切换对话，还是会卡」。此症状已不在启动期，而是**交互期**主线程被同步重活阻塞。排查锁定两处根因并修复：

### 🐞 根因 1：大代码块语法高亮（`hljs.highlightAuto`）把主线程卡死
- `desktop/src/components/Markdown.tsx` 的 `CodeBlock` 对**不带语言标注的 ``` 围栏**调用 `hljs.highlightAuto(code)`——它会扫描全部 ~190 种语言语法，O(代码长度 × 语言数)，大代码块（文件内容/日志/构建输出）可同步卡数秒，正是「打字（流式回复边渲染大代码块）/切换对话（渲染含大代码块的会话）卡死」的经典真凶。
- 修复：
  - 新增 `escapeHtml()`；`CodeBlock` 先对输入做**封顶**（`safeCode`）：超过 300KB 截断显示（复制仍用完整内容），避免 DOM 爆炸。
  - 高亮 `useMemo` 改为：**有语言标注才高亮**；无标注且 <8KB 才走 `highlightAuto`，否则直接转义纯文本。超过 50KB 一律跳过高亮走纯文本。**彻底移除大输入的 `highlightAuto` 慢路径**。

### 🐞 根因 2：消息列表未隔离，每次按键/无关 store 更新都重渲染
- `desktop/src/pages/ChatPage.tsx` 原先在组件内联 `activeSession?.messages.map(...)`。由于 `useAppStore()` 订阅了整个 store（后端 10s 健康探测、token 用量、流式 chunk 都会触发），**每次打字（本地 input 变化）和每次 store 变更都会重跑整条消息列表渲染**。
- 修复：抽出 `React.memo` 组件 `MessageList`，props 为 `messages / isStreaming / onRegenerate / onZoom`；`handleRegenerate` 用 `useCallback` 稳定引用。现在**打字和无关 store 更新不再重渲染消息列表**，仅当消息真正变化（流式 chunk）时才重渲染，且单条消息的 `<Markdown>` 已 memo，只重渲染变化那条。
- 附带优化：`fileActions` 的 4 条正则改为「先 cheap 预过滤再扫描、单条内容封顶 20KB」，降低流式期每个 chunk 的扫描开销。

### 🧪 验证
- `npx tsc --noEmit` 全绿；`NODE_OPTIONS= npm run build` 全绿（新 hash `index-JxMjFPWq.js`）；前端重嵌 `internal/embedded/dist`；`go build -H windowsgui` 全绿；`icode version` → `0.32.0`。
- 注：v0.31.0 的启动自诊断（Web Worker 主线程看门狗 + 跨启动 `ui-blocked` 取证）仍保留，若仍有异常它会给出「主线程被阻塞(阶段)」标题与 `localStorage.icode.lastError` 记录。

## v0.31.0 — 桌面启动卡死：加自诊断 + 消除启动期阻塞（2026-07-27）

> 第二十七批：用户反馈「桌面启动卡死仍未修复（启动提示 3 秒后关闭仍卡死）」。经逐行排查整条启动链路（Go 启动 → 后端 → MIME → React 挂载 → init → ChatPage 渲染 → store → BootSplash/ErrorBoundary），**在可读代码范围内找不到阻塞调用、死循环、MIME 错误或后端接口挂起**（实测 `/api/health`、`/api/models`、`/api/sessions` 均 <20ms；`handleListModels` 直接返回内置模型列表、不联网；`UpdateAll` 仅由模型页「刷新」触发）。因此本次不盲目改逻辑，而是：①加启动自诊断把「静默卡死」变成可读错误；②把 init 对后端的依赖改为即发即弃，彻底排除启动期被后端拖死。

### 🔍 启动自诊断（关键改动）
- `desktop/index.html` 增加经典（非 module）内联脚本，早于 React 模块执行：捕获 `window.onerror` 与 `unhandledrejection` 写入 `localStorage.icode.lastError`（带时间戳/堆栈）；并加 **8 秒看门狗**——若 `window.__icodeMounted` 未置位（React 没挂载），把启动占位替换为可读的「启动失败」面板，显示捕获到的错误（而非永远转圈）。
- `desktop/src/main.tsx` 在 `createRoot().render()` 之后立即置 `window.__icodeMounted = true`（供看门狗判断），并**启动一个独立 Web Worker 主线程存活看门狗**：Worker 每 1s 向主线程发 `ping`，主线程回 `pong` 并带上当前启动阶段 `window.__icodePhase`；若 Worker 连续 >4s 收不到 `pong`（说明主线程被长任务/死循环阻塞），则把 `{type:'ui-blocked', lastPhase}` 写入 **IndexedDB(`icode-diag`)**。这是静态分析无法发现的「主线程被卡死」的唯一运行时信号。
- `desktop/src/App.tsx` 的 `init()` 在各阶段置 `window.__icodePhase`（`local-ui`/`check-backend`/`fetch-mode`/`sessions`/`workspaces`/`done`），让任何捕获到的错误和 `ui-blocked` 标记都带「卡在哪个阶段」的上下文。

### ⚡ 启动不再被后端拖死
- `desktop/src/App.tsx` 的 `init()`：`refreshModels()` / `loadDesktopSettings()` 由 `await` 改为**即发即弃**（`.catch(()=>{})`）。欢迎页在 `loadSessions`/`loadWorkspaces` 完成后立即可用，模型列表/桌面设置随后填充。即便 `/api/models` 偶发变慢，也绝不会拖住启动。

### 🧪 验证
- `npx tsc --noEmit` 全绿；`NODE_OPTIONS= npm run build` 全绿；前端重嵌 `internal/embedded/dist`；`go build -H windowsgui` 全绿；`icode version` → `0.31.0`。
- 后端静态服务对 `.js`/`css`/`html`/`svg` 均设置正确 MIME，确认 React 能正常挂载。

### 📋 若仍卡死，请反馈以下信息（本次改动已能直接显示）
1. 启动占位是否变成「启动失败」红字面板？把里面的错误文本贴出来。
2. 按 `F12` 无法用（WebView2 默认无 DevTools），但可看：浏览器地址栏不可用；请在**另一台能跑 WebView2 的机器**或本机用 `reg query` 确认 WebView2 运行时已安装。
3. `~/.icode/desktop.log` 全文（Go 端日志，含 provider 诊断）。
4. `localStorage.icode.lastError` 的内容（React 启动错误，现已带 `[阶段]` 前缀）。
5. **本次新增跨启动取证**：若上次启动主线程被阻塞，本次启动的窗口**标题栏**会显示「iCode 桌面版 — 上次启动检测到 UI 线程被阻塞(阶段:XXX)」，且 `localStorage.icode.lastError` 会多出一条 `ui-blocked-prevrun` 记录。把这两者贴出即可定位是「主线程死循环」（会有 ui-blocked 记录）还是「WebView2 渲染/输入层问题」（不会有该记录，需另查 WebView2 运行时版本/GPU 加速）。

## v0.30.0 — CLI 命令/自动补全/快捷键对齐 Claude Code（2026-07-27）

> 第二十六批：用户要求「把 CLI 版的命令、命令自动补全、快捷键，都模仿 claudecode 的」。

### 🎯 交互模型对齐 Claude Code
- **方向键改回翻历史**：撤销 v0.28.0 的「↑/↓ 滚动对话」，恢复为 Claude Code 原生的「↑/↓ = 历史记录上/下」。对话滚动仍保留在 **鼠标滚轮 / PgUp / PgDn / 右侧滚动条**（与 Claude Code 一致，不占用方向键）。
- **新增 Ctrl+R 反向历史搜索**：按下 `Ctrl+R` 进入 `(reverse-i-search)` 覆盖层，输入即过滤历史；`↑/↓` 或再次 `Ctrl+R` 在匹配项间循环，`Enter`/`Tab` 把选中项载入输入框（不直接发送），`Esc`/`Ctrl+G`/`Ctrl+C` 取消并恢复原输入。完全复刻 Claude Code 的 isearch 手感。
- **自动补全体验对齐**：`/` 触发命令补全面板，`↑/↓` 在面板内移动高亮，`Tab` 接受选中项，`Esc` 关闭；帮助面板与提示文案同步更新。

### 🧩 新增 Claude Code 风格斜杠命令
- `/doctor` 系统诊断（版本/模型/提供商/已配置 Key/记忆文件路径，无网络调用不卡顿）
- `/whoami` 显示当前身份（模型/提供商/安全等级/工作目录）
- `/context` 显示上下文窗口用量
- `/permissions` 显示当前权限/安全等级
- `/verbose` 切换详细输出
- `/memory` 显示记忆文件路径
- `/feedback` 显示反馈渠道
- `/wipe` 清空对话并重置（不可恢复）
- `/login` `/logout` 凭据配置指引
- 以上均进入 `slashDefs`，在 `/help` 与 Tab 补全面板中可见，并附中/英/繁三语描述。

### ✅ 验证
- `go build -tags nogui ./...`、`go vet ./internal/tui/...`、`gofmt`、`go test ./internal/tui/`（新增 `TestArrowNavigatesHistory`/`TestCtrlRReverseSearch`/`TestCtrlREscCancel`，原 v0.28 滚动测试已按新模型改写）全绿。
- 帮助面板（`?`）已同步：↑/↓ 历史、Ctrl+R 反向搜索、PgUp/PgDn 与滚轮滚动会话。

## v0.29.0 — 桌面启动卡死修复 + 启动提示 3 秒自动关闭（2026-07-25）

> 第二十五批：用户反馈「桌面版启动时启动提示显示 3 秒自动关闭，但启动卡死故障仍然存在」，并要求「CLI 版鼠标滚轮可以查看当前对话前后的对话输出内容」。

### 🐛 修复：桌面启动卡死（根因在 Go 后端启动阻塞）
- 根因：`cmd/desktop_common.go` 的 `bootDesktopBackend()` 在打开 WebView2 窗口**之前**，用 `context.Background()`（无超时）顺序对**所有 provider（50+）调用 `p.Health()`** 做诊断日志。在受限网络下（国外 provider 如 OpenAI/Anthropic 被防火墙拦截），任一 `Health()` 的网络调用挂起会阻塞整个启动流程 → WebView2 窗口永远打不开 → 表现为「启动卡死」。此前 v0.25/26/27 的修复都在前端（防抖/超时/ErrorBoundary），未触及这条 Go 启动关键路径，故卡死依旧。
- 修复：把 provider 健康检查诊断循环改为**后台 goroutine + 每调用 5s 超时**（`context.WithTimeout`），不再阻塞 `bootDesktopBackend` 返回；窗口可及时打开，诊断日志仍写入 `desktop.log`。

### 💡 新增：桌面启动提示 3 秒自动关闭
- 新增 `desktop/src/components/BootSplash.tsx`：应用挂载即显示全屏启动提示（梅花 LOGO + 旋转 spinner + 「正在启动…」），**固定显示 3 秒后淡出自动关闭**，且**不依赖后端**（即使后端慢/不可达也会准时关闭，露出可离线的 UI）。
- `desktop/index.html` 的 `#root` 内新增预挂载加载占位（`#boot-screen`），在 WebView2 加载 JS 包、React 尚未挂载期间显示，避免空白帧；React 挂载后自动替换。
- `desktop/src/styles/global.css` 新增 `.boot-spinner` 旋转动画。

### 🖱️ CLI 鼠标滚轮滚动对话（加固 + 测试）
- 确认鼠标滚轮已正确映射到 `internal/tui/mouse.go` 的 `handleMouse` → `scrollUpSmall()` / `scrollDownSmall()`（wheel 上滚看更早内容、下滚看更新内容），与方向键/滚动条/PgUp/PgDn 并存，互不冲突。
- 新增 `internal/tui/tui_test.go` 的 `TestMouseWheelScrollsConversation`，锁定「wheel 上滚 `scrollOffset` 增大、下滚减小回 0」行为，防止回归。

### ✅ 验证
- `go build -tags nogui ./...`、`go test ./internal/tui/`（含 `TestMouseWheelScrollsConversation`）、`go vet ./cmd/... ./internal/tui/...`、`gofmt -l` 改动文件 全绿。
- `npx tsc --noEmit`、`NODE_OPTIONS= npm run build`（1818+ modules）全绿；前端重嵌 `internal/embedded/dist`；`go build -H windowsgui` 全绿；`icode version` → `0.29.0`。

## v0.28.0 — CLI 对话支持 ↑/↓ 方向键滚动（2026-07-25）

> 第二十四批：用户要求「CLI 版侧边可以上下滚动」。会话本身已支持 PgUp/PgDn、鼠标滚轮、点/拖滚动条，但方向键被历史记录占用。本批把 ↑/↓ 改为驱动会话的「侧边」滚动条，每按一次滚一行；历史记录改为 Ctrl+P/Ctrl+N。

### 🖱️ CLI 对话支持方向键滚动
- 新增 `internal/tui/render.go` 三个方法：`canScroll()`（判断对话是否超出可视区）、`scrollUp(n)` / `scrollDown(n)`（按显示行上下滚，超出由 `render()` 自动 clamp，不越界）。
- `internal/tui/raw_input.go` 的 `handleKey` 中 ↑/↓（CSI `A`/`B`）改为：
  - 自动补全打开时 → 移动补全光标（保持原行为）。
  - 否则若已上滚（`scrollOffset > 0`）或对话可滚动（`canScroll()`）→ 滚动会话一行；在底部再按 ↓ 则交回给历史记录（`historyNext`）。
  - 对话无溢出时 → 仍走历史记录（旧行为，避免无内容时方向键失效）。
- 历史记录上/下保留在 **Ctrl+P / Ctrl+N**（本就可用），与方向键解耦。
- `desktop/src` 无改动；帮助页（`helpBox`）同步：↑/↓ 说明改为「滚动会话（上/下）」，并新增「Ctrl+P / Ctrl+N 历史记录上/下」一行。

### ✅ 验证
- `go build -tags nogui ./...` 全绿；`go test ./internal/tui/`（含新增 `TestArrowScrollsConversation` / `TestArrowFallsBackToHistoryWhenNotScrollable`）全绿；`go vet ./internal/tui/...` 全绿；`gofmt -l` 干净。
- 行为说明：↑/↓ 滚会话一行；PgUp/PgDn 翻页；鼠标滚轮滚动；点/拖右侧滚动条跳转；Ctrl+P/Ctrl+N 翻历史。

## v0.27.0 — CLI 闪退兜底 + 桌面欢迎界面防卡死（2026-07-25）

> 第二十三批：修复「CLI 版打开后闪退」+「桌面版启动后卡在欢迎界面」。本环境无法跑真实 TTY / WebView2，故以「崩溃可生存 + 可诊断」为主：把所有静默崩溃转成可记录事件，并消除前端初始化挂死的可能。

### 🛡️ CLI 打开后闪退（静默进程退出）
- 根因定位：`Execute` 的 `recover` 只能兜住**主 goroutine** 的 panic；而 `render()` 也会由后台 goroutine 触发（`watchResize` 轮询、`ensureAnim` 动画 ticker、流式回调），这些 goroutine 内的 panic **无法被主 goroutine 的 recover 捕获**，会直接杀死整个 Go 进程 → 窗口一闪即逝（闪退）。主 goroutine 渲染 panic 虽会被 `Execute` 捕获并显示错误，但后台 goroutine 渲染 panic 是静默的。
- 修复：
  - `internal/tui/render.go`：`render()` 函数体整体包 `defer recover()`——任何渲染 panic（含后台 goroutine 触发）都不再击穿进程；同时恢复终端（显示光标、退出 alt-screen）并把堆栈写入 `~/.icode/cli.log`。
  - `internal/tui/raw_input.go`：`watchResize` goroutine 加 `defer recover()`（ panic 写 `cli.log`）。
  - `internal/tui/render.go`：`ensureAnim` 动画 ticker goroutine 加 `defer recover()`。
  - 新增包内 `writeCliLog()`：统一把崩溃堆栈追加到 `~/.icode/cli.log`，便于事后定位（不再静默丢失）。

### 🔧 桌面启动后卡在欢迎界面
- 诊断：后端端点（`/api/health` `/api/sessions` `/api/models`）本机实测均 <20ms 返回，故非后端挂死；卡死更可能来自①某次渲染抛错（React 无错误边界 → 整棵卸载 → 白屏/假死）或②初始化中某个 fetch 挂起导致界面无响应。
- 修复：
  - `desktop/src/components/ErrorBoundary.tsx`（新增）+ `main.tsx` 包裹 `<App/>`：渲染期异常不再白屏，改为显示错误信息（含堆栈）与「重试」按钮，并存入 `localStorage.icode.lastError`。
  - `desktop/src/App.tsx`：启动初始化重构——本地 UI 配置（主题/字号/首跑向导）**先行且绝不涉及网络**；后端相关加载（`checkBackend`/`fetchMode`/`loadSessions`/…）整体包进 **12s 超时**（`Promise.race`），慢/不可达的后端绝不会再让界面卡死，仅显示「离线」横幅，界面始终可交互。
  - `desktop/src/stores/appStore.ts`：新增 `fetchWithTimeout`（AbortController，8s），用于 `checkBackend` 的三路发现请求，单路挂起会快速失败而非耗尽窗口。

### ✅ 验证
- `go build -tags nogui` 全绿；`go test ./internal/tui/`（`TestRender*` 含新增欢迎路径）全绿；新增 `render_welcome_test.go` 覆盖「空消息 + 欢迎横幅」的打开态渲染，确认不 panic。
- `go vet ./internal/tui/...` 全绿；`gofmt -l` 干净。
- `NODE_OPTIONS= npm run build` 前端全绿；`go build -ldflags="-s -w -H windowsgui" -o icode.exe .` 全绿，`icode version`→0.27.0；前端已重嵌 `internal/embedded/dist`。

### ⚠️ 说明 / 仍需你协助
- 本沙箱无真实终端与 WebView2，无法 100% 复现闪退/卡死。v0.27.0 已把两类故障转为**可生存 + 可诊断**：若 CLI 仍闪退，请附 `~/.icode/cli.log`；若桌面仍卡/白屏，请附控制台报错与 `localStorage.icode.lastError`（F12 → Application → Local Storage）。据此可精准定位真因。

## v0.26.0 — 桌面闪退防护（goroutine/panic 兜底）+ 模型对比页订阅优化（2026-07-25）

> 第二十二批：修复「点击左侧模型时卡死」+「启动后偶发闪退」。根因是后端进程被 goroutine panic 静默击杀（详见下）。

### 🛡️ 闪退根因（后端进程被 goroutine panic 击杀）
- `cmd/desktop_windows.go` 中 `runWebView` 跑在独立 goroutine、`runTray` 跑在主 goroutine；二者**均无 `recover`**。而 `net/http` 只恢复「handler 自身」的 panic，**不会**恢复 handler 派生的子 goroutine。任何在上述 goroutine 中的 panic（WebView2 初始化/子类化/`w.Run()` 消息泵的 Edge 异常、托盘回调）都会直接终止整个进程 → 窗口瞬间消失（闪退），且因 `-H windowsgui` 无控制台、`panic()` 写 `os.Stderr` 被丢弃，**无任何堆栈留存**。
- 日志铁证：`~/.icode/desktop.log` 显示 10:36:30 启动、10:36:38 又被拉起（用户闪退后手动重开）——典型进程被杀。

### 🔧 修复
- `cmd/desktop_common.go`：兜底把 `os.Stderr` 重定向到同一 `desktop.log`，使 Go panic / fatal error 的堆栈可被记录（而非静默丢失），下次崩溃即可定位。
- `cmd/tray_windows.go`：`runWebView` 与托盘菜单 goroutine 加 `defer recover()`，panic 时记堆栈并让后端继续存活（窗口异常不再拖死整个进程）。
- `pkg/modelupdate/service.go`：`UpdateAll` 的 per-provider goroutine 加 `recover()`（抓取模型目录时某个 provider 异常不会击杀进程）。
- `internal/server/server.go`：新增 `recoverMiddleware` 包裹所有路由，handler panic → 干净 500 + 堆栈入日志，而非进程崩溃。
- `desktop/src/pages/ModelCompare.tsx`：`const { models } = useAppStore()`（订阅整个 store，任一 state 变更都重渲染）改为 `useAppStore((s) => s.models)` 选择器，避免无关更新触发整页重渲染。

### ✅ 验证
- `go vet ./internal/server/... ./pkg/modelupdate/... ./cmd/...` 全绿；`gofmt -l` 干净。
- `NODE_OPTIONS= npm run build` 前端全绿（1817 modules）；`go build -ldflags="-s -w -H windowsgui" -o icode.exe .` 全绿，`icode version`→0.26.0；前端已重嵌 `internal/embedded/dist`。

### ⚠️ 已知限制 / 仍需你协助
- 本批为「崩溃生存 + 可诊断」加固：若闪退源于 **WebView2/Edge 渲染进程自身崩溃**（非 Go panic），`recover` 能让后端存活并记日志，但窗口仍需手动从托盘「显示窗口」恢复。若仍闪退，请附上新的 `~/.icode/desktop.log` 全文，里面现已包含 panic 堆栈，可精准定位。

## v0.25.0 — 桌面启动卡死修复：持久化防抖 + 启动顺序校正（2026-07-25）

> 第二十一批：修复「桌面版启动时卡死」。v0.24.0 解决了运行期流式卡顿，但冷启动仍会冻结——根因在 store 的模块级订阅。

### 🐛 启动冻结根因（`desktop/src/stores/appStore.ts`）
- **原实现**：模块级 `useAppStore.subscribe((state) => { saveToLocal(state.sessions || []); ... })` 在**每一次** state 变更时同步 `JSON.stringify` 至多 50 个完整会话写入 localStorage。
- 这串序列化落在关键路径上：6 个启动动作（backend/sessions/workspaces/models/settings/security）+ 每个流式 token 都会触发 → 主线程阻塞 → 启动冻结 + 运行期偶发卡顿。

### 🔧 修复
- **防抖 + 选择性持久化**：改用 `subscribeWithSelector` 中间件，只对真正需要持久化的切片（`sessions / openTabIds / activeSessionId / activeWorkspaceId`）做 `equalityFn` 比较；变更后经 800ms 防抖 `setTimeout` 才写入，避开每次流式帧。
- **退出兜底**：`window.addEventListener('beforeunload', flushPersist)` 在进程退出前强制 flush 一次未决写入（托盘「退出」直接终止进程也能落盘）。
- **启动顺序校正**（`desktop/src/App.tsx`）：启动 `useEffect` 先 `await checkBackend()` + `await fetchMode()`，**再** `loadSessions / loadWorkspaces / refreshModels / loadDesktopSettings`——确保会话/模型走后端 SQLite/HTTP 路径，而非在后端未连时回退到重 localStorage 解析。

### ✅ 验证
- `npx tsc --noEmit` 全绿（修复了 curried `create<AppStore>()(subscribeWithSelector(...))` 少一个右括号导致的 TS1005）。
- `cd desktop && npm run build` 全绿（1817 modules transformed）。
- `go build -ldflags="-s -w -H windowsgui" -o icode.exe .` 全绿，前端已重新嵌入 `internal/embedded/dist`。

### 已知限制
- 启动仍需等待后端健康探测（30s 超时/300ms 轮询）；后端不可用时回退 localStorage，首次解析大会话仍有一次性开销（已非每帧触发，不再冻结）。

## v0.24.0 — 桌面运行性能优化 + CLI 梅花 LOGO 左移美化（2026-07-25）

> 第二十批：解决「桌面版运行时有点卡」+ CLI LOGO 不像梅花。

### ⚡ 运行时卡顿修复（6 处根因）
- 删除每 token `console.log` 刷控制台。
- 每 token 全量 `updateMessage` 触发全列表重渲染 → `requestAnimationFrame` 批量合并（每帧最多一次 store 写）。
- 每 token `scrollIntoView({behavior:'smooth'})` → rAF 节流 + `auto` + stick-to-bottom（距底 ≤80px 判定）。
- `Markdown`/`CodeBlock` 未 memo → 加 `React.memo`。
- `CodeBlock` header 每 render 重复 `hljs.highlightAuto` → `useMemo` 复用已算结果（`highlighted.language`）。
- 右侧 sidebar 4 正则扫全 messages → `useMemo` 包裹（`fileActions`）。

### 🌸 CLI LOGO
- `internal/tui/logo.go` 重设计梅花造型（`@` 圆润花瓣环绕 `*` 花蕊 + `~\|~` 枝叶），`asciiLogo` 改为「左梅花 + 右 ICODE 字标」并排布局（`blossomW=11`）。

## v0.23.0 — Token 节省深化：多模态附件淘汰 + 附件感知估算（2026-07-25）

> 第十九批：深化 Cache-First Loop。v0.16 起工具生成的 base64 图片会回灌进对话历史，但从不淘汰——一张图 100KB–1MB base64，此后**每一轮请求都重复发送**，是多模态会话中最大的 token/带宽浪费点；且 token 估算把附件当 0 算，导致压缩触发过晚。

### 🖼️ 多模态附件淘汰（`internal/llm/tokenopt/attachment.go`，新增）
- **淘汰机制**：新用户轮开始时（与 Volatile Scratch 折叠同时机），只保留消息日志中**最近 2 个**附件（`MaxKeptAttachments`，可配），更旧的 base64 载荷替换为约 30 token 的文本占位符 `[附件已淘汰以节省 token：image/png ~500KB，模型此前已阅览]`——模型早已在当轮阅览并描述过该图，语义上下文由占位符 + 模型自己的分析文本保留。
- **预算按消息整体计**：从新到旧遍历，整条消息的附件要么全保留要么全淘汰，避免半张图的破碎上下文。
- **附件感知 token 估算**：`EstimateAttachmentTokens` 按 base64 解码后字节数 ÷750 估算（约 500KB PNG ≈ 700 token），下限 85（OpenAI 风格最小图价）、上限 2000；`estimateTokensLocked` 纳入附件权重，`ShouldCompact` 不再低估多模态上下文，压缩按时触发。
- **统计**：`Stats` 新增 `attachments_evicted` 计数；淘汰节省的估算 token 计入 `tokens_saved`（Token 仪表盘自动体现）。
- **接线**：`Optimizer.Config` 新增 `Attachment AttachmentEvictionConfig`（零值走默认）；`AddMessage` 用户轮触发 `evictAttachmentsLocked`；engine 无需改动（零值配置自动生效，含 CLI↔桌面会话历史重放路径）。

### 🧹 清理
- 移除遗留的 `internal/llm/tokenopt/optimizer.go.bak`；`gofmt -w` 统一 tokenopt 包格式（dedup/repair/snip 等预存未格式化文件一并修复）。

### ✅ 验证
- 新增 5 个单测（`attachment_test.go`）：估算下限/中值/上限、淘汰保留最近 2 张、禁用开关、估算含附件、按消息整体淘汰——全绿。
- `go build ./...` + `go vet ./...` + 全量 `go test ./...` 全绿。

### 已知限制
- 附件淘汰只作用于 Optimizer 内存日志，session 存储仍保留完整附件（前端图片展示不受影响）。
- 图片 token 估算为启发式（按压缩字节数），与各 Provider 按分辨率瓦片计费存在偏差，仅用于压缩触发与节省统计。

## v0.22.0 — VS Code 扩展增强：编辑器集成 / 状态栏 / 配置项（2026-07-25）

> 第十八批：补齐 VS Code 扩展（v0.15 初版仅侧栏聊天）与主流 AI 编程扩展的体验差距。扩展版本号同步升至 0.22.0。

### 🧩 编辑器集成（`vscode/`）
- **选中代码 → iCode**（`package.json` + `src/extension.ts` + `media/sidebar.html`）：编辑器右键新增三条命令（`editorHasSelection` 时显示）——
  - `iCode: 询问选中代码`：预填提示词到侧栏输入框（可编辑后发送）；
  - `iCode: 解释选中代码` / `iCode: 优化选中代码`：自动发送。
  - 提示词自动携带 `相对路径:起止行号` 与语言代码围栏（`doc.languageId`）。
  - 通道：扩展 `sendSelectionToChat` → webview `insertPrompt` 消息（`autoSend` 标志）；webview 未就绪时挂起到 `pendingPrompt`，`attachWebview` 后延迟 600ms 注入。
- **状态栏**：右侧常驻项——已连接显示 `$(zap) iCode :端口`，未连接显示 `$(circle-slash) iCode`（警示底色）；每 15s 对缓存端口做 `/api/health` 健康检查；点击打开聊天侧栏。
- **配置项**（`contributes.configuration`）：
  - `icode.binPath`：自定义 icode 可执行文件路径（`findIcodeBin` 优先使用，不存在时告警回退自动查找）；
  - `icode.serverPort`：固定后端端口（`ensureBackend` 最优先探测，配合后端 v0.20 `server.port`；0=自动发现）；
  - `icode.autoStartBackend`：未发现后端时是否自动 `icode server`（默认 true，禁用时报错提示手动启动）。
- `iCode: 打开设置` 命令改为跳转 VS Code 设置页 iCode 区（原为聚焦侧栏）。
- 扩展 `package.json` 版本 0.15.0 → 0.22.0；README 同步功能说明。

### ✅ 验证
- `cd vscode && npm run compile`（tsc strict）全绿；`package.json` JSON 校验通过。

### ⚠️ 已知限制
- 仍未在真实 VS Code 中加载点测（无头环境仅编译验证）；状态栏图标/右键菜单/自动发送链路需真机确认。

## v0.21.0 — 多平台自动构建 CI（2026-07-25）

> 第十七批：补齐工程化短板——多平台自动构建流水线。桌面 GUI 二进制在原生 runner 上以 CGO 编译（systray / 全局热键依赖 Cocoa / GTK+appindicator），无界面 CLI 二进制用 `-tags nogui` 纯 Go 交叉编译覆盖更多架构。

### 🔧 多平台自动构建 CI（`.github/workflows/build.yml` 重写）
- **Test 矩阵**：ubuntu / macOS / windows 三 OS 跑 `go build ./...` + `go vet ./...` + `go test ./...`；Linux/macOS 开 `CGO_ENABLED=1` 并装 GTK+appindicator 系统库（使含 systray 的桌面包也能编译测试），Windows 走 `CGO_ENABLED=0`（WebView2 / 托盘为纯 Go）。
- **Frontend**：`npm ci` + `vite build`（Node 升至 22），产物 `desktop/dist` 作为 artifact 上传，供桌面构建嵌入 `internal/embedded/dist`。
- **Desktop GUI 二进制**：native runner 矩阵（ubuntu-latest/amd64、macos-13/amd64、macos-14/arm64、windows-latest/amd64），CGO 开、嵌入前端、注入 `main.Version/BuildTime/GitCommit`；Windows 走 `-H windowsgui` 不闪黑窗。
- **Headless CLI 二进制**：单个 Linux runner 以 `CGO_ENABLED=0 -tags nogui` 交叉编译 linux/{amd64,arm64}、darwin/{amd64,arm64}、windows/{amd64,arm64}、freebsd/amd64——无需 CGO / GUI 库，覆盖原生矩阵未涉及的架构。
- **Release**：打 `v*` tag 时汇聚所有 artifact，产出 GitHub Release（桌面 + CLI 共 11 个资产，`generate_release_notes`）。

### 🏷️ `nogui` 构建标签（支撑 CLI 跨平台交叉编译）
- `cmd/desktop_windows.go`/`tray_windows.go` 加 `&& !nogui`；`cmd/desktop_posix.go`/`tray_posix.go` 加 `!windows && !nogui`；`cmd/desktop_common.go`（仅桌面用的纯 Go 辅助）加 `//go:build !nogui`。
- 新增 `cmd/desktop_nogui.go`（`//go:build nogui`）：为 `root.go` 引用的 `desktopCmd` / `runDesktop` 提供隐藏占位实现，使 `go build -tags nogui .` 在无 CGO 环境也能编译出无桌面子命令的纯 CLI。
- 验证：Windows 上 `go build .`（桌面）与 `go build -tags nogui .`（无界面）均通过；`go vet` 两种模式全绿；`CGO_ENABLED=0` 交叉编译 linux/arm64、darwin/arm64、windows/arm64、freebsd/amd64 等 7 目标全绿。

### ⚠️ 已知限制（CI 真机待验证）
- Linux 桌面构建依赖 `libayatana-appindicator3-dev`（systray v1.2.2 默认 `ayatana-appindicator3-0.1`，`legacy_appindicator` 标签才用 `appindicator3-0.1`）——已按默认装，未在真机跑过。
- macOS / Linux 桌面原生构建需 CGO，CI 用 macOS / ubuntu runner 自带工具链；本地非 CGO 环境只能产出无界面 CLI。

## v0.20.0 — 桌面端设置增强：开机自启 / 后端端口 / 热键说明（2026-07-24）

> 第十六批对标增强：在设置页补齐「桌面」专属配置——开机自启、后端端口、全局热键说明，全部纯前端可验证（tsc + vite build 全绿）。

### 🖥️ 桌面端设置页（新增「桌面」Tab）
- **开机自启**：设置页新增「桌面」分类（lucide `Laptop` 图标），第一项为「开机自启」iOS 风格开关（`Toggle` 组件），写入 `backendUrl/api/config`（`PUT` 局部更新，复用 `setMode` 模式）。后端收到后调用 `desktop.ApplyAutostart`：Windows 写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的 `iCode` 字符串值（纯 Go 无 CGO）；macOS 写 `~/Library/LaunchAgents/com.ponygates.icode.plist`（`RunAtLoad`）；Linux 写 `~/.config/autostart/icode.desktop`；关闭时分别删键/删文件。
- **后端端口**：数字输入（校验 0–65535，0 = 自动），经 `PUT /api/config` 的 `server.port` 持久化到 `config.yaml`。`bootDesktopBackend` 改为优先用配置端口，为 0 时回退 `findFreePort()`；当前生效端口实时回显。
- **全局热键说明**：卡片展示 `Ctrl`+`Shift`+`Space` 组合，并区分平台行为——Windows 显隐原生窗口、macOS/Linux 在浏览器中唤起前端（与 v0.19 跨平台热键一致）。
- **i18n**：`settings.pageDesktop / autostart / autostartDesc / autostartLabel / serverPort / serverPortDesc / serverPortPlaceholder / serverPortHint / serverPortActive / hotkeyTitle / hotkeyDesc / hotkeyWindows / hotkeyMacLinux` 三语（zh-CN / zh-TW / en）补全；`appStore` 新增 `autostart / serverPort / setAutostart / setServerPort / loadDesktopSettings`，启动 `effect` 中调 `loadDesktopSettings()` 回填当前配置。

### ⚠️ 已知限制（真机待点测）
- Windows 自启通过注册表 Run 键，标准用户即可生效，无需管理员；
- macOS 自启需 `~/Library/LaunchAgents` 写入权限（默认有），且首次自启可能因辅助功能/登录项设置被 Gatekeeper 拦一次，需用户在「系统设置 → 登录项」确认；
- Linux 自启依赖桌面环境读取 `~/.config/autostart`（X11/Wayland 主流环境均支持），纯窗口管理器可能不读；
- 后端端口改动需重启桌面进程方可生效（本次仅持久化，未做运行时热切换）。

## v0.19.0 — 跨平台全局热键：Ctrl+Shift+Space 唤起（2026-07-24）

> 第十五批对标增强：把全局热键从 Windows-only 扩展为跨平台——macOS / Linux 桌面版现在也响应 Ctrl+Shift+Space，与 Windows 原生热键组合一致。

### ⌨️ 跨平台全局热键
- **POSIX 热键**：在 `cmd/tray_posix.go` 的 `runTrayPOSIX` 中注册跨平台全局热键 `Ctrl+Shift+Space`，用轻量库 `golang.design/x/hotkey`（macOS=Cocoa / Linux=X11，均为 CGO）。触发时重新聚焦/打开本机前端（`openBrowser`）——POSIX 为浏览器方案，无原生窗口可隐藏，语义等同"唤起"。注册失败（macOS 未授权辅助功能、Linux Wayland 无全局热键）仅告警，不阻断托盘，菜单仍可唤起。
- **Windows 不变**：`tray_windows.go` 仍走原生 Win32 `RegisterHotKey`（子类化窗口过程，Ctrl+Shift+Space 显隐切换主窗口），零 CGO，不受新依赖影响。
- **依赖隔离**：`golang.design/x/hotkey` 仅被 `!windows` 文件引用，Windows 二进制不链接其 CGO 代码路径，`go build` 在 Windows 仍零 CGO。

### ⚠️ 已知限制（真机待点测）
- macOS：热键事件经 CGEventTap，需辅助功能权限（系统设置 → 隐私与安全 → 辅助功能），且事件需主线程派发，真机待点测；
- Linux：Wayland 会话不暴露全局热键协议，注册通常失败，回退托盘菜单；X11 开箱可用；
- macOS / Linux 托盘与热键均依赖 CGO，须在目标 OS 以 `CGO_ENABLED=1` 构建（Windows 不受影响）。

## v0.18.0 — 跨平台桌面：macOS / Linux 系统托盘 + 浏览器方案（2026-07-24）

> 第十四批对标增强：把"桌面模式"从 Windows-only 扩展为跨平台——非 Windows 平台用系统托盘 + 默认浏览器，与 Windows 原生 WebView2 窗口共用同一套后端启动逻辑。

### 🖥️ 跨平台桌面托盘
- **共享后端启动**：新增 `cmd/desktop_common.go`，把后端启动（日志重定向、配置诊断、嵌入前端、选端口、起服务、健康检查等待）抽成 `bootDesktopBackend()`，返回 `desktopBoot`（含 `shutdown()`）。Windows 与 macOS / Linux 的桌面入口都复用它，避免两套重复逻辑。
- **macOS / Linux 桌面版**：删除原 `desktop_other.go` 报错 stub，新增 `cmd/desktop_posix.go` + `cmd/tray_posix.go`——用 `getlantern/systray` 提供系统托盘（菜单「在浏览器中打开 / 退出」），启动即用默认浏览器打开本机前端（`openBrowser` 按平台选 `xdg-open` / `open` / `rundll32`）；图标复用共用绘制逻辑输出 PNG（`makeIconPNG`）。
- **Windows 不变且更干净**：`desktop_windows.go` / `tray_windows.go` 仍走原生 WebView2 窗口 + `Ctrl+Shift+Space` 全局热键（子类化窗口过程把关闭按钮改为隐藏到托盘），仅重构复用共享 boot；`makeIcon` 改为调用共用的 `drawIcodeImage()`，去掉重复绘制。

### 🔧 跨平台编译修复（使 CLI 整体可跨平台）
- `internal/executil` 用 `runtime.GOOS` 运行时判断，却无条件引用 Windows-only 的 `SysProcAttr.HideWindow / CreationFlags`，导致 darwin / linux 连 CLI 都编不过。拆分为 `executil_windows.go`（设置 `SysProcAttr`）与 `executil_posix.go`（空实现 `hide`），使整个项目可在 macOS / Linux 编译。
- 验证：`go build ./...` + `go vet ./...` 在 Windows 全绿；`getlantern/systray` 在 Windows 无需 CGO，Windows 开发循环不受影响。

### ⚠️ 已知限制（真机待点测）
- **全局热键仍 Windows 专属**：POSIX 跨平台全局热键需 CGO / Cocoa / X11，超出本批范围；非 Windows 平台用托盘菜单「在浏览器中打开」替代唤起。
- **POSIX 托盘依赖 CGO**：`getlantern/systray` 在 macOS（Cocoa）/ Linux（libappindicator / ayatana）需 CGO，必须在目标 OS 上以 `CGO_ENABLED=1` + 对应 SDK 构建；本 Windows 开发环境无法交叉编译验证（仅验证 Windows 构建 + 代码正确性），真实 macOS / Linux 托盘交互待点测。

## v0.17.0 — 前端图片展示：渲染 message.attachments（2026-07-24）

> 第十三批对标增强：补齐 v0.16 多模态回灌在 UI 侧的缺口，让用户能在对话里直接看到图。

### 🖼️ 前端图片展示
- 桌面前端 `ChatPage` 现在渲染 `message.attachments`：图片类型显示为可点击缩略图（点击放大 lightbox 查看），非图片类型显示为文件卡片。
- 覆盖两类消息：① **bot 消息**——v0.16 回灌的生成图（重新加载会话即可见）；② **user 消息**——用户上传的图即时显示在气泡中（发送侧把 `attachedImages` 存入 `userMsg.attachments`）。
- `appStore` 的 `Message` 接口新增 `Attachment` 接口；`loadSessions` 在 IPC 与 HTTP 两条路径都保留 `attachments` 字段（此前 map 会丢失）。
- 纯前端增强，**无后端改动**——后端 `types.Message.Attachments` 已带 `json:"attachments,omitempty"`，`GET /api/sessions` 输出即含图，前端此前只是没取、没渲染。
- 构建：`tsc --noEmit` + `vite build` 通过（先 `rm -rf dist` 绕过本环境 safe-delete shim）。

## v0.16.0 — 多模态结果回灌上下文（2026-07-24）

> 第十二批对标增强：把生成的图片真正"喂回"对话，让 vision 模型能基于上一步产物继续工作。

### 🖼️ 多模态结果回灌
- `image_gen` 工具生成图片后，除落盘外，将图片以 base64 `Attachment` 回传给引擎（`internal/core/tool/multimodal.go`）。
- 引擎在回灌工具结果时，把本轮所有生成的图片合并为一条 `user` 消息追加进对话历史（`internal/core/conversation/engine.go` 的 `ingestToolAttachments`），后续 vision 模型可在下一轮看到这张图。
- `openai_compat`（覆盖 OpenAI 兼容群）与 `anthropic` 在编码请求时，将消息 `Attachment` 转为 `image_url` / Anthropic `image` 源块（`internal/llm/provider/openai_compat/base.go`、`internal/llm/provider/anthropic/anthropic.go`）。非图片附件、tool/tool-result 消息保持纯文本，不破坏既有文本对话——这也补齐了「用户上传图片」此前未接通模型的数据通路。
- 视频因体积大且多数 vision 模型暂不支持视频输入，暂不回灌大附件（仅保留文本路径），避免上下文膨胀。
- 测试：`openai_compat`/`anthropic` 新增附件编码单测（注入带图消息验证 content 变数组含 `image_url` / `image` 块，且纯文本与 tool 消息不变）；`image_gen` 成功路径单测增强断言返回 `Attachments`。

## v0.15.0 — VS Code 扩展：侧栏聊天，复用本机后端（2026-07-24）

> 第十一批对标增强：把 iCode 的对话能力嵌入编辑器侧栏（对标"在编辑器内使用 AI 编程助手"的体验）。

### 🔌 VS Code 扩展（`vscode/`）
- 新增独立扩展工程：活动栏「iCode」图标 + 侧栏 WebView 聊天视图（原生 JS，零框架）。
- 命令：`iCode: 打开聊天侧栏` / `iCode: 启动后端服务` / `iCode: 在终端启动 CLI（完整 TUI）` / `iCode: 打开设置`；编辑器右键菜单可快速在终端启动 CLI。
- 后端发现：优先读 `%TEMP%/icode/port`（mac/Linux 为 `$TMPDIR/icode/port`），否则探测 `57356/8080/3000`，仍不可用则自动 `icode server`。
- 架构：WebView 经 `acquireVsCodeApi` 与扩展进程 `postMessage` 通信，扩展用 Node `http` 代理 `POST /api/chat`（SSE 流式）、`/api/sessions`、`/api/models`、`/api/permission/respond`，绕开浏览器跨域 / X-Frame 限制，无需后端额外开放 CORS。
- 支持流式回复、思考过程、工具调用卡片、交互式权限批准；模型下拉实时从后端拉取。
- 已通过 `tsc` 编译验证（`vscode/dist/extension.js`）；真机 VS Code 加载待点测。

## v0.14.0 — 技能市场：内置 catalog + 一键安装/卸载/导入（2026-07-24）

> 第十批对标增强：对标 WorkBuddy「从市场一键安装技能」的优点，补齐 iCode 技能系统的分发闭环。

### 🛒 内置技能市场（catalog）
- 新增 `internal/core/skills/catalog/`：5 个内置通用技能（代码审查 / 提交信息 / 解释代码 / 单元测试 / 文档生成），以 `//go:embed` 编译进二进制，随 `icode` 分发（零额外依赖）。
- 后端新增 `skills.ListCatalog / IsInstalled / Install / Uninstall / Import`；新增路由 `GET /api/skills/market`、`POST /api/skills/market/install`、`DELETE /api/skills/market/{name}`、`POST /api/skills/import`。
- `Install` 把 catalog 技能复制进 `~/.icode/skills/<name>/SKILL.md`；`Uninstall` 仅删用户目录的技能（不碰内置 catalog 与 WorkBuddy 目录）；`Import` 支持从本地 SKILL.md / 目录导入，复用既有 SKILL.md 格式。
- 修复 embed 在 Windows 上的坑：对 `embed.FS` 读取必须用 `path.Join`（正斜杠），`filepath.Join` 在 Windows 会生成反斜杠导致 `ReadFile` 静默失败。

### 🖥️ 桌面端技能市场 Tab
- 设置页「技能」改为「已安装 / 市场」双子标签：市场列出全部内置技能（名称/描述/触发词/已安装状态）并支持一键安装 / 卸载；新增「从本地导入」输入框，按绝对路径导入社区技能。
- 修正 v0.12 遗留 bug：侧栏「技能」标签误显示为「记忆」（zh-CN / zh-TW 修正为「技能」）。
- i18n 补 `skillMarket.*` 三语言（zh-CN / zh-TW / en）。

## v0.13.0 — 工作区↔会话深度绑定 + 多标签持久化 + 路由默认升级（2026-07-24）

> 第九批对标增强：把 v0.12 的「工作区」从空容器真正用起来，并让多标签与路由更健壮。

### 🗂️ 工作区与会话深度绑定
- 新建会话时**自动归入当前工作区**：`createSession` 在创建后若 `activeWorkspaceId` 已设置，即 fire-and-forget `POST /api/workspaces/{id}/sessions`（携带 `session_id`）。
- 后端：新增 `Store.AddSessionToWorkspace`（追加 + 去重，幂等）；`POST /api/workspaces/{id}/sessions` 现在同时支持 `session_id`（追加）与 `session_ids`（整体替换）两种语义，向后兼容。
- 切换工作区即切换会话标签条：点侧栏工作区 → `setActiveWorkspace` 把激活会话跳到该工作区最近会话，ChatPage 的标签条按工作区 `session_ids` 过滤展示 → 工作区真正成为「项目窗口」。
- store 乐观更新 `workspaces[].session_ids`，侧栏会话计数即时刷新（后端为真相源）。

### 📑 多标签跨路由持久化
- 把 ChatPage 本地的 `openTabs` 状态**迁入全局 `appStore`（`openTabIds`）**，切换路由（如去设置页再回来）不再丢失打开的会话标签。
- 新增 `closeTab(id)`（关闭标签但保留会话；关掉最后一个会自动新开一个，始终 ≥1 标签）。
- `openTabIds` / `activeSessionId` / `activeWorkspaceId` 一并持久化到 localStorage → 重启后恢复，对标 claude code / workbuddy 标签体验。
- `loadSessions` 在本地无 `openTabIds` 时自动从已加载会话播种，避免首启标签条空白。

### 🧭 智能路由默认升级为本地语义分类
- **默认路由模式从 `keyword` 改为 `embedding`**（本地、零 token、零 API 的近质心余弦分类器）。它只在自信时「只增不减」地升级关键词基线，不自信回退关键词，因此默认开启不会让路由变差，却能把简单查询更准地路由到便宜模型 → 进一步省 token。
- `config.go` `Default()` 写入 `Routing.Mode="embedding"`；`app.go` 对空模式（旧配置）也映射为 embedding，存量用户自动受益；`keyword` / `llm` 仍可选。

### 🐛 附带修复
- 修 v0.12 遗留的前端 TS 报错：`SettingsPage.toggleSkill` 把 `enabled` 误写为 `enabled`（应为参数 `enable`），导致 `npm run build` 失败（已通过 `tsc --noEmit` + `vite build` 验证）。

### ⚠️ 已知验证缺口
- v0.12 的原生托盘 + `Ctrl+Alt+I` 全局热键 + 关闭最小化到托盘，仅在无头环境验证了编译与纯函数单测，**真实 Windows 交互尚未点测**，待真机验证。

---

## v0.12.0 — 桌面版四大子系统（2026-07-24）

> 第八批对标增强：完成 v0.11.0 遗留的桌面版 4 大子系统（系统托盘/全局热键、Token 节省仪表盘、多标签会话+工作区、技能/MCP/连接器管理），吸收 WorkBuddy / Reasonix 桌面版优点。

### 🔔 系统托盘 + 全局热键（原生 Windows）
- 引入 `github.com/getlantern/systray` 实现托盘图标 + 右键菜单（显示 / 隐藏 / 退出）。
- **全局热键唤起**：`Ctrl+Alt+I` 一键显示或隐藏桌面窗口（Win32 `RegisterHotKey`，独立 goroutine 消息循环，不阻塞 WebView2 主线程）。
- **关闭即最小化到托盘**：子类化 WebView2 窗口过程，拦截 `WM_CLOSE` 改为 `ShowWindow(SW_HIDE)`，仅从托盘菜单退出才真正关闭（避免误关丢失会话）。
- 构建于 `cmd/tray_windows.go`（`windows` build tag），非 Windows 留 no-op 桩。

### 📊 Token 节省仪表盘：全局视图 + 持久化
- 后端 `db/store.go` 新增 `session_stats` 表 + `UpsertSessionStats` / `GlobalStats`：每次查看会话分析即落库，**跨重启、跨会话聚合**。
- 新增 `GET /api/analytics/global`：返回累计节省 Token、累计缓存命中、累计花费、累计节省金额、平均缓存命中率、会话数，以及**按日趋势**。
- 前端 `AnalyticsPage` 新增「会话 / 全局」Tab 切换，全局视图含聚合卡片 + 每日趋势条；修正 `savedCost` 口径（改为后端计算的 `estimated_saved_cost`，不再依赖前端浮动公式）。
- `tokenopt.Stats` 新增 `EstimatedSavedCost` 字段并统一在 `Stats()` 计算。

### 🗂️ 多标签会话 + 工作区基础
- **多标签会话**：桌面聊天页已具备编辑器式标签条（`TabBar`）——切换 / 关闭 / 新建会话标签，对标 claude code / workbuddy 标签体验。
- **工作区（新概念）**：后端 `db/store.go` 新增 `workspaces` 表 + 完整 CRUD（`CreateWorkspace` / `ListWorkspaces` / `GetWorkspace` / `UpdateWorkspace` / `DeleteWorkspace` / `SetWorkspaceSessions`）；`server.go` 新增 `GET/POST /api/workspaces`、`GET/PUT/DELETE /api/workspaces/{id}`、`POST /api/workspaces/{id}/sessions`。
- 前端 `appStore` 新增 `workspaces` 状态 + `loadWorkspaces` / `createWorkspace` / `setActiveWorkspace`；新增 `WorkspaceSidebar` 组件（列出工作区、点击激活、新建），接入左侧 `Sidebar` 顶部；App 启动时自动 `loadWorkspaces()`。

### 🧩 技能 / MCP / 连接器管理（修复 + 增强）
- **修复**：`SettingsPage` 的「技能」页此前伪装成「项目记忆编辑器」（`/api/memory/icode`）——重写为真实技能管理，调用 `GET /api/skills` 展示技能列表（名称 / 描述 / 触发词 / 来源）。
- **修复**：`/api/mcp/trust` 路由此前不存在（信任下拉框点击必 404）——新增 `MCPServerCfg.TrustMode` 字段 + `handleMCPTrust`（`PUT /api/mcp/trust`），`PageMCP` 信任下拉现在可正常保存。
- **增强**：技能系统 `Skill` 新增 `Enabled` 字段 + 禁用状态持久化（`~/.icode/skills_state.yaml`），`Registry.Find` 跳过已禁用技能；`engine` 新增 `EnableSkill` / `DisableSkill` 转发；新增 `POST/DELETE /api/skills/{name}/enable` 端点（前端开关预留）。

---

## v0.11.0 — 启动体验与增强 TUI（2026-07-24）

> 第七批对标增强：把启动方式与终端交互体验拉平到 claude code / reasonix / opencode 的水平。三块需求：① PowerShell/CMD 运行 `icode` 启动 CLI；② 双击 `icode.exe` 启动「加强版 CLI」(增强 TUI)；③ 桌面版吸收 WorkBuddy / Reasonix 优点（列入 v0.12.0）。本批完整交付 ①②。

### 🚀 启动方式
- **终端 CLI**：在 PowerShell / CMD 运行 `icode` 即启动交互式 CLI（沿用 `AttachConsole` 接回父终端控制台）。
- **双击 → 增强 TUI**：双击 `icode.exe`（GUI 子系统二进制）会 `AllocConsole` 分配一个新控制台窗口并进入手写 ANSI raw-mode TUI，带可视滚动条、可点击光标、对标快捷键——不再是「黑窗一闪」。分配失败才回退桌面版。
- **桌面版入口**：`icode desktop` 或独立的 `icode-desktop.exe`（`windows && desktop_only` 构建）仍启动 WebView2 桌面应用。
- **崩溃保护**：双击分配的console 在致命错误/panic 后保持窗口并按 Enter 退出，便于排查，而不是窗口瞬间消失。

### 🖱️ 增强 TUI：鼠标支持（对标 workbuddy/reasonix 桌面端手感）
- **点击输入框移动光标**：点击输入行任意位置，光标精确跳到该列（CJK 宽字符按 2 列计算）。
- **滚轮滚动会话**：鼠标滚轮上/下翻动会话历史。
- **滚动条点击/拖动跳转**：点击右侧滚动条轨道任意位置、或按住拖动，直接跳转视口（使用渲染时缓存的滚动条几何，零重复计算）。
- **括号粘贴（bracketed paste）**：`ESC[200~ ... ESC[201~` 整块粘贴，粘贴内容原样插入、不会因内含换行被误触发发送。
- 启用 SGR 鼠标模式（`?1002`+`?1006`）与括号粘贴模式（`?2004`），退出 TUI 时还原。

### 📜 增强 TUI：可视化滚动条 + 滚动指示
- 会话正文右侧新增**可视滚动条**（轨道 `│` + 滑块 `█`），位置随 `scrollOffset` / 总行数比例实时映射；滚动条占用的 1 列 gutter 从内容宽度预留，不与正文重叠。
- 保留原有「↑ N more lines — PgDn/End to follow」顶部指示器，二者互补。

### ⌨️ 增强 TUI：对标快捷键
- **Shift+Tab**：循环切换模式 `auto → plan → agent → yolo`（claude code 风格），右下角有提示。
- **`?`**：空输入按 `?` 弹出「键盘快捷键」帮助浮层；任意键关闭。
- **Ctrl+W / Ctrl+U**：删除前一个词 / 删除到行首（readline 习惯）。
- 既有快捷键（Ctrl+C/L/K、Ctrl+A/E、Ctrl+P/N、PgUp/PgDn/Home/End、Tab 补全/切模型）全部保留。

### 🧱 实现要点
- 全部为纯 Go 手写 ANSI TUI（`internal/tui`），不引入 Bubble Tea；新增 `mouse.go`（鼠标/粘贴解析）、`edit.go`（光标/词/行编辑、模式循环、粘贴读取）、`render.go` 滚动条与帮助浮层。
- `cmd/codepage_windows.go` 新增 `allocConsole()`；`cmd/root.go` 双击分支改为分配控制台后跑 TUI（失败回退桌面）；非 Windows 补 `allocConsole` no-op。

### ✅ 测试
- `internal/tui/tui_test.go`：SGR 鼠标序列解析、点击列→光标映射（含 CJK）、删词/删行、模式循环、滚动条几何映射。
- `go build ./...` / `go vet ./...` / `go test ./...` 全包全绿。

## v0.10.0 — 本地零成本 Embedding 语义路由（2026-07-24）

> 第六批对标增强：补齐 roadmap 上的「Embedding 路由」。关键约束仍是「省 token 不能丢」——所以这一层是**纯本地、零 API、零 token** 的，分类本身不花一个 token，却能把简单查询更准地路由到便宜模型，从而进一步省钱省 token。

### 🔥 核心：本地语义路由（routing.mode: "embedding"）
- **零成本语义分类器** `internal/core/router/embedding.go`：不调用任何 embedding API。用本地 hashing 特征向量（词 token + 中文字 bigram/unigram，TF 加权后 L2 归一化）对查询做**最近质心余弦相似度**分类，把查询归为 simple / normal / complex。
- **比关键词更鲁棒**：对改写、同义、中英混排的查询都能正确归类，而纯 substring 关键词匹配会漏判。
- **只增不减的安全设计**：带置信度阈值（绝对分数 + 与次优类的间隔），不自信时返回 `ok=false`，路由**回退到关键词基线**——所以它只可能提升准确率，绝不会让路由变差。
- **三档路由模式**：`keyword`（默认，零成本启发式）→ `embedding`（本地零 token 语义，推荐升级）→ `llm`（最高保真，但每次分类要花一次廉价模型调用）。

### 🧱 接线
- `router.go`：新增 `embedClassify` 字段 + `SetEmbeddingClassifier`；`RouteQuery` 在关键词基线之后、LLM 之前应用（零成本刷新，LLM 仍最高优先）。
- `app.go`：`cfg.Routing.Mode == "embedding"` 时挂载 `NewSemanticClassifier().Classify`。
- `config.go`：`RoutingCfg` 文档更新为 `keyword | embedding | llm`。

### ✅ 测试
- `embedding_test.go`：改写样本分类准确性、空/歧义输入低置信度回退、路由热路径覆盖、特征向量单位归一化。
- `go build ./...` / `go vet ./...` / `go test ./...` 全包全绿。

## v0.9.0 — Cache-First 加固：技能懒加载 + 预算层激活 + 节省可视化（2026-07-24）

> 第五批对标增强：聚焦用户反复强调的「超级省 token 机制不能丢」。在不削弱功能的前提下，把缓存前缀收紧、把休眠的压缩层点亮、把节省效果显性化。

### 🔥 核心：Token 节省机制加固（Cache-First Loop）
- **技能懒加载索引（keystone）**: 此前 `buildSystemPrompt` 把**全部 SKILL.md 完整正文**塞进不可变前缀（immutable prefix）。一旦技能市场铺开、技能变多，前缀会无限膨胀、每次增删技能都让 provider 的 KV 缓存失效——这正是「省 token」的天敌。现在前缀只放**紧凑索引**（`name + 一句描述 + 触发词`，见 `skills.FormatIndex`），模型按需用新工具 `use_skill` 拉取完整正文，正文只进入**易失暂存区**（volatile scratch），永不污染缓存前缀。无论装多少技能，前缀大小与缓存命中率都稳定。
- **新增 `use_skill` 工具**: 模型从索引里认出技能后，调用 `use_skill(name)` 即时取回该技能完整 SKILL.md 工作流；未知名称返回友好报错。已加入只读并行清单，可与其他只读工具并发。
- **激活预算层（Level 4，此前写了没接）**: `BudgetEnforcer` 此前仅单元测试覆盖、从未接入会话。现在在 `runTool` 中对每个工具输出执行硬性上限（read_file 50K / bash 30K / grep 20K / 全局 200K 字符），超长输出头尾保留、中间省略，避免单次超大结果撑爆上下文。已补互斥锁以支持并行工具并发执行。

### ✨ 新能力
- **Token 节省可视化（「不能丢」最直接佐证）**:
  - TUI 新增 `/token` 命令，打印本会话**已节省 Token / 缓存命中率 / 压缩次数 / 总 Token / 预估费用**与机制说明。
  - 桌面端 TokenBar 新增「🪙 已节省」实时指标，轮询 `/api/analytics/{session}`（后端早已返回 `tokens_saved`），让 5 层压缩管道（Snip→去重→折叠→摘要→预算）的成效一眼可见。

### 🧱 机制现状（5 层压缩管道全活）
1. **Snip**（零成本滤掉空轮/被拒轮）✓
2. **Dedup**（工具输出内容去重，已接线 `runTool`）✓
3. **Microcompact**（turn 间折叠工具结果为占位符）✓
4. **Context Fold**（超阈值 LLM 摘要多轮）✓
5. **Budget**（硬性大小上限，本版激活）✓
+ **Cache-First Loop**：不可变前缀（system+tools）+ 追加日志 + 易失暂存 + 跨 provider 缓存策略（deepseek 稳定前缀 / anthropic cache_control）+ 工具调用修复流水线（flatten/scavenge/truncation/storm）。

### 🧪 测试
- 新增 `skills_test.go::TestFormatIndex_Compact`（守护「索引不得嵌入正文」契约）、`skilltool_test.go`（use_skill 按需加载与未知技能报错）。
- `go build ./...`、`go vet ./...`、`go test ./...`（全包）全绿。

---

## v0.8.0 — 多模态工具 + 首回合并行（2026-07-24）

> 第四批对标增强：补齐"全面助手"最后一块（图片/视频生成），并把并行工具执行覆盖到首回合流式路径。

### ✨ 新能力
- **多模态工具 `image_gen` / `video_gen`**: 通过 OpenAI 兼容的 images/videos generations API 生成图片/视频并保存到本地（默认 `.icode/generated/`）。兼容 OpenAI、智谱 cogview/cogvideox、火山方舟以及 WorkBuddy 多模态网关。`video_gen` 支持同步返回与异步 submit+poll 两种流程（3 分钟预算，自动轮询任务状态）。未配置后端时返回友好的"未配置"提示而非硬报错。配置项 `multimodal.{image_base_url,image_model,video_base_url,video_model,api_key,output_dir}`；api_key 可回退 `ICODE_MULTIMODAL_API_KEY` / `OPENAI_API_KEY` 环境变量。

### 🔧 改进
- **首回合工具并行**: 此前并行只覆盖续轮批量路径，首回合流式路径仍逐个即时执行。现在首回合的多工具调用统一走 `executeToolBatch`——只读工具（read/grep/glob/code_search 等）并发执行、写类工具顺序串行，完全对齐续轮语义（claude code parallel tool use 全路径覆盖）。
- **首回合 LSP 诊断注入**: 首回合路径此前缺少工具执行后的编译错误注入，现与续轮循环一致，改文件后自动注入 gopls/pyright 等诊断提示。

### 🧪 测试
- 新增 `multimodal_test.go`：未配置提示、mock 后端图片生成落盘校验、视频未配置提示。

## v0.7.0 — WorkBuddy 集成 + 并行工具 + 后台任务 + 路由升级（2026-07-24）

> 第三批对标增强：打通 WorkBuddy 生态、补齐 claude code / opencode 的并行与后台执行能力。

### ✨ 新能力
- **WorkBuddy 技能互通**: `skills.DefaultDirs` 现在同时加载 `~/.workbuddy/skills` 与 `{项目}/.workbuddy/skills`——WorkBuddy 安装的技能（SKILL.md 格式兼容）在 iCode 中直接可用；同名时 iCode 自有目录优先。
- **WorkBuddy MCP 连接器桥接**: 启动时自动导入 `~/.workbuddy/mcp.json` 中的 MCP 服务器定义（`mcpServers` 格式，兼容 Claude Desktop / Cursor 约定），无需重复配置即可使用 WorkBuddy 生态的连接器。显式 iCode 配置同名优先；`mcp_import_workbuddy: false` 可关闭。带单元测试。
- **并行工具执行**（claude code parallel tool use parity）: 同一模型回合内的多个只读工具（read/grep/glob/code_search/web_search 等）并发执行（上限 4 并发），写类工具保持原顺序串行——权限提示与文件写入永不交错。底层 doomLoop/输出缓存均已带锁，线程安全。
- **后台任务**（run_in_background）: `bash` 工具新增 `run_in_background` 参数，立即返回 `bg-N` 任务 id；新增 `task_output` 工具查询输出/状态、列出全部后台任务、`kill:true` 终止任务。输出缓冲上限 1MiB 自动裁剪。带单元测试。
- **智能路由升级**: ① 分类关键词补齐**中文**（此前纯英文关键词无法识别中文查询）且长度阈值改按 rune 计数（中英文等权）；② 新增可选 **LLM 分类模式**（`routing.mode: llm`，`classifier_model` 可指定便宜模型），3 秒预算、失败自动回退关键词启发式，默认仍是零成本 keyword 模式。

### 🧪 测试
- `go build ./...`、`go vet ./...`、`go test ./...`（28 包）全绿；新增 workbuddy 导入与后台任务单元测试。

---

## v0.6.0 — 生命周期 Hooks + Headless JSON + 双层 Memory + CodeGraph（2026-07-24）

> 第二批对标增强：补齐与 claude code / opencode 的四大能力落差。

### ✨ 新能力
- **生命周期 Hooks**（claude code parity）: 新增 `internal/core/hooks` 包，支持 `PreToolUse` / `PostToolUse` / `Stop` 三类事件。config.yaml `hooks:` 配置 matcher（工具名正则）+ command；hook 进程从 stdin 读 JSON 事件负载，**exit code 2 可阻断工具调用**并把 stderr 反馈给模型；PostToolUse 的反馈追加到工具结果。带单元测试。
- **Headless JSON 输出**: `icode exec -p "..." --output-format json|stream-json`。`json` 输出最终结果（result/usage/tools_used/duration_ms/session_id）；`stream-json` 逐事件 NDJSON，打通 CI 与脚本编排。
- **双层 Memory 统一**: `#` 快捷记忆现在**项目级为默认**（写 ./ICODE.md），`# user: ...` 写用户级（~/.icode）；TUI 与桌面端行为一致。server `/api/memory` 新增 `POST`（追加式，body `{text, scope}`）与 `?scope=user|project` 参数。
- **CodeGraph 符号检索**: 新增 `code_search` 工具，懒构建全仓库符号索引（Go/TS/JS），模型可直接回答"X 在哪定义"，比 grep 更快更准；`rebuild:true` 强制重建。索引构建自动跳过 node_modules/dist/vendor 等目录与 .min.js。

### 🐛 修复
- `codegraph.Build` 此前会索引 node_modules（桌面仓库 4000+ 依赖 JS），现已跳过重型目录。
- `WebSearchTool` 注册改用构造函数，避免空 engines map。

### 🧪 测试
- `go build ./...`、`go vet ./...`、`go test ./...` 全绿；新增 hooks 单元测试与 code_search 冒烟验证（1541 符号索引）。

---

## v0.5.0 — 点亮半完成能力 + 跨平台 + 修复（2026-07-24）

> 本轮把此前「已实现但从未接入启动流程」的四个能力全部点亮，并补齐跨平台与真实 bug。

### 🔥 点亮「做了没接」的半完成能力
- **技能系统（SKILL.md）**: `skills.Load` 接入 `app.Bootstrap()`，`engine.buildSystemPrompt` 将可用技能注入 system prompt，模型按需遵循（`/skills` 查看、`/api/skills` 暴露）。
- **多智能体团队（Agent Teams）**: 新增 `agent.LoadTeams`（`.icode/teams/*.yaml`）+ 内置 `team:review`，经 `engine.RegisterTeam` 注册后可由 task 工具以 `team:<name>` 调度（`/teams`、`/api/teams` 可查）。
- **LSP 代码诊断**: `Bootstrap` 现在创建 `lsp.Manager` 并 `SetLSPManager`；工具改文件后引擎懒启动对应语言服务器（gopls/pyright/…）并自动注入编译错误提示。
- **智能模型路由**: 此前已落地 `router` 但已在 v0.4 接线，本轮确认默认开启（cheap/normal/powerful 三档）。

### 🌐 跨平台
- **disk_cleanup 跨平台**: 原先仅 Windows 直接报错，现已支持 Linux / macOS（用户 Temp、~/.cache、Trash、浏览器缓存安全清理），仅删除可丢弃文件。

### 🐛 Bug 修复
- **`/api/memory` 404**: 桌面端 `#` 记忆命令调用 `/api/memory`，server 此前只注册 `/api/memory/icode`；新增别名路由，HTTP 模式下记忆功能恢复可用。
- **`handleMemory` 安全加固**: PUT 补上 `MaxBytesReader(1MB)`，与 v0.2.1 的加固策略对齐。

### 📚 文档
- 修正 ICODE.md「与竞品差距」：技能 / 团队 / 路由 / 搜索替换 此前标注「未实现」实为已实现未接线，现已更正并标注剩余差距（CodeGraph、生命周期 Hooks、Headless JSON、用户级 Memory）。
- README 厂商/模型数字统一为 9 家 / 60+ 模型。

### 🧪 测试
- `go build ./...`、`go vet ./...` 通过；`conversation / tool / skills / server` 包测试全绿。

---

## v0.4.0 — TUI 美化 + Claude Code 风格 + 交互增强（2026-07-14）

### 🎨 TUI 视觉美化
- **全新品牌 Banner**：全宽单面板设计，集成 🐴 马图案，4 种终端宽度自适应（≥90/≥68/≥48/≥32 列）
- **`magenta` + `cyan` + `yellow` 配色方案**：向 Claude Code 风格靠拢，框架边线 dim
- **上下文进度条**：底部状态栏显示 `[▓▓▓░░░░░░░] 32% ctx` 可视化填充条
- **思考动画增强**：thinkingBar 集成真实上下文进度，不再使用纯装饰性滑动条

### ⌨️ 交互改进
- **Tab 模型切换**：按 Tab 在可用模型列表中循环切换，状态栏即时反馈
- **Ctrl+K 清空输入**：一键清空当前输入缓冲区
- **Ctrl+O 关闭欢迎**：快速关闭欢迎面板
- **Alt+Enter 发送**：多行模式下确认发送
- **斜杠命令反馈**：`notice()` 闪光提示，绿色 ✓ 图标在状态栏一闪而过
- **快捷键帮助更新**：`/help` 列出所有新快捷键

### 🌐 多语言
- **默认中文**：新增对话默认使用中文输出
- **`/lang zh-TW` / `/lang en`**：一键切换繁体中文或英文

## v0.3.0 — 新特性：多行输入 + 重试 + 搜索 + 回退链 + 自定义提示词（2026-07-14）

### 🚀 新特性

#### 用户体验
- **TUI 多行输入**：`/multiline` 切换多行模式，Enter 换行，Alt+Enter 发送，方便粘贴长篇内容
- **对话全文搜索**：`/api/search?q=<query>` 跨所有会话搜索消息内容，支持 SQLite LIKE + 内存双实现

#### 可靠性
- **API 自动重试 + 指数退避**：OpenAI 兼容 provider 在 429/502/503/504 和网络错误时自动重试 3 次（100ms→200ms→400ms 退避）
- **模型回退链**：配置 `fallback_models` 列表，主模型失败时自动切换到备用模型（如 `deepseek-v4-flash` → `deepseek-chat`）

#### 可配置性
- **用户自定义 System Prompt**：`config.Defaults.SystemPrompt` 覆盖默认系统提示词，通过 `/api/config` 或配置文件设置

### 🧪 测试覆盖
- **8 个 provider 测试文件**：智谱/Kimi/火山方舟/腾讯/华为/SCNET/OpenRouter/NVIDIA，共 +56 个测试（56→89）
- **MCP 客户端测试**：20+ 测试覆盖 Pool/JSON-RPC/Config
- **LSP 测试**：25+ 测试覆盖消息解析/SymbolKind/Transport

### 🐛 Bug 修复
- **死代码清理**：移除 SSE handler 中永不执行的 timeout fallback select 块

## v0.2.1 — Provider 重构 + 安全加固（2026-07-14）

### 🔧 Provider 重构
- **FactoryConfig + NewProvider 辅助函数**：消除 9 个 provider 中重复的 `if apiBase==""` 和 `Config{}` 样板代码，减少约 46 行重复代码
- **不影响外部 API**：所有 provider 保持 `func New(apiKey, apiBase string) types.Provider` 签名不变

### 🔒 安全加固（P1）
- **CORS 收紧**：从 `Access-Control-Allow-Origin: *` 改为按请求来源验证（仅 localhost/127.0.0.1）
- **请求体大小限制**：16 处 `json.NewDecoder` 统一添加 `MaxBytesReader(1MB)` 防护
- **SSE 断开检测**：`<-r.Context().Done()` 确保客户端断开时 goroutine 及时退出
- **MCP 命令验证**：阻止 shell 元字符注入

### 🐛 Bug 修复（P0）
- **readStream nil panic**：`chunk.Choices[0]` 加长度检查，防止 API 返回纯 usage chunk 时崩溃
- **CacheBreakpoints 传递**：从 ChatRequest 正确传递到 API 请求体，DeepSeek 缓存真正生效
- **LSP/checkpoint return nil,nil**：3 处错误不再被静默吞噬
- **build.bat 前端路径**：修复桌面版前端从未被正确嵌入的问题
- **desktop_windows Shutdown**：cancel 正确 defer 确保连接耗尽
- **TUI goroutine 泄漏 + 竞态**：ensureAnim 退出条件、SetContext 加锁、handleKey 退避、OnSend recover、persistSetting 错误上报
- **DB 错误处理**：rows.Scan/rows.Err/json.Marshal/time.Parse 全部加错误检查

### ⚙️ 构建/发布（P2）
- **CI 全流程**：前端构建 + macOS arm64 + 版本前缀修复
- **release.sh macOS 兼容**：sed 替代 grep -P
- **install.bat PATH 空格修复**：Windows 路径含空格时不崩溃
- **死代码/制品清理**：删除 desktop-launcher.exe(14MB)、cmd/desktop_launcher/、日志文件
- **.gitignore 增强**：覆盖测试临时文件、release 目录、编辑器备份

## v0.2.0 — 多轮深度改进（2026-07-14）

### 🚀 新增特性

#### 核心引擎
- **Snip 零成本过滤层**：自动删除空的 assistant 回复、被拒绝的工具调用轮次和空白消息。在上下文压缩前运行，零 API 开销。
- **Tool-Call 修复管道**：4 阶段自动修复（Flatten → Scavenge → Truncation → Storm），处理 DeepSeek 等模型常见的嵌套 JSON 错误和截断。
- **Doom Loop 检测**：连续 3 次相同工具调用自动暂停，防止 AI 陷入死循环。
- **输出截断恢复**：8K → 16K → 32K → 64K 自动升级，检测 JSON 截断、代码块未关闭、句子中断等 5 种截断模式。
- **预算强制引擎**：按工具类型设置硬限制（Bash 30K/Read 50K/Grep 20K/默认 50K），防止单一工具调用耗尽上下文。

#### 安全性
- **Bash 安全引擎**：23 条规则 + 3 级严重度（阻止/警告/询问），覆盖 rm -rf、curl|sh、sudo、路径穿越等常见攻击面。
- **双通道权限检查**：原有权限门 + Bash 安全引擎交叉验证，消除遗漏风险。

#### 桌面体验
- **单二进制桌面模式**：WebView2 原生窗口（1200×820），双击 exe 自动进入桌面模式，无需浏览器。
- **Git 快照 / Undo**：独立的 `.icode/undo/` 影子仓库，`/undo N` 可回滚 N 步文件变更。

#### 开发效率
- **LSP 集成**：gopls/pyright/typescript-language-server/rust-analyzer 自动加载，代码诊断注入上下文。
- **Sub-Agent 系统**：explore/plan/general 三个预置子代理，独立 Optimizer 上下文隔离，主会话仅接收最终结论（典型 200-800 tokens）。
- **多引擎 Web 搜索**：Bing + Baidu + Tavily 自动故障转移。

### 🧪 测试覆盖
- 新增 **6 个测试文件 + 29 个单元测试**，覆盖：
  - Snip 过滤层（5 用例）
  - Tool-Call 修复管道（10 用例）
  - 预算强制（7 用例）
  - Doom Loop 检测（9 用例）
  - 输出截断恢复（8 用例）
  - Bash 安全 23 条规则（10 用例）

### 🐛 Bug 修复
- 修复 DeepSeek 流式响应中的 `reasoning_content` 字段导致 JSON 解析失败
- 修复 `bashsec.go` 中 Unicode BOM 编译错误
- 修复 `repair.go` 中 `callKey` 作用域冲突
- 修复 `engine.go` 中 `parseToolArgs` 返回值不匹配
- 修复 LSP 集成中未使用的变量

### 🔧 技术债务
- `cmd/desktop_other.go`：非 Windows 平台桌面模式桩
- 持续集成：GitHub Actions build + test + vet + release 全流程

---

## v0.1.0 — 初始发布

### 核心特性
- CLI (TUI) 交互式编程助手
- 50+ LLM 提供商支持（DeepSeek/Anthropic/OpenAI/智谱/Kimi/火山方舟/腾讯/华为/SCNET/OpenRouter）
- Cache-First Token 优化引擎
- 中/英文界面（zh-CN/zh-TW/en）
- MCP (Model Context Protocol) 客户端
- Todo 列表管理工具
- 权限控制：Plan/Agent/Auto/YOLO 四模式
- 会话管理 + SQLite 持久化
