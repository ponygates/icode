package mcp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// errNotConnected is returned by calls that arrive while the transport is down
// — before the supervisor has re-established it, or after an explicit Close.
var errNotConnected = errors.New("mcp: server not connected")

// setAuthHeaders applies the configured static headers, then — when the server
// is on the OAuth path and no explicit Authorization was configured — stamps the
// bearer token from the authorization layer. It reads only the cached token and
// never performs I/O, so it is safe to call from the SSE reader goroutine.
func (c *Client) setAuthHeaders(req *http.Request) {
	c.mu.RLock()
	headers := c.config.Headers
	oauth := c.oauth
	c.mu.RUnlock()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if oauth != nil && req.Header.Get("Authorization") == "" {
		if tok := oauth.bearerToken(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
}

// mcpToolName namespaces a server's tool for the model-facing registry.
// Non-alphanumeric characters are collapsed to underscores so a tool called
// "do.it!" or "a-b" cannot produce a name the registry cannot round-trip.
func mcpToolName(slug, tool string) string {
	return fmt.Sprintf("mcp_%s_%s", sanitizeNameComponent(slug), sanitizeNameComponent(tool))
}

func sanitizeNameComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "server"
	}
	var b strings.Builder
	b.Grow(len(s))
	lastUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		// Every rune was a separator — a Chinese or emoji server name. Without a
		// fallback these servers would all publish `mcp__<tool>` and collide.
		return "server"
	}
	return out
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func nilCloser(w io.Closer) {
	if w != nil {
		_ = w.Close()
	}
}
