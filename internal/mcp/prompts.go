package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCPPrompt is a prompt template exposed by a server (MCP `prompts/list`).
type MCPPrompt struct {
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Arguments   []MCPPromptArgument `json:"arguments,omitempty"`
	ServerName  string              `json:"server_name"`
}

// MCPPromptArgument describes one placeholder of a prompt template.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPContent is one piece of a prompt result or resource payload.
type MCPContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// setServerInfo records what the server declared during initialize.
func (c *Client) setServerInfo(info, caps map[string]any) {
	c.mu.Lock()
	c.serverInfo = info
	c.capabilities = caps
	c.mu.Unlock()
}

// capable reports whether the server advertised a capability. Servers that
// never completed an initialize handshake (or predate capability negotiation)
// report nothing; those are treated as capable so the call is still attempted
// and fails with a real error rather than being silently hidden.
func (c *Client) capable(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.capabilities == nil {
		return true
	}
	_, ok := c.capabilities[name]
	return ok
}

// Capabilities returns the capability set the server declared, if any.
func (c *Client) Capabilities() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]any, len(c.capabilities))
	for k, v := range c.capabilities {
		out[k] = v
	}
	return out
}

// Supports reports whether a named capability (tools/resources/prompts) is
// available on this server.
func (c *Client) Supports(capability string) bool { return c.capable(capability) }

// ListPrompts fetches the server's prompt templates.
func (c *Client) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	if !c.capable("prompts") {
		return nil, nil
	}
	resp, err := c.call(ctx, "prompts/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("prompts/list for %s: %w", c.config.Name, err)
	}
	var result struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse prompts for %s: %w", c.config.Name, err)
	}
	for i := range result.Prompts {
		result.Prompts[i].ServerName = c.Slug()
	}
	return result.Prompts, nil
}

// GetPrompt renders a prompt template server-side and returns its messages.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]any) ([]MCPContent, error) {
	if !c.capable("prompts") {
		return nil, fmt.Errorf("mcp server %q does not expose prompts", c.config.Name)
	}
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	resp, err := c.call(ctx, "prompts/get", params)
	if err != nil {
		return nil, fmt.Errorf("prompts/get %q: %w", name, err)
	}
	var result struct {
		Messages []struct {
			Role    string     `json:"role"`
			Content MCPContent `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse prompt %q: %w", name, err)
	}
	out := make([]MCPContent, 0, len(result.Messages))
	for _, m := range result.Messages {
		text := m.Content.Text
		if text != "" && m.Role != "" {
			text = m.Role + ": " + text
		}
		out = append(out, MCPContent{Type: m.Content.Type, Text: text, MimeType: m.Content.MimeType, URI: m.Content.URI})
	}
	return out, nil
}

// ReadResource fetches one resource by URI (MCP `resources/read`).
func (c *Client) ReadResource(ctx context.Context, uri string) ([]MCPContent, error) {
	if !c.capable("resources") {
		return nil, fmt.Errorf("mcp server %q does not expose resources", c.config.Name)
	}
	resp, err := c.call(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, fmt.Errorf("resources/read %q: %w", uri, err)
	}
	var result struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text,omitempty"`
			Blob     string `json:"blob,omitempty"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse resource %q: %w", uri, err)
	}
	out := make([]MCPContent, 0, len(result.Contents))
	for _, ct := range result.Contents {
		kind := "text"
		if ct.Text == "" && ct.Blob != "" {
			kind = "image"
		}
		out = append(out, MCPContent{Type: kind, Text: ct.Text, MimeType: ct.MimeType, Data: ct.Blob, URI: ct.URI})
	}
	return out, nil
}

// ListResources returns the server's resource catalog without reading bodies.
func (c *Client) ListResources(ctx context.Context) ([]MCPResource, error) {
	if !c.capable("resources") {
		return nil, nil
	}
	return c.DiscoverResources(ctx)
}

// Pool-level fan-out ----------------------------------------------------------

// PromptsByServer returns every connected server's prompt templates. Servers
// that do not support prompts contribute an empty entry rather than an error,
// so one broken connector cannot blank out the whole listing.
func (p *Pool) PromptsByServer(ctx context.Context) map[string][]MCPPrompt {
	out := make(map[string][]MCPPrompt)
	p.mu.RLock()
	clients := make(map[string]*Client, len(p.clients))
	for name, c := range p.clients {
		clients[name] = c
	}
	p.mu.RUnlock()

	for name, c := range clients {
		prompts, err := c.ListPrompts(ctx)
		if err != nil {
			continue
		}
		if len(prompts) > 0 {
			out[name] = prompts
		}
	}
	return out
}

// ResourcesByServer returns every connected server's resource catalog.
func (p *Pool) ResourcesByServer(ctx context.Context) map[string][]MCPResource {
	out := make(map[string][]MCPResource)
	p.mu.RLock()
	clients := make(map[string]*Client, len(p.clients))
	for name, c := range p.clients {
		clients[name] = c
	}
	p.mu.RUnlock()

	for name, c := range clients {
		res, err := c.ListResources(ctx)
		if err != nil {
			continue
		}
		if len(res) > 0 {
			out[name] = res
		}
	}
	return out
}

// PromptsFor returns one server's prompt templates.
func (p *Pool) PromptsFor(ctx context.Context, server string) ([]MCPPrompt, error) {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", server)
	}
	return client.ListPrompts(ctx)
}

// ResourcesFor returns one server's resource catalog.
func (p *Pool) ResourcesFor(ctx context.Context, server string) ([]MCPResource, error) {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", server)
	}
	return client.ListResources(ctx)
}

// ReadResource reads a resource URI from a named server.
func (p *Pool) ReadResource(ctx context.Context, server, uri string) ([]MCPContent, error) {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", server)
	}
	return client.ReadResource(ctx, uri)
}

// GetPrompt renders a prompt on a named server.
func (p *Pool) GetPrompt(ctx context.Context, server, name string, args map[string]any) ([]MCPContent, error) {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", server)
	}
	return client.GetPrompt(ctx, name, args)
}

// PromptRef is "server/prompt" — the form the UI and slash commands use.
func PromptRef(server, name string) string { return server + "/" + name }

// SplitPromptRef parses a PromptRef back into its parts.
func SplitPromptRef(ref string) (server, name string, ok bool) {
	i := strings.Index(ref, "/")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}
