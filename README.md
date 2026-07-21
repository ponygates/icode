# iCode &middot; [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](https://go.dev)

> **Multi-Model AI Coding Agent** — Your terminal-native, multi-provider coding companion.

iCode is an open-source AI coding agent that works in your terminal and on your desktop. It supports **9 LLM providers and 21 models** out of the box, with a one-click update system that keeps model lists fresh. Built on a **cache-first token optimization** architecture, it delivers up to 94% token savings on supported providers.

## Why iCode?

| Feature | iCode | Claude Code | Cursor |
|---------|-------|-------------|--------|
| **Chinese providers** | 7 domestic (DeepSeek, Zhipu, Kimi, etc.) | ❌ | ❌ |
| **One-click model update** | ✅ 50+ providers | ❌ | ❌ |
| **Cache-first token saving** | ✅ Up to 94% | Partial | ❌ |
| **Native zh-CN/zh-TW** | ✅ Full UI + help | ❌ | Partial |
| **CLI + Desktop** | ✅ Both | CLI only | Desktop only |
| **Open source** | Apache-2.0 | Proprietary | Proprietary |
| **MCP protocol** | ✅ JSON-RPC stdio | ✅ | ❌ |
| **Permission modes** | Plan / Agent / YOLO | Partial | YOLO only |

## Quick Start

```bash
# Install from source
git clone https://github.com/ponygates/icode.git
cd icode
go build -o icode .

# Configure your first API key
./icode auth set --provider deepseek --key sk-your-key-here

# Start an interactive session
./icode chat

# Run a single prompt
./icode exec -p "Explain this project architecture"

# Check system status
./icode doctor
```

### Desktop App

```bash
cd desktop
npm install
npm run dev
```

## Supported Providers

### Chinese Providers
| Provider | Models | Cache | Notes |
|----------|--------|-------|-------|
| **DeepSeek** | V3, R1 | ✅ Yes | 94% cache hit rate |
| **Zhipu (智谱)** | GLM-4-Plus, GLM-4-Flash | — | Flash: 2M free tokens/day |
| **Kimi (月之暗面)** | Moonshot v1 8K/128K | — | 128K context window |
| **Volcengine (火山方舟)** | Doubao Pro/Lite 32K | — | ByteDance ecosystem |
| **Tencent (腾讯混元)** | Hunyuan Pro, Lite | — | Up to 10M free tokens/day |
| **Huawei (华为盘古)** | Pangu 4.0 Pro/Code | — | Enterprise-grade |
| **SCNET (国家超算)** | Chat, Code | — | Subsidized pricing |

### International Providers
| Provider | Models | Cache |
|----------|--------|-------|
| **OpenRouter** | Auto, Free, GPT-4o, Claude, Gemini | — |
| **Anthropic** | Claude Sonnet 4, Haiku 4 | ✅ Prompt caching |

*One-click model update refreshes this list from provider APIs automatically.*

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                 Presentation Layer                      │
│  ┌──────────┐   ┌──────────────┐   ┌────────────────┐  │
│  │ CLI/TUI  │   │   Electron   │   │   HTTP API     │  │
│  │  (ANSI)  │   │ (React + TS) │   │  (JSON-REST)   │  │
│  └────┬─────┘   └──────┬───────┘   └───────┬────────┘  │
│       └────────┬───────┘                   │           │
│  ─ ─ ─ ─ ─ ─ ─┼─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─┼─ ─ ─ ─ ─  │
│  ┌─────────────┴─────────────────────────────────────┐  │
│  │           Application Core (Go)                   │  │
│  │  Session → Conversation → Permission → Tool       │  │
│  └─────────────────────┬─────────────────────────────┘  │
│  ┌─────────────────────┴─────────────────────────────┐  │
│  │            Intelligence Layer                      │  │
│  │  LLM Router │ Token Optimizer │ Prompt Builder    │  │
│  └─────────────────────┬─────────────────────────────┘  │
│  ┌─────────────────────┴─────────────────────────────┐  │
│  │  DeepSeek │ Zhipu │ Kimi │ Volc │ Tencent │ ...   │  │
│  │  Huawei │ SCNET │ OpenRouter │ Anthropic          │  │
│  └───────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────┤
│  Tools: Bash │ Read │ Write │ Grep │ Glob │ LS │ MCP   │
│  Data: SQLite │ Filesystem │ Cache                      │
└─────────────────────────────────────────────────────────┘
```

## Cache-First Token Optimization

iCode's token optimizer is inspired by Reasonix's prefix-cache design and extended for multi-provider use:

1. **Immutable Prefix** — system prompt + tool definitions placed at position 0, never mutated between turns
2. **Append-Only Log** — messages accumulate in strict order; no in-place edits that break cache
3. **Volatile Scratch** — tool results are ephemeral and discarded after each turn
4. **Smart Compaction** — when context overflows, old messages are summarized and folded into the prefix
5. **Per-Provider Strategies** — DeepSeek gets byte-stable prefixes, Anthropic gets `cache_control` markers

### Real-time Dashboard
```
Model: deepseek-chat  |  Mode: agent
Cache: 94%  |  Cost: ¥0.0032  |  In: 1247  Out: 512
> Write a function to sort a binary tree
```

## Commands

```bash
icode chat                   # Interactive TUI session
icode exec -p "prompt"       # Single-prompt execution
icode auth set --provider --key  # Configure API keys
icode model                  # List all available models
icode model --refresh        # Update model list from providers
icode doctor                 # System health check
icode server --port 0        # Start HTTP API server (for desktop)
```

### Chat Mode Slash Commands
```
/help      Show help overlay
/model <id>  Switch model
/mode <plan|agent|yolo>  Switch permission mode
/session   Manage sessions
/clear     Clear chat history
/exit      Exit iCode
```

## Permission Modes

| Mode | Description |
|------|-------------|
| **Plan** | Read-only survey. No file writes or command execution. |
| **Agent** | Each tool call requires user approval (A=allow, D=deny, S=session-allow) |
| **YOLO** | Auto-approve within configured bounds. Dangerous commands still blocked. |

Customize per-tool rules in `~/.icode/hooks.yaml`.

## MCP Integration

Connect to any MCP server for extended tool capabilities. Supports both stdio and SSE transport protocols.

### Configuration

Add entries under the `mcp` key in `~/.icode/config.yaml`:

```yaml
mcp:
  - name: filesystem
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
    enabled: true
  - name: fetch
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-fetch"]
    enabled: true
```

### Desktop Management

Use Settings → MCP page for visual add, edit, delete, and test of MCP servers.

### CLI Commands

```bash
icoode config add-mcp --name my-server --command npx --args "-y @modelcontextprotocol/server-filesystem /path"
icode config list-mcp
icode config remove-mcp --name my-server
```

### API Endpoints

```http
GET  /api/mcp       # List all MCP servers with connection status
PUT  /api/mcp       # Add or update an MCP server
DELETE /api/mcp     # Remove an MCP server
POST /api/mcp/test  # Test connection without persisting
GET  /api/mcp/tools # List all discovered MCP tools
```

MCP tools are automatically injected into the engine as `mcp_<server>_<tool>` and can be used directly in conversations.

## Installation

### From Source
```bash
go install github.com/ponygates/icode@latest
```

### Pre-built Binaries
Download from [GitHub Releases](https://github.com/ponygates/icode/releases).

### Windows (Scoop)
```powershell
scoop bucket add icode https://github.com/ponygates/icode
scoop install icode
```

### macOS (Homebrew)
```bash
brew install ponygates/icode/icode
```

## Development

```bash
# Build
go build -o icode .

# Test
go test ./...

# Desktop
cd desktop && npm install && npm run dev

# Server mode
go run . server --port 9090
```

## Roadmap

- [x] **P1**: Project skeleton, interfaces, config, i18n, Electron skeleton
- [x] **P2**: LLM streaming, 9 providers, SQLite, permission system
- [x] **P3**: Token optimizer, TUI, MCP protocol
- [x] **P4**: Electron backend integration, HTTP API, CI/CD
- [ ] **v0.2**: VS Code extension, more providers, tool sandbox

## License

Apache-2.0 &copy; 2025 iCode Contributors
