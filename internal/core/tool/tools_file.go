package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// ReadFileTool — read file contents
// ============================================================================

type ReadFileTool struct{}

func (t *ReadFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "read_file",
		Description: "Read the contents of a file at a given path. Image files (png/jpeg/gif/webp/bmp) are attached for vision-capable models. For large files, pass offset (1-based line) and limit (max lines) to read them in chunks — the response reports the total line count so you can plan the next chunk.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Start line, 1-based. Omit to read from the top.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max number of lines to return. Use with offset to read large files in chunks (e.g. offset=501&limit=500 for the second 500-line block).",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}

	// Images are attached as vision payloads (read_image merged into read_file).
	if res, isImage := loadImageAttachment(path); isImage {
		return res, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &types.ToolResult{Success: false, Content: "", Error: err.Error()}, nil
	}
	// Read-before-edit guard: the model has now seen this file, so later
	// edit/write/search_replace calls on it pass without the escape hatch.
	markFileRead(SessionIDFromContext(ctx), path)

	// Chunked reads (large-file parity): offset/limit select a 1-based line
	// window so the model can page through a big file without pulling the
	// whole thing into context. When neither is given, the full content is
	// returned as before.
	offset, limit := 0, 0
	if s, perr := parseArg(args, "offset"); perr == nil {
		offset, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if s, perr := parseArg(args, "limit"); perr == nil {
		limit, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if offset > 0 || limit > 0 {
		text := string(data)
		text = strings.TrimSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\r")
		lines := strings.Split(text, "\n")
		total := len(lines)
		if offset < 1 {
			offset = 1
		}
		if offset > total {
			offset = total
		}
		end := total
		if limit > 0 && offset-1+limit < end {
			end = offset - 1 + limit
		}
		chunk := lines[offset-1 : end]
		var sb strings.Builder
		fmt.Fprintf(&sb, "文件 %s 共 %d 行，当前返回第 %d–%d 行。\n",
			path, total, offset, end)
		if offset+len(chunk) <= total && len(chunk) >= 2 {
			fmt.Fprintf(&sb, "（继续读取请用 offset=%d 再取下一段）\n", offset+len(chunk))
		}
		for _, ln := range chunk {
			sb.WriteString(ln)
			sb.WriteString("\n")
		}
		return &types.ToolResult{Success: true, Content: sb.String()}, nil
	}

	return &types.ToolResult{Success: true, Content: string(data)}, nil
}

// ============================================================================
// WriteFileTool — write content to a file
// ============================================================================

type WriteFileTool struct{}

func (t *WriteFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories as needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := parseArg(args, "content")
	if err != nil {
		return nil, err
	}

	// Read-before-edit guard: overwriting an EXISTING file the model never
	// read is refused (creating a new file is always allowed — there is
	// nothing to have read). A whole-file write has no old_string to
	// self-verify, so there is no escape hatch here.
	sessionID := SessionIDFromContext(ctx)
	if prev, rerr := os.ReadFile(path); rerr == nil {
		if checkEditGuard(sessionID, path, string(prev), nil) == readGuardBlock {
			return &types.ToolResult{Success: false, Error: readGuardBlockMessage(path)}, nil
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	// The session just wrote the file, so it knows its content — later edits
	// to a session-created file must pass the guard (audit requirement).
	markFileRead(sessionID, path)

	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}, nil
}
