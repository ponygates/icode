# iCode &middot; [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](https://go.dev)

> **多模型 AI 编程 Agent** — 终端原生、多厂商支持的编程助手。

iCode 是一款开源的 AI 编程代理，支持在终端和桌面端双平台运行。开箱即用 **9 家大模型厂商、60+ 个模型**，配备一键更新系统（可扩展到 50+ 厂商），始终同步最新模型列表。基于 **Cache-First Token 优化** 架构，在支持的厂商上可实现最高 94% 的 Token 节省。

## 为什么选择 iCode？

| 功能 | iCode | Claude Code | Cursor |
|------|-------|-------------|--------|
| **国内大模型** | DeepSeek/智谱/Kimi/华为/腾讯/火山方舟/SCNET 等 7 家 | ❌ | ❌ |
| **一键模型更新** | ✅ 50+ 厂商 | ❌ | ❌ |
| **Cache-First 节省** | ✅ 最高 94% | 部分支持 | ❌ |
| **简繁中文原生** | ✅ 完整界面 + 帮助 | ❌ | 部分 |
| **CLI + 桌面双端** | ✅ 全平台 | 仅 CLI | 仅桌面 |
| **开源协议** | Apache-2.0 | 闭源 | 闭源 |
| **MCP 协议** | ✅ JSON-RPC stdio | ✅ | ❌ |
| **权限模式** | 计划/代理/自动 三级 | 部分 | 仅自动 |

## 快速开始

```bash
# 源码安装
git clone https://github.com/ponygates/icode.git
cd icode
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w -H windowsgui" -o icode.exe .       # Windows：双击启动增强 TUI（加强版 CLI，带滚动条/鼠标/快捷键）；桌面版用 icode desktop

# 配置 API 密钥
./icode auth set --provider deepseek --key sk-你的密钥

# 启动交互式对话
./icode chat

# 单条指令执行
./icode exec -p "解释这个项目的架构"

# 系统诊断
./icode doctor
```

### 桌面版启动

```bash
cd desktop
npm install
npm run dev
```

## 支持的大模型

### 国内厂商
| 厂商 | 模型 | 缓存 | 备注 |
|------|------|------|------|
| **DeepSeek** | V3、R1 | ✅ 支持 | 缓存命中率可达 94% |
| **智谱 AI** | GLM-4-Plus、GLM-4-Flash | — | Flash 每日 200 万 Token 免费 |
| **月之暗面 Kimi** | moonshot-v1-8k/128k | — | 最大 128K 上下文窗口 |
| **火山方舟** | 豆包 Pro/Lite 32K | — | 字节跳动生态 |
| **腾讯混元** | 混元 Pro、Lite | — | 每日最高 1000 万 Token 免费 |
| **华为盘古** | 盘古 4.0 Pro/Code | — | 企业级大模型 |
| **国家超算 SCNET** | Chat、Code | — | 国产超算算力补贴价格 |

### 国际厂商
| 厂商 | 模型 | 缓存 |
|------|------|------|
| **OpenRouter** | Auto、Free、GPT-4o、Claude、Gemini | — |
| **Anthropic** | Claude Sonnet 4、Haiku 4 | ✅ Prompt Caching |

*一键模型更新功能会自动从各厂商 API 拉取最新模型列表和价格。*

## 技术架构

```
┌─────────────────────────────────────────────────────────┐
│                   表现层                                 │
│  ┌──────────┐   ┌──────────────┐   ┌────────────────┐  │
│  │ CLI/TUI  │   │   Electron   │   │   HTTP API     │  │
│  │  (ANSI)  │   │ (React + TS) │   │  (JSON-REST)   │  │
│  └────┬─────┘   └──────┬───────┘   └───────┬────────┘  │
│       └────────┬───────┘                   │           │
│  ┌─────────────┴─────────────────────────────────────┐  │
│  │              应用核心 (Go)                         │  │
│  │  会话 → 对话引擎 → 权限 → 工具编排                   │  │
│  └─────────────────────┬─────────────────────────────┘  │
│  ┌─────────────────────┴─────────────────────────────┐  │
│  │              智能层                                 │  │
│  │  多模型路由 │ Token 优化器 │ Prompt 构建器           │  │
│  └─────────────────────┬─────────────────────────────┘  │
│  ┌─────────────────────┴─────────────────────────────┐  │
│  │  DeepSeek │ 智谱 │ Kimi │ 火山 │ 腾讯 │ ...       │  │
│  │  华为 │ SCNET │ OpenRouter │ Anthropic            │  │
│  └───────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────┤
│  工具: Bash │ 读/写文件 │ Grep │ Glob │ LS │ MCP      │
│  数据: SQLite │ 文件系统 │ 缓存                         │
└─────────────────────────────────────────────────────────┘
```

## Cache-First Token 优化

iCode 的 Token 优化器借鉴 Reasonix 的 Prefix-Cache 设计，并扩展为跨厂商通用方案：

1. **不可变前缀** — system prompt + 工具定义固定在位置 0，轮次间绝不修改
2. **仅追加日志** — 消息严格按序追加，无原地编辑，保证缓存稳定性
3. **易失草稿区** — 工具调用结果每轮重建，用完即弃
4. **智能压缩** — 上下文溢出时自动将早期消息摘要注入前缀
5. **分厂商策略** — DeepSeek 用字节稳定前缀，Anthropic 用 `cache_control` 标记

> **前缀绝不膨胀**：技能（SKILL.md）的完整正文**不会**进入不可变前缀——前缀只放紧凑索引，模型按需用 `use_skill` 拉取正文（正文落在易失草稿区）。无论安装多少技能，前缀大小与缓存命中率都稳定。

### 五层压缩管道（全活）
1. **Snip** — 零成本滤掉空轮、被拒轮
2. **Dedup** — 工具输出内容去重（相同 `工具+参数` 第二次起替换为占位符）
3. **Microcompact** — 轮次间把工具结果折叠为占位符
4. **Context Fold** — 超阈值时把早期多轮摘要注入上下文
5. **Budget** — 硬性大小上限（read 50K / bash 30K / grep 20K / 全局 200K），超长输出头尾保留、中间省略

运行 `/token`（TUI）或查看桌面端 TokenBar 的「🪙 已节省」即可看到本会话的实时节省量。

### 实时面板
```
Model: deepseek-chat  |  Mode: agent
Cache: 94%  |  Cost: ¥0.0032  |  In: 1247  Out: 512
> 写一个二叉树排序函数
```

## 命令列表

```bash
icode chat                     # 启动交互式 TUI 对话
icode exec -p "指令"            # 执行单次 Prompt
icode auth set --provider --key  # 配置 API 密钥
icode model                    # 查看所有可用模型
icode model --refresh           # 一键更新模型列表
icode doctor                   # 系统健康诊断
icode server --port 0          # 启动 HTTP API 服务（桌面版使用）
```

### 交互模式斜杠命令

| 命令 | 说明 |
|------|------|
| `/help` | 显示帮助面板（含快捷键和所有命令） |
| `/model [id]` | 切换模型（无参 → 交互选择器，支持数字编号和模型 ID） |
| `/mode [agent\|plan\|yolo\|auto\|ask]` | 切换权限模式 |
| `/plan` / `/ask` / `/debug` | 模式快捷方式 |
| `/session` | 显示当前会话 |
| `/sessions` | 列出已保存会话 |
| `/resume <id> [--lite[=n]]` | 载入历史会话（`--lite` 只送摘要+最近 N 条） |
| `/fork <id>[@n]` / `/branch` | 从历史会话分支出独立会话 |
| `/rename <标题>` | 重命名当前会话 |
| `/cd <path>` | 移动会话工作目录 |
| `/goal set\|show\|clear` | 长目标模式（每轮自动携带） |
| `/budget set\|show\|warn\|clear` | Token 预算护栏 |
| `/new` | 开启新会话 |
| `/clear` | 清空当前会话（归档，可用 `/restore` 恢复） |
| `/wipe` | 不可逆清空 |
| `/restore <id>` | 恢复被 /clear 软删除的会话 |
| `/undo [N]` / `/rewind` / `/checkpoint` | 回滚前 N 步文件更改 |
| `/diff` | 显示 git 工作区差异 |
| `/review [file]` | 审查 diff 或指定文件 |
| `/apply` / `/reject` | 应用/丢弃暂存编辑 |
| `/search <关键词>` | 搜索历史会话 |
| `/export [file]` | 导出会话为 Markdown |
| `/share` | 导出带时间戳 Markdown 副本 |
| `/copy [file/N]` | 复制最后输出到剪贴板 |
| `/token` / `/cost` / `/usage` / `/stats` | Token 节省报告（Cache-First 五层压缩明细） |
| `/context` | 上下文用量 |
| `/summarize` | 会话摘要 |
| `/compact` | 压缩会话（手动） |
| `/status` / `/whoami` | 系统状态 |
| `/doctor` | 运行诊断 |
| `/keys` | API Key 配置状态 |
| `/config [set <k> <v>]` | 查看/设置配置 |
| `/output-style [concise\|normal\|verbose]` | 设置输出风格 |
| `/security [level]` | 设置安全等级 |
| `/permissions` | 查看权限/安全配置 |
| `/mcp [list\|add\|remove\|get\|restart]` | 管理 MCP 服务器 |
| `/memory [edit\|prefs\|forget\|clear]` | 查看/管理记忆文件 |
| `/hooks` | 查看/生成 hooks.yaml |
| `/init` | 创建 ICODE.md |
| `/agents` / `/skills` / `/teams` | 列出 agent / 技能 / 团队 |
| `/todo` | 查看当前会话待办任务 |
| `/add-dir <dir>` | 添加额外工作目录 |
| `/update` | 刷新模型目录 |
| `/lang [zh-CN\|zh-TW\|en]` | 切换语言 |
| `/login` / `/logout` | 配置/清除 API Key |
| `/feedback` | 反馈渠道 |
| `/release-notes` / `/changelog` | 查看更新日志 |
| `/bug` | 打开预填 Bug 报告 |
| `/exit` / `/quit` | 退出 iCode |
| `/admin [off]` | 切换管理员（yolo）模式 |
| `/pr` / `/pr_comments` | PR 评论（需 gh CLI） |
| `/changelog` | 查看更新日志 |

> 桌面端（Electron）和 TUI 共享同一套命令；未知命令自动转发到后端 `/api/slash`。

## 权限模式

| 模式 | 说明 |
|------|------|
| **Plan（计划）** | 只读调研，不允许写文件和执行命令 |
| **Agent（代理）** | 每次工具调用需人工确认（A=允许, D=拒绝, S=本次会话全允许） |
| **YOLO（自动）** | 在安全范围内自动执行，危险命令仍然拦截 |

可在 `~/.icode/hooks.yaml` 中对特定工具定制审批规则。

## MCP 集成

连接任意 MCP 服务器扩展工具能力，支持 stdio 和 SSE 两种传输协议。

### 配置文件

在 `~/.icode/config.yaml` 的 `mcp` 下配置：

```yaml
mcp:
  - name: filesystem
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/项目路径"]
    enabled: true
  - name: fetch
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-fetch"]
    enabled: true
```

### 桌面版管理

在桌面版的设置 → MCP 页面中可视化添加、编辑、删除和测试 MCP 服务器。

### CLI 命令

```bash
# 添加 MCP 服务器
icode config add-mcp --name my-server --command npx --args "-y @modelcontextprotocol/server-filesystem /path"

# 列出已配置的 MCP 服务器
icode config list-mcp

# 删除 MCP 服务器
icode config remove-mcp --name my-server
```

### API 接口

```http
GET  /api/mcp       # 列出所有 MCP 服务器及连接状态
PUT  /api/mcp       # 添加或更新 MCP 服务器
DELETE /api/mcp     # 删除 MCP 服务器
POST /api/mcp/test  # 测试连接（不持久化）
GET  /api/mcp/tools # 列出所有已发现的 MCP 工具
```

MCP 工具自动以 `mcp_<服务器名>_<工具名>` 格式注入引擎，可直接在对话中调用。

## 安装方式

### 源码编译
```bash
go install github.com/ponygates/icode@latest
```

### 预编译二进制
从 [GitHub Releases](https://github.com/ponygates/icode/releases) 下载对应平台版本。

### Windows (Scoop)
```powershell
scoop bucket add icode https://github.com/ponygates/icode
scoop install icode
```

### macOS (Homebrew)
```bash
brew install ponygates/icode/icode
```

## 开发参与

```bash
# 编译
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w -H windowsgui" -o icode.exe .       # Windows：双击启动增强 TUI（加强版 CLI，带滚动条/鼠标/快捷键）；桌面版用 icode desktop

# 测试
go test ./...

# 桌面版
cd desktop && npm install && npm run dev

# 启动 API 服务
go run . server --port 9090
```

## 路线图

- [x] **P1**: 项目骨架、核心接口、配置系统、i18n、Electron 前端骨架
- [x] **P2**: LLM 流式集成、9 大 Provider、SQLite 持久化、权限系统
- [x] **P3**: Token 优化器、TUI 终端界面、MCP 协议
- [x] **P4**: Electron 桌面版联调、HTTP API 服务、CI/CD
- [x] **v0.5**: 技能系统（SKILL.md）、多智能体团队、LSP 诊断、智能路由、跨平台磁盘清理、/api/skills 与 /api/teams
- [x] **v0.6**: 生命周期 Hooks（PreToolUse/PostToolUse/Stop）、Headless JSON 输出（`--output-format json|stream-json`）、双层 Memory（项目级 + 用户级）、`code_search` 符号索引工具
- [x] **v0.7**: WorkBuddy 技能/MCP 桥接（自动导入 `~/.workbuddy/mcp.json` + 技能目录互通）、并行工具执行（只读并发）、后台任务（`run_in_background` + `task_output`）、LLM 分级路由（`routing.mode: llm`）+ 中英文关键词升级
- [x] **v0.8**: 多模态工具（`image_gen` / `video_gen`，走 OpenAI 兼容后端）、首回合并行工具执行 + LSP 诊断
- [x] **v0.9**: Cache-First 加固——技能懒加载索引 + `use_skill` 工具 + 激活预算层(Level 4) + Token 节省可视化(`/token`、桌面「🪙 已节省」)
- [x] **v0.10**: 本地零成本 Embedding 语义路由（`routing.mode: embedding`，纯离线/零 token，比关键词更准）
- [x] **v0.11**: 技能市场分发（内置 catalog + 一键安装/卸载/导入）、VS Code 扩展、多模态结果回灌
- [x] **v0.12–v0.15**: 桌面系统托盘 + 全局热键、Token 节省仪表盘、多标签会话 + 工作区、技能/MCP/连接器管理、VS Code 扩展聊天面板
- [x] **v0.16–v0.20**: 多模态结果回灌上下文（vision 闭环）、桌面设置页（开机自启 / 后端端口）、路由默认升级 embedding
- [x] **v0.25–v0.35**: 桌面卡死/启动加固（MCP boot 阻塞、localStorage 序列化、主线程重渲染隔离、`/model` 选择器、简易 WebView2 UI）
- [x] **v0.36**: 一键自动更新模型（新增检测 + 下架标记 + 文档富化）
- [x] **v0.37**: 语音转写（三端麦克风按钮）、屏幕/输入自动化、启动自动恢复最近会话、文件树面板 + 暂存编辑 diff 审查覆盖层
- [x] **v0.38**: LSP 诊断（结构化条目 + 一键复制定位）、知识库 RAG 检索升级 IDF/BM25 加权排序、桌面系统通知（含免打扰时段）、检查点预览与撤销加固
- [x] **v0.39**: 分级授权兜底（连续拦截自动退回手动模式，`permission.strike_threshold` 可配）、闲时任务调度（`/idle`，低峰窗口跨午夜）、Goal 可验收目标模式（`/goal set <目标> --verify <验收命令>`）
- [ ] **当前**: 后端安全与并发加固（CSRF/同源防护、fetch SSRF 拦截、文件工具目录沙箱、配置/工具注册表加锁、API Key 脱敏）+ 前端类型与测试加固（vitest、i18n 三语键一致性）

## 许可证

Apache-2.0 &copy; 2025 iCode Contributors
