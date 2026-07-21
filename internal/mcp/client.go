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
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
)

// Transport defines how the client communicates with an MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportSSE   Transport = "sse"
)

// ServerConfig defines the configuration for connecting to an MCP server.
type ServerConfig struct {
	Name    string    `json:"name" yaml:"name"`
	Type    Transport `json:"type" yaml:"type"`
	Command string    `json:"command,omitempty" yaml:"command,omitempty"`
	Args    []string  `json:"args,omitempty" yaml:"args,omitempty"`
	Env     []string  `json:"env,omitempty" yaml:"env,omitempty"`
	URL     string    `json:"url,omitempty" yaml:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Enabled bool      `json:"enabled" yaml:"enabled"`
}

// Client manages a connection to a single MCP server.
type Client struct {
	config   ServerConfig
	tools    []types.ToolDef
	resources []MCPResource

	mu     sync.RWMutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cancel context.CancelFunc

	reqID   atomic.Int64
	pending map[int64]chan *jsonrpcResponse
	notify  chan *jsonrpcNotification
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
	return &Client{
		config:  cfg,
		pending: make(map[int64]chan *jsonrpcResponse),
		notify:  make(chan *jsonrpcNotification, 64),
	}
}

// Connect establishes the connection based on the transport type.
func (c *Client) Connect(ctx context.Context) error {
	switch c.config.Type {
	case TransportStdio:
		return c.connectStdio(ctx)
	case TransportSSE:
		return c.connectSSE(ctx)
	default:
		return fmt.Errorf("unsupported transport: %s", c.config.Type)
	}
}

func (c *Client) connectStdio(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Create cancellable context so Close() can clean up the subprocess
	ctx, c.cancel = context.WithCancel(ctx)
	c.cmd = executil.CommandContext(ctx, c.config.Command, c.config.Args...)

	// Merge env vars: inherit parent environment, then overlay configured vars
	if len(c.config.Env) > 0 {
		c.cmd.Env = append(os.Environ(), c.config.Env...)
	}

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	// Start the JSON-RPC reader — it exits when ctx is cancelled or the pipe closes
	go c.readLoop()

	// Initialize the MCP session
	resp, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "iCode",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	var initResult struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		return fmt.Errorf("parse init result: %w", err)
	}

	// Send initialized notification
	c.sendNotification(ctx, "notifications/initialized", nil)

	return nil
}

func (c *Client) connectSSE(ctx context.Context) error {
	// Placeholder: SSE transport will be implemented in a future iteration
	return fmt.Errorf("SSE transport not yet implemented")
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

	c.mu.Lock()
	c.tools = make([]types.ToolDef, len(result.Tools))
	for i, t := range result.Tools {
		c.tools[i] = types.ToolDef{
			Name:        fmt.Sprintf("mcp_%s_%s", c.config.Name, t.Name),
			Description: fmt.Sprintf("[MCP:%s] %s", c.config.Name, t.Description),
			Parameters:  t.InputSchema,
		}
	}
	c.mu.Unlock()

	return c.tools, nil
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
	// Strip the mcp_<server>_ prefix
	actualName := strings.TrimPrefix(name, fmt.Sprintf("mcp_%s_", c.config.Name))

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

// Close terminates the server connection and cleans up all resources.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel the context first — kills the subprocess via CommandContext.
	if c.cancel != nil {
		c.cancel()
	}
	// Unblock any goroutines waiting on pending RPC responses.
	for id, ch := range c.pending {
		ch <- &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &jsonrpcError{
				Code:    -1,
				Message: "client closed",
			},
		}
		delete(c.pending, id)
	}
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
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
	c.stdin.Write(append(data, '\n'))
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
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
			ch <- &resp
		}
	}
}

// ============================================================================
// Pool — manages multiple MCP clients
// ============================================================================

// Pool manages multiple MCP server connections, providing unified tool
// discovery and execution across all connected servers.
type Pool struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewPool creates an empty MCP client pool.
func NewPool() *Pool {
	return &Pool{
		clients: make(map[string]*Client),
	}
}

// Add registers and connects to a new MCP server.
func (p *Pool) Add(ctx context.Context, cfg ServerConfig) error {
	if !cfg.Enabled {
		return nil
	}

	client := NewClient(cfg)
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Name, err)
	}

	// Discover tools immediately
	if _, err := client.DiscoverTools(ctx); err != nil {
		client.Close()
		return fmt.Errorf("discover tools for %s: %w", cfg.Name, err)
	}

	p.mu.Lock()
	p.clients[cfg.Name] = client
	p.mu.Unlock()

	return nil
}

// AllTools returns all tools from all connected MCP servers.
func (p *Pool) AllTools() []types.ToolDef {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var all []types.ToolDef
	for _, client := range p.clients {
		all = append(all, client.Tools()...)
	}
	return all
}

// Execute routes a tool call to the appropriate MCP server.
func (p *Pool) Execute(ctx context.Context, name string, args map[string]any) (*types.ToolResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, client := range p.clients {
		for _, t := range client.Tools() {
			if t.Name == name {
				return client.CallTool(ctx, name, args)
			}
		}
	}

	return nil, fmt.Errorf("MCP tool %q not found", name)
}

// CloseAll shuts down all MCP server connections.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, client := range p.clients {
		client.Close()
	}
	p.clients = make(map[string]*Client)
}

// Remove disconnects and removes a single MCP server by name.
func (p *Pool) Remove(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.clients[name]; ok {
		c.Close()
		delete(p.clients, name)
	}
}

// Has reports whether a server with the given name is currently connected.
func (p *Pool) Has(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.clients[name]
	return ok
}

// Count returns the number of connected MCP servers.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.clients)
}
