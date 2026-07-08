// Package tui provides a cross-platform terminal UI for iCode.
// It uses buffered line input (no raw mode) for maximum compatibility
// across PowerShell, cmd.exe, bash, WSL, SSH, and all terminal emulators.
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// ANSI support detection. Set in init.
var ansiOK bool

func init() {
	// Check if stdout is a terminal and supports ANSI
	fi, _ := os.Stdout.Stat()
	ansiOK = (fi.Mode()&os.ModeCharDevice) != 0
	// On Windows, also check for ConPTY or Windows Terminal
	// via environment variables
	if os.Getenv("WT_SESSION") != "" || os.Getenv("TERM_PROGRAM") != "" {
		ansiOK = true
	}
}

// Color helpers that degrade gracefully
const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cDim    = "\033[2m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cCyan   = "\033[36m"
	cGray   = "\033[90m"
)

func s(seq string) string {
	if !ansiOK { return "" }
	return seq
}

// Mode / Role re-exports for backward compatibility
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

// Message for rendering
type Message struct {
	Role    Role
	Content string
}

// Callback interface — unchanged from before
type Callback interface {
	OnSend(text string)
	OnSlashCommand(cmd string, args []string)
	OnPermissionResponse(decision string)
}

// StreamWriter — callback pushes data back to CLI
type StreamWriter interface {
	AddMessage(role Role, content string)
	AppendStream(text string)
	EndStream()
	SetStatus(input, output int, cacheHit float64, cost string)
}

// Config for CLI
type Config struct {
	Mode     Mode
	Model    string
	Provider string
	Callback Callback
}

// CLI is the main terminal UI.
type CLI struct {
	mode     Mode
	model    string
	provider string
	callback Callback

	mu       sync.Mutex
	messages []Message
	running  bool
	streaming bool
	streamCh chan streamEvent

	// Pending token status
	lastTokens types.TokenUsage

	reader io.Reader
	writer io.Writer
}

type streamEvent struct {
	etype string // "text", "tool", "done", "error"
	text  string
	usage types.TokenUsage
}

// New creates a CLI.
func New(cfg Config) *CLI {
	return &CLI{
		mode:     cfg.Mode,
		model:    cfg.Model,
		provider: cfg.Provider,
		callback: cfg.Callback,
		reader:   os.Stdin,
		writer:   os.Stdout,
		streamCh: make(chan streamEvent, 32),
	}
}

// Run starts the CLI main loop.
func (t *CLI) Run() error {
	t.running = true

	t.AddMessage(RoleSystem, fmt.Sprintf("iCode %s | %s | %s",
		t.provider, t.model, t.mode))
	t.AddMessage(RoleSystem, "Type /help for commands. Type your question and press Enter to send.")
	t.AddMessage(RoleSystem, fmt.Sprintf("%sCtrl+C to cancel / exit%s", s(cDim), s(cReset)))

	fmt.Fprintln(t.writer)

	// Use bufio.Reader for maximum platform compatibility
	reader := bufio.NewReader(t.reader)
	fmt.Fprint(t.writer, prompt(t.mode))

	for t.running {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		text := strings.TrimSpace(line)

		if text == "" {
			if t.streaming {
				// During streaming, don't show prompt
			} else {
				fmt.Fprint(t.writer, prompt(t.mode))
			}
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(text, "/") {
			t.handleSlash(text)
			if !t.running {
				return nil
			}
			if !t.streaming {
				fmt.Fprint(t.writer, prompt(t.mode))
			}
			continue
		}

		// User message
		t.AddMessage(RoleUser, text)
		fmt.Fprintln(t.writer)

		if t.callback != nil {
			t.streaming = true
			go func() {
				t.callback.OnSend(text)
			}()
		}

		// Wait for streaming to complete
		t.drainStream()

		if t.running {
			fmt.Fprint(t.writer, prompt(t.mode))
		}
	}

	return nil
}

func (t *CLI) handleSlash(text string) {
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		fmt.Fprintln(t.writer)
		fmt.Fprintln(t.writer, s(cYellow), "Commands:", s(cReset))
		fmt.Fprintln(t.writer, "  /help          Show this help")
		fmt.Fprintln(t.writer, "  /model <id>    Switch model")
		fmt.Fprintln(t.writer, "  /mode <m>      Switch mode (plan/agent/yolo)")
		fmt.Fprintln(t.writer, "  /session       Show session info")
		fmt.Fprintln(t.writer, "  /clear         Clear history")
		fmt.Fprintln(t.writer, "  /exit          Exit iCode")
		fmt.Fprintln(t.writer, "  Ctrl+C         Cancel streaming / exit seamlessly")
		fmt.Fprintln(t.writer)

	case "/exit", "/quit":
		fmt.Fprintln(t.writer, "Goodbye!")
		t.running = false

	case "/model":
		if len(args) > 0 {
			t.model = args[0]
			t.AddMessage(RoleSystem, fmt.Sprintf("Model: %s", args[0]))
		}

	case "/mode":
		if len(args) > 0 {
			t.mode = args[0]
			t.AddMessage(RoleSystem, fmt.Sprintf("Mode: %s", args[0]))
		}

	case "/session":
		t.AddMessage(RoleSystem, "Active session info (use /clear to reset)")

	case "/clear":
		t.messages = nil
		fmt.Fprintln(t.writer)
		t.AddMessage(RoleSystem, "History cleared. New session started.")

	default:
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// drainStream reads streaming events and prints them in real-time.
func (t *CLI) drainStream() {
	for t.streaming {
		select {
		case ev, ok := <-t.streamCh:
			if !ok {
				t.streaming = false
				return
			}
			switch ev.etype {
			case "text":
				if ansiOK {
					fmt.Fprint(t.writer, s(cGreen), ev.text, s(cReset))
				} else {
					fmt.Fprint(t.writer, ev.text)
				}
			case "tool":
				fmt.Fprintf(t.writer, "\n%s[Tool: %s]%s\n",
					s(cCyan), ev.text, s(cReset))
			case "done":
				if ev.usage.TotalTokens > 0 {
					fmt.Fprintf(t.writer, "\n%s[%d prompt + %d completion tokens]%s\n",
						s(cDim), ev.usage.PromptTokens, ev.usage.CompletionTokens, s(cReset))
				}
				fmt.Fprintln(t.writer)
				t.streaming = false
			case "error":
				fmt.Fprintf(t.writer, "\n%s[Error: %s]%s\n",
					s(cRed), ev.text, s(cReset))
				fmt.Fprintln(t.writer)
				t.streaming = false
			}
		case <-time.After(120 * time.Second):
			fmt.Fprintf(t.writer, "\n%s[Stream timeout]%s\n", s(cRed), s(cReset))
			t.streaming = false
		}
	}
}

// AddMessage adds a message and prints it.
func (t *CLI) AddMessage(role Role, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: role, Content: content})
	t.mu.Unlock()

	if t.streaming {
		return
	}
	t.printMessage(role, content)
}

func (t *CLI) printMessage(role Role, content string) {
	switch role {
	case RoleUser:
		fmt.Fprintf(t.writer, "%s>> %s%s\n", s(cGreen+s(cBold)), content, s(cReset))
	case RoleAssistant:
		if ansiOK {
			fmt.Fprint(t.writer, s(cGreen))
		}
		fmt.Fprint(t.writer, content)
		if ansiOK {
			fmt.Fprint(t.writer, s(cReset))
		}
		fmt.Fprintln(t.writer)
	case RoleSystem:
		fmt.Fprintf(t.writer, "%s%s%s\n", s(cDim), content, s(cReset))
	case RoleError:
		fmt.Fprintf(t.writer, "%s[!] %s%s\n", s(cRed), content, s(cReset))
	case RoleTool:
		fmt.Fprintf(t.writer, "%s[@] %s%s\n", s(cCyan), content, s(cReset))
	}
}

// AppendStream pushes streaming text directly to the output.
func (t *CLI) AppendStream(text string) {
	if !t.streaming {
		t.streaming = true
	}
	t.streamCh <- streamEvent{etype: "text", text: text}
}

// EndStream signals end of streaming with stored token data.
func (t *CLI) EndStream() {
	t.mu.Lock()
	usage := t.lastTokens
	t.lastTokens = types.TokenUsage{}
	t.mu.Unlock()
	t.streamCh <- streamEvent{etype: "done", usage: usage}
}

// SetStatus stores token stats for the next EndStream call.
func (t *CLI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.mu.Lock()
	t.lastTokens = types.TokenUsage{
		PromptTokens:     input,
		CompletionTokens: output,
		TotalTokens:      input + output,
	}
	t.mu.Unlock()
}

// Stop stops the CLI.
func (t *CLI) Stop() {
	t.running = false
}

func prompt(mode string) string {
	switch mode {
	case "plan":
		return s(cBlue) + "plan > " + s(cReset)
	case "yolo":
		return s(cRed) + "yolo > " + s(cReset)
	default:
		return s(cGreen) + "agent > " + s(cReset)
	}
}
