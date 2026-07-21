package tool

import "context"

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
