package mcp

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/ponygates/icode/internal/types"
)

// Pool manages multiple MCP server connections, providing unified tool
// discovery and execution across all connected servers.
type Pool struct {
	mu      sync.RWMutex
	clients map[string]*Client
	// slugs records which tool-name namespaces are taken, so two servers whose
	// names collapse to the same component (e.g. "my-api" and "my_api", or any
	// two names written in Chinese) cannot both publish `mcp_<same>_<tool>`.
	slugs map[string]bool
	// onToolsChanged, when set, fires after a connected server signals
	// notifications/tools/list_changed and its catalog is re-discovered.
	onToolsChanged func(clientName string)
}

// NewPool creates an empty MCP client pool.
func NewPool() *Pool {
	return &Pool{
		clients: make(map[string]*Client),
		slugs:   make(map[string]bool),
	}
}

// SetOnToolsChanged wires a callback fired when a server's tool list changes
// (notifications/tools/list_changed). The caller (server layer) refreshes the
// engine's MCP tool registry from it.
func (p *Pool) SetOnToolsChanged(fn func(clientName string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onToolsChanged = fn
}

// claimSlugLocked reserves a tool-name namespace derived from name, appending a
// numeric suffix on collision. Caller holds p.mu.
func (p *Pool) claimSlugLocked(name string) string {
	base := sanitizeNameComponent(name)
	slug := base
	for i := 2; p.slugs[slug]; i++ {
		slug = base + "_" + strconv.Itoa(i)
	}
	p.slugs[slug] = true
	return slug
}

// releaseSlug frees a namespace claimed by claimSlugLocked. Caller holds p.mu.
func (p *Pool) releaseSlug(slug string) {
	delete(p.slugs, slug)
}

// notifyToolsChanged fires the refresh callback for a server, if wired.
func (p *Pool) notifyToolsChanged(name string) {
	p.mu.RLock()
	cb := p.onToolsChanged
	p.mu.RUnlock()
	if cb != nil {
		cb(name)
	}
}

// Add registers and connects to a new MCP server.
func (p *Pool) Add(ctx context.Context, cfg ServerConfig) error {
	if !cfg.Enabled {
		return nil
	}

	p.mu.Lock()
	if _, exists := p.clients[cfg.Name]; exists {
		p.mu.Unlock()
		return fmt.Errorf("MCP server %q already registered", cfg.Name)
	}
	slug := p.claimSlugLocked(cfg.Name)
	p.mu.Unlock()

	client := NewClient(cfg)
	client.SetSlug(slug)

	fail := func(err error) error {
		client.Close()
		p.mu.Lock()
		p.releaseSlug(slug)
		p.mu.Unlock()
		return err
	}

	if err := client.Connect(ctx); err != nil {
		return fail(fmt.Errorf("connect to %s: %w", cfg.Name, err))
	}

	// Auto-refresh the catalog when the server signals a tool-list change
	// (Reasonix parity: notifications/tools/list_changed).
	client.SetOnNotification(func(method string, _ any) {
		if method != "notifications/tools/list_changed" {
			return
		}
		_ = client.RefreshTools(context.Background())
		p.notifyToolsChanged(cfg.Name)
	})

	// A supervisor restart re-discovers tools on its own; the engine's registry
	// still holds the pre-crash set, so tell it to reload.
	client.SetOnReconnect(func() { p.notifyToolsChanged(cfg.Name) })

	// Discover tools immediately
	if _, err := client.DiscoverTools(ctx); err != nil {
		return fail(fmt.Errorf("discover tools for %s: %w", cfg.Name, err))
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

// AllToolsByServer returns every discovered tool grouped by owning server
// name so callers can apply per-server policy (e.g. trust modes) by tool.
func (p *Pool) AllToolsByServer() map[string][]types.ToolDef {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make(map[string][]types.ToolDef)
	for name, client := range p.clients {
		out[name] = append(out[name], client.Tools()...)
	}
	return out
}

// ToolsByServer returns the tools published by one server, or nil when that
// server is not registered. Prefer this over counting `mcp_<name>_` prefixes:
// a colliding server is namespaced under a suffixed slug, so the prefix no
// longer equals the config name.
func (p *Pool) ToolsByServer(name string) []types.ToolDef {
	p.mu.RLock()
	client, ok := p.clients[name]
	p.mu.RUnlock()
	if !ok {
		return nil
	}
	return client.Tools()
}

// Execute routes a tool call to the appropriate MCP server.
func (p *Pool) Execute(ctx context.Context, name string, args map[string]any) (*types.ToolResult, error) {
	// Snapshot the clients, then release p.mu: CallTool is a network/pipe
	// round-trip that can take minutes, and holding the read lock across it
	// blocks CloseAll and Remove for its whole duration.
	p.mu.RLock()
	clients := make([]*Client, 0, len(p.clients))
	for _, client := range p.clients {
		clients = append(clients, client)
	}
	p.mu.RUnlock()

	for _, client := range clients {
		if client.OwnsTool(name) {
			return client.CallTool(ctx, name, args)
		}
	}

	return nil, fmt.Errorf("MCP tool %q not found", name)
}

// Supports reports whether a connected server declared a capability
// (tools/resources/prompts). Cheap: reads the cached initialize result.
func (p *Pool) Supports(name, capability string) bool {
	p.mu.RLock()
	client, ok := p.clients[name]
	p.mu.RUnlock()
	return ok && client.Supports(capability)
}

// CloseAll shuts down all MCP server connections.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, client := range p.clients {
		clients = append(clients, client)
	}
	p.clients = make(map[string]*Client)
	p.slugs = make(map[string]bool)
	p.mu.Unlock()

	for _, client := range clients {
		client.Close()
	}
}

// Remove disconnects and removes a single MCP server by name.
func (p *Pool) Remove(name string) {
	p.mu.Lock()
	c, ok := p.clients[name]
	if !ok {
		p.mu.Unlock()
		return
	}
	slug := c.Slug()
	delete(p.clients, name)
	p.releaseSlug(slug)
	p.mu.Unlock()

	// Close outside p.mu: a restart in flight holds the client's lifecycle lock
	// and then wants p.mu to report its refreshed catalog. Closing under the
	// write lock would deadlock against it.
	c.Close()
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
