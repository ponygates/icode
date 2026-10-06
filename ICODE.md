# iCode — 项目上下文

## 项目描述

iCode 是一个多模型 AI 编码助手，提供 CLI（TUI）、桌面版（WebView2/浏览器）与 VS Code 扩展三端统一体验。开箱支持 14 个 LLM 提供商、60+ 模型，缓存优先架构可实现最高 94% token 节省。Go 语言编写，使用 cobra CLI 框架，附带 WebView2（Windows）/ 浏览器（macOS·Linux）桌面应用与 VS Code 扩展。一键自动更新模型：刷新后自动检测新增/下架模型，连续 2 次确认标记下架，文档页富化补充元数据。**当前版本 v0.1.0**（版本归零基线，历史 v0.2.x–v0.53.x 的整合，详见 CHANGELOG.md 与 docs/archive/）。

## 构建命令

- `go build -o icode .` — 构建 CLI + 桌面二进制（Linux/macOS；桌面需先 `cd desktop && npm run build` 把前端嵌入 `internal/embeded/dist`）
- 无界面纯 CLI（跨平台交叉编译，无需 CGO / GUI 库）：`CGO_ENABLED=0 go build -tags nogui -o icode .`，再 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags nogui .` 等
- Windows 增强 TUI（双击 `icode.exe` 会自动打开终端窗口运行，带可视滚动条/可点击光标/对标快捷键）：`go build -ldflags="-s -w" -o icode.exe .`，或直接用 `build.bat` / `install.bat`。**切勿加 `-H windowsgui`**——GUI 子系统会破坏中文输入法桥接（拼音字母漏入 + IME 上屏串直接写屏，即"文字乱码跑到输入框上方"bug 的根因）。桌面版请显式运行 `icode desktop`（或 `desktop_only` 标签构建的 `icode-desktop.exe`，后者才用 windowsgui）。
- **多平台自动构建 CI**：`.github/workflows/build.yml` 在 push/PR 跑测试矩阵，打 `v*` tag 时由 native runner 产出桌面 GUI 二进制（CGO）+ 单个 Linux runner 交叉编译无界面 CLI 二进制（7 个资产），自动建 GitHub Release。
- **CI 门禁**（都在 `.github/workflows/build.yml`）：
  - `Lint`：`golangci-lint`（配置 `.golangci.yml`，v2 schema：errcheck/govet/staticcheck/ineffassign/unused + gofmt/goimports）。存量技术债按「一行理由」粒度写进配置的 `exclusions.rules`，新增代码的问题会直接让 CI 变红；本地跑 `golangci-lint run ./...`。
  - `Coverage gate`：`scripts/check_coverage.sh` 真判红——总覆盖率低于 `TOTAL_MIN`、或 `internal/core/conversation` / `internal/core/tool` 跌破各自地板、或这两个包从 profile 里消失（构建挂了/测试被删），一律 fail；profile 作为 artifact 上传。阈值改地板前先跑 `go test ./... -coverprofile=coverage.out` 量一次。
  - `Race`：三平台都跑 `go test -race`（Windows 用 MSYS2 的 `mingw-w64-ucrt-x86_64-gcc` + `CGO_ENABLED=1`）。
- **Windows 铁律**：CLI 构建**永远不要**加 `-H windowsgui`（会破坏中文输入法桥接，即"文字乱码跑到输入框上方"的根因）；只有 `desktop_only` 标签的桌面二进制才用 windowsgui。CI 的 build-cli 矩阵保持 `CGO_ENABLED=0`，不引用任何 Windows 图形 GUI 包。

## 发布签名与校验（供应链）

发布产物不再只靠 PE/ELF magic 过关（那是 0 保护）：

1. CI 的 release job 对**每个资产**生成 `checksums.txt`（sha256）并逐个生成 `<asset>.minisig`（minisign 分离签名），`checksums.txt` 本身也被签名；两者既作为 Release 附件，sha256 清单同时写进 Release 正文。
2. 密钥：`bash scripts/sign_release.sh keygen` 生成一次。
   - 公钥 → 提交到 `scripts/signing/icode.pub`；build job 用 `-ldflags -X github.com/ponygates/icode/internal/update.MinisignPublicKey=<base64>` 注入，随二进制分发。
   - 私钥 → **绝不入库**（`.gitignore` 已挡 `*.sec`），只存在仓库 secret `MINISIGN_SECRET_KEY`（`base64 -w0 icode.sec`）。CI 需要**空口令**私钥，因为 minisign 只从终端读口令。
   - 只支持 minisign 默认 `Ed`（非 pre-hash）签名：Go 侧仅用 stdlib `crypto/ed25519` 校验，`ED`（Blake2b）类型会被明确拒绝，别用 `--hashed` 签名。
3. `internal/update/upgrade.go` 在换掉运行中的二进制之前：先按 `checksums.txt` 校 sha256，再用内置公钥校 minisign 签名；两项任一失败即拒绝安装。
4. **占位公钥 = fail closed**：`MinisignPublicKey` 默认是 `REPLACE_WITH_BASE64_MINISIGN_PUBLIC_KEY`（"未配置"是显式状态，不是静默跳过），此时自动更新直接拒绝安装并说明原因。
   - **逃生阀（仅供开发构建）**：`ICODE_UPDATE_ALLOW_UNSIGNED=1` 时允许"仅 sha256 校验、不校签名"安装，并打印醒目的 SECURITY WARNING。一旦内置了真公钥，该变量**完全无效**，签名必须通过。`install.sh` 里的同名策略一致。
5. release job 在没有配置 `MINISIGN_SECRET_KEY` 时**故意失败**：宁可不出发布，也不发未签名的二进制。

## 安装渠道

- `install.sh`（Linux/macOS 一行 curl）：识别 os/arch → 取 `checksums.txt` → sha256 不符即拒绝 → （配置公钥后）minisign 验签 → 装到 `~/.local/bin`，其次 `/usr/local/bin`（必要时 sudo）→ 打印版本。纯函数（`detect_os`/`detect_arch`/`asset_name`/`checksum_for`/`verify_sha256`/`pick_bin_dir`）可用 `source install.sh` 单独测试，下载基址可用 `ICODE_RELEASE_URL_BASE=file:///...` 覆盖。
- `scripts/homebrew/icode.rb`（Homebrew）、`scripts/scoop/icode.json`（Scoop）。
- `scripts/winget/PonyGates.iCode/0.1.0/*.yaml`（winget 清单源副本，schema 1.6.0）：真实清单必须 PR 到 microsoft/winget-pkgs，流程见 `scripts/winget/README.md`；`InstallerSha256` 是标注清楚的占位零值，发布后用 `checksums.txt` 的真实值回填。

## 功能亮点
- **系统托盘 + 全局热键**：`icode desktop` 启动后常驻系统托盘，右键菜单「显示/隐藏/退出」；`Ctrl+Shift+Space` 全局唤起或隐藏窗口；关闭窗口默认最小化到托盘（仅托盘退出才真正关闭）。
- **Token 节省仪表盘**：分析页「会话/全局」双 Tab。全局视图跨会话、跨重启聚合累计节省 Token / 缓存命中 / 花费 / 节省金额，并展示按日趋势（数据落库 `session_stats` 表）。
- **多标签会话 + 工作区**：聊天页编辑器式标签条（切换/关闭/新建会话）；侧栏新增「工作区」面板，可创建/切换工作区（后端 `workspaces` 表 + `GET/POST/PUT/DELETE /api/workspaces`）。
- **技能 / MCP / 连接器管理**：设置页「技能」展示真实技能列表并支持启用/禁用（`/api/skills/{name}/enable`）；MCP 信任模式可正常保存（`PUT /api/mcp/trust`）；连接器（WorkBuddy）状态可查看。
- **技能市场（对标 WorkBuddy 优点）**：设置「技能」页增加「市场」子标签，列出内置 13 个技能（编码 5：代码审查 / 提交信息 / 解释代码 / 单元测试 / 文档生成；办公 4：docx 报告 / pptx 幻灯片 / xlsx 表格 / 周报；通用 4：邮件 / 会议纪要 / 翻译 / 公众号文章）与「已安装」状态，一键安装 / 卸载；支持从本地绝对路径导入社区技能（`POST /api/skills/import`）、添加远程技能源（GitHub 仓库 backed）、行业包独立目录分发（`industry-packs/insurance/` 含 5 个保险技能）；`/skill-doctor` 一键体检技能安装状态。技能以内置 catalog 形式（embed 进二进制）随 `icode` 分发。

> 以下各小节按引入批次排列，历史版本对应关系见 `docs/archive/CHANGELOG_HISTORY.md`。

### 工作区与多标签（桌面）
- **工作区↔会话深度绑定**：新建会话自动归入当前工作区（后端 `AddSessionToWorkspace` + `POST /api/workspaces/{id}/sessions` 支持追加语义）；切换工作区即切换会话标签条。
- **多标签跨路由持久化**：`openTabs` 迁入全局 `appStore`（`openTabIds`），新增 `closeTab`；`openTabIds`/`activeSessionId`/`activeWorkspaceId` 持久化到 localStorage，重启与切页不丢。
- **路由默认升级**：`routing.mode` 默认从 `keyword` 改为 `embedding`（本地零 token 语义分类，只增不减），存量空配置也映射为 embedding。

### 技能市场（桌面）
- **技能市场（对标 WorkBuddy 优点）**：内置 `skills/catalog/`（13 个技能：编码 5 / 办公 4 / 通用 4，embed 进二进制）构成本地市场；后端 `GET /api/skills/market` + `POST /api/skills/market/install` + `DELETE /api/skills/market/{name}` + `POST /api/skills/import`；`Install` 复制进 `~/.icode/skills`，`Uninstall` 仅删用户目录技能，`Import` 从本地路径导入。修复 embed 在 Windows 须用 `path.Join`（正斜杠）的坑。后续扩展：技能按 `Category` 分组展示；行业技能（保险 5 个）剥离到 `industry-packs/insurance/`；远程技能源（`internal/core/skills/remote.go`，GitHub 仓库 backed）与零成本市场索引（`sources.go`，`ghAPIBase/ghRawBase` 可测钩子）；`/skill-doctor` 安装体检。
- **桌面技能市场 Tab**：设置「技能」改「已安装/市场」双子标签，市场支持一键安装/卸载 + 本地导入；修正侧栏「技能」误显「记忆」的遗留 bug；i18n 补 `skillMarket.*` 三语言。

### 多模态生成结果回灌
- **多模态结果回灌上下文（对标 WorkBuddy/Claude 多模态闭环）**：`image_gen` 工具生成图片后，除落盘外，还将图片以 base64 作为 `Attachment` 回传给引擎；引擎在回灌工具结果时，把本轮所有生成的图片合并为一条 `user` 消息追加进对话历史，后续 vision 模型（Anthropic/OpenAI/Kimi/智谱/DeepSeek/火山/昆仑等）能在下一轮「看到」这张图继续对话。

### 前端图片展示与附件编码
- **前端图片展示（补齐 v0.16 多模态回灌的 UI 缺口）**：桌面前端 `ChatPage` 现在渲染 `message.attachments`——图片类型显示为可点击缩略图（点击放大 lightbox），非图片类型显示为文件卡片；覆盖 bot 消息（v0.16 回灌的生成图，重载会话即可见）与 user 消息（用户上传图即时显示）。`Message` 类型新增 `Attachment` 接口；`loadSessions` 在 IPC/HTTP 两条路径都保留 `attachments` 字段；发送侧把上传图存入 `userMsg.attachments`。纯前端增强，无后端改动（后端 `types.Message.Attachments` 已含 `json:"attachments,omitempty"`）。
- **Provider 真正消费附件**：`openai_compat`（覆盖 OpenAI 兼容群）与 `anthropic` 在编码请求时，将消息的 `Attachment` 转为 `image_url` / Anthropic `image` 源块；非图片附件、tool/tool-result 消息保持纯文本，不破坏既有文本对话。这也顺带补齐了「用户上传图片」此前未接通模型的数据通路。
- 视频因体积大且多数 vision 模型暂不支持视频输入，暂不回灌大附件（仅保留文本路径说明），避免上下文膨胀。

### 桌面端设置（自启/端口/热键）
- **桌面端设置页（新增「桌面」Tab）**：设置页新增「桌面」分类（lucide `Laptop` 图标），含三项：① 开机自启 iOS 风格开关（写入 `PUT /api/config`，后端按平台落地——Windows 注册表 Run 键、macOS LaunchAgent plist、Linux XDG autostart `.desktop`，纯 Go 无 CGO）；② 后端端口数字输入（校验 0–65535，0=自动，持久化到 `config.yaml`，`bootDesktopBackend` 优先用配置端口、0 回退 `findFreePort`）；③ 全局热键说明卡片（`Ctrl+Shift+Space`，区分 Windows 显隐原生窗口 / macOS·Linux 浏览器唤起）。i18n 补三语 key；`appStore` 增 `autostart/serverPort/setAutostart/setServerPort/loadDesktopSettings`，启动回填当前配置。纯前端改动，tsc + vite build 全绿。

### VS Code 扩展
- **VS Code 扩展（对标编辑器内 AI 助手体验）**：新增 `vscode/` 独立扩展工程，活动栏「iCode」图标 + 侧栏 WebView 聊天视图（原生 JS，零框架），命令含打开侧栏 / 启动后端 / 终端启动完整 TUI / 打开设置。后端发现优先读 `%TEMP%/icode/port` 或探测 `57356/8080/3000`，否则自动 `icode server`；WebView 经 `acquireVsCodeApi` 与扩展进程通信，扩展用 Node `http` 代理 `POST /api/chat`（SSE 流式）、`/api/sessions`、`/api/models`、`/api/permission/respond`，绕开跨域 / X-Frame 限制无需后端额外开 CORS。支持流式回复、思考过程、工具调用卡片、交互式权限批准。

### VS Code 扩展增强（编辑器集成 / 状态栏 / 配置项）
- **编辑器集成**：选中代码右键「询问 / 解释 / 优化选中代码」（`editorHasSelection` 时显示），提示词自动带 `相对路径:起止行号` + 语言代码围栏；「询问」预填输入框可编辑，「解释/优化」自动发送。通道为扩展→webview `insertPrompt` 消息（含 `autoSend`），webview 未就绪时挂起 `pendingPrompt` 待 `attachWebview` 注入。
- **状态栏**：右侧常驻 `$(zap) iCode :端口`（已连接）/ `$(circle-slash) iCode`（未连接，警示底色），15s 健康轮询，点击打开侧栏。
- **配置项**：`icode.binPath`（自定义二进制路径）、`icode.serverPort`（固定端口，`ensureBackend` 最优先，配合后端固定端口 `server.port`）、`icode.autoStartBackend`（默认 true）。

## 常用命令

- `go build ./...` — 编译所有包
- `go vet ./...` — 静态检查
- `go test ./...` — 运行测试
- `golangci-lint run ./...` — 静态检查 + 格式（CI `Lint` job 用的同一份 `.golangci.yml`）
- `go test ./... -coverprofile=coverage.out && bash scripts/check_coverage.sh coverage.out` — 覆盖率门禁（本地预演）
- `go run . doctor` — 系统诊断
- `go run . chat` — 启动交互式会话
- `go run . exec -p "<prompt>"` — 非交互式单次执行

桌面端：
- `cd desktop && npm install` — 安装依赖
- `cd desktop && npm run dev` — 开发模式
- 或直接运行 `desktop.exe`

## 架构总览

```
main.go / desktop_main.go          入口（CLI / 桌面）
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
  tui/                       终端 UI（全屏 raw 模式 + 后备行模式）
  types/                     共享类型定义
  update/                    自更新（跨平台资产映射 + sha256/minisign 验签 + magic 校验 + 重启）
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
- **双端统一**: CLI（TUI）+ 桌面（WebView2 / 浏览器）+ VS Code 扩展共享同一后端，会话数据存储在 SQLite

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

### 历史批次说明

v0.7–v0.53 的逐批增强记录（WorkBuddy 桥接、多模态工具、Cache-First 加固、Embedding 路由、附件淘汰、模型自动更新、每模型参数覆盖、厂商实时模型获取等）已整合进 CHANGELOG.md 的 **v0.1.0 功能基线**；逐版本细节见 `docs/archive/CHANGELOG_HISTORY.md`。

### 与竞品差距

> 四轮对标审查（交互对标 / 功能差距 / 引擎安全 / 长时流式）已全部闭环，报告归档于 `docs/archive/`。当前状态：

1. **非 Windows 平台托盘/热键**: macOS / Linux 提供系统托盘 + 菜单 + 全局热键 `Ctrl+Shift+Space`（CGO，需目标 OS 原生构建）。**已知限制**：① macOS 需辅助功能权限，真机待点测；② Linux Wayland 不暴露全局热键协议，注册失败时回退托盘菜单；③ 本 Windows 开发环境无法交叉编译验证 macOS/Linux GUI 构建。
2. **托盘真机验证**: 原生托盘/热键仅在无头环境验证编译与纯函数单测，真实交互待点测。
3. **后端安全与并发加固（下一阶段）**: CSRF/同源防护、fetch SSRF 拦截、文件工具目录沙箱、配置/工具注册表加锁、API Key 脱敏。
4. **前端巨型组件**: `ChatPage.tsx` 113KB / `SettingsPage.tsx` 76KB——长期健康项，拆分回归风险高，等专项迭代。

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

### Windows 编码与命令执行铁律（两次数据事故的教训）

- **PowerShell 5.1 文本操作黑名单**：对源码/含中文文件禁止使用裸 `Get-Content`（按 ANSI/GBK 读 UTF-8 会产生不可逆的 `?` 替换）、`>` 重定向落盘（写出 UTF-16 LE BOM）、`Set-Content -Encoding UTF8`（带 BOM）。**唯一安全路径**：`[IO.File]::ReadAllLines/ReadAllText` + `WriteAllLines/WriteAllText`，编码显式传 `UTF8Encoding($false)`（无 BOM）；行尾按仓库现状用 `` `n `` 归一。
- **git 状态类命令只读**：`git status`/`git stash`/`git checkout` 等一律不附带写操作参数；任何可能改写工作区的 git 子命令先向风哥请示。
- **文件损坏救援**：优先 `git fsck --no-reflogs --unreachable` 找 dangling 对象（stash pop 后对象仍在库中），`git cat-file -p <sha>` 提取；提取后校验字节数与 `git rev-parse` 的 blob 大小一致再落盘。
- **`cmd /C` + 嵌套引号**：Go `exec.Command` 的 `EscapeArg` 遵循 CommandLineToArgvW 规则而 cmd.exe 不遵循，带引号参数会被重编码报"文件名、目录名或卷标语法不正确"。`cmd /C` 调用必须经 `executil` 的 `fixCmdQuote`（`SysProcAttr.CmdLine` 原始命令行 + `/S /C`）。
- **pnpm 10 非交互环境**：`pnpm run`/`pnpm add` 会触发 `approve-builds` 交互门；直接调用 `node_modules\.bin\` 下的 `.CMD` 入口（`tsc.CMD`/`vite.CMD`/`vitest.CMD`）。注意无扩展名的 `tsc` 是 sh 脚本，PowerShell 无法执行。
- **前端依赖版本**：vite 锁 5.4.x，vitest 必须 1.x（vitest 4 要求 vite 6+，启动即 `ERR_PACKAGE_PATH_NOT_EXPORTED`）。

## 文件位置

- `E:\icode\main.go` — CLI 入口
- `E:\icode\desktop_main.go` — 桌面专用构建入口（`-tags desktop_only`）
- `E:\icode\internal\tui\tui.go` — 终端 UI
- `E:\icode\internal\server\server.go` — HTTP 服务器
- `E:\icode\internal\core\conversation\engine.go` — 对话引擎
- `E:\icode\internal\mcp\client.go` — MCP 客户端
- `E:\icode\internal\llm\provider\` — 14 个提供商实现
- `E:\icode\internal\types\types.go` — 核心类型定义
- `E:\icode\internal\core\tool\tools.go` — 工具系统
- `E:\icode\internal\core\tokenopt\optimizer.go` — token 优化
- `E:\icode\pkg\modelupdate\service.go` — 模型自动更新服务（富化+Diff+下架+文档抓取）
- `E:\icode\cmd\commands.go` — CLI 命令
- `E:\icode\desktop\` — 桌面端
