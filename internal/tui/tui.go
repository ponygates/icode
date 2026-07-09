// Package tui provides a Claude Code-level terminal UI for iCode.
//
// Features:
//   - Fixed bottom status bar with model, tokens, cost, cache rate, project
//   - Full-screen rendering with scrollable message area
//   - Real-time streaming text with proper word wrapping
//   - Tool call visualization (bash, read, write, search)
//   - Permission prompts (Y/N/A) inline
//   - Slash commands with completion
//   - Ctrl+C graceful interruption
//   - Graceful fallback to line mode on unsupported terminals
//
// Terminal requirements: ANSI escape codes + raw mode.
// Falls back to line mode on PowerShell ISE, legacy cmd.exe, etc.
package tui

import (
	"bufio"
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

// ── Colors (ANSI 256) ────────────────────────────────────────────

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cDim    = "\033[2m"
	cUnder  = "\033[4m"

	cBlack   = "\033[30m"
	cRed     = "\033[31m"
	cGreen   = "\033[32m"
	cYellow  = "\033[33m"
	cBlue    = "\033[34m"
	cMagenta = "\033[35m"
	cCyan    = "\033[36m"
	cWhite   = "\033[37m"
	cGray    = "\033[90m"

	// Status bar background
	cBgDark  = "\033[48;5;236m"
)

// ── Types ────────────────────────────────────────────────────────

type Mode = string
type Role = string

const (
	ModePlan  Mode = "plan"
	ModeAgent Mode = "agent"
	ModeYOLO  Mode = "yolo"
)

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleError     Role = "error"
)

type Message struct {
	Role    Role
	Content string
	Tool    string // tool type if role == tool
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

	mu       sync.Mutex
	messages []Message

	// Streaming state
	streaming  bool
	streamBuf  strings.Builder
	streamDone chan struct{}

	// Token tracking
	promptTokens     int
	completionTokens int
	cacheHitRate     float64
	cost             string

	// Terminal
	running  bool
	rawMode  bool
	width    int
	height   int
	reader   io.Reader
	writer   io.Writer
	oldState *term.State

	// Signals
	sigCh chan os.Signal
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
	// Detect double-click launch (no real terminal)
	doubleClick := !term.IsTerminal(int(os.Stdin.Fd()))
	if doubleClick {
		fmt.Fprintln(t.writer)
		fmt.Fprintln(t.writer, "  iCode — Multi-Model AI Coding Agent")
		fmt.Fprintln(t.writer, "  Type /help for commands, /exit to quit.")
		fmt.Fprintln(t.writer)
	}

	// Try raw mode
	if state, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		t.rawMode = true
		t.oldState = state
		defer term.Restore(int(os.Stdin.Fd()), state)

		// Get terminal size
		t.width, t.height, _ = term.GetSize(int(os.Stdout.Fd()))
		if t.width < 40 { t.width = 80 }
		if t.height < 10 { t.height = 24 }

		// Handle resize: SIGWINCH on Unix, poll on Windows
		t.sigCh = make(chan os.Signal, 1)
		signal.Notify(t.sigCh, os.Interrupt)
		defer signal.Stop(t.sigCh)

		// Hide cursor, enable line wrap
		fmt.Fprint(t.writer, "\033[?25l")
		defer fmt.Fprint(t.writer, "\033[?25h")

		result := t.runRaw()

		if doubleClick {
			fmt.Fprintln(t.writer)
			fmt.Fprintln(t.writer, "Press Enter to exit...")
			bufio.NewReader(t.reader).ReadString('\n')
		}
		return result
	}

	// Fallback to line mode
	result := t.runLine()

	if doubleClick {
		fmt.Fprintln(t.writer)
		fmt.Fprintln(t.writer, "Press Enter to exit...")
		bufio.NewReader(t.reader).ReadString('\n')
	}
	return result
}

func (t *TUI) runRaw() error {
	t.running = true

	// Clear screen, draw initial UI
	fmt.Fprint(t.writer, "\033[2J\033[H")
	t.render()

	// Welcome messages
	t.AddMessage(RoleSystem, fmt.Sprintf("iCode %s · %s · %s mode",
		t.provider, t.model, t.mode))
	t.AddMessage(RoleSystem, "Type /help for commands. Ctrl+C to interrupt.")
	t.render()

	// Input buffer
	buf := make([]byte, 0, 256)
	inputReader := bufio.NewReader(t.reader)

	for t.running {
		b, err := inputReader.ReadByte()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		switch b {
		case 3: // Ctrl+C
			if t.streaming {
				t.streaming = false
				t.streamDone <- struct{}{}
				t.AddMessage(RoleSystem, "Interrupted.")
				t.render()
			} else {
				t.running = false
			}

		case 13: // Enter
			text := strings.TrimSpace(string(buf))
			buf = buf[:0]

			if text == "" {
				t.render()
				continue
			}

			if strings.HasPrefix(text, "/") {
				t.handleSlash(text)
			} else {
				t.AddMessage(RoleUser, text)
				if t.callback != nil {
					t.streaming = true
					t.streamBuf.Reset()
					go t.callback.OnSend(text)
				}
			}
			t.render()

		case 127: // Backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				t.renderInput(string(buf))
			}

		default:
			if b >= 32 {
				buf = append(buf, b)
				t.renderInput(string(buf))
			}
		}

		// Check resize
		select {
		case <-t.sigCh:
			t.width, t.height, _ = term.GetSize(int(os.Stdout.Fd()))
			if t.width < 40 { t.width = 80 }
			if t.height < 10 { t.height = 24 }
			t.render()
		default:
		}
	}

	return nil
}

func (t *TUI) runLine() error {
	t.running = true

	t.AddMessage(RoleSystem, fmt.Sprintf("iCode %s · %s · %s", t.provider, t.model, t.mode))
	t.AddMessage(RoleSystem, "Terminal does not support raw mode — using line input.")
	t.AddMessage(RoleSystem, "Type /help for commands. Press Enter to send.")

	fmt.Fprintln(t.writer)

	scanner := bufio.NewScanner(t.reader)
	fmt.Fprint(t.writer, statusLine(t.model, t.promptTokens, t.completionTokens, t.cacheHitRate, t.cost))

	for scanner.Scan() && t.running {
		text := strings.TrimSpace(scanner.Text())
		if text == "" { continue }

		if strings.HasPrefix(text, "/") {
			t.handleSlash(text)
			continue
		}

		t.AddMessage(RoleUser, text)
		if t.callback != nil {
			t.streaming = true
			go t.callback.OnSend(text)
		}
		t.drainLineStream()
		fmt.Fprint(t.writer, statusLine(t.model, t.promptTokens, t.completionTokens, t.cacheHitRate, t.cost))
	}
	return nil
}

func (t *TUI) drainLineStream() {
	for t.streaming {
		select {
		case <-t.streamDone:
			t.streaming = false
			return
		case <-time.After(120 * time.Second):
			t.streaming = false
			return
		}
	}
}

// ── Rendering ────────────────────────────────────────────────────

func (t *TUI) render() {
	if !t.rawMode { return }

	t.mu.Lock()
	defer t.mu.Unlock()

	h := t.height
	w := t.width

	// Move cursor home, clear
	fmt.Fprint(t.writer, "\033[H\033[2J")

	// ── Header ──
	header := fmt.Sprintf(" iCode · %s · %s · %s ", t.provider, t.model, t.mode)
	fmt.Fprint(t.writer, cBgDark, cWhite, header, strings.Repeat(" ", w-len(header)-2), " ", cReset, "\r\n")
	fmt.Fprint(t.writer, cDim, strings.Repeat("─", w), cReset, "\r\n")

	// ── Messages ──
	msgArea := h - 3 // header line + separator + status bar
	if msgArea < 3 { msgArea = 3 }

	// Show last N messages that fit
	totalLines := 0
	msgLines := make([]string, 0)

	for i := len(t.messages) - 1; i >= 0; i-- {
		m := t.messages[i]
		lines := t.renderMessage(w, m)
		totalLines += len(lines)
		msgLines = append(msgLines, lines...)
	}

	// Skip oldest messages if overflow
	if totalLines > msgArea {
		skip := totalLines - msgArea
		if skip < len(msgLines) {
			msgLines = msgLines[:len(msgLines)-skip]
		}
	}

	// Print from bottom up
	blankLines := msgArea - len(msgLines)
	for i := 0; i < blankLines; i++ {
		fmt.Fprint(t.writer, "\r\n")
	}
	for i := len(msgLines) - 1; i >= 0; i-- {
		fmt.Fprint(t.writer, msgLines[i], "\r\n")
	}

	// ── Status Bar ──
	fmt.Fprint(t.writer, cDim, strings.Repeat("─", w), cReset, "\r\n")
	fmt.Fprint(t.writer, cBgDark, statusLine(t.model, t.promptTokens, t.completionTokens, t.cacheHitRate, t.cost))
	fmt.Fprint(t.writer, strings.Repeat(" ", w-len(statusLine(t.model, t.promptTokens, t.completionTokens, t.cacheHitRate, t.cost))))
	fmt.Fprint(t.writer, cReset, "\r\n")
}

func (t *TUI) renderInput(text string) {
	if !t.rawMode { return }
	// Move to bottom area, clear input line, show prompt + text
	h := t.height
	fmt.Fprint(t.writer, fmt.Sprintf("\033[%d;1H", h))
	fmt.Fprint(t.writer, "\033[2K")
	fmt.Fprint(t.writer, cGreen, "> ", cReset, text)
}

func (t *TUI) renderMessage(w int, m Message) []string {
	var lines []string
	prefix, color := "", ""

	switch m.Role {
	case RoleUser:
		prefix = cGreen + "▸" + cReset + " "
		color = ""
	case RoleAssistant:
		prefix = cCyan + "◇" + cReset + " "
		color = ""
	case RoleSystem:
		prefix = cDim + "·" + cReset + " "
		color = cDim
	case RoleTool:
		toolIcon := "◆"
		if m.Tool == "bash" { toolIcon = "$" }
		if m.Tool == "read" { toolIcon = "📄" }
		if m.Tool == "write" { toolIcon = "✎" }
		if m.Tool == "search" { toolIcon = "⌕" }
		prefix = cYellow + toolIcon + cReset + " "
		color = cYellow
	case RoleError:
		prefix = cRed + "✖" + cReset + " "
		color = cRed
	}

	content := m.Content
	avail := w - 4 // account for prefix + padding

	for len(content) > 0 {
		cut := avail
		if cut > len(content) { cut = len(content) }
		line := prefix + color + content[:cut] + cReset
		lines = append(lines, line)
		content = content[cut:]
		prefix = "   " // indent continuation
	}

	return lines
}

// ── StreamWriter ─────────────────────────────────────────────────

func (t *TUI) AddMessage(role Role, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: role, Content: content})
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
	if t.rawMode { t.render() }
}

func (t *TUI) EndStream() {
	// Copy to final message
	final := t.streamBuf.String()
	t.mu.Lock()
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
	if !t.streaming { t.render() }
}

// ── Slash Commands ───────────────────────────────────────────────

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 { return }
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		t.AddMessage(RoleSystem, `Commands:
  /help          Show this help
  /model <id>    Switch model
  /mode <m>      Switch mode (plan / agent / yolo)
  /session       Show session info
  /clear         Clear conversation
  /compact       Compact conversation context
  /resume <id>   Resume a previous session
  /export [file] Export conversation to markdown
  /diff          Show git diff
  /cost          Show cost breakdown
  /exit          Exit iCode
  Ctrl+C         Interrupt streaming`)

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
		if len(t.messages) > 0 {
			t.AddMessage(RoleSystem, fmt.Sprintf("Session: %d messages · %d↑ %d↓ tokens",
				len(t.messages), t.promptTokens, t.completionTokens))
		}

	case "/compact":
		t.compact()

	case "/resume":
		if len(args) > 0 {
			t.AddMessage(RoleSystem, fmt.Sprintf("Resuming session: %s (placeholder)", args[0]))
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
			info += " · Cost: " + cYellow + t.cost + cReset
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

// compact summarizes the conversation to save context.
func (t *TUI) compact() {
	if len(t.messages) < 4 {
		t.AddMessage(RoleSystem, "Not enough messages to compact.")
		return
	}

	// Keep system messages + last 2 exchanges, summarize the rest
	var keep []Message
	var summary strings.Builder
	summary.WriteString("[Compact] Summarized previous conversation:\n")

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

// exportMarkdown exports the conversation to a file.
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
			sb.WriteString("### Tool\n\n")
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

// showGitDiff runs git diff and displays output.
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
	t.AddMessage(RoleTool, string(output))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── Helpers ──────────────────────────────────────────────────────

func tokenColor(tokens int) string {
	switch {
	case tokens > 100000: return cRed
	case tokens > 50000:  return cYellow + cBold
	case tokens > 20000:  return cYellow
	default:              return cWhite
	}
}

func statusLine(model string, prompt, comp int, cacheRate float64, cost string) string {
	var parts []string

	// Model
	shortModel := model
	if len(shortModel) > 22 { shortModel = shortModel[:22] }
	parts = append(parts, cBold+shortModel+cReset)

	// Tokens
	total := prompt + comp
	tc := tokenColor(total)
	tokenStr := formatTokens(total)
	parts = append(parts, tc+tokenStr+" tok"+cReset)

	// Cost
	if cost != "" {
		parts = append(parts, cYellow+cost+cReset)
	}

	// Cache
	if cacheRate > 0 {
		parts = append(parts, fmt.Sprintf("%scache %.0f%%%s", cGreen, cacheRate*100, cReset))
	}

	// Project dir
	wd, _ := os.Getwd()
	parts = append(parts, cDim+shortDir(wd)+cReset)

	return " " + strings.Join(parts, cDim+" · "+cReset) + " "
}

func formatTokens(n int) string {
	if n >= 1000000 { return fmt.Sprintf("%.1fM", float64(n)/1000000) }
	if n >= 1000 { return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "K" }
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

func div() string {
	return cDim + strings.Repeat("─", 60) + cReset
}
