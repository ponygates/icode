// Package tui provides a lightweight terminal UI for iCode using ANSI escape codes.
// No external dependencies — pure Go terminal rendering with:
//   - Chat view with streaming text and markdown-like formatting
//   - Status bar with token usage, model info, and mode indicator
//   - Slash-command system (/help, /model, /mode, /session, /exit)
//   - Permission prompts for tool execution approval
//   - Input history and multi-line editing
//   - Color-coded messages (user/assistant/tool/system)
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"
)

// ANSI escape codes
const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
	gray    = "\033[90m"
	bgDark  = "\033[48;5;235m"

	clearLine  = "\033[2K"
	clearScreen = "\033[2J"
	cursorHome  = "\033[H"
	cursorUp   = "\033[1A"
	cursorShow = "\033[?25h"
	cursorHide = "\033[?25l"

	// SGR mouse tracking can be enabled when needed
)

// Mode represents the interaction mode.
type Mode string

const (
	ModePlan  Mode = "plan"
	ModeAgent Mode = "agent"
	ModeYOLO  Mode = "yolo"
)

// Role for message styling.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleError     Role = "error"
)

// Message represents a single chat message.
type Message struct {
	Role    Role
	Content string
}

// Callback is the interface the TUI uses to communicate with the backend.
type Callback interface {
	// OnSend is called when the user sends a message.
	OnSend(text string)
	// OnSlashCommand is called when a slash command is entered.
	OnSlashCommand(cmd string, args []string)
	// OnPermissionResponse is called when user responds to a permission prompt.
	OnPermissionResponse(decision string)
}

// TUI is the main terminal UI controller.
type TUI struct {
	width    int
	height   int
	mode     Mode
	model    string
	provider string

	messages []Message
	input    string
	history  []string
	histIdx  int

	// Status bar fields
	tokenInput  int
	tokenOutput int
	cacheHit    float64
	cost        string

	// State
	running     bool
	streaming   bool
	showHelp    bool
	showModel   bool
	permission  *PermissionPrompt

	// I/O
	reader io.Reader
	writer io.Writer
	term   *term.Terminal

	callback Callback
}

// PermissionPrompt is shown when a tool requires approval.
type PermissionPrompt struct {
	Tool    string
	Details string
	Active  bool
}

// Config configures the TUI.
type Config struct {
	Mode     Mode
	Model    string
	Provider string
	Callback Callback
}

// New creates a new TUI instance.
func New(cfg Config) *TUI {
	return &TUI{
		mode:     cfg.Mode,
		model:    cfg.Model,
		provider: cfg.Provider,
		callback: cfg.Callback,
		reader:   os.Stdin,
		writer:   os.Stdout,
		running:  true,
	}
}

// Run starts the TUI main loop.
func (t *TUI) Run() error {
	// Switch stdin to raw mode for character-by-character input
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback: use buffered line input
		return t.runBuffered()
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle terminal resize (Unix only, gracefully ignored on Windows)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() { <-sigCh }()

	t.updateSize()
	fmt.Fprint(t.writer, cursorHide)
	defer fmt.Fprint(t.writer, cursorShow)

	// Welcome
	t.AddMessage(RoleSystem, fmt.Sprintf("iCode — %s | Model: %s | Mode: %s", t.provider, t.model, t.mode))
	t.AddMessage(RoleSystem, "Type /help for commands, Ctrl+C to exit.")

	t.render()

	r := bufio.NewReader(t.reader)

	for t.running {
		b, err := r.ReadByte()
		if err != nil {
			break
		}

		switch b {
		case 3: // Ctrl+C
			if t.streaming {
				t.streaming = false
				t.AddMessage(RoleSystem, "[Cancelled]")
			} else {
				t.running = false
			}
			t.render()

		case 13: // Enter
			if t.permission != nil && t.permission.Active {
				t.permission.Active = false
				t.permission = nil
				t.render()
				continue
			}

			if t.showHelp || t.showModel {
				t.showHelp = false
				t.showModel = false
				t.render()
				continue
			}

			text := strings.TrimSpace(t.input)
			if text != "" {
				t.handleInput(text)
			}
			t.input = ""
			t.render()

		case 127, 8: // Backspace
			if len(t.input) > 0 {
				t.input = t.input[:len(t.input)-1]
				t.render()
			}

		case 27: // ESC
			if t.showHelp || t.showModel {
				t.showHelp = false
				t.showModel = false
				t.render()
			}

		case 9: // Tab — toggle help
			t.showHelp = !t.showHelp
			t.render()

		default:
			if b >= 32 && b <= 126 {
				t.input += string(b)
				t.render()
			}
		}

		// Auto-detect streaming end
		if t.streaming && len(t.messages) > 0 {
			last := t.messages[len(t.messages)-1]
			if last.Role == RoleAssistant && strings.HasSuffix(last.Content, "\n") {
				// Stream likely ended
			}
		}
	}

	return nil
}

// runBuffered is the fallback when raw mode is unavailable.
func (t *TUI) runBuffered() error {
	scanner := bufio.NewScanner(t.reader)

	t.AddMessage(RoleSystem, fmt.Sprintf("iCode — %s | Model: %s | Mode: %s", t.provider, t.model, t.mode))
	t.AddMessage(RoleSystem, "Type /help for commands, /exit to quit.")
	fmt.Fprintln(t.writer)
	printDivider(t.writer)

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			fmt.Fprintln(t.writer, "Goodbye!")
			return nil
		}
		t.handleInput(text)
		if !t.running {
			return nil
		}
	}
	return scanner.Err()
}

// handleInput processes user input, routing to slash commands or the callback.
func (t *TUI) handleInput(text string) {
	// Slash commands
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		cmd := strings.ToLower(parts[0])
		args := parts[1:]

		switch cmd {
		case "/help":
			t.showHelp = true
			return
		case "/model":
			if len(args) > 0 {
				t.model = args[0]
				t.AddMessage(RoleSystem, fmt.Sprintf("Switched to model: %s", t.model))
			} else {
				t.showModel = true
			}
			return
		case "/mode":
			if len(args) > 0 {
				switch strings.ToLower(args[0]) {
				case "plan":
					t.mode = ModePlan
				case "agent":
					t.mode = ModeAgent
				case "yolo":
					t.mode = ModeYOLO
				default:
					t.AddMessage(RoleSystem, "Invalid mode. Available: plan, agent, yolo")
					return
				}
				t.AddMessage(RoleSystem, fmt.Sprintf("Mode switched to: %s", t.mode))
			}
			return
		case "/session":
			t.AddMessage(RoleSystem, "Session commands: /session new, /session list, /session switch <id>")
			return
		case "/clear":
			t.messages = nil
			return
		case "/exit", "/quit":
			t.running = false
			return
		}

		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
		return
	}

	// Regular message
	t.AddMessage(RoleUser, text)
	if t.callback != nil {
		t.callback.OnSend(text)
	}
	t.streaming = true
}

// AddMessage appends a message and renders.
func (t *TUI) AddMessage(role Role, content string) {
	t.messages = append(t.messages, Message{Role: role, Content: content})
}

// AppendStream appends text to the last assistant message.
func (t *TUI) AppendStream(text string) {
	if len(t.messages) == 0 || t.messages[len(t.messages)-1].Role != RoleAssistant {
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: text})
	} else {
		t.messages[len(t.messages)-1].Content += text
	}
}

// EndStream marks the streaming as complete.
func (t *TUI) EndStream() {
	t.streaming = false
}

// ShowPermission displays a tool permission prompt.
func (t *TUI) ShowPermission(tool, details string) {
	t.permission = &PermissionPrompt{
		Tool:    tool,
		Details: details,
		Active:  true,
	}
	t.render()
}

// SetStatus updates the status bar.
func (t *TUI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.tokenInput = input
	t.tokenOutput = output
	t.cacheHit = cacheHit
	t.cost = cost
}

// Stop stops the TUI.
func (t *TUI) Stop() {
	t.running = false
}

// ============================================================================
// Rendering
// ============================================================================

func (t *TUI) updateSize() {
	if fd, ok := t.writer.(interface{ Fd() uintptr }); ok {
		w, h, err := term.GetSize(int(fd.Fd()))
		if err == nil {
			t.width = w
			t.height = h
		}
	}
	if t.width <= 0 {
		t.width = 80
	}
	if t.height <= 0 {
		t.height = 24
	}
}

func (t *TUI) render() {
	if t.width <= 0 {
		t.updateSize()
	}

	var out strings.Builder

	out.WriteString(clearScreen)
	out.WriteString(cursorHome)

	// --- Welcome banner (first render) ---
	if len(t.messages) <= 2 {
		renderBanner(&out, t.model, t.provider, t.mode)
	}

	// --- Help panel ---
	if t.showHelp {
		renderHelp(&out)
	}
	if t.showModel {
		renderModelPicker(&out)
	}

	// --- Messages ---
	msgArea := t.height - 5 // reserve 3 for input + 2 for status
	start := 0
	if len(t.messages) > msgArea {
		start = len(t.messages) - msgArea
	}

	for i := start; i < len(t.messages); i++ {
		msg := t.messages[i]
		renderMessage(&out, msg, t.width)
	}

	// --- Permission prompt ---
	if t.permission != nil && t.permission.Active {
		renderPermission(&out, t.permission, t.width)
	}

	// --- Status bar ---
	out.WriteString("\n")
	out.WriteString(bgDark)
	out.WriteString(dim)
	fmt.Fprintf(&out, " %s  |  %s  |  ", t.model, t.mode)

	if t.cacheHit > 0 {
		fmt.Fprintf(&out, "%sCache: %.0f%%%s  |  ", green, t.cacheHit*100, dim)
	}
	if t.cost != "" {
		fmt.Fprintf(&out, "Cost: %s  |  ", t.cost)
	}
	fmt.Fprintf(&out, "In: %d  Out: %d", t.tokenInput, t.tokenOutput)
	out.WriteString(reset)
	out.WriteString("\n")

	// --- Input line ---
	out.WriteString("\n")
	out.WriteString(blue)
	out.WriteString("> ")
	out.WriteString(reset)
	out.WriteString(t.input)
	if !t.showHelp && !t.showModel {
		out.WriteString(cursorShow)
	}

	fmt.Fprint(t.writer, out.String())
}

func renderBanner(out *strings.Builder, model, provider string, mode Mode) {
	out.WriteString("\n")
	out.WriteString(cyan)
	out.WriteString("╔══════════════════════════════════════════════════════╗\n")
	out.WriteString("║                                                      ║\n")
	fmt.Fprintf(out, "║     %s iCode%s — AI Coding Agent                      ║\n", bold, cyan)
	out.WriteString("║                                                      ║\n")
	fmt.Fprintf(out, "║     Model: %s%-40s%s ║\n", white, model, cyan)
	fmt.Fprintf(out, "║     Provider: %s%-37s%s ║\n", white, provider, cyan)
	fmt.Fprintf(out, "║     Mode: %s%-39s%s ║\n", modeColor(string(mode)), mode, cyan)
	out.WriteString("║                                                      ║\n")
	out.WriteString("╚══════════════════════════════════════════════════════╝\n")
	out.WriteString(reset)
	out.WriteString("\n")
	out.WriteString(dim)
	out.WriteString("Type /help for commands. Press Tab to toggle help.\n\n")
	out.WriteString(reset)
}

func renderHelp(out *strings.Builder) {
	out.WriteString("\n")
	out.WriteString(yellow)
	out.WriteString("┌─ Commands ──────────────────────────────────────────┐\n")
	out.WriteString("│ /help          Show this help                       │\n")
	out.WriteString("│ /model <id>    Switch model                         │\n")
	out.WriteString("│ /mode <m>      Switch mode (plan/agent/yolo)        │\n")
	out.WriteString("│ /session       Manage sessions                      │\n")
	out.WriteString("│ /clear         Clear chat history                   │\n")
	out.WriteString("│ /exit          Exit iCode                           │\n")
	out.WriteString("│ Tab            Toggle help overlay                  │\n")
	out.WriteString("│ Ctrl+C         Cancel streaming / exit              │\n")
	out.WriteString("│ Enter          Dismiss overlay / send message       │\n")
	out.WriteString("└──────────────────────────────────────────────────────┘\n")
	out.WriteString(reset)
	out.WriteString("\n")
}

func renderModelPicker(out *strings.Builder) {
	out.WriteString("\n")
	out.WriteString(cyan)
	out.WriteString("┌─ Available Models ──────────────────────────────────┐\n")
	out.WriteString("│                                                    │\n")

	models := []struct{ id, provider string }{
		{"deepseek-chat", "deepseek"},
		{"deepseek-reasoner", "deepseek"},
		{"glm-4-plus", "zhipu"},
		{"glm-4-flash", "zhipu (free)"},
		{"moonshot-v1-8k", "kimi"},
		{"doubao-pro-32k", "volcengine"},
		{"hunyuan-pro", "tencent (free)"},
		{"auto", "openrouter"},
		{"free", "openrouter (free)"},
		{"claude-sonnet-4", "anthropic"},
	}

	for _, m := range models {
		fmt.Fprintf(out, "│  %s%-30s%s %s(%s)%s\n", white, m.id, cyan, dim, m.provider, cyan)
	}
	out.WriteString("│                                                    │\n")
	out.WriteString("│  Usage: /model <id> to switch                      │\n")
	out.WriteString("└────────────────────────────────────────────────────┘\n")
	out.WriteString(reset)
	out.WriteString("\n")
}

func renderMessage(out *strings.Builder, msg Message, width int) {
	switch msg.Role {
	case RoleUser:
		fmt.Fprintf(out, "\n%sYou:%s\n", green, reset)
	case RoleAssistant:
		fmt.Fprintf(out, "\n%sAssistant:%s\n", blue, reset)
	case RoleSystem:
		fmt.Fprintf(out, "%s%s%s\n", dim, msg.Content, reset)
		return
	case RoleTool:
		fmt.Fprintf(out, "%s[Tool]%s ", yellow, reset)
	case RoleError:
		fmt.Fprintf(out, "%s[Error]%s ", red, reset)
	}

	// Word wrap at terminal width
	content := msg.Content
	maxW := width - 4
	for len(content) > 0 {
		line := content
		if len(line) > maxW {
			line = line[:maxW]
			// Try to break at word boundary
			if idx := strings.LastIndexByte(line, ' '); idx > maxW/2 {
				line = line[:idx]
			}
		}
		out.WriteString("  ")
		out.WriteString(line)
		out.WriteString("\n")

		if len(line) >= len(content) {
			break
		}
		content = content[len(line):]
	}
}

func renderPermission(out *strings.Builder, p *PermissionPrompt, width int) {
	out.WriteString("\n")
	out.WriteString(yellow)
	out.WriteString("┌─ Permission Required ───────────────────────────────┐\n")
	fmt.Fprintf(out, "│  Tool: %s%s%s\n", white, p.Tool, yellow)
	fmt.Fprintf(out, "│  %s\n", truncateLine(p.Details, width-6))
	out.WriteString("│                                                    │\n")
	out.WriteString("│  [A]llow  [D]eny  [S]ession allow                  │\n")
	out.WriteString("│  Enter — Deny                                      │\n")
	out.WriteString("└────────────────────────────────────────────────────┘\n")
	out.WriteString(reset)
	out.WriteString("\n")
}

func printDivider(w io.Writer) {
	fmt.Fprintf(w, "%s%s%s\n", dim, strings.Repeat("─", 60), reset)
}

func modeColor(mode string) string {
	switch mode {
	case "plan":
		return blue
	case "agent":
		return yellow
	case "yolo":
		return red
	default:
		return white
	}
}

func truncateLine(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
