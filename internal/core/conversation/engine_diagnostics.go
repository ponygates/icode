package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/lsp"
	"github.com/ponygates/icode/internal/types"
)

// checkDiagnosticsAfterTool queries the LSP manager for diagnostics on
// the given file path. If compilation errors are found, returns a formatted
// string suitable for injection into the model's context for auto-fix.
func (e *Engine) checkDiagnosticsAfterTool(filePath string) string {
	if e.lspManager == nil || filePath == "" {
		return ""
	}
	lang := lsp.DetectLanguage(filePath)
	if lang == "" {
		return ""
	}
	// Lazily start the language server for this language on first use.
	// StartLanguageServer is idempotent (no-ops if already running) and
	// returns an error when the binary is absent, so this stays safe.
	// Cap with a timeout so an unresponsive server initialize cannot hang
	// the first tool call that triggers it.
	lspCtx, lspCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer lspCancel()
	if err := e.lspManager.StartLanguageServer(lspCtx, lang); err != nil {
		return ""
	}
	client := e.lspManager.GetClient(lang)
	if client == nil {
		return ""
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return ""
	}
	uri := "file://" + filepath.ToSlash(absPath)
	diags, err := client.Diagnostics(uri)
	if err != nil || len(diags) == 0 {
		return ""
	}
	// Filter to errors only (severity 1 = error)
	var errors []string
	for _, d := range diags {
		if d.Severity == 1 {
			line := d.Range.Start.Line + 1
			msg := strings.TrimRight(d.Message, "\n")
			errors = append(errors, fmt.Sprintf("  L%d: %s", line, msg))
		}
	}
	if len(errors) == 0 {
		return ""
	}
	return fmt.Sprintf("\n⚠️ LSP diagnostics for %s:\n%s\n",
		filePath, strings.Join(errors, "\n"))
}

// collectDiagnostics checks all tool calls for file modifications and
// queries LSP diagnostics for each modified file. Returns a combined
// diagnostics message for all files, or "" if no errors found. A file whose
// diagnostics are unchanged since the last injection is skipped (G1) so the
// model is only alerted to new or changed compile errors.
func (e *Engine) collectDiagnostics(toolCalls []types.ToolCall) string {
	checked := make(map[string]bool)
	var parts []string
	for _, tc := range toolCalls {
		// Extract file path from common editing tools
		filePath := extractFilePath(tc.Name, tc.Arguments)
		if filePath == "" || checked[filePath] {
			continue
		}
		checked[filePath] = true
		msg := e.checkDiagnosticsAfterTool(filePath)
		if msg == "" {
			continue
		}
		if e.rememberDiagnostics(filePath, msg) {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "🔧 以下文件存在编译错误，请修复:\n" + strings.Join(parts, "")
}

// rememberDiagnostics records the diagnostics text for a file and reports
// whether it is new (should be injected). Identical re-runs are suppressed to
// avoid re-alerting the model every turn.
func (e *Engine) rememberDiagnostics(filePath, msg string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if msg == "" {
		delete(e.diagCache, filePath)
		return false
	}
	if e.diagCache[filePath] == msg {
		return false
	}
	e.diagCache[filePath] = msg
	return true
}

// extractFilePath extracts the file path from a tool call's arguments.
func extractFilePath(toolName, args string) string {
	// Try to parse JSON arguments
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return ""
	}
	switch toolName {
	case "write_file", "edit", "search_replace", "read_file":
		if fp, ok := parsed["file_path"].(string); ok {
			return fp
		}
		if fp, ok := parsed["filePath"].(string); ok {
			return fp
		}
	}
	if fp, ok := parsed["file_path"].(string); ok {
		return fp
	}
	return ""
}

// ingestToolAttachments collects inline multimodal output (e.g. images produced
// by the image_gen tool) from this round's tool results and appends a single
// user message carrying them, so vision-capable models can reference the
// generated artifact on the next turn. Tool messages themselves stay
// text-only because most providers reject image content inside tool messages.
func (e *Engine) ingestToolAttachments(sessionID string, toolCalls []types.ToolCall, opt *tokenopt.Optimizer) {
	var imgs []types.Attachment
	for _, tc := range toolCalls {
		if tc.Result != nil {
			imgs = append(imgs, tc.Result.Attachments...)
		}
	}
	if len(imgs) == 0 {
		return
	}
	msg := types.Message{
		Role:        types.RoleUser,
		Content:     "（上一步工具生成的可视化结果，见附件）",
		Attachments: imgs,
		Timestamp:   time.Now(),
	}
	e.appendPersisted(sessionID, opt, msg)
}
