// mcpwire.go — MCP pool lifecycle at the app layer.
//
// The pool used to be created ONLY inside the HTTP server (icode server /
// icode desktop), which meant MCP tools were invisible to `icode chat`,
// `icode exec`, print mode, and ACP — servers configured via /mcp were stored
// on disk but the TUI could never call them. Promoting the pool to Bootstrap
// closes that gap: one pool, every surface (Claude Code parity).
package app

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/mcp"
)

// initMCPPool creates the shared MCP pool and starts connecting every enabled
// server from the user config (plus the WorkBuddy bridge) in the background.
//
// IMPORTANT: an enabled-but-unreachable MCP server (or a stdio subprocess
// that never responds) can block its connect for up to the client's own 30s
// call timeout. Connections therefore run concurrently in a background
// goroutine with a bounded context so the boot path (and the desktop window)
// is never blocked — the classic "桌面启动卡死" lesson from the server path.
func (app *App) initMCPPool() {
	if app.Engine == nil {
		return
	}
	app.MCPPool = mcp.NewPool()
	app.mcpToolNames = make(map[string]bool)
	// Auto-refresh the engine's MCP tool registry when a connected server
	// signals notifications/tools/list_changed (Reasonix parity).
	app.MCPPool.SetOnToolsChanged(func(name string) {
		log.Printf("[iCode MCP] tool list changed on %s — refreshing registry", name)
		app.RefreshMCPTools()
	})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[iCode MCP] init panic: %v", r)
			}
		}()
		mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer mcpCancel()

		mcpList := app.Cfg.MCP
		// WorkBuddy bridge: auto-import connectors from ~/.workbuddy/mcp.json so
		// servers configured in WorkBuddy are usable here without re-configuring.
		if app.Cfg.ImportWorkBuddyEnabled() {
			if imported, err := config.LoadWorkBuddyMCP(config.WorkBuddyMCPPath(), mcpList); err != nil {
				log.Printf("[iCode MCP] workbuddy import skipped: %v", err)
			} else if len(imported) > 0 {
				log.Printf("[iCode MCP] imported %d server(s) from WorkBuddy mcp.json", len(imported))
				mcpList = append(mcpList, imported...)
			}
		}
		// Connect every enabled server CONCURRENTLY (each bounded by mcpCtx) so
		// a slow/unreachable server can never serialise the others or the boot
		// path. The shared 20s deadline caps total wall time even with many
		// connectors imported from WorkBuddy.
		var wg sync.WaitGroup
		for _, mc := range mcpList {
			if !mc.Enabled {
				continue
			}
			wg.Add(1)
			go func(mc config.MCPServerCfg) {
				defer wg.Done()
				// A panic connecting one server must not hang wg.Wait.
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[iCode MCP] connect %q panic recovered: %v", mc.Name, r)
					}
				}()
				if err := app.MCPPool.Add(mcpCtx, mcp.ServerConfigFrom(mc)); err != nil {
					log.Printf("[iCode MCP] failed to connect %q: %v", mc.Name, err)
				}
			}(mc)
		}
		wg.Wait()
		log.Printf("[iCode MCP] background connect finished")
		app.RefreshMCPTools()
	}()
}

// RefreshMCPTools re-registers every discovered MCP tool into the conversation
// engine, replacing any previously-registered MCP tools. Tools from disabled
// or disconnected servers are dropped. Also re-applies the per-server trust
// modes to the permission gate so an untrusted server's write tools never run
// unprompted in yolo/auto. Safe to call concurrently.
func (app *App) RefreshMCPTools() {
	if app.Engine == nil || app.MCPPool == nil {
		return
	}
	app.mcpMu.Lock()
	defer app.mcpMu.Unlock()

	// Remove stale MCP tools registered in a previous refresh.
	for name := range app.mcpToolNames {
		app.Engine.UnregisterTool(name)
	}
	app.mcpToolNames = make(map[string]bool)

	for _, def := range app.MCPPool.AllTools() {
		app.Engine.RegisterTool(&mcp.ToolAdapter{ToolDef: def, Pool: app.MCPPool})
		app.mcpToolNames[def.Name] = true
	}

	// Enforce per-server trust modes in the permission gate: without this,
	// trust_mode was stored and displayed but never applied, so an untrusted
	// MCP server's write tools executed unprompted in yolo/auto.
	trustByServer := make(map[string]string)
	app.Cfg.WithRLock(func() {
		for _, m := range app.Cfg.MCP {
			if m.TrustMode != "" {
				trustByServer[m.Name] = m.TrustMode
			}
		}
	})
	if app.Gate != nil && len(trustByServer) > 0 {
		policy := make(map[string]string)
		for serverName, defs := range app.MCPPool.AllToolsByServer() {
			mode := trustByServer[serverName]
			if mode == "" || mode == "all" {
				continue
			}
			for _, def := range defs {
				policy[def.Name] = mode
			}
		}
		app.Gate.SetMCPPolicy(policy)
	}
}
