package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// reconnectBackoff is the first restart delay; it doubles per attempt.
const reconnectBackoff = time.Second

// maxReconnectAttempts bounds the supervisor so a server that is configured but
// permanently broken (bad command, crashing binary) cannot restart forever.
const maxReconnectAttempts = 5

// serverToolName maps a model-facing tool name back to the exact name the
// server expects. Falls back to the input so callers that pass a bare server
// tool name (no mcp_ prefix) keep working.
func (c *Client) serverToolName(mangled string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if name, ok := c.toolByName[mangled]; ok {
		return name
	}
	return mangled
}

// OwnsTool reports whether this server published the given model-facing name.
func (c *Client) OwnsTool(mangled string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.toolByName[mangled]
	return ok
}

// Slug returns the namespace this server's tool names were built under.
func (c *Client) Slug() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.slug
}

// IsAlive reports whether the transport is currently usable.
func (c *Client) IsAlive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return false
	}
	select {
	case <-c.dead:
		return false
	default:
		return true
	}
}

// handleDrop runs when the transport ends on its own: the stdio child exited,
// or its stdout reached EOF. Every in-flight call is released with an error
// (they used to sit and wait out their full timeout while nothing could ever
// answer them) and, for stdio, a bounded restart is scheduled.
//
// Explicit Close() sets closed first, so a deliberate shutdown never restarts.
func (c *Client) handleDrop(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	alreadyDead := false
	select {
	case <-c.dead:
		alreadyDead = true
	default:
		close(c.dead)
	}
	pending := c.pending
	c.pending = make(map[int64]chan *jsonrpcResponse)
	transport, name := c.config.Type, c.config.Name
	c.mu.Unlock()

	if alreadyDead {
		return
	}

	for id, ch := range pending {
		select {
		case ch <- &jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: -1, Message: "mcp server disconnected"}}:
		default:
		}
	}
	log.Printf("[mcp] server %q disconnected: %v", name, cause)

	if transport != TransportStdio {
		// SSE reconnects inside sseReadLoop, which already retries with backoff.
		return
	}
	c.supervise(cause)
}

// supervise restarts a dropped stdio server with exponential backoff. It is
// idempotent: a second drop while one restart is in flight does nothing.
func (c *Client) supervise(_ error) {
	c.mu.Lock()
	if c.supervising || c.closed {
		c.mu.Unlock()
		return
	}
	c.supervising = true
	baseCtx := c.baseCtx
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.supervising = false
		c.mu.Unlock()
	}()

	if baseCtx == nil {
		return
	}

	delay := reconnectBackoff
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		select {
		case <-baseCtx.Done():
			return
		case <-time.After(delay):
		}

		c.mu.RLock()
		closed := c.closed
		c.mu.RUnlock()
		if closed {
			return
		}

		if err := c.reconnect(baseCtx); err != nil {
			log.Printf("[mcp] restart %d/%d failed: %v", attempt, maxReconnectAttempts, err)
			delay = minDuration(delay*2, 30*time.Second)
			continue
		}
		log.Printf("[mcp] server %q restarted after %d attempt(s)", c.config.Name, attempt)
		return
	}
	log.Printf("[mcp] server %q gave up after %d restart attempts — check its command with /mcp", c.config.Name, maxReconnectAttempts)
}

// reconnect re-establishes the session and refreshes the catalog. It must not
// go through Connect, which would start a second notification pump.
func (c *Client) reconnect(ctx context.Context) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if err := c.connectStdio(ctx); err != nil {
		return err
	}
	if _, err := c.DiscoverTools(ctx); err != nil {
		return fmt.Errorf("re-discover tools: %w", err)
	}

	c.mu.RLock()
	fn := c.onReconnect
	c.mu.RUnlock()
	if fn != nil {
		fn()
	}
	return nil
}

// shutdownLocked releases the transport. Caller holds c.mu.
func (c *Client) shutdownLocked() {
	c.closed = true
	select {
	case <-c.dead:
	default:
		close(c.dead)
	}
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	// Cancelling the lifetime context is what makes the supervisor stop waiting
	// and what kills the child process through CommandContext.
	if c.lifeCancel != nil {
		c.lifeCancel()
	}
	for id, ch := range c.pending {
		select {
		case ch <- &jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: -1, Message: "client closed"}}:
		default:
		}
	}
	c.pending = make(map[int64]chan *jsonrpcResponse)

	if c.stdin != nil {
		nilCloser(c.stdin)
		c.stdin = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// Close terminates the server connection and stops any restart. Pending calls
// are released rather than left hanging.
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.shutdownLocked()
	return nil
}

var errDropped = errors.New("mcp: connection dropped")
