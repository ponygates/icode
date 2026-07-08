# iCode &middot; [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](https://go.dev)

> **多模型 AI 编程 Agent** — 终端原生、多厂商支持的编程助手。

iCode 是一款开源的 AI 编程代理，支持在终端和桌面端双平台运行。开箱即用 **9 家大模型厂商、21 个模型**，配备一键更新系统，始终同步最新模型列表。基于 **Cache-First Token 优化** 架构，在支持的厂商上可实现最高 94% 的 Token 节省。

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
go build -o icode .

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
```
/help      显示帮助面板
/model <id>  切换模型
/mode <plan|agent|yolo>  切换权限模式
/session   管理会话
/clear     清空对话历史
/exit      退出 iCode
```

## 权限模式

| 模式 | 说明 |
|------|------|
| **Plan（计划）** | 只读调研，不允许写文件和执行命令 |
| **Agent（代理）** | 每次工具调用需人工确认（A=允许, D=拒绝, S=本次会话全允许） |
| **YOLO（自动）** | 在安全范围内自动执行，危险命令仍然拦截 |

可在 `~/.icode/hooks.yaml` 中对特定工具定制审批规则。

## MCP 集成

连接任意 MCP 服务器扩展工具能力：

```yaml
# ~/.icode/mcp.json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/项目路径"],
      "enabled": true
    }
  }
}
```

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
go build -o icode .

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
- [ ] **v0.2**: VS Code 扩展、更多 Provider、工具沙箱隔离

## 许可证

Apache-2.0 &copy; 2025 iCode Contributors
