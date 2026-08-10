package tool

import (
	"context"

	"github.com/ponygates/icode/internal/types"
)

// ctxSessionKey carries the current chat session ID through the context that
// wraps every tool.Execute call. Session-scoped tools (TodoWrite, Task,
// checkpoint hooks, ...) read this to know which session's state to touch,
// avoiding a factory-per-session wire-up.
type ctxSessionKey struct{}

// WithSessionID returns ctx with sessionID attached. The engine calls this
// right before dispatching a tool call.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxSessionKey{}, sessionID)
}

// SessionIDFromContext returns the session ID attached with WithSessionID,
// or "" if the caller did not supply one (tests, out-of-band tool calls).
func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxSessionKey{}).(string); ok {
		return v
	}
	return ""
}

// ctxProgressKey carries the optional incremental-output callback attached
// by the engine for the current tool turn. Tools (bash) that support live
// streaming read it and forward chunks; a nil value means "no live UI".
type ctxProgressKey struct{}

// WithProgress attaches a ToolProgressFunc to ctx. The engine sets it before
// dispatching a turn's tools and it stays valid for that turn only.
func WithProgress(ctx context.Context, fn types.ToolProgressFunc) context.Context {
	return context.WithValue(ctx, ctxProgressKey{}, fn)
}

// ProgressFromContext returns the live-output callback attached with
// WithProgress, or nil when none is set.
func ProgressFromContext(ctx context.Context) types.ToolProgressFunc {
	if fn, ok := ctx.Value(ctxProgressKey{}).(types.ToolProgressFunc); ok {
		return fn
	}
	return nil
}
