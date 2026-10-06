package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// EditTool — precise in-place string replacement with MultiEdit support
// ============================================================================

type EditTool struct{}

type editOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type editInput struct {
	FilePath string   `json:"file_path"`
	OldStr   string   `json:"old_string"`
	NewStr   string   `json:"new_string"`
	Replace  bool     `json:"replace_all"`
	Edits    []editOp `json:"edits"`
}

func (t *EditTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "edit",
		Description: "Edit a file by replacing exact matching strings, preserving indentation. Single edit (file_path+old_string+new_string) or MultiEdit (file_path+edits[]) for one file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "File to edit",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Exact text to find",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Replacement text",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "[single mode] Replace all matches (default: false)",
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "[MultiEdit mode] Ordered replacements for one file",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "Text to find",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]any{
								"type":        "boolean",
								"description": "Replace all matches",
							},
						},
						"required": []string{"old_string", "new_string"},
					},
				},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"file_path", "old_string", "new_string"}},
				map[string]any{"required": []string{"file_path", "edits"}},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in editInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("invalid args: %v", err),
		}, nil
	}
	if in.FilePath == "" {
		return &types.ToolResult{Success: false, Error: "file_path is required"}, nil
	}

	content, err := os.ReadFile(in.FilePath)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read %s: %v", in.FilePath, err)}, nil
	}
	text := string(content)
	original := text

	// Read-before-edit guard: refuse edits to files this conversation never
	// read, UNLESS every old_string matches the file uniquely and verbatim
	// (self-verifying escape hatch for headless/CI/--yolo runs — see the
	// comment on checkEditGuard). Skipped when no needle is provided so the
	// regular "old_string is required" argument error still surfaces.
	sessionID := SessionIDFromContext(ctx)
	var needles []string
	if len(in.Edits) > 0 {
		for _, ed := range in.Edits {
			if ed.OldString != "" {
				needles = append(needles, ed.OldString)
			}
		}
	} else if in.OldStr != "" {
		needles = append(needles, in.OldStr)
	}
	guardEscape := false
	if len(needles) > 0 {
		switch checkEditGuard(sessionID, in.FilePath, text, needles) {
		case readGuardBlock:
			return &types.ToolResult{Success: false, Error: readGuardBlockMessage(in.FilePath)}, nil
		case readGuardEscape:
			guardEscape = true
		}
	}

	totalReplacements := 0

	// applyEdit performs one replacement on text. When the exact old_string is
	// absent it retries with whitespace-normalized fuzzy matching (Aider-style
	// anchor matching) so a model that mismatches indentation still succeeds.
	applyEdit := func(oldStr, newStr string, replaceAll bool) (newText string, replaced int, errMsg string) {
		c := strings.Count(text, oldStr)
		if c > 0 {
			if c > 1 && !replaceAll {
				return "", 0, fmt.Sprintf("old_string %q appears %d times. Use replace_all or add context.", truncateStr(oldStr, 60), c)
			}
			if replaceAll {
				return strings.ReplaceAll(text, oldStr, newStr), c, ""
			}
			return strings.Replace(text, oldStr, newStr, 1), 1, ""
		}
		// Fuzzy fallback: whitespace-normalized match.
		idx, end, fuzzyNew := fuzzyFind(text, oldStr, newStr)
		if idx < 0 {
			return "", 0, fmt.Sprintf("old_string %q not found.", truncateStr(oldStr, 60))
		}
		return text[:idx] + fuzzyNew + text[end:], 1, ""
	}

	if len(in.Edits) > 0 {
		// MultiEdit mode: apply edits sequentially
		for _, ed := range in.Edits {
			if ed.OldString == "" {
				continue
			}
			nt, n, errMsg := applyEdit(ed.OldString, ed.NewString, ed.ReplaceAll)
			if errMsg != "" {
				// Let the model know which edit failed but keep partial progress
				return &types.ToolResult{
					Success: false,
					Content: fmt.Sprintf("after %d replacements, failed on: %s",
						totalReplacements, errMsg),
				}, nil
			}
			text = nt
			totalReplacements += n
		}
	} else {
		// Single edit mode (backward compatible)
		if in.OldStr == "" {
			return &types.ToolResult{Success: false, Error: "old_string is required"}, nil
		}
		nt, n, errMsg := applyEdit(in.OldStr, in.NewStr, in.Replace)
		if errMsg != "" {
			return &types.ToolResult{Success: false, Error: errMsg}, nil
		}
		text = nt
		totalReplacements = n
	}

	if err := os.WriteFile(in.FilePath, []byte(text), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("write: %v", err)}, nil
	}
	// After a successful edit the result carries the unified diff, so the
	// model has genuinely seen the file — track it like read_file does.
	markFileRead(sessionID, in.FilePath)

	diffOut := unifiedDiff(in.FilePath, original, text)
	res := &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Edited %s (%d replacements)\n%s", in.FilePath, totalReplacements, diffOut),
	}
	if guardEscape {
		res.Content += readGuardEscapeWarning
	}
	return res, nil
}

// fuzzyFind locates oldStr in text after collapsing runs of whitespace in
// both sides (so indentation or line-ending differences don't break the
// match). It returns the byte span of the matched region plus the
// replacement with the file's own indentation style applied — the caller
// splices matchedNew at idx..end, preserving the file's whitespace while
// applying the model's content change.
func fuzzyFind(text, oldStr, newStr string) (idx, end int, matchedNew string) {
	needleWords := wordSpans(oldStr)
	if len(needleWords) == 0 {
		return -1, 0, ""
	}
	textWords := wordSpans(text)

	// Find the first run of textWords whose word texts equal the needle's.
	needleTexts := make([]string, len(needleWords))
	for i, w := range needleWords {
		needleTexts[i] = oldStr[w.start:w.end]
	}
	matched := -1
	for i := 0; i+len(needleTexts) <= len(textWords); i++ {
		ok := true
		for k, nt := range needleTexts {
			if text[textWords[i+k].start:textWords[i+k].end] != nt {
				ok = false
				break
			}
		}
		if ok {
			matched = i
			break
		}
	}
	if matched < 0 {
		return -1, 0, ""
	}

	first := textWords[matched]
	last := textWords[matched+len(needleTexts)-1]
	idx = first.start
	end = last.end
	// Include any trailing whitespace of the matched region so the splice
	// lands cleanly on the line break.
	for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\n' || text[end] == '\r') {
		end++
	}

	// Re-indent the replacement with the matched region's base indentation:
	// the whitespace between the start of the matched line and the match
	// itself (e.g. the "\t" before "if x {"), so multiline replacements stay
	// aligned inside their block.
	leadWS := leadingWhitespace(text[:idx])
	lines := strings.Split(newStr, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = leadWS + lines[i]
		}
	}
	matchedNew = strings.Join(lines, "\n")
	return idx, end, matchedNew
}

// wordSpan is a byte range of one whitespace-delimited word.
type wordSpan struct{ start, end int }

// wordSpans returns the byte spans of all whitespace-delimited words.
func wordSpans(s string) []wordSpan {
	var spans []wordSpan
	i := 0
	isWS := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	for i < len(s) {
		for i < len(s) && isWS(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isWS(s[i]) {
			i++
		}
		spans = append(spans, wordSpan{start, i})
	}
	return spans
}

// leadingWhitespace returns the whitespace prefix of the LAST line of s
// (the indentation immediately before the match). Handles Windows line
// endings so the base indent is whatever is between the last \n and the
// matched token.
func leadingWhitespace(s string) string {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return s[i:]
}

// unifiedDiff produces a compact unified-diff-format string showing what
// changed. Uses Myers diff internally (simplified inline implementation).
func unifiedDiff(path, oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Find changed regions
	type hunk struct{ oldStart, newStart, oldCount, newCount int }
	var hunks []hunk

	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			i++
			j++
			continue
		}
		h := hunk{oldStart: i, newStart: j, oldCount: 0, newCount: 0}
		for i < len(oldLines) && j < len(newLines) && oldLines[i] != newLines[j] {
			h.oldCount++
			h.newCount++
			i++
			j++
		}
		// Drain remaining when one side is exhausted
		for i < len(oldLines) && (j >= len(newLines) || oldLines[i] != newLines[j]) {
			h.oldCount++
			i++
		}
		for j < len(newLines) && (i >= len(oldLines) || oldLines[i] != newLines[j]) {
			h.newCount++
			j++
		}
		if h.oldCount > 0 || h.newCount > 0 {
			hunks = append(hunks, h)
		}
	}
	// Remaining lines after the shorter side exhausted
	if i < len(oldLines) || j < len(newLines) {
		hunks = append(hunks, hunk{
			oldStart: i, newStart: j,
			oldCount: len(oldLines) - i,
			newCount: len(newLines) - j,
		})
	}

	if len(hunks) == 0 {
		return "(no changes)"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- %s\n+++ %s\n", path, path))
	for _, h := range hunks {
		if h.oldCount == 0 {
			h.oldStart-- // context before insertion
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldCount,
			h.newStart+1, h.newCount)

		o, n := h.oldStart, h.newStart
		for k := 0; k < h.oldCount || k < h.newCount; k++ {
			switch {
			case k < h.oldCount && k < h.newCount:
				b.WriteString(fmt.Sprintf("-%s\n+%s\n", oldLines[o+k], newLines[n+k]))
			case k < h.oldCount:
				b.WriteString(fmt.Sprintf("-%s\n", oldLines[o+k]))
			case k < h.newCount:
				b.WriteString(fmt.Sprintf("+%s\n", newLines[n+k]))
			}
		}
		_ = n
	}
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func parseBoolArg(args, key string) (bool, error) {
	parsed, err := parseJSONArgs(args)
	if err != nil {
		return false, nil
	}
	if v, ok := parsed[key]; ok {
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return false, nil
}

// parseJSONArgs parses a JSON argument string into a map.
func parseJSONArgs(rawJSON string) (map[string]any, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return nil, err
	}
	return result, nil
}
