package tool

// screen_read — screen recognition: captures the screen (vision attachment)
// PLUS the foreground window context (title + owning process) that plain
// screenshot lacks, so vision-capable models can reason about WHAT the user
// is looking at, not just pixels. On non-Windows builds the window context
// degrades gracefully to the capture alone.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// ScreenReadTool recognises the active screen: full-window context + image.
type ScreenReadTool struct{}

func (t *ScreenReadTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "screen_read",
		Description: "Recognise what is on the user's screen right now: captures the display as a vision image AND reports the foreground window title and owning application, so you can answer questions about the user's current context. Prefer this over screenshot when you need to understand the scene; use screenshot when you only need raw pixels of a region.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "Optional focus for your analysis, e.g. 'what error dialog is showing'. The capture always covers the full primary screen.",
				},
			},
		},
	}
}

func (t *ScreenReadTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		Question string `json:"question"`
	}
	if args != "" {
		_ = json.Unmarshal([]byte(args), &in)
	}

	png, w, h, err := captureScreen(0, 0, 0, 0)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "屏幕捕获失败: " + err.Error()}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[屏幕识别 %dx%d PNG 已附上，供视觉模型分析]\n", w, h))
	if in.Question != "" {
		fmt.Fprintf(&b, "分析重点：%s\n", in.Question)
	}
	title, proc := foregroundWindow()
	if title != "" || proc != "" {
		b.WriteString("前台窗口:\n")
		if title != "" {
			fmt.Fprintf(&b, "  标题: %s\n", truncN(title, 120))
		}
		if proc != "" {
			fmt.Fprintf(&b, "  进程: %s\n", proc)
		}
	} else {
		b.WriteString("（当前平台无法获取前台窗口信息）\n")
	}
	b.WriteString("请结合图像与上述上下文回答。")

	return &types.ToolResult{
		Success: true,
		Content: b.String(),
		Attachments: []types.Attachment{{
			Type:     "image",
			MIMEType: "image/png",
			Data:     base64.StdEncoding.EncodeToString(png),
		}},
	}, nil
}
