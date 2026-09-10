# iCode — 项目上下文

## 项目描述

iCode 是一个多模型 AI 编码助手，可运行于终端和桌面。开箱支持 14 个 LLM 提供商、60+ 模型，缓存优先架构可实现最高 94% token 节省。Go 语言编写，使用 cobra CLI 框架，附带 WebView2（Windows）/ 浏览器（macOS·Linux）桌面应用与 VS Code 扩展。一键自动更新模型：刷新后自动检测新增/下架模型，连续 2 次确认标记下架，文档页富化补充元数据。

## 构建命令

- `go build -o icode .` — 构建 CLI + 桌面二进制（Linux/macOS；桌面需先 `cd desktop && npm run build` 把前端嵌入 `internal/embedded/dist`）
- 无界面纯 CLI（跨平台交叉编译，无需 CGO / GUI 库）：`CGO_ENABLED=0 go build -tags nogui -o icode .`，再 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags nogui .` 等
- Windows 增强 TUI（双击 `icode.exe` 启动，带可视滚动条/可点击光标/对标快捷键）：`go build -ldflags="-s -w -H windowsgui" -o icode.exe .`，或直接用 `build.bat` / `install.bat`（已默认带该标志）。桌面版请显式运行 `icode desktop`（或 `desktop_only` 标签构建的 `icode-desktop.exe`）。
- **多平台自动构建 CI**：`.github/workflows/build.yml` 在 push/PR 跑测试矩阵，打 `v*` tag 时由 native runner 产出桌面 GUI 二进制（CGO）+ 单个 Linux runner 交叉编译无界面 CLI 二进制（11 个资产），自动建 GitHub Release。

## 桌面版功能（v0.12.0 → v0.15.0）
- **系统托盘 + 全局热键**：`icode desktop` 启动后常驻系统托盘，右键菜单「显示/隐藏/退出」；`Ctrl+Alt+I` 全局唤起或隐藏窗口；关闭窗口默认最小化到托盘（仅托盘退出才真正关闭）。
- **Token 节省仪表盘**：分析页「会话/全局」双 Tab。全局视图跨会话、跨重启聚合累计节省 Token / 缓存命中 / 花费 / 节省金额，并展示按日趋势（数据落库 `session_stats` 表）。
- **多标签会话 + 工作区**：聊天页编辑器式标签条（切换/关闭/新建会话）；侧栏新增「工作区」面板，可创建/切换工作区（后端 `workspaces` 表 + `GET/POST/PUT/DELETE /api/workspaces`）。
- **技能 / MCP / 连接器管理**：设置页「技能」展示真实技能列表并支持启用/禁用（`/api/skills/{name}/enable`）；MCP 信任模式可正常保存（`PUT /api/mcp/trust`）；连接器（WorkBuddy）状态可查看。
- **技能市场（对标 WorkBuddy 优点）**：设置「技能」页增加「市场」子标签，列出内置 5 个通用技能（代码审查 / 提交信息 / 解释代码 / 单元测试 / 文档生成）与「已安装」状态，一键安装 / 卸载；并支持从本地绝对路径导入社区技能（`POST /api/skills/import`）。技能以内置 catalog 形式（embed 进二进制）随 `icode` 分发。

### v0.13.0 增强
- **工作区↔会话深度绑定**：新建会话自动归入当前工作区（后端 `AddSessionToWorkspace` + `POST /api/workspaces/{id}/sessions` 支持追加语义）；切换工作区即切换会话标签条。
- **多标签跨路由持久化**：`openTabs` 迁入全局 `appStore`（`openTabIds`），新增 `closeTab`；`openTabIds`/`activeSessionId`/`activeWorkspaceId` 持久化到 localStorage，重启与切页不丢。
- **路由默认升级**：`routing.mode` 默认从 `keyword` 改为 `embedding`（本地零 token 语义分类，只增不减），存量空配置也映射为 embedding。

### v0.14.0 增强
- **技能市场（对标 WorkBuddy 优点）**：内置 `skills/catalog/`（5 个通用技能，embed 进二进制）构成本地市场；后端 `GET /api/skills/market` + `POST /api/skills/market/install` + `DELETE /api/skills/market/{name}` + `POST /api/skills/import`；`Install` 复制进 `~/.icode/skills`，`Uninstall` 仅删用户目录技能，`Import` 从本地路径导入。修复 embed 在 Windows 须用 `path.Join`（正斜杠）的坑。
- **桌面技能市场 Tab**：设置「技能」改「已安装/市场」双子标签，市场支持一键安装/卸载 + 本地导入；修正侧栏「技能」误显「记忆」的遗留 bug；i18n 补 `skillMarket.*` 三语言。

### v0.16.0 增强
- **多模态结果回灌上下文（对标 WorkBuddy/Claude 多模态闭环）**：`image_gen` 工具生成图片后，除落盘外，还将图片以 base64 作为 `Attachment` 回传给引擎；引擎在回灌工具结果时，把本轮所有生成的图片合并为一条 `user` 消息追加进对话历史，后续 vision 模型（Anthropic/OpenAI/Kimi/智谱/DeepSeek/火山/昆仑等）能在下一轮「看到」这张图继续对话。

### v0.17.0 增强
- **前端图片展示（补齐 v0.16 多模态回灌的 UI 缺口）**：桌面前端 `ChatPage` 现在渲染 `message.attachments`——图片类型显示为可点击缩略图（点击放大 lightbox），非图片类型显示为文件卡片；覆盖 bot 消息（v0.16 回灌的生成图，重载会话即可见）与 user 消息（用户上传图即时显示）。`Message` 类型新增 `Attachment` 接口；`loadSessions` 在 IPC/HTTP 两条路径都保留 `attachments` 字段；发送侧把上传图存入 `userMsg.attachments`。纯前端增强，无后端改动（后端 `types.Message.Attachments` 已含 `json:"attachments,omitempty"`）。
- **Provider 真正消费附件**：`openai_compat`（覆盖 OpenAI 兼容群）与 `anthropic` 在编码请求时，将消息的 `Attachment` 转为 `image_url` / Anthropic `image` 源块；非图片附件、tool/tool-result 消息保持纯文本，不破坏既有文本对话。这也顺带补齐了「用户上传图片」此前未接通模型的数据通路。
- 视频因体积大且多数 vision 模型暂不支持视频输入，暂不回灌大附件（仅保留文本路径说明），避免上下文膨胀。

### v0.20.0 增强
- **桌面端设置页（新增「桌面」Tab）**：设置页新增「桌面」分类（lucide `Laptop` 图标），含三项：① 开机自启 iOS 风格开关（写入 `PUT /api/config`，后端按平台落地——Windows 注册表 Run 键、macOS LaunchAgent plist、Linux XDG autostart `.desktop`，纯 Go 无 CGO）；② 后端端口数字输入（校验 0–65535，0=自动，持久化到 `config.yaml`，`bootDesktopBackend` 优先用配置端口、0 回退 `findFreePort`）；③ 全局热键说明卡片（`Ctrl+Shift+Space`，区分 Windows 显隐原生窗口 / macOS·Linux 浏览器唤起）。i18n 补三语 key；`appStore` 增 `autostart/serverPort/setAutostart/setServerPort/loadDesktopSettings`，启动回填当前配置。纯前端改动，tsc + vite build 全绿。

### v0.15.0 增强
- **VS Code 扩展（对标编辑器内 AI 助手体验）**：新增 `vscode/` 独立扩展工程，活动栏「iCode」图标 + 侧栏 WebView 聊天视图（原生 JS，零框架），命令含打开侧栏 / 启动后端 / 终端启动完整 TUI / 打开设置。后端发现优先读 `%TEMP%/icode/port` 或探测 `57356/8080/3000`，否则自动 `icode server`；WebView 经 `acquireVsCodeApi` 与扩展进程通信，扩展用 Node `http` 代理 `POST /api/chat`（SSE 流式）、`/api/sessions`、`/api/models`、`/api/permission/respond`，绕开跨域 / X-Frame 限制无需后端额外开 CORS。支持流式回复、思考过程、工具调用卡片、交互式权限批准。

### v0.22.0 增强（VS Code 扩展）
- **编辑器集成**：选中代码右键「询问 / 解释 / 优化选中代码」（`editorHasSelection` 时显示），提示词自动带 `相对路径:起止行号` + 语言代码围栏；「询问」预填输入框可编辑，「解释/优化」自动发送。通道为扩展→webview `insertPrompt` 消息（含 `autoSend`），webview 未就绪时挂起 `pendingPrompt` 待 `attachWebview` 注入。
- **状态栏**：右侧常驻 `$(zap) iCode :端口`（已连接）/ `$(circle-slash) iCode`（未连接，警示底色），15s 健康轮询，点击打开侧栏。
- **配置项**：`icode.binPath`（自定义二进制路径）、`icode.serverPort`（固定端口，`ensureBackend` 最优先，配合后端 v0.20 `server.port`）、`icode.autoStartBackend`（默认 true）。扩展版本同步 0.22.0。
- `go build ./...` — 编译所有包
- `go vet ./...` — 静态检查
- `go test ./...` — 运行测试
- `go run . doctor` — 系统诊断
- `go run . chat` — 启动交互式会话
- `go run . exec -p "<prompt>"` — 非交互式单次执行

桌面端：
- `cd desktop && npm install` — 安装依赖
- `cd desktop && npm run dev` — 开发模式
- 或直接运行 `desktop.exe`

## 架构总览

```
main.go / main_ui.go / desktop_main.go     入口（CLI / simpleui / 桌面）
cmd/                        Cobra CLI 命令（root, chat, exec, auth, doctor, server,
                            acp, msoffice, cleanup, keydebug, tray_* / webview_*）
internal/
  app/                       App 启动和生命周期
  acp/                       ACP（Agent Client Protocol）stdio 服务端，编辑器接入
  audit/                     审计/对标差距分析辅助
  config/                    配置加载 + 国际化（zh-CN/zh-TW/en）
  core/
    agent/                   子代理注册、运行、多智能体团队（team:*）
    checkpoint/              基于 git worktree 的检查点/回退（/rewind /replay）
    codegraph/               符号索引（code_search 工具，懒构建）
    context/                 项目上下文加载（ICODE.md + 项目分析）
    conversation/            智能体对话循环引擎 + doom-loop 熔断 + 截断恢复
    hooks/                   生命周期 hooks（15 种事件，PreToolUse 可阻断）
    knowledge/               本地知识库 RAG（IDF/BM25 加权）
    permission/              权限门（plan/agent/yolo/auto + 分类器 + AllowedPaths 沙箱）
    plugins/                 插件加载
    prefmem/                 用户偏好记忆（跨轮，自动落盘）
    privacy/                 隐私脱敏（6 级安全等级）
    router/                  智能模型路由（keyword / embedding 本地零 token / llm）
    searchreplace/           SEARCH/REPLACE 编辑块 + staged diff
    session/                 会话持久化
    sessionum/               会话摘要/lite-resume/预算护栏
    skills/                  SKILL.md 系统（懒加载索引 + use_skill 工具 + 市场）
    slashcmd/                用户自定义斜杠命令
    slashui/                 三端共享斜杠命令集（/share html /replay 等）
    todo/                    待办事项存储
    tool/                    内置工具注册表（38 个：bash/read/write/edit/grep/glob/
                            search_replace/task/browser/computer-use/todo/image_gen/
                            video_gen/voice/code_search/disk_cleanup/ask_user_form…）
    voice/                   语音识别（百度/智谱/讯飞，WebAudio 16kHz PCM）
  db/                        SQLite 存储（WAL + busy_timeout）
  desktop/                   桌面后端启动/生命周期（WebView2 / 浏览器）
  embedded/                  前端 dist embed（go:embed，CI 构建填充）
  executil/                  跨平台命令执行工具
  llm/
    provider/                14 个提供商（anthropic, deepseek, zhipu, kimi,
                             volcengine, tencent, huawei, scnet, nvidia, ollama,
                             openrouter, openai_compat, agnes, sensenova）+ 注册表
    tokenopt/                缓存优先 token 优化（不可变前缀、仅追加日志、5 层压缩）
  lsp/                       LSP 代码诊断（项目语言自动探测 + 文件改后注入错误）
  mcp/                       MCP 客户端（JSON-RPC stdio + SSE 传输，tools/list_changed）
  mesh/                      跨机 mesh（token 鉴权、消息转发）
  msoffice/                  零依赖内置办公文档生成（docx/xlsx/pptx，OOXML）
  notify/                    系统通知（免打扰窗口）
  scheduler/                 自动化调度器（RRULE 秒级 + 模板库 + 执行历史）
  secure/                    密钥加密（Windows DPAPI / 跨平台兜底）
  server/                    HTTP API 服务器（REST + SSE 流式，loopback + 同源校验）
  static/                    静态资源
  tui/                       终端 UI（全屏 raw 模式 + 后备行模式 + simpleui）
  types/                     共享类型定义
  update/                    自更新（跨平台资产映射 + magic 校验 + 重启）
  xgo/                       panic 恢复安全 goroutine 包装
pkg/
  modelupdate/               模型列表自动更新（API+文档富化+Diff检测+下架标记+持久化）
desktop/                     WebView2 + React + TS 桌面前端（含 Git 工作台/分屏/技能市场）
vscode/                      VS Code 扩展（侧栏聊天/选中代码/状态栏/自动起后端）
configs/                     默认配置文件
```

## 设计原则

- **缓存优先**: 系统提示+工具定义组成不可变前缀，跨轮保持稳定，利用提供商 KV 缓存
- **多提供商**: 所有提供商实现 `types.Provider` 接口，统一注册
- **智能体循环**: `conversation.Engine` 流式推送事件（text/tool_use/done/error），内联执行工具，默认最多 25 轮（可配 `tools.max_tool_rounds`，0=默认 25）；续轮与首轮共用 `chatStreamWithFallback`（限流退避 + 备用模型 + CacheTTL）
- **隐私优先**: 6 级安全等级（本地处理→脱敏→本地大模型→代理模式），从不发送遥测
- **双端统一**: CLI（TUI/simpleui）+ 桌面（WebView2 / 浏览器）+ VS Code 扩展共享同一后端，会话数据存储在 SQLite

## 当前状态

### 已有功能
- 14 个 LLM 提供商（DeepSeek/Zhipu/Kimi/火山引擎/腾讯/华为/SCNet/NVIDIA/Ollama/OpenRouter/Anthropic/Agnes/SenseNova + 任意 openai_compat 50+）
- 流式事件推送（goroutine + channel）
- 内置工具系统（38 个：bash/read_file/write_file/edit/search_replace/grep/glob/ls/task/子代理 team:*/browser/computer-use/todo/image_gen/video_gen/voice/code_search/disk_cleanup/ask_user_form/ask_user_question…）
- 权限控制 4 模式（Plan/Agent/YOLO/Auto）
- SQLite 会话持久化
- 基于 git 的检查点/回退
- 缓存优先 token 优化
- 全屏 TUI（思考动画、上下文条、待办计数、安全徽章）
- HTTP API 服务器（REST + SSE 流式）
- MCP 客户端（stdio 传输，工具发现和执行）
- 子代理系统（Task 工具）
- 隐私脱敏 6 级
- 国际化（zh-CN/zh-TW/en）
- 72+ 斜杠命令（/help /model /mode /session /clear /share /replay /rewind /checkpoint /context /vim /statusline /output-style /loop /usage /cost /token /preset /zen /goal /permissions /kb …）
- 成本计算和仪表盘
- 桌面端 WebView2 / 浏览器应用（系统托盘 + 全局热键 + 多标签分屏 + Git 工作台 + 技能市场 + 自动化模板库 + 自动更新闭环）+ VS Code 扩展 + ACP 编辑器协议（Zed/Neovim 可接入）
- 技能系统（SKILL.md，自动注入 system prompt，模型按需遵循）
- 智能模型路由（按查询复杂度自动选 cheap/normal/powerful 模型）
- 模型自动更新（API+文档富化+Diff检测+下架标记，一键刷新）
- 多智能体团队（Agent Teams，task 工具以 `team:<name>` 调度，内置 team:review）
- LSP 代码诊断（文件改后自动启动对应语言服务器并注入编译错误提示）
- SEARCH/REPLACE 编辑块（search_replace 工具，支持 unified diff 应用）
- 跨平台磁盘清理（disk_cleanup 支持 Windows / Linux / macOS）
- 生命周期 Hooks（PreToolUse/PostToolUse/Stop，config.yaml `hooks:` 配置，exit 2 阻断工具调用）
- Headless JSON 输出（`icode exec -p "..." --output-format json|stream-json`，CI/管道可用）
- 用户级 + 项目级双层 Memory（`#` 记项目 ICODE.md，`# user:` 记 ~/.icode，TUI/桌面端行为一致）
- CodeGraph 符号检索（code_search 工具，懒构建索引，模型可直接查"X 在哪定义"）

### 已完成（v0.7 第三批新增）
- WorkBuddy 技能互通（`~/.workbuddy/skills` + 项目 `.workbuddy/skills` 自动加载，iCode 目录同名优先）
- WorkBuddy MCP 桥接（启动时自动导入 `~/.workbuddy/mcp.json`，`mcp_import_workbuddy: false` 可关）
- 并行工具执行（同回合只读工具并发 ≤4，写类工具顺序执行）
- 后台任务（bash `run_in_background` → `bg-N`，`task_output` 查询/列表/kill）
- 路由升级（中文关键词 + rune 长度阈值；可选 `routing.mode: llm` LLM 分级，失败回退关键词）

### 已完成（v0.8 第四批新增）
- 多模态工具 `image_gen` / `video_gen`（OpenAI 兼容 images/videos API，兼容智谱/火山/WorkBuddy 网关；未配置时友好提示）
- 首回合工具并行（首回合流式路径改走 `executeToolBatch`，只读并发、写类串行，与续轮语义一致）
- 首回合 LSP 诊断注入（与续轮循环对齐）

### 已完成（v0.9 第五批新增 — Cache-First 加固）
- **技能懒加载索引**：不可变前缀只放紧凑索引（`name+描述+触发词`），不再嵌入 SKILL.md 完整正文 → 前缀大小与缓存命中率与技能数量解耦。
- **`use_skill` 工具**：模型按需拉取技能完整正文，正文只进易失暂存区，永不污染缓存前缀。
- **激活预算层（Level 4）**：`BudgetEnforcer` 正式接入 `runTool`（read 50K / bash 30K / grep 20K / 全局 200K），超长输出头尾保留中间省略；补互斥锁支持并行。
- **Token 节省可视化**：TUI `/token` 命令 + 桌面端 TokenBar「🪙 已节省」实时指标（轮询 `/api/analytics`）。
- 5 层压缩管道（Snip→Dedup→Fold→Summary→Budget）现已**全活**。

### 已完成（v0.10 第六批新增 — 本地语义路由）
- **本地零成本 Embedding 路由**（`internal/core/router/embedding.go`）：纯离线、零 API、零 token 的最近质心余弦分类器（词 token + 中文字 bigram 的 hashing 特征），把简单查询更准地路由到便宜模型 → 省钱省 token，而分类本身不花一个 token。
- 三档路由：`keyword`（默认）→ `embedding`（推荐，本地零 token）→ `llm`（最高保真但每次分类花一次廉价模型调用）。
- 只增不减：带置信度阈值，不自信时回退关键词基线，绝不让路由变差。

### 已完成（v0.23 第十九批新增 — Token 节省深化：多模态附件淘汰）
- **多模态附件淘汰**（`internal/llm/tokenopt/attachment.go`）：v0.16 回灌的 base64 图片此前每轮重复发送、永不淘汰。现在新用户轮开始时只保留最近 2 个附件（可配 `Config.Attachment`），更旧的替换为 ~30 token 占位符（模型此前已阅览并描述过该图，语义由占位符 + 模型自身分析保留）；预算按消息整体计（全留或全淘汰）。
- **附件感知 token 估算**：`EstimateAttachmentTokens` 按 base64 解码字节数 ÷750（下限 85 / 上限 2000）估算图片 token；`estimateTokensLocked` 纳入附件权重 → `ShouldCompact` 不再低估多模态上下文。
- **统计**：`Stats.attachments_evicted` 新计数，节省量计入 `tokens_saved`（Token 仪表盘自动体现）。engine 零改动（零值配置自动生效，含 CLI↔桌面历史重放路径）。

### 已完成（v0.36 第二十批新增 — 一键自动更新模型）
- **三阶段更新管线**：API 获取 ID 列表 → 内置元数据富化 → llms.txt 文档页补充。`fetchDocModels()` 从 7 个 provider 的 llms.txt 抓取补充元数据（上下文/视觉/推理），404 优雅降级。
- **Diff 检测 + 连续 2 次下架确认**：`compareModels()` 对比 API vs 内置列表，新增模型自动加入；下架模型需连续 2 次 API 缺失才标记 `Deprecated: true`（避免抖动误判），灰显+⚠图标保留可用。
- **富化合并**：`mergeModelInfo()` 优先级 内置 > 文档 > API，确保 API 只返回 ID 骨架时仍能展示完整元数据。
- **Desktop Toast**：刷新后 8 秒浮动通知"✅ +N 新模型 | ⚠ M 已下架"，展开每 provider 详情。
- **SimpleUI 摘要**：刷新结果显示"📋 变更: ➕N 新增, ⚠️M 下架"+ 每个新模型名称。
- **持久化**：`~/.icode/cache/update-history.json`（最近 50 条）+ `{provider}.json`（含 Deprecated/DeprecatedCount）。
- `ModelInfo` 新增 `Deprecated` + `DeprecatedCount`；`ProviderUpdate` 新增 `Added`/`Removed`。

### 与竞品差距（剩余）
> 以下为 2026-07-25 状态（第十九批完成后，含 v0.23.0）。
> **更新（2026-09-06）**：2026-08-29 差距分析的 C1-C4（/share html、/replay、ACP、hooks 15 种）与 D1-D7（多会话流式 + 分屏、Git 工作台、自动化模板库、办公文档技能、桌面 en i18n、自动更新闭环、首跑向导测试连接）已全部落地（v0.48–v0.52）；2026-08-31 第三轮审查 24 项已全部闭环（v0.52.2 三补）；v0.53.0/v0.53.1 补齐 14 项四对标对齐（含 /usage loops、prompt_cache_ttl、model_pricing、/preset、/zen、用量达限自动重试、Focus view）。第四轮审查（2026-09-06）聚焦引擎长时流式、文档一致性、测试盲区。
> **更新（2026-09-10）**：v0.53.2 修复 provider 重试从未生效的潜藏 bug（`doRequestWithRetry` 用 `GetBody()` 取新副本）+ 限流消费 `Retry-After`（三层打通，12 家 OpenAI 兼容厂商单点受益）+ 桌面 SSE 心跳；v0.53.3 新增 `types.ModelFetcher` 实时获取厂商模型（13 家全覆盖）+ `ProviderCfg.EnabledModels` 勾选启用（`/api/models/fetch`、`/api/models/selection`，勾选中未在目录里的模型自动注册为 custom）；v0.53.4 新增全部厂商一键获取（并发限流 3）+ 模型排序（上下文长度/新发现优先），并修复 `/api/models` 驼峰/蛇形字段不一致导致上下文窗口长期失真；v0.53.5 新增 `internal/llm/modelmeta` 统一解析厂商 `/models`（过滤 embedding/语音/图像/审核等**非对话**条目——此前它们被原样上架并统一标记 `Tools: true`，必须等到实际发起对话才失败），并让**厂商自报的上下文窗口与最大输出自动生效**（OpenRouter `context_length` + `top_provider.max_completion_tokens` 等，此前被丢弃）、在勾选保存时随 custom 模型落盘。**当前版本 v0.53.6。**

> **更新（2026-09-10 · v0.53.6）**：每模型设置真正生效。此前 ⚙️「保存设置」把 `temperature`/`top_p`/`max_tokens` 发到只解码 provider/api_key/api_base 的 `/api/config/key`，**静默丢弃却显示「已保存」**；且弹窗状态硬编码 `0.7/4096/0.9`，打开即保存会覆盖模型真实参数。现拆分为 `/api/config/model`（每模型参数）+ `/api/config/key`（凭据），新增 `conversation.ModelParamsResolver` 覆盖层（主对话 / 工具 JSON 补发 / 备用模型三个请求点统一解析，引擎不 import config），`temperature` 改为 `*float64` 使**显式 0 可真正下发**，`top_p` 全链路打通（Anthropic extended thinking 下按官方要求与 temperature 一并抑制），`/api/models` 回显覆盖值。

> **更新（2026-09-10 · v0.53.7）**：补上 v0.53.6 覆盖层的最后缺口——**子代理继承每模型生成参数**。子代理（Task 工具 / 会话 fork）走的是 `agent.Runner` 自建 `ChatRequest` 的另一条链路，温度原本**硬编码 0.1**，导致 ⚙️ 设置对子代理完全无效。现 `Runner` 新增 `ParamsResolver` + `resolveGeneration`，由引擎在懒创建 runner 时注入同一个解析器；默认仍为 0.1，仅用户为该模型配了值才覆盖（含显式 0）。`MaxTokens` 刻意不接——`AgentDef.MaxTokens` 是按代理的设计预算，不是按模型的。新增 `Runner.HasModelParamsResolver()` 使接线可被断言验证。

> **更新（2026-09-10 · v0.53.8）**：修复「清单里没有、手动加又说已存在」这对矛盾提示，并给勾选清单加搜索筛选框。根因：`/api/models/fetch` **只返回厂商 `/models`**（外加用户自定义），不含内置目录；而手动添加命中 `cataloguedByProvider` → 409。更严重的是保存时 `SetEnabledModels` 写入的是清单勾选集，**厂商没报的内置模型会被过滤器永久挤出模型列表且再也加不回来**。现在清单改为「厂商实时列表 + 内置目录 + 用户自定义」三方并集（内置补充项带 `builtin_only` 标记与「内置」徽标、表头说明来源），内置模型在清单里显示目录数值以与 `/api/models` 一致；手动添加内置模型改为**并入启用集合并返回 200**（仅在厂商开关关闭时才报错）；清单新增按 id/名称的搜索框，「全选」作用域改为可见行并采用**并集**语义，避免筛选状态下静默清空隐藏项。**当前版本 v0.53.8。**

1. **非 Windows 平台托盘/热键（已补齐）**: v0.18 起 `icode desktop` 在 macOS / Linux 提供系统托盘 + 菜单「在浏览器中打开 / 退出」+ 自动打开默认浏览器；v0.19 起 POSIX 也注册全局热键 `Ctrl+Shift+Space`（用 `golang.design/x/hotkey`，CGO），触发即重新聚焦/打开本机前端，与 Windows 原生热键组合一致。Windows 仍走原生 WebView2 窗口 + 子类化窗口过程（`Ctrl+Shift+Space` 显隐切换）。**已知限制**：① macOS 需授予辅助功能（Accessibility）权限且热键事件需主线程派发，真机待点测；② Linux Wayland 会话不暴露全局热键协议，注册通常失败，回退托盘菜单；③ macOS / Linux 的原生托盘与热键依赖 CGO（Cocoa / libappindicator / ayatana），必须在目标 OS 上以 `CGO_ENABLED=1` + 对应 SDK 构建，本 Windows 开发环境无法交叉编译验证（仅验证 Windows 构建与代码），真机待点测。
2. **托盘真机验证**: v0.12 原生托盘/热键仅在无头环境验证编译与纯函数单测，真实 Windows 交互待点测（v0.18 未改变此状态）。
> 已完成：模型自动更新（v0.36，API+文档富化+Diff检测+下架标记+持久化）、Token 节省深化（v0.23，多模态附件淘汰 + 附件感知估算，多模态会话最大浪费点消除）、VS Code 扩展增强（v0.22，选中代码右键发送 + 状态栏后端状态 + binPath/serverPort/autoStart 配置项）、跨平台自动构建 CI（v0.21，桌面 GUI 原生 CGO 构建 + 无界面 CLI 纯 Go 交叉编译，覆盖 win/linux/darwin amd64+arm64 + freebsd）、跨平台桌面后端启动（v0.18，executil 跨平台编译修复 + 共享 bootDesktopBackend）、桌面端设置增强（v0.20，开机自启 / 后端端口 / 全局热键说明，纯前端可验证）、前端图片展示（v0.17，ChatPage 渲染 message.attachments 缩略图 + lightbox）、多模态结果回灌上下文（v0.16，image_gen 回灌图片 + Provider 编码 image_url/Anthropic 图片块）、技能市场分发（v0.14）、VS Code 扩展（v0.15）、工作区↔会话深度绑定（v0.13）、多标签跨路由持久化（v0.13）、Embedding 路由默认开启（v0.13）、v0.12 桌面四大子系统。

## 约定

> 本行下为长期记忆约定，永久生效。

- **语言**：与用户的所有对话回复、思考过程一律使用简体中文。
- Go 1.26+，module path: `github.com/ponygates/icode`
- 包名匹配目录名（如 `conversation`, `tokenopt`）
- 公开类型/函数 PascalCase，私有 camelCase
- 所有文件读写和命令执行经过 tool 注册表，权限门审批
- 优先编辑已有文件，不创建文档文件除非明确要求
- 引用代码时使用 `file_path:line_number` 格式
- 保持回答简洁直接

## 文件位置

- `E:\icode\main.go` — 入口
- `E:\icode\internal\tui\tui.go` — 终端 UI
- `E:\icode\internal\server\server.go` — HTTP 服务器
- `E:\icode\internal\core\conversation\engine.go` — 对话引擎
- `E:\icode\internal\mcp\client.go` — MCP 客户端
- `E:\icode\internal\llm\provider\` — 9 个提供商实现
- `E:\icode\internal\types\types.go` — 核心类型定义
- `E:\icode\internal\core\tool\tools.go` — 工具系统
- `E:\icode\internal\core\tokenopt\optimizer.go` — token 优化
- `E:\icode\pkg\modelupdate\service.go` — 模型自动更新服务（富化+Diff+下架+文档抓取）
- `E:\icode\cmd\commands.go` — CLI 命令
- `E:\icode\desktop\` — 桌面端
