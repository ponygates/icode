// Package tokenopt — Tool-Call Repair Pipeline.
//
// Reasonix 核心机制：4 步修复流水线，专门针对 DeepSeek 等开源模型的
// 常见工具调用故障模式。iCode 独有的创新。
//
// Pipeline stages:
//  1. Flatten  — 扁平化深度嵌套的参数结构 >10 层或 >10 个参数
//  2. Scavenge — 从 reasoning_content 中找回模型"忘记"发出的工具调用
//  3. Truncation — 检测不平衡 JSON，自动补全大括号
//  4. Storm    — 抑制滑动窗口内重复的 (tool, args) 调用

package tokenopt

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// ToolCallRepairConfig controls the repair pipeline behavior.
type ToolCallRepairConfig struct {
	// MaxNestDepth is the maximum allowed JSON nesting depth.
	// Beyond this, the arguments are flattened. Default: 10.
	MaxNestDepth int

	// MaxParams is the maximum number of parameters before flattening.
	// Default: 10.
	MaxParams int

	// StormWindow is the number of recent tool calls to scan for duplicates.
	// Default: 5.
	StormWindow int

	// MaxSameCall is the maximum times the same (tool, args) can appear
	// within StormWindow before storm suppression kicks in. Default: 3.
	MaxSameCall int

	// EnabledStages controls which pipeline stages are active.
	EnabledStages []string // e.g., ["flatten", "scavenge", "truncation", "storm"]
}

// DefaultToolCallRepairConfig returns sensible defaults for DeepSeek models.
func DefaultToolCallRepairConfig() ToolCallRepairConfig {
	return ToolCallRepairConfig{
		MaxNestDepth: 10,
		MaxParams:    10,
		StormWindow:  5,
		MaxSameCall:  3,
		EnabledStages: []string{"flatten", "scavenge", "truncation", "storm"},
	}
}

// RepairedCall represents a tool call after repair processing.
type RepairedCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Repaired  bool   `json:"repaired"`   // true if this call was modified
	Stage     string `json:"stage"`      // which stage repaired it
	Original  string `json:"original,omitempty"` // original arguments before repair
}

// callKey is used internally for storm duplicate detection.
type callKey struct {
	name string
	args string
}

// RepairReport summarizes what the pipeline did.
type RepairReport struct {
	TotalCalls   int            `json:"total_calls"`
	Repaired     int            `json:"repaired"`
	Suppressed   int            `json:"suppressed"`
	Recovered    int            `json:"recovered"` // calls recovered from reasoning_content
	StageStats   map[string]int `json:"stage_stats"`
}

// ToolCallRepairPipeline implements the 4-stage repair pipeline.
type ToolCallRepairPipeline struct {
	config ToolCallRepairConfig
}

// NewToolCallRepairPipeline creates a new repair pipeline.
func NewToolCallRepairPipeline(cfg ToolCallRepairConfig) *ToolCallRepairPipeline {
	return &ToolCallRepairPipeline{config: cfg}
}

// RepairArgs runs the full 4-stage pipeline on tool call arguments.
// Returns the repaired arguments and a report of what was done.
func (p *ToolCallRepairPipeline) RepairArgs(calls []RepairedCall) ([]RepairedCall, RepairReport) {
	report := RepairReport{
		StageStats: make(map[string]int),
	}

	if len(calls) == 0 {
		return calls, report
	}

	enabled := make(map[string]bool)
	for _, s := range p.config.EnabledStages {
		enabled[s] = true
	}

	result := make([]RepairedCall, 0, len(calls))

	// Stage 4: Storm — track recent calls for duplicate detection
	recentCalls := make([]callKey, 0, p.config.StormWindow)

	for _, call := range calls {
		original := call

		// Stage 1: Flatten
		if enabled["flatten"] {
			call = p.flattenStage(call)
			if call.Repaired {
				report.Repaired++
				report.StageStats["flatten"]++
			}
		}

		// Stage 3: Truncation
		if enabled["truncation"] {
			call = p.truncationStage(call)
			if call.Repaired && call.Stage == "truncation" {
				report.Repaired++
				report.StageStats["truncation"]++
			}
		}

		// Stage 4: Storm — check for duplicates
		if enabled["storm"] && p.isDuplicateCall(call, recentCalls) {
			report.Suppressed++
			report.StageStats["storm"]++
			continue // skip this call entirely
		}

		// Update recent calls tracking with the FINAL (possibly repaired) args
		recentCalls = append(recentCalls, callKey{name: call.Name, args: call.Arguments})
		if len(recentCalls) > p.config.StormWindow {
			recentCalls = recentCalls[1:]
		}

		result = append(result, call)
		_ = original // original saved for debugging if needed
	}

	report.TotalCalls = len(calls)
	return result, report
}

// flattenStage flattens deeply nested JSON arguments.
func (p *ToolCallRepairPipeline) flattenStage(call RepairedCall) RepairedCall {
	if call.Arguments == "" || call.Arguments == "{}" {
		return call
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(call.Arguments), &parsed); err != nil {
		return call
	}

	if len(parsed) <= p.config.MaxParams && !hasDeepNesting(parsed, p.config.MaxNestDepth, 0) {
		return call
	}

	// Flatten: convert nested structure to dot notation
	flat := make(map[string]interface{})
	flattenMap("", parsed, flat, p.config.MaxNestDepth)

	flatJSON, err := json.Marshal(flat)
	if err != nil {
		return call
	}

	return RepairedCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: string(flatJSON),
		Repaired:  true,
		Stage:     "flatten",
		Original:  call.Arguments,
	}
}

// truncationStage detects and fixes truncated JSON arguments.
func (p *ToolCallRepairPipeline) truncationStage(call RepairedCall) RepairedCall {
	args := strings.TrimSpace(call.Arguments)
	if args == "" || args == "{}" {
		return call
	}

	// Check for unbalanced braces/brackets
	openBraces := strings.Count(args, "{")
	closeBraces := strings.Count(args, "}")
	openBrackets := strings.Count(args, "[")
	closeBrackets := strings.Count(args, "]")

	needsRepair := false
	repaired := args

	// Missing closing braces
	if openBraces > closeBraces {
		repaired += strings.Repeat("}", openBraces-closeBraces)
		needsRepair = true
	}

	// Missing closing brackets
	if openBrackets > closeBrackets {
		repaired += strings.Repeat("]", openBrackets-closeBrackets)
		needsRepair = true
	}

	// Check for trailing comma before closing brace
	if needsRepair {
		repaired = strings.TrimRight(repaired, " \t\n\r")
		// Remove trailing comma before the newly added braces
		if strings.HasSuffix(repaired, ",") {
			repaired = strings.TrimSuffix(repaired, ",")
		}
		repaired += "\n"
	}

	// Extra check: if the args end with a property name without value
	// like `{"path": "file.go", "content": ` — this is clearly truncated
	if strings.Count(repaired, `"`)%2 != 0 {
		// Unbalanced quotes — add closing quote
		repaired += `"`
		needsRepair = true
	}
	// If still unbalanced after quote fix, add more braces
	openBraces2 := strings.Count(repaired, "{")
	closeBraces2 := strings.Count(repaired, "}")
	if openBraces2 > closeBraces2 {
		repaired += strings.Repeat("}", openBraces2-closeBraces2)
		needsRepair = true
	}

	if !needsRepair {
		return call
	}

	return RepairedCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: repaired,
		Repaired:  true,
		Stage:     "truncation",
		Original:  call.Arguments,
	}
}

// ScavengeFromReasoning scans a reasoning_content string for tool calls
// that the model generated during chain-of-thought but didn't emit as
// actual tool_use events. Returns the recovered tool calls.
//
// This is DeepSeek-specific: the model often writes valid tool calls in
// its reasoning block but then "forgets" to output them as real events.
func (p *ToolCallRepairPipeline) ScavengeFromReasoning(reasoningContent string) []RepairedCall {
	if reasoningContent == "" {
		return nil
	}

	var recovered []RepairedCall

	// Look for tool call patterns in reasoning content
	// Pattern 1: `{"name": "tool_name", "arguments": {...}}`
	// Pattern 2: `tool_name({"param": "value"})`
	// Pattern 3: Markdown code blocks containing tool call JSON
	lines := strings.Split(reasoningContent, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and markdown fences
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}

		// Pattern: I'll use the read_file tool to check...
		for _, prefix := range []string{"use the", "call the", "run the", "execute the"} {
			if strings.Contains(strings.ToLower(trimmed), prefix) {
				// Extract tool name (usually follows "tool" or "command")
				for _, toolName := range knownToolNames() {
					if strings.Contains(trimmed, toolName) {
						// Check if there's a JSON block after it
						if i+1 < len(lines) {
							nextLine := strings.TrimSpace(lines[i+1])
							if isJSONObject(nextLine) {
								recovered = append(recovered, RepairedCall{
									Name:      toolName,
									Arguments: nextLine,
									Repaired:  true,
									Stage:     "scavenge",
								})
							}
						}
						break
					}
				}
			}
		}

		// Pattern: explicit JSON tool call in reasoning
		if isJSONObject(trimmed) && len(trimmed) > 20 {
			var parsed map[string]interface{}
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				if name, hasName := parsed["name"]; hasName {
					if args, hasArgs := parsed["arguments"]; hasArgs {
						argsJSON, _ := json.Marshal(args)
						nameStr, _ := name.(string)
						if isKnownTool(nameStr) {
							recovered = append(recovered, RepairedCall{
								Name:      nameStr,
								Arguments: string(argsJSON),
								Repaired:  true,
								Stage:     "scavenge",
							})
						}
					}
				}
			}
		}
	}

	return recovered
}

// isDuplicateCall checks if the call is a duplicate of recent calls.
func (p *ToolCallRepairPipeline) isDuplicateCall(call RepairedCall, recent []callKey) bool {
	key := callKey{name: call.Name, args: normalizeArgs(call.Arguments)}
	count := 0
	for _, rc := range recent {
		if rc.name == key.name && normalizeArgs(rc.args) == key.args {
			count++
			if count >= p.config.MaxSameCall {
				return true
			}
		}
	}
	return false
}

// ============================================================================
// Helpers
// ============================================================================

func hasDeepNesting(m map[string]interface{}, maxDepth, currentDepth int) bool {
	if currentDepth > maxDepth {
		return true
	}
	for _, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			if hasDeepNesting(val, maxDepth, currentDepth+1) {
				return true
			}
		case []interface{}:
			for _, item := range val {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if hasDeepNesting(itemMap, maxDepth, currentDepth+1) {
						return true
					}
				}
			}
		}
	}
	return false
}

func flattenMap(prefix string, m map[string]interface{}, result map[string]interface{}, maxDepth int) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			if len(val) <= 3 && !hasDeepNesting(val, maxDepth, 0) {
				// Small enough to keep nested
				result[key] = v
			} else {
				flattenMap(key, val, result, maxDepth)
			}
		default:
			result[key] = v
		}
	}
}

func normalizeArgs(args string) string {
	// Strip whitespace for comparison
	var normalized map[string]interface{}
	if err := json.Unmarshal([]byte(args), &normalized); err == nil {
		if b, err := json.Marshal(normalized); err == nil {
			return string(b)
		}
	}
	return strings.Join(strings.Fields(args), " ")
}

func isJSONObject(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

func isKnownTool(name string) bool {
	tools := knownToolNames()
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}

func knownToolNames() []string {
	return []string{
		"bash", "read_file", "write_file", "edit", "grep", "glob", "ls",
		"fetch", "git_diff", "git_commit", "git_status", "search_replace",
		"todo_write", "task", "web_search", "ask_user_question",
	}
}

// ExtractReasoningContent extracts reasoning/thinking content from a
// raw LLM response string. DeepSeek models often include reasoning in
// a separate field or in 标签.
func ExtractReasoningContent(rawResponse string) string {
	// Pattern 1: DeepSeek 格式
	if idx := strings.Index(rawResponse, "reasoning_content"); idx >= 0 {
		// Try to extract JSON field value
		rest := rawResponse[idx:]
		colonIdx := strings.Index(rest, ":")
		if colonIdx >= 0 {
			valStart := colonIdx + 1
			valStr := strings.TrimSpace(rest[valStart:])
			if strings.HasPrefix(valStr, `"`) {
				// String value — find closing quote
				endIdx := strings.Index(valStr[1:], `"`)
				if endIdx > 0 {
					return valStr[1 : endIdx+1]
				}
			}
		}
	}

	// Pattern 2: Chinese model think tags
	thinkTagOpen := "reasoning"
	thinkTagClose := "/reasoning"
	if strings.Contains(rawResponse, "<"+thinkTagOpen+">") {
		start := strings.Index(rawResponse, "<"+thinkTagOpen+">")
		end := strings.Index(rawResponse, "<"+thinkTagClose+">")
		if start >= 0 && end > start {
			return rawResponse[start+len("<"+thinkTagOpen+">") : end]
		}
	}

	// Pattern 3: DeepSeek-style think tags
	if strings.Contains(rawResponse, "<|thought|>") {
		start := strings.Index(rawResponse, "<|thought|>")
		end := strings.LastIndex(rawResponse, "<|/thought|>")
		if start >= 0 && end > start {
			return rawResponse[start+len("<|thought|>") : end]
		}
	}

	return ""
}

// EscapeJSONString properly escapes a string for use in JSON.
func EscapeJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r == '\t':
			b.WriteString("\\t")
		case r < 0x20:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// IsTruncatedJSON checks if a JSON string appears to be truncated.
func IsTruncatedJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	// Count braces
	openBrace := strings.Count(s, "{")
	closeBrace := strings.Count(s, "}")
	openBracket := strings.Count(s, "[")
	closeBracket := strings.Count(s, "]")

	if openBrace > closeBrace || openBracket > closeBracket {
		return true
	}

	// Check if ends with a comma (implies more params expected)
	s = strings.TrimRight(s, " \t\n\r")
	if strings.HasSuffix(s, ",") {
		return true
	}

	// Check for unbalanced quotes
	inString := false
	escape := false
	for _, r := range s {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' {
			inString = !inString
		}
	}

	if !unicode.IsPrint(rune(s[len(s)-1])) {
		return true
	}

	return inString // unbalanced quotes = truncated
}
