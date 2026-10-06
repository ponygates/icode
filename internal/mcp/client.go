// Package mcp implements the Model Context Protocol (MCP) client for iCode.
//
// MCP is an open protocol that standardizes how applications provide context to LLMs.
// It enables dynamic tool discovery and execution from external servers.
//
// Supported transports:
//   - stdio: spawns a child process and communicates via stdin/stdout (JSON-RPC)
//   - sse: HTTP-based Server-Sent Events transport
//
// Reference: https://modelcontextprotocol.io
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/netsec"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/xgo"
)

// Transport defines how the client communicates with an MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportSSE   Transport = "sse"
)

// ServerConfig defines the configuration for connecting to an MCP server.
type ServerConfig struct {
	Name    string            `json:"name" yaml:"name"`
	Type    Transport         `json:"type" yaml:"type"`
	Command string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args    []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env     []string          `json:"env,omitempty" yaml:"env,omitempty"`
	URL     string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Enabled bool              `json:"enabled" yaml:"enabled"`
}

// Client manages a connection to a single MCP server.
type Client struct {
	config    ServerConfig
	tools     []types.ToolDef
	resources []MCPResource

	mu     sync.RWMutex
	connMu sync.Mutex // lifecycle: Connect/Close/supervisor, never held over I/O
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cancel context.CancelFunc

	reqID   atomic.Int64
	pending map[int64]chan *jsonrpcResponse
	notify  chan *jsonrpcNotification

	// slug namespaces this server's tool names. It equals config.Name unless
	// the Pool had to disambiguate a collision (see Pool.Add), in which case
	// it differs — which is why every name mangling path uses slug and every
	// reverse lookup goes through toolByName rather than string trimming.
	slug string

	// toolByName maps the mangled name handed to the model back to the exact
	// name the server expects. Populated by DiscoverTools.
	toolByName map[string]string

	// dead is closed when the transport drops on its own (child exited, stream
	// EOF). closed is set only by an explicit Close, which must not restart.
	dead   chan struct{}
	closed bool

	// supervising marks a restart loop as running, so a double drop cannot
	// spawn two of them. baseCtx is the lifetime context Connect was given;
	// restarts are bounded by it, not by any single request's deadline.
	supervising bool
	baseCtx     context.Context

	// lifeCtx is detached from any caller's context and lives as long as the
	// client does. The child process and the supervisor are bound to it, never
	// to the context handed to Connect: boot code connects with a 20s deadline,
	// and os/exec kills a CommandContext child the moment that deadline expires
	// — which used to take every stdio server down seconds after startup.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	// onReconnect fires after a successful automatic restart so the Pool can
	// refresh the engine's tool registry.
	onReconnect func()

	// onNotification, when set, receives server-push notifications (method +
	// params) such as notifications/tools/list_changed. Called from the notify
	// pump goroutine; must be concurrency-safe.
	onNotification func(method string, params any)

	// SSE transport fields
	httpClient   *http.Client
	sseEndpoint  string // session-scoped endpoint URL for POST/GET
	sseSessionID string

	// oauth drives the HTTP authorization layer (401 challenge → PKCE code
	// flow → bearer injection → refresh). It is nil for stdio and for SSE
	// servers that were given a static Authorization header.
	oauth     *OAuthManager
	oauthOpts *oauthOptions

	// serverInfo / capabilities are what the server returned from initialize.
	// resources and prompts are optional parts of the protocol, so every call
	// for them checks capabilities first instead of timing out.
	serverInfo   map[string]any
	capabilities map[string]any
}

// MCPResource represents a resource exposed by the server.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// NewClient creates an MCP client for the given server config.
func NewClient(cfg ServerConfig) *Client {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &Client{
		config:     cfg,
		slug:       cfg.Name,
		pending:    make(map[int64]chan *jsonrpcResponse),
		notify:     make(chan *jsonrpcNotification, 64),
		dead:       make(chan struct{}),
		toolByName: make(map[string]string),
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
	}
}

// SetSlug namespaces this server's tool names. The Pool calls it when two
// configured servers would otherwise mangle to the same prefix. Safe to call
// only before Connect/DiscoverTools, which is how Pool.Add uses it.
func (c *Client) SetSlug(slug string) {
	c.mu.Lock()
	c.slug = slug
	c.mu.Unlock()
}

// SetOnReconnect wires a handler fired after an automatic restart succeeds and
// the catalog has been re-read, so the Pool can refresh the engine's registry.
func (c *Client) SetOnReconnect(fn func()) {
	c.mu.Lock()
	c.onReconnect = fn
	c.mu.Unlock()
}

// SetOnNotification wires a server-push notification handler (method + params).
// The notify pump goroutine is started lazily on first Connect.
func (c *Client) SetOnNotification(fn func(method string, params any)) {
	c.mu.Lock()
	c.onNotification = fn
	c.mu.Unlock()
}

// SetOAuthBrowser wires the function used to open the OAuth authorization URL
// in the platform browser. The desktop/server layers already own such a helper
// (rundll32 on Windows, open on macOS, xdg-open elsewhere); passing it here keeps
// this package free of its own platform switch. Without it the interactive flow
// prints the URL for a TTY user to open manually.
func (c *Client) SetOAuthBrowser(fn func(string) error) {
	if c.oauthOpts == nil {
		c.oauthOpts = &oauthOptions{}
	}
	c.oauthOpts.openBrowser = fn
}

// SetOAuthOptions injects the full OAuth configuration (store dir, clock,
// transport, validator). Production callers use SetOAuthBrowser; tests use this
// to point the whole layer at local httptest servers and a scratch directory.
func (c *Client) SetOAuthOptions(opt oauthOptions) {
	c.oauthOpts = &opt
}

// startNotifyPump drains the notify channel and dispatches to onNotification.
// It exits when the channel is closed (on Close).
func (c *Client) startNotifyPump() {
	go func() {
		for n := range c.notify {
			c.mu.RLock()
			fn := c.onNotification
			c.mu.RUnlock()
			if fn != nil {
				fn(n.Method, n.Params)
			}
		}
	}()
}

// Connect establishes the connection based on the transport type.
func (c *Client) Connect(ctx context.Context) error {
	// connMu serialises lifecycle transitions only and is never held across an
	// RPC round-trip. connectStdio used to call initialize while holding c.mu,
	// and stdioCall then took c.mu to register the pending id — so every stdio
	// server deadlocked on its very first request and no stdio MCP server could
	// finish connecting at all.
	c.connMu.Lock()
	defer c.connMu.Unlock()

	var err error
	switch c.config.Type {
	case TransportStdio:
		err = c.connectStdio(ctx)
	case TransportSSE:
		err = c.connectSSE(ctx)
	default:
		return fmt.Errorf("unsupported transport: %s", c.config.Type)
	}
	if err == nil {
		c.startNotifyPump()
	}
	return err
}

func (c *Client) connectStdio(ctx context.Context) error {
	if err := c.spawnStdio(); err != nil {
		return err
	}

	// Initialize the MCP session
	resp, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "iCode",
			"version": "0.1.0",
		},
	})
	if err != nil {
		c.teardownStdio()
		return fmt.Errorf("initialize: %w", err)
	}

	var initResult struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		c.teardownStdio()
		return fmt.Errorf("parse init result: %w", err)
	}
	c.setServerInfo(initResult.ServerInfo, initResult.Capabilities)

	// Send initialized notification
	c.sendNotification(ctx, "notifications/initialized", nil)

	return nil
}

// spawnStdio starts the server process and its reader. The process handle is
// swapped under c.mu; syscalls (Start, pipe setup) run outside the lock so no
// file-descriptor operation is ever issued while it is held.
func (c *Client) spawnStdio() error {
	// The process is bound to the client's lifetime context, not the caller's:
	// cancelling a connect deadline must not kill a healthy server.
	runCtx, cancel := context.WithCancel(c.lifeCtx)
	proc := executil.CommandContext(runCtx, c.config.Command, c.config.Args...)

	// Merge env vars: inherit parent environment, then overlay configured vars
	if len(c.config.Env) > 0 {
		proc.Env = append(os.Environ(), c.config.Env...)
	}

	stdin, err := proc.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := proc.Start(); err != nil {
		cancel()
		return fmt.Errorf("start server: %w", err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		if proc.Process != nil {
			_ = proc.Process.Kill()
		}
		return errors.New("mcp: client closed during connect")
	}
	// Fresh drop signal per connection: handleDrop closes it, and a restarted
	// connection must not look already-dead to its next reader.
	c.dead = make(chan struct{})
	c.baseCtx = c.lifeCtx
	c.cmd, c.stdin, c.stdout, c.cancel = proc, stdin, stdout, cancel
	c.mu.Unlock()

	// Start the JSON-RPC reader — it exits when ctx is cancelled or the pipe closes
	xgo.GoSafe("mcp.readLoop", c.readLoop)
	return nil
}

// teardownStdio stops a half-established connection (failed handshake) without
// tripping the restart supervisor.
func (c *Client) teardownStdio() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdownLocked()
}

func (c *Client) connectSSE(ctx context.Context) error {
	// No lock is held across the HTTP exchange — sseCall takes c.mu per
	// request, so a connect-time hold would stall every concurrent call for
	// the whole handshake.
	runCtx, cancel := context.WithCancel(c.lifeCtx)
	// Guarded transport: the SSE endpoint event tells us where to POST next, so
	// a hostile server could otherwise aim our requests at the cloud-metadata
	// plane. Loopback/private stay allowed — local MCP servers are the norm.
	client := netsec.GuardedClient(30*time.Second, false)

	// Step 1: POST to the server URL to initialize an SSE session
	initURL := c.config.URL
	c.setupOAuth(initURL)
	if c.oauth != nil {
		// Silent pass first: reuse or refresh a stored token without ever
		// opening a browser. Only a 401 below escalates to an interactive flow.
		_ = c.oauth.EnsureValid(runCtx)
	}
	const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"iCode","version":"0.1.0"}}}`
	resp, err := c.doWithAuth(runCtx, client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(runCtx, "POST", initURL, strings.NewReader(initBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		c.setAuthHeaders(req)
		return req, nil
	})
	if err != nil {
		cancel()
		return fmt.Errorf("init request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		cancel()
		return fmt.Errorf("init 返回 403：服务器拒绝了凭据，请为该 MCP 服务器配置正确的 Authorization 头或完成 OAuth 授权")
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		return fmt.Errorf("init returned status %d", resp.StatusCode)
	}

	// Step 2: Parse SSE stream to find the endpoint event
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	endpoint := ""

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType := strings.TrimPrefix(line, "event: ")
			if eventType == "endpoint" {
				// Next data line is the endpoint URL
				if scanner.Scan() {
					dataLine := scanner.Text()
					if strings.HasPrefix(dataLine, "data: ") {
						endpoint = strings.TrimPrefix(dataLine, "data: ")
						break
					}
				}
			}
		}
	}

	if endpoint == "" {
		cancel()
		return fmt.Errorf("no endpoint event received from SSE server")
	}

	// Resolve relative endpoint URLs against the base URL
	if !strings.HasPrefix(endpoint, "http") {
		base, err := url.Parse(initURL)
		if err != nil {
			cancel()
			return fmt.Errorf("parse base URL: %w", err)
		}
		rel, err := url.Parse(endpoint)
		if err != nil {
			cancel()
			return fmt.Errorf("parse endpoint URL: %w", err)
		}
		endpoint = base.ResolveReference(rel).String()
	}

	// The endpoint comes from the server, not the operator: it decides where
	// every later request of ours is POSTed. Guard it against the metadata
	// plane (169.254.0.0/16, fe80::/10) while still allowing a local server to
	// hand back a loopback path.
	if err := netsec.ValidateConfiguredURL(endpoint); err != nil {
		cancel()
		return fmt.Errorf("SSE server supplied an unusable endpoint: %w", err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		client.CloseIdleConnections()
		return errors.New("mcp: client closed during connect")
	}
	c.dead = make(chan struct{})
	c.baseCtx = c.lifeCtx
	c.cancel, c.httpClient, c.sseEndpoint = cancel, client, endpoint
	c.mu.Unlock()

	// Step 3: Start background GET listener for SSE events. The endpoint is
	// passed in rather than re-read from the struct, so the reader never races
	// with a reconnect swapping it.
	xgo.GoSafe("mcp.sseReadLoop", func() { c.sseReadLoop(runCtx, endpoint) })

	return nil
}

// setupOAuth builds the authorization layer for an SSE server. It is skipped
// when the operator pinned an Authorization header (static credentials win, and
// silently launching a browser behind their back would be wrong) and for stdio,
// which has no HTTP to authenticate.
func (c *Client) setupOAuth(resourceURL string) {
	if c.config.Type != TransportSSE {
		return
	}
	c.mu.RLock()
	static := c.config.Headers
	c.mu.RUnlock()
	if _, ok := headerInsensitive(static, "Authorization"); ok {
		return
	}
	opt := oauthOptions{}
	if c.oauthOpts != nil {
		opt = *c.oauthOpts
	}
	opt.server = c.config.Name
	if opt.resourceURL == "" {
		opt.resourceURL = resourceURL
	}
	c.oauth = newOAuthManager(opt)
}

// doWithAuth issues an HTTP request, and on a 401 runs the OAuth flow exactly
// once before a single retry. A second 401 after a fresh token is reported as a
// configuration error rather than looping — the requirement that authorization
// be triggered, never retried forever.
func (c *Client) doWithAuth(ctx context.Context, client *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	do := func() (*http.Response, error) {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	resp, err := do()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	if c.oauth == nil {
		return resp, nil
	}
	ch := parseChallengeFromResponse(resp)
	resp.Body.Close()
	if _, err := c.oauth.Authorize(ctx, ch); err != nil {
		return nil, fmt.Errorf("MCP 服务器 %q 需要 OAuth 授权，但未完成：%v", c.config.Name, err)
	}
	resp, err = do()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, fmt.Errorf("MCP 服务器 %q 在授权后仍返回 401，请检查其 OAuth 配置或改用静态 Authorization 头", c.config.Name)
	}
	return resp, nil
}

// headerInsensitive looks up a header key ignoring case, since operators write
// "authorization" and "Authorization" interchangeably in config.
func headerInsensitive(h map[string]string, key string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// sseReadLoop keeps the SSE event stream open and dispatches JSON-RPC
// messages. endpoint is passed in so this reader is bound to the endpoint it
// was started with, even if a reconnect installs a new one.
func (c *Client) sseReadLoop(ctx context.Context, endpoint string) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		c.setAuthHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}

		// A closed or errored stream ends readSSEResponse immediately. Without a
		// pause here the loop re-GETs in a tight spin, hammering a server that is
		// already down — the historical behaviour was a 2 s sleep only on the
		// request-failure branch, not on the stream-ending one.
		c.readSSEResponse(resp.Body)
		resp.Body.Close()

		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

// readSSEResponse parses an SSE stream and dispatches JSON-RPC messages.
func (c *Client) readSSEResponse(body io.Reader) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventType, data string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		} else if line == "" {
			// End of event — dispatch
			if eventType == "message" && data != "" {
				c.dispatchSSEMessage(data)
			}
			eventType = ""
			data = ""
		}
	}
}

// dispatchSSEMessage parses a JSON-RPC message from SSE and routes it.
func (c *Client) dispatchSSEMessage(raw string) {
	data := []byte(raw)

	// Check if it's a notification (no "id" field)
	var peek struct {
		ID *int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return
	}

	if peek.ID == nil {
		var notif jsonrpcNotification
		if err := json.Unmarshal(data, &notif); err == nil {
			select {
			case c.notify <- &notif:
			default:
			}
		}
		return
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return
	}

	c.mu.RLock()
	ch, ok := c.pending[resp.ID]
	c.mu.RUnlock()
	if ok {
		ch <- &resp
	}
}

// sseCall sends a JSON-RPC request via HTTP POST to the SSE endpoint.
func (c *Client) sseCall(ctx context.Context, method string, params any) (*jsonrpcResponse, error) {
	id := c.reqID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan *jsonrpcResponse, 1)
	c.mu.Lock()
	if c.closed || c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (%s)", errNotConnected, c.config.Name)
	}
	select {
	case <-c.dead:
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (%s)", errNotConnected, c.config.Name)
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	resp, err := c.doWithAuth(ctx, c.httpClient, func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.sseEndpoint, strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.setAuthHeaders(httpReq)
		return httpReq, nil
	})
	if err != nil {
		return nil, fmt.Errorf("post request: %w", err)
	}
	defer resp.Body.Close()

	// If the server responds directly (non-SSE), parse the JSON-RPC response
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		var directResp jsonrpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&directResp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if directResp.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", directResp.Error.Code, directResp.Error.Message)
		}
		return &directResp, nil
	}

	// Otherwise, wait for the response to arrive via the SSE stream
	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", r.Error.Code, r.Error.Message)
		}
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("MCP call timeout for %s", method)
	}
}

// DiscoverTools fetches the tool list from the server.
func (c *Client) DiscoverTools(ctx context.Context) ([]types.ToolDef, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	defs := make([]types.ToolDef, len(result.Tools))
	byName := make(map[string]string, len(result.Tools))
	for i, t := range result.Tools {
		mangled := mcpToolName(c.slug, t.Name)
		// Two servers can legitimately mangle to the same name (server "a" with
		// tool "b_c", server "a_b" with tool "c"), and the registry would let the
		// later one silently replace the earlier. The Pool owns that collision;
		// here we only keep the exact server-side name so the call can be routed
		// back without reverse-parsing the mangled form.
		defs[i] = types.ToolDef{
			Name:        mangled,
			Description: fmt.Sprintf("[MCP:%s] %s", c.slug, t.Description),
			Parameters:  t.InputSchema,
			ServerName:  c.slug,
		}
		byName[mangled] = t.Name
	}

	c.mu.Lock()
	c.tools = defs
	c.toolByName = byName
	c.mu.Unlock()

	return defs, nil
}

// DiscoverResources fetches available resources from the server.
func (c *Client) DiscoverResources(ctx context.Context) ([]MCPResource, error) {
	resp, err := c.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	var result struct {
		Resources []MCPResource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse resources: %w", err)
	}

	c.mu.Lock()
	c.resources = result.Resources
	c.mu.Unlock()

	return c.resources, nil
}

// CallTool invokes a named tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*types.ToolResult, error) {
	// Route by the exact server-side name recorded at discovery. Reverse-parsing
	// the mangled name cannot work: the server slug and the tool name are both
	// free-form and may contain underscores.
	actualName := c.serverToolName(name)

	resp, err := c.call(ctx, "tools/call", map[string]any{
		"name":      actualName,
		"arguments": args,
	})
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("MCP tool error: %v", err),
		}, nil
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("parse tool result: %v", err),
		}, nil
	}

	var content strings.Builder
	for _, c := range result.Content {
		content.WriteString(c.Text)
	}

	return &types.ToolResult{
		Success: !result.IsError,
		Content: content.String(),
	}, nil
}

// Tools returns the cached tool list.
func (c *Client) Tools() []types.ToolDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tools
}

// RefreshTools re-discovers the server's tool list and replaces the cached
// catalog (Reasonix parity: called on notifications/tools/list_changed).
func (c *Client) RefreshTools(ctx context.Context) error {
	defs, err := c.DiscoverTools(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.tools = defs
	c.mu.Unlock()
	return nil
}

// ============================================================================
// JSON-RPC protocol
// ============================================================================

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonrpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func (c *Client) call(ctx context.Context, method string, params any) (*jsonrpcResponse, error) {
	// Dispatch by transport type
	if c.config.Type == TransportSSE {
		return c.sseCall(ctx, method, params)
	}
	return c.stdioCall(ctx, method, params)
}

func (c *Client) stdioCall(ctx context.Context, method string, params any) (*jsonrpcResponse, error) {
	id := c.reqID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan *jsonrpcResponse, 1)
	c.mu.Lock()
	if c.closed || c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (%s)", errNotConnected, c.config.Name)
	}
	select {
	case <-c.dead:
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (%s)", errNotConnected, c.config.Name)
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("MCP call timeout for %s", method)
	}
}

func (c *Client) sendNotification(ctx context.Context, method string, params any) {
	notif := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(notif)
	// Notifications have no ID per JSON-RPC 2.0 spec, so no pending cleanup needed.
	if c.config.Type == TransportSSE && c.sseEndpoint != "" {
		req, err := http.NewRequestWithContext(ctx, "POST", c.sseEndpoint, strings.NewReader(string(data)))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		c.setAuthHeaders(req)
		resp, err := c.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return
	}
	_, _ = c.stdin.Write(append(data, '\n'))
}

func (c *Client) readLoop() {
	c.mu.RLock()
	stdout := c.stdout
	c.mu.RUnlock()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Check if it's a notification (no "id" field)
		var peek struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			continue
		}

		if peek.ID == nil {
			var notif jsonrpcNotification
			if err := json.Unmarshal(line, &notif); err == nil {
				select {
				case c.notify <- &notif:
				default:
				}
			}
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		c.mu.RLock()
		ch, ok := c.pending[resp.ID]
		c.mu.RUnlock()
		if ok {
			select {
			case ch <- &resp:
			default:
			}
		}
	}

	// The pipe reached EOF: the child exited or crashed. Without this the
	// client kept handing out 30-second timeouts forever.
	cause := scanner.Err()
	if cause == nil {
		cause = errors.New("stdout closed")
	}
	// Snapshot the handle: a restart may already have swapped c.cmd, and Wait
	// on the wrong process would report nothing (or panic on a nil cmd).
	c.mu.RLock()
	proc := c.cmd
	c.mu.RUnlock()
	if proc != nil && proc.Process != nil {
		if err := proc.Wait(); err != nil && cause != nil {
			// The child's exit status is the actual cause; keep it ahead of the
			// generic "stdout closed".
			cause = fmt.Errorf("%v; %w", err, cause)
		}
	}
	c.handleDrop(cause)
}
