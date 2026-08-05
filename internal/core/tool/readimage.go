package tool

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// maxImageBytes caps the raw file size (15MB) so a huge screenshot can't
// blow up the token budget of the active model.
const maxImageBytes = 15 << 20

// imageMIME maps a file extension to a MIME type for image attachments, or ""
// when the file is not a known image format. Strict — unlike inferImageMIME it
// does NOT default unknown extensions to image/png, so read_file can tell a
// binary image from a text file.
func imageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return ""
	}
}

// loadImageAttachment reads a local image file and builds a vision attachment.
// The image payload is returned as base64 so vision-capable models can see
// screenshots and diagrams. ok is false when the path is not an image.
func loadImageAttachment(path string) (*types.ToolResult, bool) {
	mime := imageMIME(path)
	if mime == "" {
		return nil, false
	}

	info, err := os.Stat(path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_file: %v", err)}, true
	}
	if info.IsDir() {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_file: %s is a directory", path)}, true
	}
	if info.Size() > maxImageBytes {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("read_file: %s is %.1fMB (limit 15MB)", path, float64(info.Size())/(1<<20)),
		}, true
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read_file: %v", err)}, true
	}

	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Loaded image: %s (%d bytes, %s)", path, len(data), mime),
		Attachments: []types.Attachment{{
			Type:     "image",
			MIMEType: mime,
			Data:     base64.StdEncoding.EncodeToString(data),
		}},
	}, true
}
