# iCode — Project Context

## Project Description

iCode is a multi-model AI coding agent that runs in the terminal and on the desktop.
It supports 9 LLM providers and 21 models out of the box, with a cache-first token
optimization architecture that delivers up to 94% token savings on supported providers.
Built in Go with cobra, it also ships a desktop app (Electron + web UI).

## Build Commands

- `go build -o icode .` — Build the CLI binary.
- `go build ./...` — Compile all packages without emitting binaries.
- `go test ./...` — Run the test suite.
- `go run . doctor` — Check system status and configuration.
- `go run . chat` — Start an interactive session.
- `go run . exec -p "<prompt>"` — Run a single prompt non-interactively.

Desktop app (separate toolchain):

- `cd desktop && npm install` — Install desktop app dependencies.
- `cd desktop && npm run dev` — Run the desktop app in dev mode.

## Architecture Overview

```
main.go                  Entry point — wires providers, session store, engine
cmd/                     Cobra CLI commands (root, chat, exec, auth, doctor)
internal/
  app/                   Application bootstrap and lifecycle
  config/                Configuration loading + i18n
  core/
    context/             ICODE.md project context loader
    conversation/        Agentic conversation loop (Engine)
    permission/          Permission gate (Plan / Agent / YOLO modes)
    session/             Session persistence (SQLite-backed)
    tool/                Built-in tool registry (read/write/bash/grep/glob)
  db/                    SQLite store
  llm/
    provider/            Provider implementations (anthropic, deepseek,
      <name>/            zhipu, kimi, volcengine, tencent, huawei, scnet,
      registry.go        openrouter, openai_compat) + registry
    tokenopt/            Cache-first token optimizer (immutable prefix,
                         append-only log, smart compaction)
  mcp/                   MCP (Model Context Protocol) JSON-RPC stdio client
  server/                HTTP server mode
  tui/                   Terminal UI
  types/                 Shared types (Message, ChatRequest, Provider, ...)
pkg/
  modelupdate/           One-click model list update mechanism
desktop/                 Electron desktop app + web UI
configs/                 Default config files
```

Key design principles:

- **Cache-first**: system prompt + tool defs form an immutable prefix kept stable
  across turns so providers can reuse KV cache entries. Conversation messages are
  append-only; older messages are summarized into the prefix on overflow.
- **Multi-provider**: every provider implements the `types.Provider` interface and
  is registered in `internal/llm/provider/registry.go`.
- **Agentic loop**: `conversation.Engine` streams events (text / tool_use / done /
  error), executes tool calls inline, and recurses up to 10 tool rounds per turn.

## Conventions

- Go 1.26+ module path: `github.com/ponygates/icode`
- Package names match their directory name (e.g. `conversation`, `tokenopt`).
- Public types/functions use PascalCase; unexported helpers use camelCase.
- All file reads/writes and command execution go through the tool registry in
  `internal/core/tool` so the permission gate can approve/deny each action.
- Prefer editing existing files over creating new ones; never create documentation
  files unless explicitly requested.
- When referencing code, use the `file_path:line_number` pattern.
- On Windows use forward slashes in Bash commands; backslashes are reserved for
  Go strings and JSON paths.
- Keep responses concise and direct; lead with the answer, not the reasoning.
