# 更新日志

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
