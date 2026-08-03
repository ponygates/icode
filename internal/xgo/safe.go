// Package xgo provides small concurrency helpers shared across internal
// packages: panic-safe goroutine launchers and common timeouts.
package xgo

import (
	"log"
	"runtime/debug"
)

// Common timeouts for operations that historically used context.Background()
// with no deadline and could block indefinitely. Centralised here so they
// are easy to audit and tune. Prefer passing an explicit timeout per call
// site over relying on these defaults, but use these when a default is the
// pragmatic choice.
const (
	// MCPAddTimeout is the cap for connecting a single MCP server.
	MCPAddTimeout = 20 // seconds
	// LSPStartTimeout caps LSP server initialize.
	LSPStartTimeout = 30
	// ToolExecTimeout is the fallback cap for tool execution when no per-tool
	// timeout is configured.
	ToolExecTimeout = 120
	// ServerShutdownTimeout caps graceful HTTP server shutdown.
	ServerShutdownTimeout = 15
	// GitOpTimeout caps git init/config/checkpoint operations.
	GitOpTimeout = 30
)

// GoSafe launches a goroutine that recovers from any panic, logging the
// stack trace and the supplied name so a single panic in a background loop
// (MCP read loop, LSP reader, bgtask waiter, etc.) does not silently kill
// the connection or the whole process.
//
// Usage:
//
//	xgo.GoSafe("mcp.readLoop", func() { c.readLoop() })
//
// The recovered panic is logged via the standard logger; the goroutine then
// exits (it does not re-panic) so the surrounding process keeps running.
func GoSafe(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("goroutine %q panic recovered: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

// GoSafeCtx is like GoSafe but for the common pattern where the goroutine
// should be cancellable via a context. The fn receives the already-derived
// context; cancellation handling is the caller's responsibility inside fn.
// Kept as a separate helper to keep GoSafe dependency-free.
func GoSafeCtx(name string, fn func()) { GoSafe(name, fn) }
