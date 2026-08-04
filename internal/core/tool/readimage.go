package tool

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/ponygates/icode/internal/types"
)

// ReadImageTool reads a local image file and feeds it back into the
// conversation as a vision attachment (Claude Code read_image parity), so
// vision-capable models can see screenshots and diagrams. Unlike read_file it
// returns no text content — the image itself is the payload.
type ReadImageTool struct{}

func (t *ReadImageTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "read_image",
		Description: "Read a local image file (png/jpeg/gif/webp/bmp) and attach it to the conversation so vision-capable models can see it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the image file",
				},
			},
			"required": []string{"path"},
		},
	}
}

// maxImageBytes caps the raw file size (15MB) so a huge screenshot can't
// blow up the token budget of the active model.
const maxImageBytes = 15 << 20

func (t *ReadImageTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}

	mime := inferImageMIME(path)
	if mime == "" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("read_image: unsupported image format (want png/jpeg/gif/webp/bmp): %s", path),
		}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_image: %v", err)}, nil
	}
	if info.IsDir() {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_image: %s is a directory", path)}, nil
	}
	if info.Size() > maxImageBytes {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("read_image: %s is %.1fMB (limit 15MB)", path, float64(info.Size())/(1<<20)),
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_image: %v", err)}, nil
	}

	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Loaded image: %s (%d bytes, %s)", path, len(data), mime),
		Attachments: []types.Attachment{{
			Type:     "image",
			MIMEType: mime,
			Data:     base64.StdEncoding.EncodeToString(data),
		}},
	}, nil
}
