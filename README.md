# iCode &middot; [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev)

> **Multi-Model AI Coding Agent** — Your terminal-native, multi-provider coding companion.

iCode is an open-source AI coding agent that works in your terminal and on your desktop. It supports **9 LLM providers and 60+ models** out of the box, with a one-click update system that keeps model lists fresh (and can pull from 50+ providers). Built on a **cache-first token optimization** architecture, it delivers up to 94% token savings on supported providers.

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
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w -H windowsgui" -o icode.exe .       # Windows: no black console on double-click

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
│  │ CLI/TUI  │   │ WebView2+React │   │   HTTP API    │  │
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

> **The prefix never bloats**: full SKILL.md bodies are NOT embedded in the immutable prefix — only a compact skill index lives there. The model loads a skill's full instructions on demand via the `use_skill` tool (the body lands in the volatile scratch zone). No matter how many skills you install, the prefix size and cache hit rate stay stable.

### Five-Layer Compression Pipeline (all active)
1. **Snip** — zero-cost removal of empty / rejected turns
2. **Dedup** — tool-output content deduplication (identical `tool+args` collapses to a placeholder)
3. **Microcompact** — folds inter-turn tool results into placeholders
4. **Context Fold** — summarizes early turns into context when over threshold
5. **Budget** — hard size caps (read 50K / bash 30K / grep 20K / global 200K), head+tail kept, middle elided

Run `/token` (TUI) or watch the desktop TokenBar's "🪙 Saved" chip to see live savings for the session.

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
icode config add-mcp --name my-server --command npx --args "-y @modelcontextprotocol/server-filesystem /path"
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
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w -H windowsgui" -o icode.exe .       # Windows: no black console on double-click

# Test
go test ./...

# Desktop
cd desktop && npm install && npm run dev

# Server mode
go run . server --port 9090
```

## Roadmap

- [x] **P1**: Project skeleton, interfaces, config, i18n, WebView2 desktop skeleton
- [x] **P2**: LLM streaming, 9 providers, SQLite, permission system
- [x] **P3**: Token optimizer, TUI, MCP protocol
- [x] **P4**: WebView2 desktop integration, HTTP API, CI/CD
- [x] **v0.5**: Skills (SKILL.md), Agent Teams, LSP diagnostics, smart routing, cross-platform disk cleanup, /api/skills & /api/teams
- [x] **v0.6**: Lifecycle Hooks (PreToolUse/PostToolUse/Stop), headless JSON output (`--output-format json|stream-json`), dual-layer Memory (project + user), `code_search` symbol index tool
- [x] **v0.7**: WorkBuddy skill/MCP bridge (auto-import `~/.workbuddy/mcp.json` + shared skill dirs), parallel tool execution (read-only concurrency), background tasks (`run_in_background` + `task_output`), LLM-graded routing (`routing.mode: llm`) with zh/en keyword upgrade
- [x] **v0.8**: Multimodal tools (`image_gen` / `video_gen` via OpenAI-compatible backends), first-turn parallel tool execution + LSP diagnostics
- [x] **v0.9**: Cache-First hardening — lazy skill index + `use_skill` tool + activated budget layer (Level 4) + token-savings visibility (`/token`, desktop "🪙 Saved" chip)
- [x] **v0.10**: Local zero-cost embedding routing (`routing.mode: embedding`, fully offline / zero-token, more accurate than keywords)
- [x] **v0.11**: Skill marketplace distribution (built-in catalog + install/uninstall/import), VS Code extension, multimodal result feedback
- [x] **v0.12–v0.15**: Desktop system tray + global hotkey, token-savings dashboard, multi-tab sessions + workspaces, skill/MCP/connector management, VS Code extension chat panel
- [x] **v0.16–v0.20**: Multimodal result feed-back into context (vision round-trips), desktop settings page (autostart / backend port), routing default → embedding
- [x] **v0.25–v0.35**: Desktop freeze/startup hardening (MCP boot blocking, localStorage serialization, main-thread re-render isolation, `/model` picker, simple WebView2 UI)
- [x] **v0.36**: One-click model auto-update (detection + deprecation marking + doc enrichment)
- [x] **v0.37**: Voice transcription (mic button on all ends), screen/input automation, auto-resume most recent session on startup, file-tree panel + staged-edit diff review overlay
- [x] **v0.38**: LSP diagnostics (structured entries + one-click locate copy), knowledge-base RAG upgraded with IDF/BM25-weighted ranking, desktop system notifications (with do-not-disturb window), checkpoint preview & undo hardening
- [x] **v0.39**: Tiered-permission fallback (auto downgrade to manual mode after consecutive blocks, `permission.strike_threshold`), idle-time task scheduling (`/idle`, cross-midnight windows), verifiable Goal mode (`/goal set <goal> --verify <cmd>`)
- [ ] **Current**: Backend security & concurrency hardening (CSRF/same-origin guard, fetch SSRF blocking, directory sandbox for file tools, config/tool-registry locking, API-key redaction) + frontend type/test hardening (vitest, i18n locale parity)

## License

Apache-2.0 &copy; 2025 iCode Contributors
