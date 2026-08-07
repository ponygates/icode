// Computer-use tools: screenshot + mouse/keyboard control (Claude Code
// /computer-use parity). Let a vision-capable model see the screen via the
// screenshot tool and interact with the user's desktop through mouse/keyboard
// tools. All control surfaces are high-risk and therefore gated behind the
// permission system (see AccessLevelOf in the permission package), so the user
// always approves an action before it executes.
//
// The actual Windows implementation lives in computeruse_windows.go (GDI
// capture + SendInput); other platforms get friendly "not supported" stubs.
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// ScreenshotTool captures the screen (or a region) as a PNG attachment.
type ScreenshotTool struct{}

func (t *ScreenshotTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "screenshot",
		Description: "Capture the current screen (optionally a region) as an image attached for vision-capable models. Use this to see what the user is looking at before interacting with the computer.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x":      map[string]any{"type": "integer", "description": "Region left edge in pixels (default: 0, full screen)"},
				"y":      map[string]any{"type": "integer", "description": "Region top edge in pixels (default: 0, full screen)"},
				"width":  map[string]any{"type": "integer", "description": "Region width (default: full screen width)"},
				"height": map[string]any{"type": "integer", "description": "Region height (default: full screen height)"},
			},
		},
	}
}

func (t *ScreenshotTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		X, Y, W, H int
	}
	if args != "" {
		_ = json.Unmarshal([]byte(args), &in)
	}
	png, w, h, err := captureScreen(in.X, in.Y, in.W, in.H)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Captured %dx%d screenshot (%d bytes PNG), attached for vision.", w, h, len(png)),
		Attachments: []types.Attachment{{
			Type:     "image",
			MIMEType: "image/png",
			Data:     base64.StdEncoding.EncodeToString(png),
		}},
	}, nil
}

// MouseMoveTool moves the cursor to absolute screen coordinates.
type MouseMoveTool struct{}

func (t *MouseMoveTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "mouse_move",
		Description: "Move the mouse cursor to an absolute screen position (top-left is 0,0). Coordinate with a recent screenshot to know where to point.",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"x", "y"},
			"properties": map[string]any{
				"x": map[string]any{"type": "integer", "description": "Target X (screen pixel)"},
				"y": map[string]any{"type": "integer", "description": "Target Y (screen pixel)"},
			},
		},
	}
}

func (t *MouseMoveTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct{ X, Y int }
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid args: " + err.Error()}, nil
	}
	if err := moveMouse(in.X, in.Y); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Moved mouse to (%d, %d)", in.X, in.Y)}, nil
}

// MouseClickTool presses/releases a mouse button, optionally at a position.
type MouseClickTool struct{}

func (t *MouseClickTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "mouse_click",
		Description: "Click a mouse button (left/right/middle) at the current cursor position, or move to x/y first. Set double=true to double-click.",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"button"},
			"properties": map[string]any{
				"button": map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}, "description": "Button to click"},
				"x":      map[string]any{"type": "integer", "description": "Optional X to move to first"},
				"y":      map[string]any{"type": "integer", "description": "Optional Y to move to first"},
				"double": map[string]any{"type": "boolean", "description": "Double-click (default false)"},
			},
		},
	}
}

func (t *MouseClickTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		Button string `json:"button"`
		X, Y   int
		Double bool
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid args: " + err.Error()}, nil
	}
	btn := strings.ToLower(in.Button)
	if btn != "left" && btn != "right" && btn != "middle" {
		return &types.ToolResult{Success: false, Error: "button must be left/right/middle"}, nil
	}
	if err := clickMouse(btn, in.Double, in.X, in.Y); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Clicked %s button%s", btn, map[bool]string{true: " (double)", false: ""}[in.Double])}, nil
}

// MouseScrollTool sends wheel-scroll events.
type MouseScrollTool struct{}

func (t *MouseScrollTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "mouse_scroll",
		Description: "Scroll the mouse wheel. Positive dy scrolls up (away from user), negative down. dx scrolls horizontally (rarely used).",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"dy"},
			"properties": map[string]any{
				"dx": map[string]any{"type": "integer", "description": "Horizontal wheel ticks"},
				"dy": map[string]any{"type": "integer", "description": "Vertical wheel ticks (positive = up)"},
			},
		},
	}
}

func (t *MouseScrollTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct{ DX, DY int }
	_ = json.Unmarshal([]byte(args), &in)
	if err := scrollMouse(in.DX, in.DY); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Scrolled wheel (%d, %d)", in.DX, in.DY)}, nil
}

// TypeTextTool types arbitrary text using Unicode key input.
type TypeTextTool struct{}

func (t *TypeTextTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "type_text",
		Description: "Type text into the focused field using simulated keyboard input. Supports any Unicode text (Chinese included). Newlines are typed as Enter.",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"text"},
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Text to type"},
			},
		},
	}
}

func (t *TypeTextTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct{ Text string }
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid args: " + err.Error()}, nil
	}
	if in.Text == "" {
		return &types.ToolResult{Success: false, Error: "text is required"}, nil
	}
	if err := typeText(in.Text); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Typed %d characters", len([]rune(in.Text)))}, nil
}

// KeyPressTool presses named keys and keyboard shortcuts.
type KeyPressTool struct{}

func (t *KeyPressTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "key_press",
		Description: "Press a named key or shortcut. Examples: enter, tab, escape, backspace, delete, up, down, ctrl+c, ctrl+v, alt+tab, ctrl+shift+s, f5.",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"key"},
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key or shortcut (modifiers joined with +)"},
			},
		},
	}
}

func (t *KeyPressTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct{ Key string }
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid args: " + err.Error()}, nil
	}
	if in.Key == "" {
		return &types.ToolResult{Success: false, Error: "key is required"}, nil
	}
	if err := pressKey(in.Key); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Pressed %s", in.Key)}, nil
}
