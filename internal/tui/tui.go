// Package tui provides a Claude Code-style terminal UI for iCode.
// Uses pure line-mode rendering — no raw mode, no alternate screen.
// Works in PowerShell, cmd, bash, Windows Terminal, SSH.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// ── Types ────────────────────────────────────────────────────────

type Mode = string
type Role = string

const (
	ModeDefault Mode = "default"
	ModePlan    Mode = "plan"
	ModeAgent   Mode = "agent"
	ModeYOLO    Mode = "yolo"
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
	Role     Role
	Content  string
	Tool     string
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

	mu       sync.Mutex
	messages []Message

	streaming  bool
	streamBuf  strings.Builder
	streamDone chan struct{}

	promptTokens     int
	completionTokens int
	cacheHitRate     float64
	cost             string

	running bool
	reader  io.Reader
	writer  io.Writer
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
	t.running = true

	// Welcome banner (Claude Code style)
	t.printBanner()

	reader := bufio.NewReader(t.reader)

	for t.running {
		// Print prompt
		fmt.Fprint(t.writer, t.promptStr())

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			continue
		}

		text := strings.TrimSpace(line)
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
		t.printUser(text)
		if t.callback != nil {
			t.streaming = true
			t.streamBuf.Reset()
			go t.callback.OnSend(text)
		}
		t.drainStream()
	}

	return nil
}

func (t *TUI) drainStream() {
	for t.streaming {
		select {
		case <-t.streamDone:
			t.streaming = false
		case <-time.After(120 * time.Second):
			t.streaming = false
		}
	}
}

// ── Banner (Claude Code style) ───────────────────────────────────

func (t *TUI) printBanner() {
	cwd, _ := os.Getwd()
	shortCwd := shortDir(cwd)

	// Separator
	sep := strings.Repeat("─", 60)
	fmt.Fprintln(t.writer)
	fmt.Fprintln(t.writer, sep)

	// Title line
	fmt.Fprintf(t.writer, "✻ iCode v0.2  %s\n", shortCwd)

	// Info line
	fmt.Fprintf(t.writer, "  Model: %s  Mode: %s\n", t.model, t.mode)

	fmt.Fprintln(t.writer, sep)
	fmt.Fprintln(t.writer, "  /help for commands, ! for shell, Ctrl+C to exit")
	fmt.Fprintln(t.writer)
}

func (t *TUI) promptStr() string {
	switch t.mode {
	case "plan":
		return "plan ❯ "
	case "yolo":
		return "yolo ❯ "
	default:
		return "❯ "
	}
}

// ── Message Printing ─────────────────────────────────────────────

func (t *TUI) printUser(text string) {
	fmt.Fprintf(t.writer, "  > %s\n\n", text)
}

func (t *TUI) printAssistant(text string) {
	// Claude Code style: ✻ prefix for assistant
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(t.writer, "✻ %s\n", line)
		} else {
			fmt.Fprintf(t.writer, "  %s\n", line)
		}
	}
	fmt.Fprintln(t.writer)
}

func (t *TUI) printSystem(text string) {
	fmt.Fprintf(t.writer, "  %s\n\n", text)
}

func (t *TUI) printError(text string) {
	fmt.Fprintf(t.writer, "✖ %s\n\n", text)
}

func (t *TUI) printTool(tool, args, output string) {
	// Compact tool call display
	if args != "" {
		fmt.Fprintf(t.writer, "⚡ %s %s\n", tool, args)
	} else {
		fmt.Fprintf(t.writer, "⚡ %s\n", tool)
	}
	if output != "" {
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			fmt.Fprintf(t.writer, "  %s\n", line)
		}
	}
	fmt.Fprintln(t.writer)
}

func (t *TUI) printStatusBar() {
	total := t.promptTokens + t.completionTokens
	parts := []string{t.mode, t.model}
	if total > 0 {
		parts = append(parts, formatTokens(total)+" tok")
	}
	if t.cost != "" {
		parts = append(parts, t.cost)
	}
	if t.cacheHitRate > 0 {
		parts = append(parts, fmt.Sprintf("%.0f%% cache", t.cacheHitRate*100))
	}
	fmt.Fprintf(t.writer, "%s\n\n", strings.Join(parts, " · "))
}

// ── StreamWriter ─────────────────────────────────────────────────

func (t *TUI) AddMessage(role Role, content string) {
	switch role {
	case RoleUser:
		t.printUser(content)
	case RoleAssistant:
		t.printAssistant(content)
	case RoleSystem:
		t.printSystem(content)
	case RoleError:
		t.printError(content)
	default:
		t.printSystem(content)
	}
}

func (t *TUI) AddToolMessage(tool, toolArgs, content string) {
	t.printTool(tool, toolArgs, content)
}

func (t *TUI) AppendStream(text string) {
	t.streamBuf.WriteString(text)
	// Print streaming text directly (no re-render)
	fmt.Fprint(t.writer, text)
}

func (t *TUI) EndStream() {
	final := t.streamBuf.String()
	if strings.TrimSpace(final) != "" {
		fmt.Fprintln(t.writer)
	}
	t.streaming = false
	select {
	case t.streamDone <- struct{}{}:
	default:
	}
}

func (t *TUI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.promptTokens = input
	t.completionTokens = output
	t.cacheHitRate = cacheHit
	t.cost = cost
}

// ── Slash Commands ───────────────────────────────────────────────

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		t.printSystem(`Commands:
  /help            Show this help
  /model <id>      Switch model
  /mode <name>     Switch mode (default/plan/agent/yolo)
  /compact         Compact conversation context
  /clear           Clear conversation
  /session         Show session info
  /resume <id>     Resume a session
  /export [file]   Export to markdown
  /diff            Show git diff
  /cost            Show cost breakdown
  /exit            Exit iCode

Shortcuts:
  !<command>       Run shell command
  Ctrl+C           Exit`)

	case "/exit", "/quit":
		fmt.Fprintln(t.writer, "Goodbye!")
		t.running = false

	case "/model":
		if len(args) > 0 {
			t.model = args[0]
			t.printSystem(fmt.Sprintf("Model → %s", args[0]))
		}

	case "/mode":
		if len(args) > 0 {
			t.mode = args[0]
			t.printSystem(fmt.Sprintf("Mode → %s", args[0]))
		}

	case "/session":
		t.printSystem(fmt.Sprintf("%d messages · %d↑ %d↓ tokens",
			len(t.messages), t.promptTokens, t.completionTokens))

	case "/compact":
		t.compact()

	case "/resume":
		if len(args) > 0 {
			t.printSystem(fmt.Sprintf("Resuming: %s", args[0]))
		} else {
			t.printSystem("Usage: /resume <session-id>")
		}

	case "/export":
		t.exportMarkdown(args)

	case "/diff":
		t.showGitDiff()

	case "/clear":
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.printSystem("Conversation cleared.")

	case "/cost":
		info := fmt.Sprintf("Tokens: %d prompt + %d completion = %d total",
			t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens)
		if t.cost != "" {
			info += " · Cost: " + t.cost
		}
		if t.cacheHitRate > 0 {
			info += fmt.Sprintf(" · Cache: %.0f%%", t.cacheHitRate*100)
		}
		t.printSystem(info)

	default:
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// ── Shell Mode ───────────────────────────────────────────────────

func (t *TUI) execShell(cmdStr string) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return
	}

	fmt.Fprintf(t.writer, "⚡ bash %s\n", cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return
	}

	execCmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	output, err := execCmd.CombinedOutput()

	if err != nil {
		fmt.Fprintf(t.writer, "✖ %s\n", err)
	}
	if len(output) > 0 {
		// Print output with indentation
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			fmt.Fprintf(t.writer, "  %s\n", line)
		}
	}
	fmt.Fprintln(t.writer)
}

// ── Compact ──────────────────────────────────────────────────────

func (t *TUI) compact() {
	if len(t.messages) < 4 {
		t.printSystem("Not enough messages to compact.")
		return
	}

	var keep []Message
	var summary strings.Builder
	summary.WriteString("[Compacted] Summary:\n")

	count := 0
	for _, m := range t.messages {
		if m.Role == RoleSystem || count >= len(t.messages)-4 {
			keep = append(keep, m)
		} else {
			summary.WriteString(fmt.Sprintf("  %s: %s\n", m.Role, truncate(m.Content, 80)))
			count++
		}
	}

	t.messages = keep
	t.printSystem(summary.String())
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
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n\n", t.mode))
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
		t.printError(fmt.Sprintf("Export failed: %v", err))
		return
	}
	t.printSystem(fmt.Sprintf("Exported to %s (%d messages)", filename, len(t.messages)))
}

// ── Git Diff ─────────────────────────────────────────────────────

func (t *TUI) showGitDiff() {
	cmd := exec.Command("git", "diff")
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.printError(fmt.Sprintf("git diff: %v", err))
		return
	}
	if len(output) == 0 {
		t.printSystem("No unstaged changes.")
		return
	}
	t.printTool("git_diff", "", string(output))
}

// ── Helpers ──────────────────────────────────────────────────────

func formatTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// unused but needed for interface compliance
var _ = term.IsTerminal
