# iCode

**Multi-Model AI Coding Agent** — CLI + Desktop, native Chinese support, cache-first token optimization.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev)

## Features

- 🔌 **50+ LLM Providers** — DeepSeek, Zhipu, Kimi, OpenRouter, Anthropic, and more, with one-click model update
- 💰 **Cache-First Token Optimization** — Up to 94% token savings via immutable prefix + append-only log architecture
- 🌐 **Native zh-CN / zh-TW / en** — Full Chinese interface with simplified/traditional toggle
- 🖥️ **CLI (TUI) + Electron Desktop** — Consistent dual experience
- 🛠️ **Built-in Tools** — Bash, File Read/Write, Grep, Glob, LS, Fetch, MCP protocol
- 📊 **Coding Plan / Token Plan** — Choose between performance and cost-optimized modes
- 🆓 **Free Tier Support** — Built-in free models from OpenRouter, Zhipu GLM-4-Flash, etc.

## Quick Start

```bash
# Install from source
git clone https://github.com/ponygates/icode.git
cd icode
go build -o icode .

# Configure your API key
./icode auth set --provider deepseek --key your-api-key

# Start interactive chat
./icode chat

# Execute a single prompt
./icode exec -p "Explain this codebase"
```

## Desktop App

```bash
cd desktop
npm install
npm run dev
```

## Supported Providers (Phase 1)

| Provider | Models | Plan |
|----------|--------|------|
| DeepSeek | V3, R1 | Coding / Reasoning |
| Zhipu (智谱) | GLM-4-Plus, GLM-4-Flash | Coding / Free |
| Kimi (月之暗面) | Moonshot v1 8K/128K | Coding / Token |
| OpenRouter | Auto, Free, Claude, GPT-4o, Gemini | Auto / Free / Pay-per-token |

## Architecture

```
CLI (Go/Cobra) + Electron Desktop (React/TypeScript)
         │
    ┌────┴────┐
    │  API Server (Go)  │  ← JSON-RPC over HTTP/WS
    └────┬────┘
         │
    ┌────┴────────────────────────┐
    │  Application Core           │
    │  Session → Conversation     │
    │  TokenOpt → Permission      │
    └────┬────────────────────────┘
         │
    ┌────┴────────────────────────┐
    │  LLM Provider Layer         │
    │  DeepSeek | Zhipu | Kimi    │
    │  OpenRouter | ...           │
    └─────────────────────────────┘
```

## Roadmap

- [x] Phase 1: Project skeleton, core interfaces, config, i18n
- [ ] Phase 2: LLM integration, streaming chat, tool execution
- [ ] Phase 3: Token optimizer, TUI, MCP, permission system
- [ ] Phase 4: Electron desktop, auto-update, VS Code extension
- [ ] Phase 5: CI/CD, documentation, v0.1.0 release

## Development

```bash
# Run Go tests
go test ./...

# Lint
golangci-lint run

# Build for current platform
go build -o icode .

# Build Desktop
cd desktop && npm run dist
```

## License

Apache-2.0 © 2025 iCode Contributors
