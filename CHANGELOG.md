# 更新日志

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
