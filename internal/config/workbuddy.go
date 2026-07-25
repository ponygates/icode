package config

// WorkBuddy interop — import MCP server definitions from WorkBuddy's
// ~/.workbuddy/mcp.json so connectors configured there are usable in iCode
// without duplicating configuration.
//
// WorkBuddy format:
//
//	{
//	  "mcpServers": {
//	    "playwright": {"command": "npx", "args": ["@playwright/mcp@latest"], "env": {...}},
//	    "remote":     {"url": "https://example.com/sse", "headers": {...}}
//	  }
//	}

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// workBuddyMCPFile mirrors the on-disk mcp.json schema used by WorkBuddy
// (and Claude Desktop / Cursor, which share the same convention).
type workBuddyMCPFile struct {
	MCPServers map[string]workBuddyMCPServer `json:"mcpServers"`
}

type workBuddyMCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Type    string            `json:"type,omitempty"`
	Disabled bool             `json:"disabled,omitempty"`
}

// ImportWorkBuddyEnabled reports whether WorkBuddy MCP auto-import is on
// (default true when the field is unset).
func (c *Config) ImportWorkBuddyEnabled() bool {
	if c.MCPImportWorkBuddy == nil {
		return true
	}
	return *c.MCPImportWorkBuddy
}

// WorkBuddyMCPPath returns the default location of WorkBuddy's mcp.json.
func WorkBuddyMCPPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".workbuddy", "mcp.json")
}

// LoadWorkBuddyMCP reads a WorkBuddy-style mcp.json and converts every entry
// into an iCode MCPServerCfg. Entries already present in existing (matched by
// name) are skipped so explicit iCode config always wins. The returned slice
// is sorted by name for deterministic behaviour. A missing file is not an
// error — it simply returns nil.
func LoadWorkBuddyMCP(path string, existing []MCPServerCfg) ([]MCPServerCfg, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var file workBuddyMCPFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	have := make(map[string]bool, len(existing))
	for _, m := range existing {
		have[m.Name] = true
	}
	var out []MCPServerCfg
	for name, s := range file.MCPServers {
		if have[name] || s.Disabled {
			continue
		}
		cfg := MCPServerCfg{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Headers: s.Headers,
			Enabled: true,
		}
		// Flatten env map to KEY=VALUE (iCode convention).
		if len(s.Env) > 0 {
			keys := make([]string, 0, len(s.Env))
			for k := range s.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				cfg.Env = append(cfg.Env, k+"="+s.Env[k])
			}
		}
		// Infer transport: explicit type wins, otherwise url => sse, command => stdio.
		switch {
		case s.Type != "":
			cfg.Type = s.Type
		case s.URL != "":
			cfg.Type = "sse"
		default:
			cfg.Type = "stdio"
		}
		out = append(out, cfg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
