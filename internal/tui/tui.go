// Package tui provides a Claude Code-style terminal UI for iCode.
//
// Visual design closely mirrors Claude Code:
//   - Welcome banner with version/model/directory
//   - User input at bottom with > prompt
//   - Assistant messages with ✻ decorator
//   - Thinking blocks with dimmed border
//   - Tool calls as compact single lines
//   - Permission prompts with [y/n/a] options
//   - Bottom status bar with model, tokens, cost, mode
//   - Slash commands, !shell mode, @file mentions
//   - Shift+Tab to cycle permission modes
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// ── ANSI Colors ──────────────────────────────────────────────────

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	italic = "\033[3m"

	black   = "\033[30m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
	gray    = "\033[90m"
	brightRed   = "\033[91m"
	brightGreen = "\033[92m"
	brightYellow= "\033[93m"
	brightBlue  = "\033[94m"
	brightCyan  = "\033[96m"

	bgDark = "\033[48;5;236m"
	bgBlue = "\033[44m"
)

// ── Types ────────────────────────────────────────────────────────

type Mode = string
type Role = string

const (
	ModeDefault Mode = "default"
	ModePlan     Mode = "plan"
	ModeAgent    Mode = "agent"
	ModeYOLO     Mode = "yolo"
)

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleError     Role = "error"
	RoleThinking  Role = "thinking"
)

type Message struct {
	Role    Role
	Content string
	Tool    string
	ToolArgs string
}

type Callback interface {
	OnSend(text string)
	OnSlashCommand(cmd string, args []string)
	OnPermissionResponse(decision string)
}

type StreamWriter interface {
	AddMessage(role Role, content string)
	AppendStream(text string)
	EndStream()
	SetStatus(input, output int, cacheHit float64, cost string)
}

type Config struct {
	Mode     Mode
	Model    string
	Provider string
	Callback Callback
}

// ── TUI ──────────────────────────────────────────────────────────

type TUI struct {
	mode     Mode
	model    string
	provider string
	callback Callback
	version  string

	mu       sync.Mutex
	messages []Message

	// Streaming
	streaming  bool
	streamBuf  strings.Builder
	streamDone chan struct{}

	// Token tracking
	promptTokens     int
	completionTokens int
	cacheHitRate     float64
	cost             string

	// Terminal
	running   bool
	rawMode   bool
	width     int
	height    int
	reader    io.Reader
	writer    io.Writer
	oldState  *term.State

	// Input
	inputBuf   []byte
	shellMode  bool // ! prefix
	sigCh      chan os.Signal
}

func New(cfg Config) *TUI {
	return &TUI{
		mode:       cfg.Mode,
		model:      cfg.Model,
		provider:   cfg.Provider,
		callback:   cfg.Callback,
		reader:     os.Stdin,
		writer:     os.Stdout,
		streamDone: make(chan struct{}, 1),
	}
}

// ── Lifecycle ────────────────────────────────────────────────────

func (t *TUI) Run() error {
	doubleClick := !term.IsTerminal(int(os.Stdin.Fd()))

	// Try raw mode
	if state, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		t.rawMode = true
		t.oldState = state
		defer term.Restore(int(os.Stdin.Fd()), state)

		t.width, t.height, _ = term.GetSize(int(os.Stdout.Fd()))
		if t.width < 40 { t.width = 80 }
		if t.height < 10 { t.height = 24 }

		t.sigCh = make(chan os.Signal, 1)
		signal.Notify(t.sigCh, os.Interrupt)
		defer signal.Stop(t.sigCh)

		fmt.Fprint(t.writer, "\033[?25l")
		defer fmt.Fprint(t.writer, "\033[?25h")

		result := t.runRaw()

		if doubleClick {
			fmt.Fprintln(t.writer, "\n\r"+dim+"Press Enter to exit..."+reset)
			bufio.NewReader(t.reader).ReadString('\n')
		}
		return result
	}

	// Line mode fallback
	return t.runLine()
}

func (t *TUI) runRaw() error {
	t.running = true

	// Clear screen
	fmt.Fprint(t.writer, "\033[2J\033[H")

	// Welcome banner (like Claude Code)
	t.printWelcome()
	t.render()

	inputReader := bufio.NewReader(t.reader)

	for t.running {
		// Render input prompt at bottom
		t.renderInput()

		b, err := inputReader.ReadByte()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Handle special keys
		switch b {
		case 3: // Ctrl+C
			if t.streaming {
				t.streaming = false
				t.AddMessage(RoleSystem, dim+"[interrupted]"+reset)
				t.render()
			} else {
				t.running = false
			}

		case 4: // Ctrl+D
			t.running = false

		case 12: // Ctrl+L — clear screen
			fmt.Fprint(t.writer, "\033[2J\033[H")
			t.render()

		case 13, 10: // Enter
			text := strings.TrimSpace(string(t.inputBuf))
			t.inputBuf = t.inputBuf[:0]

			if text == "" {
				continue
			}

			// Shell mode (! prefix)
			if strings.HasPrefix(text, "!") {
				t.execShell(text[1:])
				continue
			}

			// Slash command
			if strings.HasPrefix(text, "/") {
				t.handleSlash(text)
				continue
			}

			// User message
			t.AddMessage(RoleUser, text)
			if t.callback != nil {
				t.streaming = true
				t.streamBuf.Reset()
				go t.callback.OnSend(text)
			}
			t.render()

		case 27: // ESC sequence — check for Shift+Tab etc
			// Read next bytes
			b2, _ := inputReader.ReadByte()
			if b2 == 91 { // [
				b3, _ := inputReader.ReadByte()
				if b3 == 90 { // Shift+Tab — cycle modes
					t.cycleMode()
					t.render()
				}
			}

		case 127, 8: // Backspace
			if len(t.inputBuf) > 0 {
				t.inputBuf = t.inputBuf[:len(t.inputBuf)-1]
			}

		default:
			if b >= 32 && b < 127 {
				t.inputBuf = append(t.inputBuf, b)
			}
		}
	}

	return nil
}

func (t *TUI) runLine() error {
	t.running = true

	t.printWelcomeLine()
	fmt.Fprintln(t.writer)

	reader := bufio.NewReader(t.reader)

	for t.running {
		fmt.Fprint(t.writer, t.promptStr())
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF { return nil }
			continue
		}

		text := strings.TrimSpace(line)
		if text == "" { continue }

		if strings.HasPrefix(text, "!") {
			t.execShell(text[1:])
			continue
		}

		if strings.HasPrefix(text, "/") {
			t.handleSlash(text)
			continue
		}

		t.AddMessage(RoleUser, text)
		if t.callback != nil {
			t.streaming = true
			t.streamBuf.Reset()
			go t.callback.OnSend(text)
		}
		t.drainLineStream()
	}
	return nil
}

func (t *TUI) drainLineStream() {
	for t.streaming {
		select {
		case <-t.streamDone:
			t.streaming = false
		case <-time.After(120 * time.Second):
			t.streaming = false
		}
	}
}

// ── Welcome Banner ───────────────────────────────────────────────

func (t *TUI) printWelcome() {
	w := t.width

	// Top border
	fmt.Fprint(t.writer, dim+strings.Repeat("─", w)+reset+"\r\n")

	// Banner
	cwd, _ := os.Getwd()
	shortCwd := shortDir(cwd)

	banner := fmt.Sprintf(" %s✻ iCode%s %s%s%s  %s%s%s",
		brightCyan, reset,
		dim, "v0.2", reset,
		dim, shortCwd, reset,
	)
	fmt.Fprint(t.writer, banner+"\r\n")

	// Model + mode
	modeLabel := t.modeLabel()
	info := fmt.Sprintf(" %sModel:%s %s  %sMode:%s %s  %sProviders:%s 9",
		dim, reset, t.model,
		dim, reset, modeLabel,
		dim, reset,
	)
	fmt.Fprint(t.writer, info+"\r\n")

	// Bottom border
	fmt.Fprint(t.writer, dim+strings.Repeat("─", w)+reset+"\r\n")

	// Tip
	fmt.Fprint(t.writer, dim+"  Type /help for commands. Shift+Tab to cycle modes. Ctrl+C to interrupt."+reset+"\r\n\r\n")
}

func (t *TUI) printWelcomeLine() {
	cwd, _ := os.Getwd()
	fmt.Fprintln(t.writer)
	fmt.Fprintln(t.writer, dim+strings.Repeat("─", 60)+reset)
	fmt.Fprintf(t.writer, "%s✻ iCode%s %sv0.2%s  %s%s%s\n",
		brightCyan, reset, dim, reset, dim, shortDir(cwd), reset)
	fmt.Fprintf(t.writer, "%sModel:%s %s  %sMode:%s %s\n",
		dim, reset, t.model,
		dim, reset, t.modeLabel())
	fmt.Fprintln(t.writer, dim+strings.Repeat("─", 60)+reset)
	fmt.Fprintln(t.writer, dim+"  Type /help for commands. Ctrl+C to interrupt."+reset)
	fmt.Fprintln(t.writer)
}

func (t *TUI) modeLabel() string {
	switch t.mode {
	case "plan":     return blue + "plan" + reset
	case "agent":    return green + "agent" + reset
	case "yolo":     return brightRed + "yolo" + reset
	default:         return "default"
	}
}

func (t *TUI) promptStr() string {
	modeColor := green
	switch t.mode {
	case "plan":  modeColor = blue
	case "yolo":  modeColor = brightRed
	}
	return modeColor + "❯ " + reset
}

// ── Rendering ────────────────────────────────────────────────────

func (t *TUI) render() {
	if !t.rawMode { return }

	t.mu.Lock()
	defer t.mu.Unlock()

	w := t.width
	h := t.height

	// Move cursor home, clear screen
	fmt.Fprint(t.writer, "\033[H\033[2J")

	// ── Welcome banner ──
	fmt.Fprint(t.writer, dim+strings.Repeat("─", w)+reset+"\r\n")
	cwd, _ := os.Getwd()
	fmt.Fprintf(t.writer, " %s✻ iCode%s %sv0.2%s  %s%s%s\r\n",
		brightCyan, reset, dim, reset, dim, shortDir(cwd), reset)
	fmt.Fprintf(t.writer, " %sModel:%s %s  %sMode:%s %s  %s%s tok%s\r\n",
		dim, reset, t.model,
		dim, reset, t.modeLabel(),
		dim, reset, reset)
	fmt.Fprint(t.writer, dim+strings.Repeat("─", w)+reset+"\r\n\r\n")

	// ── Messages ──
	msgArea := h - 7 // banner(4) + status(2) + input(1)
	if msgArea < 3 { msgArea = 3 }

	// Render all messages, collect lines
	allLines := []string{}
	for _, m := range t.messages {
		allLines = append(allLines, t.renderMessageLines(m, w)...)
	}

	// Show last N lines that fit
	if len(allLines) > msgArea {
		allLines = allLines[len(allLines)-msgArea:]
	}

	// Pad with blank lines if needed
	for i := len(allLines); i < msgArea; i++ {
		fmt.Fprint(t.writer, "\r\n")
	}

	for _, line := range allLines {
		fmt.Fprint(t.writer, line+"\r\n")
	}

	// ── Status bar ──
	fmt.Fprint(t.writer, "\r\n"+dim+strings.Repeat("─", w)+reset+"\r\n")
	fmt.Fprint(t.writer, t.statusBar(w))

	// ── Input prompt ──
	t.renderInput()
}

func (t *TUI) renderInput() {
	if !t.rawMode { return }

	h := t.height
	fmt.Fprintf(t.writer, "\033[%d;1H\033[2K", h)

	modeColor := green
	switch t.mode {
	case "plan":  modeColor = blue
	case "yolo":  modeColor = brightRed
	}

	if t.streaming {
		fmt.Fprint(t.writer, dim+"  ⠼ working..."+reset)
	} else if t.shellMode {
		fmt.Fprint(t.writer, yellow+"! "+reset+string(t.inputBuf))
	} else {
		fmt.Fprint(t.writer, modeColor+"❯ "+reset+string(t.inputBuf))
	}
}

func (t *TUI) renderMessageLines(m Message, w int) []string {
	var lines []string

	switch m.Role {
	case RoleUser:
		// User messages: just the text, slightly indented
		for _, line := range wrapText(m.Content, w-2) {
			lines = append(lines, dim+"> "+reset+line)
		}
		lines = append(lines, "")

	case RoleAssistant:
		// Assistant: ✻ decorator + content
		contentLines := wrapText(m.Content, w-4)
		for i, line := range contentLines {
			if i == 0 {
				lines = append(lines, brightCyan+"✻ "+reset+line)
			} else {
				lines = append(lines, "  "+line)
			}
		}
		lines = append(lines, "")

	case RoleThinking:
		// Thinking block: dimmed with border
		lines = append(lines, dim+"  ┌─ thinking ──────────────────────"+reset)
		for _, line := range wrapText(m.Content, w-6) {
			lines = append(lines, dim+"  │ "+italic+line+reset)
		}
		lines = append(lines, dim+"  └─────────────────────────────────"+reset)
		lines = append(lines, "")

	case RoleTool:
		// Tool call: compact single line with tool name
		toolLine := fmt.Sprintf("  %s⚡ %s%s%s %s",
			yellow, bold, m.Tool, reset, reset)
		if m.ToolArgs != "" {
			toolLine += dim + " " + m.ToolArgs + reset
		}
		lines = append(lines, toolLine)

		// Tool output (if content exists)
		if m.Content != "" {
			for _, line := range wrapText(m.Content, w-6) {
				lines = append(lines, dim+"    "+line+reset)
			}
		}
		lines = append(lines, "")

	case RoleSystem:
		lines = append(lines, dim+"  "+m.Content+reset)
		lines = append(lines, "")

	case RoleError:
		lines = append(lines, red+"  ✖ "+m.Content+reset)
		lines = append(lines, "")
	}

	return lines
}

func (t *TUI) statusBar(w int) string {
	var parts []string

	// Mode indicator
	modeStr := t.modeLabel()
	parts = append(parts, modeStr)

	// Model
	parts = append(parts, dim+t.model+reset)

	// Tokens
	total := t.promptTokens + t.completionTokens
	if total > 0 {
		tc := tokenColor(total)
		parts = append(parts, tc+formatTokens(total)+" tok"+reset)
	}

	// Cost
	if t.cost != "" {
		parts = append(parts, yellow+t.cost+reset)
	}

	// Cache
	if t.cacheHitRate > 0 {
		parts = append(parts, green+fmt.Sprintf("%.0f%%", t.cacheHitRate*100)+reset)
	}

	bar := " " + strings.Join(parts, dim+" · "+reset) + " "
	return bar
}

// ── StreamWriter ─────────────────────────────────────────────────

func (t *TUI) AddMessage(role Role, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: role, Content: content})
	t.mu.Unlock()
	if !t.streaming { t.render() }
}

func (t *TUI) AddToolMessage(tool, toolArgs, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleTool, Content: content, Tool: tool, ToolArgs: toolArgs})
	t.mu.Unlock()
	if !t.streaming { t.render() }
}

func (t *TUI) AppendStream(text string) {
	t.streamBuf.WriteString(text)
	t.mu.Lock()
	// Create assistant message if needed
	if len(t.messages) == 0 || t.messages[len(t.messages)-1].Role != RoleAssistant {
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: ""})
	}
	last := &t.messages[len(t.messages)-1]
	last.Content = t.streamBuf.String()
	t.mu.Unlock()
	if t.rawMode { t.render() } else { fmt.Fprint(t.writer, text) }
}

func (t *TUI) EndStream() {
	t.mu.Lock()
	final := t.streamBuf.String()
	if len(t.messages) > 0 && t.messages[len(t.messages)-1].Content == "" {
		t.messages[len(t.messages)-1].Content = final
	}
	t.mu.Unlock()
	t.streaming = false
	select {
	case t.streamDone <- struct{}{}:
	default:
	}
	t.render()
}

func (t *TUI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.promptTokens = input
	t.completionTokens = output
	t.cacheHitRate = cacheHit
	t.cost = cost
}

// ── Slash Commands ──────────────────────────────────────────────

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 { return }
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		t.AddMessage(RoleSystem, `Available commands:
  /help          Show this help
  /model <id>    Switch model
  /mode <m>      Switch mode (default/plan/agent/yolo)
  /compact       Compact conversation context
  /clear         Clear conversation history
  /session       Show session info
  /resume <id>   Resume a previous session
  /export [file] Export conversation to markdown
  /diff          Show git diff
  /cost          Show cost breakdown
  /exit          Exit iCode

Shortcuts:
  !<cmd>         Run shell command
  @<file>        Reference a file
  Shift+Tab      Cycle permission modes
  Ctrl+L         Clear screen
  Ctrl+C         Interrupt / exit`)

	case "/exit", "/quit":
		t.running = false

	case "/model":
		if len(args) > 0 {
			t.model = args[0]
			t.AddMessage(RoleSystem, fmt.Sprintf("Model → %s", args[0]))
		}

	case "/mode":
		if len(args) > 0 {
			t.mode = args[0]
			t.AddMessage(RoleSystem, fmt.Sprintf("Mode → %s", args[0]))
		}

	case "/session":
		t.AddMessage(RoleSystem, fmt.Sprintf("Session: %d messages · %d↑ %d↓ tokens",
			len(t.messages), t.promptTokens, t.completionTokens))

	case "/compact":
		t.compact()

	case "/resume":
		if len(args) > 0 {
			t.AddMessage(RoleSystem, fmt.Sprintf("Resuming session: %s", args[0]))
		} else {
			t.AddMessage(RoleSystem, "Usage: /resume <session-id>")
		}

	case "/export":
		t.exportMarkdown(args)

	case "/diff":
		t.showGitDiff()

	case "/clear":
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cacheHitRate = 0
		t.cost = ""
		t.mu.Unlock()
		t.render()
		t.AddMessage(RoleSystem, "Conversation cleared.")

	case "/cost":
		info := fmt.Sprintf("Tokens: %d prompt + %d completion = %d total",
			t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens)
		if t.cost != "" {
			info += " · Cost: " + yellow + t.cost + reset
		}
		if t.cacheHitRate > 0 {
			info += fmt.Sprintf(" · Cache: %.0f%%", t.cacheHitRate*100)
		}
		t.AddMessage(RoleSystem, info)

	default:
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// ── Mode Cycling ─────────────────────────────────────────────────

func (t *TUI) cycleMode() {
	modes := []string{"default", "plan", "agent", "yolo"}
	i := 0
	for idx, m := range modes {
		if m == t.mode { i = idx; break }
	}
	t.mode = modes[(i+1)%len(modes)]
	t.AddMessage(RoleSystem, fmt.Sprintf("Mode: %s", t.modeLabel()))
}

// ── Shell Mode ───────────────────────────────────────────────────

func (t *TUI) execShell(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" { return }

	t.AddToolMessage("bash", cmd, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parts := strings.Fields(cmd)
	if len(parts) == 0 { return }

	execCmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	output, err := execCmd.CombinedOutput()

	if err != nil {
		t.AddMessage(RoleError, fmt.Sprintf("%s: %s", err, string(output)))
	} else {
		t.AddToolMessage("output", "", string(output))
	}
	t.render()
}

// ── Compact ──────────────────────────────────────────────────────

func (t *TUI) compact() {
	if len(t.messages) < 4 {
		t.AddMessage(RoleSystem, "Not enough messages to compact.")
		return
	}

	var keep []Message
	var summary strings.Builder
	summary.WriteString("[Compacted] Previous conversation summary:\n")

	count := 0
	for _, m := range t.messages {
		if m.Role == RoleSystem || count >= len(t.messages)-4 {
			keep = append(keep, m)
		} else {
			summary.WriteString(fmt.Sprintf("  %s: %s\n", m.Role, truncate(m.Content, 80)))
			count++
		}
	}

	t.mu.Lock()
	t.messages = keep
	t.mu.Unlock()
	t.render()
	t.AddMessage(RoleSystem, summary.String())
}

// ── Export ───────────────────────────────────────────────────────

func (t *TUI) exportMarkdown(args []string) {
	filename := "icode-export.md"
	if len(args) > 0 {
		filename = args[0]
	}

	var sb strings.Builder
	sb.WriteString("# iCode Conversation Export\n\n")
	sb.WriteString(fmt.Sprintf("**Model:** %s  \n", t.model))
	sb.WriteString(fmt.Sprintf("**Provider:** %s  \n", t.provider))
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n", t.mode))
	sb.WriteString(fmt.Sprintf("**Tokens:** %d prompt + %d completion  \n\n", t.promptTokens, t.completionTokens))
	sb.WriteString("---\n\n")

	for _, m := range t.messages {
		switch m.Role {
		case RoleUser:
			sb.WriteString("## User\n\n")
		case RoleAssistant:
			sb.WriteString("## Assistant\n\n")
		case RoleSystem:
			sb.WriteString("> ")
		case RoleTool:
			sb.WriteString("### Tool: " + m.Tool + "\n\n")
		case RoleError:
			sb.WriteString("### Error\n\n")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}

	if err := os.WriteFile(filename, []byte(sb.String()), 0644); err != nil {
		t.AddMessage(RoleError, fmt.Sprintf("Export failed: %v", err))
		return
	}
	t.AddMessage(RoleSystem, fmt.Sprintf("Exported to %s (%d messages)", filename, len(t.messages)))
}

// ── Git Diff ─────────────────────────────────────────────────────

func (t *TUI) showGitDiff() {
	cmd := exec.Command("git", "diff")
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.AddMessage(RoleError, fmt.Sprintf("git diff: %v", err))
		return
	}
	if len(output) == 0 {
		t.AddMessage(RoleSystem, "No unstaged changes.")
		return
	}
	t.AddToolMessage("git_diff", "", string(output))
}

// ── Helpers ──────────────────────────────────────────────────────

func tokenColor(tokens int) string {
	switch {
	case tokens > 100000: return brightRed
	case tokens > 50000:  return yellow + bold
	case tokens > 20000:  return yellow
	default:              return white
	}
}

func formatTokens(n int) string {
	if n >= 1000000 { return fmt.Sprintf("%.1fM", float64(n)/1000000) }
	if n >= 1000 { return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k" }
	return strconv.Itoa(n)
}

func shortDir(path string) string {
	parts := strings.Split(path, string(os.PathSeparator))
	n := len(parts)
	if n >= 3 {
		return parts[n-3] + "/" + parts[n-2] + "/" + parts[n-1]
	}
	if n >= 2 {
		return parts[n-2] + "/" + parts[n-1]
	}
	return path
}

func wrapText(text string, width int) []string {
	if width < 1 { width = 60 }
	var result []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			result = append(result, "")
			continue
		}
		// Simple word-wrap
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				result = append(result, line)
				line = w
			} else {
				line += " " + w
			}
		}
		result = append(result, line)
	}
	return result
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}

// context import for shell timeout
var _ = context.Background
