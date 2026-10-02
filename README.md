# iCode · [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE) [![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev) [![Version](https://img.shields.io/badge/version-0.1.0-blue)](CHANGELOG.md)

> **Multi-Model AI Coding Agent** — terminal-native, multi-provider, cache-first coding companion.

iCode is an open-source AI coding agent with a unified experience across **CLI (TUI), desktop (WebView2), and a VS Code extension**. It supports **14 built-in LLM providers (60+ models)** out of the box, plus any OpenAI-compatible endpoint, and keeps model lists fresh with a one-click auto-update system. Its **cache-first token optimization** delivers up to **94% token savings** on supported providers.

> 中文文档请见 [README_zh.md](README_zh.md)。

## Why iCode?

| Feature | iCode | Claude Code | Cursor |
|---------|-------|-------------|--------|
| **Chinese providers** | 7 domestic (DeepSeek, Zhipu, Kimi, etc.) | ❌ | ❌ |
| **One-click model update** | ✅ live vendor APIs + deprecation detection | ❌ | ❌ |
| **Cache-first savings** | ✅ up to 94%, 5-layer pipeline | Partial | ❌ |
| **Native zh-CN/zh-TW** | ✅ full UI + help | ❌ | Partial |
| **Surfaces** | CLI + Desktop + VS Code + ACP | CLI only | Desktop only |
| **Local zero-cost routing** | ✅ embedding classifier, zero tokens | ❌ | ❌ |
| **Open source** | Apache-2.0 | Proprietary | Proprietary |
| **MCP protocol** | ✅ stdio + SSE | ✅ | ❌ |
| **Permission modes** | Plan / Agent / Auto / YOLO | Partial | YOLO only |

## Quick Start

```bash
# Build from source
git clone https://github.com/ponygates/icode.git
cd icode
go build -o icode .                                          # Linux / macOS
go build -ldflags="-s -w" -o icode.exe .                        # Windows (console subsystem; do NOT add -H windowsgui — it breaks the CJK IME bridge); desktop via `icode desktop`

# Configure your first API key
./icode auth set --provider deepseek --key sk-your-key-here

# Start an interactive session (or just ./icode)
./icode chat

# Non-interactive single prompt (script/CI friendly)
./icode exec -p "Explain this project architecture"
./icode -P "fix the failing test" --output-format json

# Version & diagnostics
./icode version
./icode doctor
```

### Desktop App

```bash
icode desktop          # native WebView2 window (Windows) / tray + browser (macOS, Linux)
# frontend dev mode from source:
cd desktop && npm install && npm run dev
```

The desktop app ships with a system tray + global hotkey, multi-tab sessions, workspaces, a Git workbench, a skill marketplace, a token-savings dashboard, and self-update.

## Supported Providers

### Chinese Providers
| Provider | Models | Cache | Notes |
|----------|--------|-------|-------|
| **DeepSeek** | V3, R1 | ✅ Yes | 94% cache hit rate |
| **Zhipu (智谱)** | GLM-4-Plus, GLM-4-Flash | — | Flash: 2M free tokens/day |
| **Kimi (月之暗面)** | moonshot-v1 8K/128K | — | 128K context window |
| **Volcengine (火山方舟)** | Doubao Pro/Lite 32K | — | ByteDance ecosystem |
| **Tencent (腾讯混元)** | Hunyuan Pro, Lite | — | Up to 10M free tokens/day |
| **Huawei (华为盘古)** | Pangu 4.0 Pro/Code | — | Enterprise-grade |
| **SCNET (国家超算)** | Chat, Code | — | Subsidized pricing |

### International & Others
| Provider | Models | Cache |
|----------|--------|-------|
| **Anthropic** | Claude Sonnet 4, Haiku 4 | ✅ Prompt caching |
| **OpenRouter** | Auto, Free, GPT-4o, Claude, Gemini | — |
| **NVIDIA** | NIM series | — |
| **Agnes / SenseNova / Ollama** | own series + local models | Partial |
| **Any OpenAI-compatible endpoint** | openai_compat (50+) | depends |

*"Fetch models" pulls the vendor's live `/models` list with checkbox enablement; additions and deprecations are detected automatically (2 consecutive misses before deprecation).*

## Cache-First Token Optimization

1. **Immutable Prefix** — system prompt + tool definitions at position 0, never mutated between turns
2. **Append-Only Log** — messages accumulate in strict order; no in-place edits that break cache
3. **Volatile Scratch** — tool results are ephemeral and rebuilt each turn
4. **Smart Compaction** — early messages summarized and folded when context overflows
5. **Per-Provider Strategies** — byte-stable prefixes for DeepSeek, `cache_control` markers for Anthropic

> **The prefix never bloats**: full SKILL.md bodies are NOT embedded in the immutable prefix — only a compact skill index lives there; the model loads a skill on demand via the `use_skill` tool (the body lands in the volatile scratch zone).

### Five-Layer Compression Pipeline (all active)
1. **Snip** — zero-cost removal of empty / rejected turns
2. **Dedup** — identical `tool+args` output collapses to a placeholder
3. **Microcompact** — folds inter-turn tool results into placeholders
4. **Context Fold** — summarizes early turns when over threshold
5. **Budget** — hard caps (read 50K / bash 30K / grep 20K / global 200K), head+tail kept, middle elided

Run `/token` (TUI) or watch the desktop TokenBar's "🪙 Saved" chip for live savings; the analytics page shows cross-session trends.

## Commands

```bash
icode                    # interactive TUI session (default command)
icode chat               # equivalent
icode -P "prompt"        # print mode: non-interactive single prompt
icode exec -p "prompt"   # single-shot execution (legacy alias, kept for compat)
icode auth set --provider --key  # configure API keys
icode model              # list all available models
icode model --refresh    # update the model list from providers
icode doctor             # system health check (version/platform/providers/DB)
icode server --port 0    # start the HTTP API server (desktop/extension)
icode desktop            # launch the desktop app
icode upgrade            # check for updates and self-update
```

### Slash Commands in Chat (72+)

| Category | Commands |
|----------|----------|
| **Sessions** | `/new` `/session` `/sessions` `/resume` `/fork` `/branch` `/rename` `/clear` `/restore` `/wipe` `/search` `/export` `/share` `/replay` |
| **Model/Mode** | `/model` `/mode` `/plan` `/admin` `/preset` `/zen` `/output-style` |
| **Context/Cost** | `/token` `/cost` `/usage` `/context` `/compact` `/summarize` `/budget` `/goal` |
| **Files/Code** | `/diff` `/review` `/apply` `/reject` `/undo` `/rewind` `/checkpoint` `/lsp` `/init` |
| **Ecosystem** | `/mcp` `/agents` `/skills` `/teams` `/kb` `/hooks` `/memory` `/todo` `/add-dir` |
| **System** | `/help` `/doctor` `/keys` `/config` `/status` `/permissions` `/security` `/lang` `/update` `/bug` `/exit` |

> The desktop and TUI share the same slash-command layer; unknown commands are forwarded to `/api/slash`.

## Permission Modes

| Mode | Description |
|------|-------------|
| **Plan** | Read-only survey. No file writes or command execution. |
| **Agent** | Each tool call requires approval (A=allow, D=deny, S=session-allow) |
| **Auto** | A classifier auto-approves safe operations; sensitive ones still ask |
| **YOLO** | Auto-approve within configured bounds. Dangerous commands still blocked. |

Customize per-tool rules in `~/.icode/config.yaml` and `hooks.yaml`; repeated blocks auto-downgrade to manual mode.

## MCP Integration

Both stdio and SSE transports are supported.

```yaml
mcp:
  - name: filesystem
    type: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
    enabled: true
```

MCP tools are injected as `mcp_<server>_<tool>` and usable directly in conversations; WorkBuddy users get automatic bridging of `~/.workbuddy/mcp.json`.

## Installation

### Package managers
```bash
# npm — cross-platform launcher, pulls the matching prebuilt binary
npm install -g icode

# Homebrew (macOS / Linux)
brew install ponygates/tap/icode

# Scoop (Windows)
scoop bucket add icode https://github.com/ponygates/icode
scoop install icode

# WinGet (Windows)
winget install PonyGates.iCode
```

> Every channel verifies against the same release `checksums.txt`. The npm and
> winget packages are generated at release time by `scripts/gen_npm_packages.sh`
> and `scripts/gen_winget_manifest.sh`; Homebrew/Scoop manifests live at
> `scripts/homebrew/icode.rb` and `scripts/scoop/icode.json`.

### One-line installer (Linux / macOS)
```bash
curl -fsSL https://raw.githubusercontent.com/ponygates/icode/master/install.sh | bash
```

### From Source
```bash
go install github.com/ponygates/icode@latest
```

### Pre-built Binaries
Download from [GitHub Releases](https://github.com/ponygates/icode/releases) — headless CLI covers win/linux/darwin/freebsd amd64+arm64; desktop builds are native per platform.

## Development

```bash
# Build & test everything
go build ./... && go vet ./... && go test ./...

# Full binary (CLI + desktop combined)
build.bat                    # Windows: frontend + Go in one step
build.bat --cli              # CLI only (no embedded frontend)

# Headless CLI (cross-compile, no CGO)
CGO_ENABLED=0 go build -tags nogui -o icode .

# Desktop frontend
cd desktop && npm install && npm run dev
npx tsc --noEmit && npm test # type-check + unit tests

# VS Code extension
cd vscode && npm run compile
```

## Roadmap

- [x] **0.1.0 baseline**: multi-provider access, cache-first optimization, 38 tools, three-surface experience, security & privacy, skill/MCP/RAG ecosystem (consolidating 53 historical releases — see [CHANGELOG](CHANGELOG.md))
- [x] **0.1.3 security hardening**: CSRF/same-origin guard, fetch SSRF blocking (`internal/netsec`), directory sandbox (AllowedPaths), registry locking, API-key redaction — plus per-launch Bearer token now required on all privileged mutating endpoints (shell/config/permission/update), loopback included; port file carries the token for first-party clients (desktop / VS Code)
- [ ] **Next**: mesh token rotation, splitting the large frontend components, more live provider integrations
- [ ] **Long term**: community skill marketplace, richer hooks coverage

## License

Apache-2.0 © 2025 iCode Contributors
