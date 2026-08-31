package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

// runPrintMode implements the non-interactive mode (Claude Code's `-p`
// parity): run one prompt to completion, print the result, exit. Designed for
// scripts, pipes, and CI — no TUI, no raw terminal mode.
//
//	icode -p "fix the failing test"                  # plain text to stdout
//	icode -p "..." --output-format json              # single JSON result object
//	icode -p "..." --output-format stream-json       # one JSON event per line
//	icode -p "..." -c                                # continue the last session
//	icode -p "..." --resume <session-id>
//
// Diagnostics (tool calls, permission auto-approvals, usage, errors) go to
// stderr so stdout stays clean and pipeable.
func runPrintMode(prompt, outFmt string, cont bool, resumeID, provider, model, mode string) error {
	cfg, _ := config.Load()
	if cfg != nil {
		if model == "" && cfg.Defaults.Model != "" {
			model = cfg.Defaults.Model
		}
		if provider == "" && cfg.Defaults.Provider != "" {
			provider = cfg.Defaults.Provider
		}
		if mode == "" && cfg.Defaults.Mode != "" {
			mode = cfg.Defaults.Mode
		}
	}
	if model == "" {
		model = "openrouter/free"
	}
	if provider == "" {
		provider = "openrouter"
	}
	if mode == "" {
		mode = "agent"
	}

	a, err := app.Bootstrap()
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	if a == nil || a.Engine == nil {
		return fmt.Errorf("engine unavailable — configure an API key with 'icode auth set'")
	}
	if mode != "" {
		a.Gate.SetMode(permission.Mode(strings.ToLower(mode)))
	}
	// Unattended: there is no one to answer an interactive permission prompt,
	// so approve silently and log it to stderr. Users who want a hard stop can
	// run in plan mode (`--mode plan`), where mutating ops are denied.
	a.Engine.SetPermissionHandler(func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
		fmt.Fprintf(os.Stderr, "[print] 自动放行工具: %s\n", req.Tool)
		return permission.DecisionAllow
	})

	// Session resolution: --resume > --continue (most recent) > fresh session.
	sessionID := resumeID
	if sessionID == "" && cont {
		if sess := latestSession(a.SessStore); sess != nil {
			sessionID = sess.ID
			fmt.Fprintf(os.Stderr, "[print] 继续会话 %s\n", sessionID)
		}
	}
	if sessionID == "" {
		sess := &types.Session{
			ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
			ModelID:      model,
			ProviderName: provider,
			Title:        truncate(prompt, 40),
		}
		if err := a.SessStore.Create(sess); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		sessionID = sess.ID
	}

	start := time.Now()
	eventCh, err := a.Engine.Send(context.Background(), sessionID, prompt)
	if err != nil {
		return fmt.Errorf("engine send: %w", err)
	}

	streamJSON := outFmt == "stream-json"
	var text strings.Builder
	var toolLines []string
	failed := false
	failMsg := ""
	var usage types.TokenUsage

	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}

	for event := range eventCh {
		switch event.Type {
		case types.EventText:
			if streamJSON {
				emit(map[string]any{"type": "assistant", "delta": event.Content})
			} else if outFmt != "json" {
				fmt.Print(event.Content)
			}
			text.WriteString(event.Content)
		case types.EventThinking:
			if streamJSON {
				emit(map[string]any{"type": "thinking", "delta": event.Content})
			} else if outFmt != "json" {
				fmt.Fprintf(os.Stderr, "[thinking] %s\n", strings.TrimSpace(event.Content))
			}
		case types.EventToolUse:
			args := event.ToolCall.Arguments
			if strings.TrimSpace(args) == "{}" {
				args = ""
			}
			desc := event.ToolCall.Name
			if args != "" {
				desc += " " + args
			}
			toolLines = append(toolLines, desc)
			if streamJSON {
				emit(map[string]any{"type": "tool_use", "name": event.ToolCall.Name, "arguments": event.ToolCall.Arguments})
			} else {
				fmt.Fprintf(os.Stderr, "⏺ %s\n", desc)
			}
		case types.EventToolProgress:
			if outFmt != "json" {
				fmt.Fprintf(os.Stderr, "%s", event.Content)
			}
		case types.EventSystem:
			if streamJSON {
				emit(map[string]any{"type": "system", "content": event.Content})
			} else {
				fmt.Fprintf(os.Stderr, "%s\n", strings.TrimSpace(event.Content))
			}
		case types.EventDone:
			usage = event.Meta.Usage
			if streamJSON {
				emit(map[string]any{
					"type":    "result",
					"subtype": "success",
					"session": sessionID,
					"result":  text.String(),
					"usage": map[string]int{
						"input":  usage.PromptTokens,
						"output": usage.CompletionTokens,
						"cache":  usage.CacheHitTokens,
					},
					"duration_ms": time.Since(start).Milliseconds(),
				})
			}
		case types.EventError:
			failed = true
			failMsg = event.Content
			if streamJSON {
				emit(map[string]any{"type": "result", "subtype": "error", "error": event.Content})
			} else {
				fmt.Fprintf(os.Stderr, "\n✗ %s\n", event.Content)
			}
		}
	}

	switch outFmt {
	case "json":
		res := map[string]any{
			"type":    "result",
			"subtype": "success",
			"session": sessionID,
			"result":  text.String(),
			"tools":   toolLines,
			"usage": map[string]int{
				"input":  usage.PromptTokens,
				"output": usage.CompletionTokens,
				"cache":  usage.CacheHitTokens,
			},
			"duration_ms": time.Since(start).Milliseconds(),
		}
		if failed {
			res["subtype"] = "error"
			res["error"] = failMsg
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	default:
		// text / stream-json: finish the line and print a compact usage line.
		fmt.Println()
		if outFmt == "text" {
			fmt.Fprintf(os.Stderr, "[print] %d in / %d out / %d cached · %s\n",
				usage.PromptTokens, usage.CompletionTokens, usage.CacheHitTokens,
				time.Since(start).Round(time.Millisecond))
		}
	}

	if failed {
		// Non-zero exit so scripts can detect failures.
		return fmt.Errorf("%s", failMsg)
	}
	return nil
}

// latestSession returns the most recently updated session, or nil.
func latestSession(store types.SessionStore) *types.Session {
	if store == nil {
		return nil
	}
	list, err := sessionum.ListNonDeleted(store, 20)
	if err != nil || len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	return &list[0]
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
