// adapt.go — shared adapters between the MCP client layer and its consumers.
//
// ToolAdapter and ServerConfigFrom used to live (duplicated) inside the HTTP
// server; they moved here when the MCP pool was promoted to the app layer so
// the TUI / exec / print surfaces get the same MCP tool wiring the server has.
package mcp

import (
	"context"
	"encoding/json"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/types"
)

// ServerConfigFrom converts a persisted config entry (config.MCPServerCfg)
// into the live ServerConfig used to connect a client. Shared by the app
// bootstrap wiring, the TUI /mcp command, and the HTTP server handlers so all
// three surfaces stay in sync.
func ServerConfigFrom(c config.MCPServerCfg) ServerConfig {
	return ServerConfig{
		Name:    c.Name,
		Type:    Transport(c.Type),
		Command: c.Command,
		Args:    c.Args,
		Env:     c.Env,
		URL:     c.URL,
		Headers: c.Headers,
		Enabled: c.Enabled,
	}
}

// ToolAdapter wraps a discovered MCP tool definition so it satisfies the
// types.Tool interface and routes execution through the shared MCP pool.
// Used by every surface that registers MCP tools into the conversation engine.
type ToolAdapter struct {
	ToolDef types.ToolDef
	Pool    *Pool
}

func (a *ToolAdapter) Def() types.ToolDef { return a.ToolDef }

func (a *ToolAdapter) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		// Fall back to an empty arg map so the MCP server still receives a call.
		m = map[string]any{}
	}
	return a.Pool.Execute(ctx, a.ToolDef.Name, m)
}
