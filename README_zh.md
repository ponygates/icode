# iCode · [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev) [![Version](https://img.shields.io/badge/version-0.1.0-blue)](CHANGELOG.md)

> **多模型 AI 编程 Agent** — 终端原生、多厂商支持、缓存优先的编程助手。

iCode 是一款开源的 AI 编程代理，提供 **CLI（TUI）、桌面版（WebView2）、VS Code 扩展** 三端统一体验。开箱即用 **14 家大模型厂商、60+ 个模型**，支持任意 OpenAI 兼容端点即插即用；一键更新系统持续同步最新模型列表。基于 **Cache-First Token 优化** 架构，在支持的厂商上可实现最高 **94% 的 Token 节省**。

## 为什么选择 iCode？

| 功能 | iCode | Claude Code | Cursor |
|------|-------|-------------|--------|
| **国内大模型** | DeepSeek/智谱/Kimi/火山/腾讯/华为/SCNET 等 7 家 | ❌ | ❌ |
| **一键模型更新** | ✅ 厂商 API 实时拉取 + 下架检测 | ❌ | ❌ |
| **Cache-First 节省** | ✅ 最高 94%，五层压缩管道 | 部分支持 | ❌ |
| **简繁中文原生** | ✅ 完整界面 + 帮助（zh-CN/zh-TW/en） | ❌ | 部分 |
| **多端形态** | ✅ CLI + 桌面 + VS Code 扩展 + ACP | 仅 CLI | 仅桌面 |
| **本地零成本路由** | ✅ Embedding 语义分类不花一个 token | ❌ | ❌ |
| **开源协议** | Apache-2.0 | 闭源 | 闭源 |
| **MCP 协议** | ✅ stdio + SSE | ✅ | ❌ |
| **权限模式** | Plan / Agent / Auto / YOLO 四级 | 部分 | 仅自动 |

## 快速开始

```bash
# 源码构建
git clone https://github.com/ponygates/icode.git
cd icode
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w" -o icode.exe .                        # Windows（控制台子系统，勿加 -H windowsgui——会破坏中文输入法桥接）；桌面版用 icode desktop

# 配置 API 密钥
./icode auth set --provider deepseek --key sk-你的密钥

# 启动交互式对话（或直接 ./icode）
./icode chat

# 单条指令执行（脚本/CI 友好）
./icode exec -p "解释这个项目的架构"
./icode -P "修复失败的测试" --output-format json

# 查看版本与系统诊断
./icode version
./icode doctor
```

### 桌面版

```bash
icode desktop          # 已安装 iCode 时直接启动（原生 WebView2 窗口）
# 或从源码运行前端开发模式：
cd desktop && npm install && npm run dev
```

桌面版含系统托盘 + 全局热键、多标签会话、工作区、Git 工作台、技能市场、Token 节省仪表盘与自动更新。

## 支持的大模型

### 国内厂商
| 厂商 | 代表模型 | 缓存 | 备注 |
|------|---------|------|------|
| **DeepSeek** | V3、R1 | ✅ 支持 | 缓存命中率可达 94% |
| **智谱 AI** | GLM-4-Plus、GLM-4-Flash | — | Flash 每日 200 万 Token 免费 |
| **月之暗面 Kimi** | moonshot-v1 8K/128K | — | 最大 128K 上下文 |
| **火山方舟** | 豆包 Pro/Lite 32K | — | 字节跳动生态 |
| **腾讯混元** | 混元 Pro、Lite | — | 每日最高 1000 万 Token 免费 |
| **华为盘古** | 盘古 4.0 Pro/Code | — | 企业级大模型 |
| **国家超算 SCNET** | Chat、Code | — | 超算算力补贴价格 |

### 国际厂商与其他
| 厂商 | 代表模型 | 缓存 |
|------|---------|------|
| **Anthropic** | Claude Sonnet 4、Haiku 4 | ✅ Prompt Caching |
| **OpenRouter** | Auto、Free、GPT-4o、Claude、Gemini | — |
| **NVIDIA** | NIM 系列 | — |
| **Agnes / SenseNova / Ollama** | 各自系列 + 本地模型 | 部分 |
| **任意 OpenAI 兼容端点** | openai_compat 即插即用（50+） | 视端点 |

*「获取模型」实时拉取厂商 `/models` 清单，勾选启用；新增/下架自动检测（连续 2 次确认才标记下架）。*

## 技术架构

```
┌──────────────────────────────────────────────────────────┐
│                       表现层                              │
│  ┌──────────┐  ┌───────────────┐  ┌───────────────────┐  │
│  │ CLI / TUI │  │ WebView2+React │  │ VS Code / ACP /   │  │
│  │  (ANSI)  │  │  (React + TS)  │  │    HTTP API       │  │
│  └────┬─────┘  └───────┬───────┘  └─────────┬─────────┘  │
│       └────────────┬───┴────────────────────┘            │
│  ┌─────────────────┴──────────────────────────────────┐  │
│  │                 应用核心 (Go)                        │  │
│  │  会话 → 对话引擎 → 权限门 → 工具注册表（38 个）        │  │
│  │  技能 / MCP / 子代理团队 / Hooks / LSP / RAG / 调度   │  │
│  └─────────────────────┬──────────────────────────────┘  │
│  ┌─────────────────────┴──────────────────────────────┐  │
│  │                   智能层                             │  │
│  │  模型路由 │ Token 优化器 │ Prompt 构建 │ 模型自动更新   │  │
│  └─────────────────────┬──────────────────────────────┘  │
│  ┌─────────────────────┴──────────────────────────────┐  │
│  │  DeepSeek │ 智谱 │ Kimi │ 火山 │ 腾讯 │ 华为 │ SCNET │  │
│  │  NVIDIA │ Ollama │ OpenRouter │ Anthropic │ Agnes   │  │
│  │  SenseNova │ openai_compat（任意兼容端点）            │  │
│  └────────────────────────────────────────────────────┘  │
├──────────────────────────────────────────────────────────┤
│  数据: SQLite（WAL）│ 文件系统 │ 检查点（git worktree）    │
└──────────────────────────────────────────────────────────┘
```

## Cache-First Token 优化（核心优势）

1. **不可变前缀** — system prompt + 工具定义固定在位置 0，轮次间绝不修改
2. **仅追加日志** — 消息严格按序追加，无原地编辑，保证缓存稳定
3. **易失草稿区** — 工具结果每轮重建，用完即弃
4. **智能压缩** — 上下文溢出时自动摘要折叠
5. **分厂商策略** — DeepSeek 字节稳定前缀，Anthropic `cache_control` 标记

> **前缀绝不膨胀**：技能（SKILL.md）完整正文不进入不可变前缀——前缀只放紧凑索引，模型按需用 `use_skill` 拉取正文。无论装多少技能，前缀大小与缓存命中率都稳定。

### 五层压缩管道（全活）
1. **Snip** — 零成本滤掉空轮、被拒轮
2. **Dedup** — 相同 `工具+参数` 的输出第二次起替换为占位符
3. **Microcompact** — 跨轮把工具结果折叠为占位符
4. **Context Fold** — 超阈值时把早期多轮摘要注入上下文
5. **Budget** — 硬性上限（read 50K / bash 30K / grep 20K / 全局 200K），头尾保留中间省略

运行 `/token`（TUI）或查看桌面端 TokenBar「🪙 已节省」可看实时节省量；分析页有跨会话全局趋势。

## 命令列表

```bash
icode                    # 交互式 TUI 对话（默认命令）
icode chat               # 等效
icode -P "指令"           # 打印模式：非交互执行单次 prompt
icode exec -p "指令"      # 单次执行（旧入口，保留兼容）
icode auth set --provider --key   # 配置 API 密钥
icode model              # 查看所有可用模型
icode model --refresh    # 一键更新模型列表
icode doctor             # 系统健康诊断（版本/平台/Provider/数据库）
icode server --port 0    # 启动 HTTP API 服务（桌面版/扩展使用）
icode desktop            # 启动桌面版
icode upgrade            # 检查并自更新
```

### 交互模式斜杠命令（72+）

| 类别 | 命令 |
|------|------|
| **会话** | `/new` `/session` `/sessions` `/resume` `/fork` `/branch` `/rename` `/clear` `/restore` `/wipe` `/search` `/export` `/share` `/replay` |
| **模型/模式** | `/model` `/mode` `/plan` `/admin` `/preset` `/zen` `/output-style` |
| **上下文/成本** | `/token` `/cost` `/usage` `/context` `/compact` `/summarize` `/budget` `/goal` |
| **文件/代码** | `/diff` `/review` `/apply` `/reject` `/undo` `/rewind` `/checkpoint` `/lsp` `/init` |
| **扩展生态** | `/mcp` `/agents` `/skills` `/teams` `/kb` `/hooks` `/memory` `/todo` `/add-dir` |
| **系统** | `/help` `/doctor` `/keys` `/config` `/status` `/permissions` `/security` `/lang` `/update` `/bug` `/exit` |

> 桌面端与 TUI 共享同一套斜杠命令（slashui 层）；未知命令自动转发后端 `/api/slash`。

## 权限模式

| 模式 | 说明 |
|------|------|
| **Plan（计划）** | 只读调研，不允许写文件和执行命令 |
| **Agent（代理）** | 每次工具调用需人工确认（A=允许，D=拒绝，S=本会话全允许） |
| **Auto（智能）** | 分类器自动放行安全操作，敏感操作仍询问 |
| **YOLO（全自动）** | 在配置范围内自动执行，危险命令仍拦截 |

可在 `~/.icode/config.yaml` 与 `hooks.yaml` 中定制审批规则；连续拦截自动降级回手动模式。

## MCP 集成

支持 stdio 和 SSE 两种传输协议，连接任意 MCP 服务器扩展工具能力。

```yaml
# ~/.icode/config.yaml
mcp:
  - name: filesystem
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/项目路径"]
    enabled: true
```

```bash
icode config add-mcp --name my-server --command npx --args "-y @modelcontextprotocol/server-filesystem /path"
icode config list-mcp
icode config remove-mcp --name my-server
```

MCP 工具自动以 `mcp_<服务器名>_<工具名>` 注入引擎，对话中直接可用；WorkBuddy 用户自动桥接 `~/.workbuddy/mcp.json`。

## 安装方式

### 包管理器
```bash
# npm —— 跨平台启动器，自动拉取匹配系统的预编译二进制
npm install -g icode

# Homebrew（macOS / Linux）
brew install ponygates/tap/icode

# Scoop（Windows）
scoop bucket add icode https://github.com/ponygates/icode
scoop install icode

# WinGet（Windows）
winget install PonyGates.iCode
```

### 一行安装（Linux / macOS）
```bash
curl -fsSL https://raw.githubusercontent.com/ponygates/icode/master/install.sh | bash
```

> 各渠道均从同一份发布产物 `checksums.txt` 取 sha256 校验；npm/winget 清单由
> `scripts/gen_npm_packages.sh`、`scripts/gen_winget_manifest.sh` 在发布时生成，
> Homebrew/Scoop 清单见 `scripts/homebrew/icode.rb`、`scripts/scoop/icode.json`。

### 源码编译
```bash
go install github.com/ponygates/icode@latest
```

### 预编译二进制
从 [GitHub Releases](https://github.com/ponygates/icode/releases) 下载（CLI 覆盖 win/linux/darwin/freebsd amd64+arm64，桌面版为各平台原生构建）。

## 开发参与

```bash
# 构建全部
go build ./... && go vet ./... && go test ./...

# 完整二进制（CLI + 桌面合一）
build.bat                    # Windows：前端 + Go 一键构建
build.bat --cli              # 仅 CLI（不含嵌入前端）

# 无界面纯 CLI（跨平台交叉编译，无 CGO）
CGO_ENABLED=0 go build -tags nogui -o icode .

# 桌面前端
cd desktop && npm install && npm run dev
npx tsc --noEmit && npm test # 类型检查 + 单测

# VS Code 扩展
cd vscode && npm run compile
```

## 路线图

- [x] **0.1.0 基线**：多模型接入、缓存优先优化、38 工具、三端体验、安全隐私、技能/MCP/RAG 生态（历史 53 个版本的整合，详见 [CHANGELOG](CHANGELOG.md)）
- [ ] **下一阶段**：后端安全与并发加固（CSRF/同源防护、fetch SSRF 拦截、文件工具目录沙箱、配置/工具注册表加锁、API Key 脱敏）
- [ ] **长期**：前端巨型组件拆分（ChatPage/SettingsPage）、更多国内厂商实时接入、技能市场社区化

## 许可证

Apache-2.0 © 2025 iCode Contributors
