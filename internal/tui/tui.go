// Package tui provides a Claude Code-style terminal UI for iCode.
//
// Two rendering paths:
//   - Raw mode (full screen): used when stdin is a real TTY and the console
//     supports virtual-terminal processing. Provides a fixed bottom status
//     bar, in-place streaming, tool-call rendering, and a thinking box — the
//     signature Claude Code look.
//   - Line mode (fallback): used when stdin is not a TTY (piped input, logged
//     output) or raw mode is unavailable. Plain text, no ANSI, no garble.
//
// The Windows console code page is forced to UTF-8 and VT is enabled by
// cmd.fixConsoleCodepage, so glyphs and colors render correctly.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"golang.org/x/term"
)

// ── Types ────────────────────────────────────────────────────────

type Mode = string
type Role = string

const (
	ModeAuto  Mode = "auto"
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
	RoleThinking  Role = "thinking"
)

type Message struct {
	Role     Role
	Content  string
	Tool     string
	ToolArgs string
}

// Callback bridges user input / slash commands back to the backend.
type Callback interface {
	OnSend(text string)
	OnSlashCommand(cmd string, args []string)
	OnPermissionResponse(decision string)
	// OnInterrupt is called when the user presses Esc or Ctrl+C during
	// streaming to cancel the current LLM turn.
	OnInterrupt()
	// OnListSessions returns a formatted list of past sessions.
	OnListSessions() string
	// OnResume loads a past session's messages; returns a status line.
	OnResume(id string) string
	// TodoCounts returns the current session's todo counts for the status
	// bar. Returns all zeros when there is no active session or no list.
	TodoCounts() (pending, active, done, total int)
	// SessionID returns the active session ID, or "".
	SessionID() string
	// OnStatus returns a formatted system status report (providers, keys,
	// MCP servers, cache stats, etc.).
	OnStatus() string
}

// StreamWriter is the surface the backend uses to push data into the UI.
type StreamWriter interface {
	AddMessage(role Role, content string)
	AddToolMessage(tool, toolArgs, content string)
	AppendToolResult(content string)
	AppendStream(text string)
	EndStream()
	SetStatus(input, output int, cacheHit float64, cost string)
}

// Config configures a new TUI.
type Config struct {
	Mode     Mode
	Model    string
	Provider string
	Lang     string // UI language: zh-CN | zh-TW | en
	Theme    string // UI theme: auto | dark | light
	Callback Callback
}

// ── TUI ──────────────────────────────────────────────────────────

type TUI struct {
	mode          Mode
	model         string
	provider      string
	models        []string // available models (for Tab switching)
	modelIdx      int      // current index in models slice
	lang          string
	theme         string
	securityLevel string
	callback      Callback

	// input autocomplete state (raw mode)
	acOpen  bool
	acItems []acItem
	acIdx   int

	// tool output folding (Claude Code-style)
	toolFolded bool

	mu       sync.Mutex
	messages []Message

	streaming  bool
	streamBuf  strings.Builder
	streamDone chan struct{}

	// renderPending / renderTimer coalesce full-screen redraws so a burst of
	// streamed tokens doesn't trigger one expensive redraw per chunk.
	renderPending bool
	renderTimer   *time.Timer

	// turnStart timestamps when a generation begins (for the status bar clock).
	turnStart time.Time

	promptTokens     int
	completionTokens int
	cacheHitRate     float64
	cost             string

	contextTokens int // prompt tokens of the latest request (context-window usage estimate)
	contextWindow int // model context window (in tokens)
	animRunning   bool
	dirEntries    []string // cached top-level cwd listing for the explorer pane

	running bool
	reader  io.Reader      // raw mode: *bufio.Reader
	writer  io.Writer

	// raw-mode state
	rawMode  bool
	color    bool
	width    int
	height   int
	inputBuf string
	cursor   int
	history  []string
	histIdx  int

	// renderMu serializes terminal writes (the streaming goroutine also renders).
	renderMu sync.Mutex

	// multiline toggles multi-line input mode. When enabled, Enter inserts a
	// newline into the input buffer instead of submitting; Alt+Enter submits.
	multiline bool

	// pending permission prompt (agent mode, interactive approval)
	permPending bool
	permPrompt  string

	// welcomeVisible controls the Claude Code-style startup banner (big ASCII
	// logo + model/dir info). Shown on a fresh session until dismissed via
	// Esc/Enter, the first keystroke, or the /welcome command.
	welcomeVisible bool

	// scrollOffset tracks how many lines the user has scrolled up from the
	// bottom of the conversation. 0 means "auto-follow" (the default).
	scrollOffset int

	// statusNotice is a one-line flash message shown in the status bar (e.g.
	// "✓ Model switched to deepseek-v4-flash"), cleared after the next render.
	statusNotice string

	// lastRenderW, lastRenderH track the dimensions used in the last frame
	// so render() can detect a size change and issue a full clear.
	lastRenderW int
	lastRenderH int
}

// New creates a TUI instance. Security level defaults to "local" (safest).
// Unlike Claude Code, iCode NEVER sends telemetry or usage data anywhere.
func New(cfg Config) *TUI {
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	secLvl := "local"
	if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
		secLvl = string(c.SecurityLevel)
	}
	return &TUI{
		mode:           cfg.Mode,
		model:          cfg.Model,
		provider:       cfg.Provider,
		lang:           cfg.Lang,
		theme:          cfg.Theme,
		securityLevel:  secLvl,
		callback:       cfg.Callback,
		reader:         os.Stdin,
		writer:         os.Stdout,
		streamDone:     make(chan struct{}, 1),
		width:          80,
		height:         24,
		lastRenderW:    80,
		lastRenderH:    24,
		histIdx:        -1,
		dirEntries:     listCwd(),

		welcomeVisible: true, // show the startup banner on a fresh session
	}
}

// ── Lifecycle ────────────────────────────────────────────────────

// Run selects the best available rendering mode and starts the loop.
func (t *TUI) Run() error {
	t.running = true

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		if state, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, state)
			t.rawMode = true
			t.color = true
			// Initial terminal-size measurement uses termSize() (tries both
			// stdin and stdout handles) so alt-screen switching and Windows
			// console quirks don't leave the UI at default 80×24.
			if w, h, ok := t.termSize(); ok {
				t.width, t.height = w, h
			}
			return t.runRaw()
		}
	}
	return t.runLine()
}

// ── Raw mode (full screen) ───────────────────────────────────────

func (t *TUI) runRaw() error {
	t.writer = os.Stdout
	// Enter the alternate screen buffer, clear it, and home the cursor before
	// the first paint. This is essential: without it, anything printed before
	// the TUI started (startup logs, the CLI banner) remains in the scrollback,
	// and on consoles that address rows relative to the screen *buffer* rather
	// than the visible window (notably legacy Windows conhost) the absolute
	// row moves in render() land above the viewport — so the header and the top
	// of the ASCII logo get painted off-screen ("top half of the banner
	// missing"). The alternate screen gives us a clean, window-sized canvas
	// where row 1 is always the top of what the user sees. We restore the
	// original screen (and cursor) on exit.
	// Enter the alternate screen buffer and *force the visible window to the
	// top of a clean canvas* before the first paint. This is critical: a
	// centred banner computed for a height larger than the real visible window
	// (or any pre-TUI output still in the scrollback) would leave the top rows
	// of the ASCII logo painted above the viewport — the "top half of the
	// banner missing" symptom.
	//
	// We combine three measures so it works whether or not the terminal honours
	// the alternate screen:
	//   • \x1b[?1049h  enter alt screen (clean, window-sized canvas)
	//   • \x1b[3J       erase the scrollback history (xterm) so the window
	//                  can never stay scrolled down to old content
	//   • \x1b[2J\x1b[H clear the screen and home the cursor to (1,1)
	//   • \x1b[?25l     hide the cursor while painting
	// On terminals that ignore ?1049h the 3J/2J/H trio still scroll the window
	// to the top and clear it, so the banner's top is always visible.
	fmt.Fprint(t.writer, "\x1b[?1049h\x1b[3J\x1b[2J\x1b[H\x1b[?25l")
	defer fmt.Fprint(t.writer, "\x1b[?25h\x1b[?1049l")

	// Attempt to resize the terminal window to a comfortable size for the TUI.
	// Uses ANSI escape \x1b[8;H;Wt supported by Windows Terminal, xterm, etc.
	// Does nothing (silently fails) on terminals that don't support it.
	t.resizeTerminal()

	// Immediately re-measure the terminal size before the first render.
	// The measurement in Run() can be stale on Windows where GetConsoleScreen-
	// BufferInfo may return cached values from before the alternate-screen
	// switch. Using termSize() (stdin+stdout) gives the most reliable result.
	// This also captures the result of the resizeTerminal() call above.
	if w, h, ok := t.termSize(); ok {
		t.width, t.height = w, h
	}
	// Also record the initial dimensions as last-rendered so the first render
	// doesn't spuriously trigger a full clear.
	t.lastRenderW, t.lastRenderH = t.width, t.height
	t.render()
	go t.watchResize()
	t.reader = bufio.NewReader(t.reader)

	for t.running {
		t.render()
		r, _, err := t.reader.(*bufio.Reader).ReadRune()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !t.handleKey(r) {
			break
		}
	}
	return nil
}

// watchResize polls the terminal size and reapplies it whenever the window
// changes. We poll (instead of relying on SIGWINCH) because SIGWINCH is not
// available on Windows, where a large share of users run iCode. The 150ms
// cadence is imperceptible and only triggers a repaint on an actual change, so
// it costs nothing while the size is stable.
func (t *TUI) watchResize() {
	lastW, lastH := 0, 0
	t.mu.Lock()
	lastW, lastH = t.width, t.height
	t.mu.Unlock()
	for t.running {
		time.Sleep(150 * time.Millisecond)
		if w, h, ok := t.termSize(); ok {
			t.mu.Lock()
			changed := w != lastW || h != lastH
			if changed {
				lastW, lastH = w, h
				t.width, t.height = w, h
			}
			t.mu.Unlock()
			if changed && t.rawMode {
				// Clear the whole screen once on a size change so a shrink can
				// never leave orphaned rows from the taller previous frame,
				// then repaint from a clean canvas at the new dimensions.
				t.renderMu.Lock()
				fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
				t.renderMu.Unlock()
				t.render()
			}
		}
	}
}

// dismissWelcome hides the startup banner if it is currently showing and the
// input line is empty (so an in-progress command is never discarded). It
// returns true when it closed the banner.
func (t *TUI) dismissWelcome() bool {
	t.mu.Lock()
	visible := t.welcomeVisible && t.inputBuf == ""
	if visible {
		t.welcomeVisible = false
	}
	t.mu.Unlock()
	if visible {
		t.render()
	}
	return visible
}

// handleKey processes a single input rune in raw mode.
// Returns false to signal the loop should exit.
func (t *TUI) handleKey(r rune) bool {
	switch r {
	case 0x03: // Ctrl+C
		if t.streaming {
			if t.callback != nil {
				t.callback.OnInterrupt()
			}
			return true
		}
		if t.inputBuf == "" {
			fmt.Fprint(t.writer, "\r\n")
			t.running = false
			return false
		}
		t.inputBuf = ""
		t.cursor = 0
		return true
	case 0x04: // Ctrl+D
		if t.inputBuf == "" && !t.streaming {
			t.running = false
			return false
		}
		return true
	case 0x0c: // Ctrl+L — clear & redraw
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		return true
	case 0x0b: // Ctrl+K — clear input buffer
		t.inputBuf = ""
		t.cursor = 0
		return true
	case 0x0f: // Ctrl+O — dismiss welcome
		if t.welcomeVisible {
			t.dismissWelcome()
			return true
		}
	case 0x01: // Ctrl+A — home
		t.cursor = 0
		return true
	case 0x05: // Ctrl+E — end
		t.cursor = len([]rune(t.inputBuf))
		return true
	case 0x10: // Ctrl+P — history prev OR move suggestion cursor up
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx > 0 {
				t.acIdx--
			}
			return true
		}
		t.historyPrev()
		return true
	case 0x0e: // Ctrl+N — history next OR move suggestion cursor down
		if t.acOpen && len(t.acItems) > 0 {
			if t.acIdx < len(t.acItems)-1 {
				t.acIdx++
			}
			return true
		}
		t.historyNext()
		return true
	case 0x09: // Tab — accept suggestion OR cycle model
		if t.acOpen && len(t.acItems) > 0 {
			t.acceptSuggestion()
			return true
		}
		if len(t.models) > 1 && !t.streaming {
			// Cycle to next model
			t.modelIdx = (t.modelIdx + 1) % len(t.models)
			t.model = t.models[t.modelIdx]
			t.add(RoleSystem, "Tab → "+t.model)
			return true
		}
		return true
	case 0x1b:
		// Arrow keys / escape sequences: read the next bytes from the
		// buffered reader. Without this, ReadRune would consume each byte
		// as a separate rune and stray "[" / "A" characters would appear
		// in the input buffer — the "garbled text on up/down" bug.
		if br, ok := t.reader.(*bufio.Reader); ok {
			if ur, _, err := br.ReadRune(); err == nil {
				// Alt+Enter — submit current input (useful in multi-line mode).
				if ur == '\r' || ur == '\n' {
					text := strings.TrimSpace(t.inputBuf)
					t.inputBuf = ""
					t.cursor = 0
					if text != "" {
						t.pushHistory(text)
						t.submit(text)
					}
					return true
				}
				if ur == '[' {
				dir, _, err2 := br.ReadRune(); err2IsNil := err2 == nil
				if err2IsNil {
					switch dir {
					case 'A': // ↑ history prev
						if t.acOpen && len(t.acItems) > 0 {
							if t.acIdx > 0 { t.acIdx-- }
						} else { t.historyPrev() }
						return true
					case 'B': // ↓ history next
						if t.acOpen && len(t.acItems) > 0 {
							if t.acIdx < len(t.acItems)-1 { t.acIdx++ }
						} else { t.historyNext() }
						return true
					case 'C': // → cursor right
						runes := []rune(t.inputBuf)
						if t.cursor < len(runes) { t.cursor++ }
						return true
					case 'D': // ← cursor left
						if t.cursor > 0 { t.cursor-- }
						return true
					case 'H': t.cursor = 0; return true   // Home
					case 'F': t.cursor = len([]rune(t.inputBuf)); return true // End
					case '5': // PgUp (^[[5~)
						br.ReadRune() // consume trailing '~'
						t.scrollPgUp()
						return true
					case '6': // PgDn (^[[6~)
						br.ReadRune() // consume trailing '~'
						t.scrollPgDn()
						return true
					}
				}
			}
			}
		}
		// Plain Esc — stop streaming, dismiss panels, or welcome screen
		if t.streaming {
			if t.callback != nil {
				t.callback.OnInterrupt()
			}
			return true
		}
		if t.acOpen {
			t.acOpen = false
			t.acItems = nil
			return true
		}
		if t.dismissWelcome() {
			return true
		}
		return true
	case '\r', '\n':
		if t.multiline {
			// In multi-line mode, Enter inserts a newline. Submit with Alt+Enter.
			t.inputBuf += "\n"
			t.cursor++
			return true
		}
		text := strings.TrimSpace(t.inputBuf)
		t.inputBuf = ""
		t.cursor = 0
		if text == "" {
			if t.dismissWelcome() {
				return true
			}
			return true
		}
		t.pushHistory(text)
		t.submit(text)
		return true
	case 0x7f, 0x08: // Backspace / DEL
		t.deleteAtCursor()
		t.updateSuggestions()
		return true
	}

	if r < 0x20 {
		// Ignore other control characters.
		return true
	}

	// Printable rune (incl. Chinese) — insert at cursor.
	// The first keystroke also clears the welcome banner so typing feels
	// immediate (Claude Code does the same).
	if t.dismissWelcome() {
		// banner dismissed; fall through to insert the rune into a clean prompt
	}
	runes := []rune(t.inputBuf)
	if t.cursor >= len(runes) {
		t.inputBuf += string(r)
	} else {
		runes = append(runes, 0)
		copy(runes[t.cursor+1:], runes[t.cursor:])
		runes[t.cursor] = r
		t.inputBuf = string(runes)
	}
	t.cursor++
	t.updateSuggestions()
	return true
}

func (t *TUI) deleteAtCursor() {
	runes := []rune(t.inputBuf)
	if t.cursor == 0 || len(runes) == 0 {
		return
	}
	runes = append(runes[:t.cursor-1], runes[t.cursor:]...)
	t.inputBuf = string(runes)
	t.cursor--
}

func (t *TUI) historyPrev() {
	if len(t.history) == 0 {
		return
	}
	if t.histIdx == -1 {
		t.histIdx = len(t.history) - 1
	} else if t.histIdx > 0 {
		t.histIdx--
	}
	t.inputBuf = t.history[t.histIdx]
	t.cursor = len([]rune(t.inputBuf))
}

func (t *TUI) historyNext() {
	if len(t.history) == 0 || t.histIdx == -1 {
		return
	}
	if t.histIdx < len(t.history)-1 {
		t.histIdx++
		t.inputBuf = t.history[t.histIdx]
	} else {
		t.histIdx = -1
		t.inputBuf = ""
	}
	t.cursor = len([]rune(t.inputBuf))
}

func (t *TUI) pushHistory(s string) {
	if len(t.history) == 0 || t.history[len(t.history)-1] != s {
		t.history = append(t.history, s)
	}
	t.histIdx = -1
}

func (t *TUI) submit(text string) {
	// Shell mode (! prefix)
	if strings.HasPrefix(text, "!") {
		t.execShell(text[1:])
		return
	}
	// Quick memory append (# prefix) — matches Claude Code's `#` shortcut.
	// The text after the `#` is written to the user memory file (~/.icode/
	// CLAUDE.md) and NOT sent to the LLM. This lets users capture a
	// preference in-line without leaving the chat.
	if strings.HasPrefix(text, "#") {
		t.appendMemory(strings.TrimSpace(text[1:]))
		return
	}
	// Slash command
	if strings.HasPrefix(text, "/") {
		t.handleSlash(text)
		return
	}

	// User message — expand @file references first.
	expanded := t.expandFileRefs(text)
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: expanded})
	t.scrollOffset = 0 // auto-follow on new turn
	t.mu.Unlock()

	if t.callback != nil {
		t.mu.Lock()
		t.streaming = true
		t.streamBuf.Reset()
		t.turnStart = time.Now()
		t.mu.Unlock()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(expanded)
		}()
		t.ensureAnim()
		t.drainStream()
	}
}

func (t *TUI) drainStream() {
	<-t.streamDone
	t.streaming = false
}

// ── Rendering (raw mode) ─────────────────────────────────────────

func (t *TUI) render() {
	// Snapshot all state under the data mutex, then write under the render
	// mutex. This keeps render() safe to call from the streaming goroutine
	// (which also appends streamed text and triggers renders) without
	// re-locking t.mu. Width/height are also snapshotted here so a concurrent
	// resize (watchResize) cannot race the render.
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	streaming := t.streaming
	streamContent := t.streamBuf.String()
	inputBuf := t.inputBuf
	cursor := t.cursor
	rawMode := t.rawMode
	status := t.statusLine()
	permPending := t.permPending
	permPrompt := t.permPrompt
	welcomeVisible := t.welcomeVisible
	W := t.width
	H := t.height
	t.mu.Unlock()

	// Clear one-shot flash notice after rendering
	if t.statusNotice != "" {
		t.mu.Lock()
		t.statusNotice = ""
		t.mu.Unlock()
	}

	if !rawMode {
		return // line mode renders incrementally, not full-screen
	}

	// Re-measure terminal dimensions on every render so window resizes are
	// always picked up, even when watchResize's polling misses the change.
	// Update t.width/t.height so other goroutines see the latest values too.
	// Try both stdin and stdout handles — on Windows, GetConsoleScreenBufferInfo
	// may require an output handle on some system configurations.
	if w, h, ok := t.termSize(); ok {
		W, H = w, h
		t.mu.Lock()
		t.width, t.height = w, h
		t.mu.Unlock()
	}

	if W < 20 {
		W = 20
	}
	if H < 10 {
		H = 10
	}

	// ── Layout ───────────────────────────────────────────────────
	// The input owns the bottom 3 rows: 1 prompt line + 1 hint line + 1 status
	// line. Everything else (header, conversation, status bar) lives above.
	const inputRows = 3
	contentRows := H - inputRows
	if contentRows < 4 {
		contentRows = 4
	}

	// Bottom status bar: a single compact line (model · tokens · context% ·
	// cost · cache), truncated to the terminal width so it stays a thin strip.
	statusLine := status
	if visibleWidth(statusLine) > W {
		statusLine = truncVisible(statusLine, W)
	}
	statusW := []string{statusLine}

	// Overlays drawn above the input box.
	acLines := t.autocompleteLines()
	permLines := []string{}
	if permPending {
		// Claude Code-style bordered permission box
		title := "⏸ " + t.tstr("perm.title")
		boxW := min(visibleWidth(title)+4, W-4)
		if boxW < 40 {
			boxW = 40
		}
		if boxW > W-4 {
			boxW = W - 4
		}
		// Truncate the prompt to the inner width by *visible* columns so a
		// long CJK command can't push the right │ out of line.
		prompt := truncVisible(permPrompt, boxW-2)
		opts := "[1] 允许   [2] 全部允许   [3] 拒绝"
		permLines = append(permLines,
			t.paint("yellow", "  ╭"+repeat("─", boxW)+"╮"),
			t.paint("yellow", "  │ ")+t.paint("bold", title)+padVisible("", boxW-visibleWidth(title)-2)+t.paint("yellow", " │"),
			t.paint("dim", "  │ ")+prompt+padVisible("", boxW-visibleWidth(prompt)-2)+t.paint("dim", " │"),
			t.paint("yellow", "  │ ")+t.paint("dim", opts)+padVisible("", boxW-visibleWidth(opts)-2)+t.paint("yellow", " │"),
			t.paint("yellow", "  ╰"+repeat("─", boxW)+"╯"),
		)
	}

	// Body height = rows between the top header/hrule and the bottom (status
	// separator + status). Claude Code is a single column, so the conversation
	// takes the full terminal width.
	bodyH := contentRows - 1 /*header*/ - 2 /*hrule×2*/ - len(statusW) - len(permLines) - len(acLines)
	if bodyH < 3 {
		bodyH = 3
	}

	conv := t.conversationLines(msgs, streaming, streamContent, W)
	if welcomeVisible && len(msgs) == 0 && !streaming {
		welcome := t.welcomeLines(W, bodyH)
		// Start the welcome at row 1 (right after the hrule). No extra
		// topMargin — that was pushing the logo partially off-screen
		// on terminals whose initial height measurement was inaccurate.
		conv = welcome
		// Welcome mode always shows the latest — reset scroll.
		t.scrollOffset = 0
	} else if len(conv) > bodyH && t.scrollOffset > 0 {
		// User has scrolled up: show N lines above the bottom.
		total := len(conv)
		maxOff := total - bodyH
		if t.scrollOffset > maxOff {
			t.scrollOffset = maxOff
		}
		start := total - bodyH - t.scrollOffset
		conv = conv[start : start+bodyH]
		// Prepend a scroll indicator.
		indicator := t.paint("yellow", fmt.Sprintf("  ↑ %d more lines — PgDn/End to follow", t.scrollOffset))
		conv = append([]string{""}, conv...)          // blank line
		conv = append([]string{indicator}, conv...)    // indicator
		if len(conv) > bodyH {
			conv = conv[:bodyH]
		}
	} else if len(conv) > bodyH {
		// Auto-follow: always show the latest content.
		t.scrollOffset = 0
		conv = conv[len(conv)-bodyH:]
	}

	// Assemble the full screen (everything except the final input box).
	var out []string
	out = append(out, t.headerLine())
	out = append(out, t.hrule(W))
	out = append(out, conv...)
	for _, sl := range statusW {
		out = append(out, sl)
	}
	for _, pl := range permLines {
		out = append(out, pl)
	}
	for _, al := range acLines {
		out = append(out, al)
	}
	if len(out) > contentRows {
		out = out[len(out)-contentRows:]
	}

	// ── Write frame (absolute rows, in-place clear) ──────────────
	t.renderMu.Lock()
	defer t.renderMu.Unlock()

	// Full clear when dimensions change, so orphaned text from the previous
	// frame at a different size never bleeds into the new layout.
	if W != t.lastRenderW || H != t.lastRenderH {
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		t.lastRenderW, t.lastRenderH = W, H
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?25l") // hide cursor while repainting
	for i, ln := range out {
		row := i + 1
		if row > contentRows {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		buf.WriteString(ln)
	}
	// Clear any rows left between the content block and the input box so old
	// text from a taller previous frame never lingers.
	for row := len(out) + 1; row <= contentRows; row++ {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
	}
	fmt.Fprint(t.writer, buf.String())
	t.drawInputBox(W, H, inputBuf, cursor, streaming)
}

// headerLine renders the compact top bar: app name (Claude Code's ✻ glyph
// style), working directory, and mode — the signature Claude Code header.
func (t *TUI) headerLine() string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	modeLabel := t.mode
	if modeLabel == "" {
		modeLabel = ModeAuto
	}
	return t.paint("orange", "✻ iCode") + " " + appVersionStr() +
		t.paint("dim", "  ·  ") + short +
		t.paint("dim", "  ·  mode: ") + modeLabel
}

// welcomeLines renders the Claude Code-style welcome screen: a single bordered
// box with two columns inside — left shows model/usage/path, right shows tips
// and "what's new" notes. The whole box is in the orange/red Claude accent
// color to match the reference screenshot exactly.
func (t *TUI) welcomeLines(width, maxH int) []string {
	if maxH < 1 || width < 30 {
		return nil
	}

	// Show the full two-column welcome if it fits.
	box := t.welcomeBox(width)
	if box != nil && len(box) <= maxH {
		return box
	}

	// Fallback: minimal welcome on narrow terminals
	tagline := t.paint("orange", "✦") + "  " + t.paint("bold", "Welcome to iCode")
	return []string{"  " + tagline}
}

// welcomeBox returns the two-column Claude Code welcome panel wrapped in an
// orange border, or nil if the terminal is too narrow to fit it.
func (t *TUI) welcomeBox(width int) []string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)

	left := []string{
		t.paint("orange", "  iCode "+appVersionStr()),
		"",
		"  " + t.paint("bold", "Welcome back!"),
		"",
		"  " + t.paint("orange", "✦") + " " + t.paint("orange", "✦") + " " + t.paint("orange", "✦"),
		"    " + t.paint("orange", "◆"),
		"  " + t.paint("orange", "✦") + " " + t.paint("orange", "✦") + " " + t.paint("orange", "✦"),
		"",
		"  " + t.paint("dim", t.model) + "  " + t.paint("dim", "·") + "  API Usage  " + t.paint("dim", "·") + "  Billing",
		"  " + t.paint("dim", short),
	}

	right := []string{
		t.paint("orange", "Tips for getting started"),
		t.paint("orange", "─────────────────────"),
		"  Run /init to create a ICODE.md file with",
		"  instructions for iCode",
		"",
		t.paint("orange", "What's new"),
		t.paint("orange", "──────────"),
		"  Check the iCode changelog for updates",
	}

	// Build the box with the two columns side by side
	leftW := 0
	for _, l := range left {
		if vw := visibleWidth(l); vw > leftW {
			leftW = vw
		}
	}
	rightW := 0
	for _, l := range right {
		if vw := visibleWidth(l); vw > rightW {
			rightW = vw
		}
	}

	gap := 4
	totalW := leftW + gap + rightW + 4 // 4 = padding
	if totalW > width-2 {
		// Too narrow — trim
		if rightW > 20 {
			rightW = 20
		}
		totalW = leftW + gap + rightW + 4
		if totalW > width-2 {
			return nil
		}
	}

	bar := repeat("─", totalW-2)
	top := t.paint("orange", "╭"+bar+"╮")
	bot := t.paint("orange", "╰"+bar+"╯")
	out := []string{top}

	maxRows := len(left)
	if len(right) > maxRows {
		maxRows = len(right)
	}
	for i := 0; i < maxRows; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		// Pin each column to its measured width with fitVis() — which both
		// pads AND hard-truncates to the exact visible width. The left column
		// is always ≤ leftW, but after the narrow-terminal trim path rightW
		// may be smaller than some right-column lines, so truncating (not just
		// padding) keeps every row's right │ aligned with the ╮/╯ corners.
		row := t.paint("orange", "│") + " " + fitVis(l, leftW) +
			strings.Repeat(" ", gap) + fitVis(r, rightW) + " " + t.paint("orange", "│")
		out = append(out, row)
	}
	out = append(out, bot)
	return out
}

// welcomeLeft returns lines for the left panel: model, provider, cwd.
func (t *TUI) welcomeLeft(width int) []string {
	cwd, _ := os.Getwd()
	prefix := "  "
	contentW := width - visibleWidth(prefix)
	if contentW < 10 {
		contentW = 10
	}

	var out []string
	out = append(out, t.paint("dim", prefix+"Model")+":    "+t.model)
	out = append(out, t.paint("dim", prefix+"Provider")+": "+t.provider)
	out = append(out, t.paint("dim", prefix+"Mode")+":     "+t.mode)
	out = append(out, t.paint("dim", prefix+"cwd")+":     "+shortDir(cwd))
	out = append(out, "")
	out = append(out, t.paint("dim", prefix+t.tstr("welcome.hint")))
	out = append(out, t.paint("dim", prefix+t.tstr("welcome.close")))
	return out
}

// welcomeRight returns lines for the right panel: quick commands.
func (t *TUI) welcomeRight(width int) []string {
	prefix := ""
	var out []string
	out = append(out, t.paint("dim", prefix+"Commands:"))
	out = append(out, "")
	out = append(out, t.paint("dim", prefix+"  /help")+"    "+t.tstr("cmd.help"))
	out = append(out, t.paint("dim", prefix+"  /model")+"   "+t.tstr("cmd.model"))
	out = append(out, t.paint("dim", prefix+"  /provider")+" "+t.tstr("cmd.provider"))
	out = append(out, t.paint("dim", prefix+"  /mode")+"    "+t.tstr("cmd.mode"))
	out = append(out, t.paint("dim", prefix+"  /clear")+"   "+t.tstr("cmd.clear"))
	out = append(out, "")
	out = append(out, t.paint("dim", prefix+"/exit  /quit  "+t.tstr("cmd.exit")))
	return out
}

func (t *TUI) messageLinesW(m Message, width int) []string {
	switch m.Role {
	case RoleThinking:
		return thinkingLines(m.Content, width)
	case RoleUser:
		return wrapPrefixed("  ⏣ ", "    ", m.Content, width)
	case RoleAssistant:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleSystem:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleError:
		return wrapPrefixed("  × ", "    ", m.Content, width)
	case RoleTool:
		var out []string
		pre := "  ⏺ "
		head := pre + m.Tool
		// Hide empty/no-op parameter objects like "{}" so the tool line
		// shows "⏺ git_status" instead of "⏺ git_status {}".
		args := strings.TrimSpace(m.ToolArgs)
		if args == "{}" || args == "" {
			args = ""
		}
		if args != "" {
			head += " " + truncate(args, 60)
		}
		out = append(out, t.paint("cyan", head))
		if m.Content != "" {
			toolOutput := m.Content
			// Claude Code-style: fold long tool output with a summary line
			const maxLines = 8
			if !t.toolFolded {
				fold := strings.Split(toolOutput, "\n")
				if len(fold) > maxLines {
					toolOutput = strings.Join(fold[:maxLines], "\n") + "\n" +
						t.paint("dim", fmt.Sprintf("    ⎿  ... %d more lines (use /expand to show all)", len(fold)-maxLines))
				}
			}
			for _, l := range wrapPrefixed("    ⎿ ", "      ", toolOutput, width) {
				out = append(out, t.paint("dim", l))
			}
		}
		return out
	}
	return wrapPrefixed("  ", "  ", m.Content, width)
}

// conversationLines builds the full-width conversation: every message (+ the
// in-flight stream). Claude Code-style thin dim separators divide turns.
// While the model is "thinking" (stream started but no tokens yet) a prominent
// animated thinking box is shown with the rotating spinner and sliding gradient bar.
func (t *TUI) conversationLines(msgs []Message, streaming bool, streamContent string, width int) []string {
	sep := t.paint("dim", "  "+strings.Repeat("─", min(width-2, 80)))
	var lines []string
	all := append([]Message{}, msgs...)
	if streaming {
		all = append(all, Message{Role: RoleAssistant, Content: streamContent})
	}
	for i, m := range all {
		// Replace the empty in-flight assistant message with the thinking box.
		if streaming && i == len(all)-1 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		// Insert thin separator between turns, except before the first message
		// or before a tool message (which is part of the same assistant turn).
		if i > 0 && m.Role != RoleTool && all[i-1].Role != RoleAssistant {
			lines = append(lines, "")
			lines = append(lines, sep)
			lines = append(lines, "")
		} else if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, t.messageLinesW(m, width)...)
	}
	if streaming && strings.TrimSpace(streamContent) == "" {
		lines = append(lines, "")
		lines = append(lines, t.thinkingBox(width)...)
	}
	return lines
}

// thinkingBox renders the framed "thinking" indicator — a bordered box
// containing the spinning glyph and sliding gradient bar, styled to match
// Claude Code's streaming thinking state. Returns individual lines.
func (t *TUI) thinkingBox(width int) []string {
	// Build a headline line with the spinner and slider
	headline := "  " + t.tstr("status.gen") + "  " + t.thinkingBar()

	boxW := width - 2
	if boxW < 24 {
		boxW = 24
	}
	inner := t.paint("dim", "│") + " " + padVisible(headline, boxW-4) + " " + t.paint("dim", "│")
	top := t.paint("dim", "┌"+strings.Repeat("─", boxW-2)+"┐")
	bot := t.paint("dim", "└"+strings.Repeat("─", boxW-2)+"┘")

	return []string{top, inner, bot}
}

// padVisible pads s with spaces to reach the given display width, accounting
// for embedded ANSI escape sequences that take zero visible columns.
func padVisible(s string, w int) string {
	vw := visibleWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

// contextBar renders a "NN% ▓▓░░" usage meter of width w (display columns).
// Colour-coded: green (<50%), yellow (50–80%), red (>80%) — Claude Code style.
func (t *TUI) contextBar(w int) string {
	if w < 6 {
		return strings.Repeat("░", w)
	}
	pct := 0
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct = t.contextTokens * 100 / t.contextWindow
	}
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	barW := w - 4
	if barW < 1 {
		barW = 1
	}
	filled := barW * pct / 100

	// Colour-coded threshold
	colour := t.c("green") // < 50%
	if pct > 80 {
		colour = t.c("red")
	} else if pct > 50 {
		colour = t.c("yellow")
	}
	pctStr := fmt.Sprintf("%2d%%", pct)
	if pct > 80 {
		pctStr = t.paint("bold", pctStr)
	}
	reset := "\x1b[0m"
	if !t.color {
		colour, reset = "", ""
		pctStr = fmt.Sprintf("%2d%%", pct)
	}
	return colour + pctStr + " " + strings.Repeat("▓", filled) + strings.Repeat("░", barW-filled) + reset
}

// thinkingBar is an animated "thinking" indicator inspired by Claude Code's
// glimmer bar. It combines a rotating spinner on the left with a growing
// gradient bar on the right that sweeps back and forth. The combined effect
// gives smooth, continuous motion feedback while the model works.
//
// Visual:  ◌ [▓▓▓▓▓░░░░░░░░░]  32%  ⏱ 12s
func (t *TUI) thinkingBar() string {
	const trackLen = 16
	elapsed := time.Since(t.turnStart)
	frame := int(elapsed.Milliseconds() / 100)
	if frame < 0 {
		frame = 0
	}

	// ── Spinner ──
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[frame%len(spinners)]

	// ── Context progress bar ──
	// Use actual context usage when available, otherwise animate.
	var filled int
	if t.contextWindow > 0 && t.contextTokens > 0 {
		filled = t.contextTokens * trackLen / t.contextWindow
		if filled > trackLen {
			filled = trackLen
		}
	} else {
		// Animated growing bar during stream
		filled = (frame % (trackLen + 1))
	}

	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < trackLen; i++ {
		if i < filled {
			if i < filled-1 {
				b.WriteString("▓")
			} else {
				b.WriteString("▒")
			}
		} else {
			b.WriteString("░")
		}
	}
	b.WriteString("]")

	// Context percentage
	var pctStr string
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		pctStr = fmt.Sprintf(" %d%%", pct)
	}

	return t.paint("cyan", spinner) + " " + b.String() + t.paint("dim", pctStr)
}

// fit returns s padded (or truncated with an ellipsis) to exactly w *visible*
// columns, so split panes line up under the vertical frame line. ANSI escape
// sequences are measured as zero width so colored lines still align.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) > w {
		return truncVisible(s, w)
	}
	return s + strings.Repeat(" ", w-visibleWidth(s))
}

// fitVis pins s to EXACTLY w visible columns: it hard-truncates (no overflow,
// even for CJK runes that would otherwise push truncVisible one cell past w)
// and pads with spaces. ANSI escape sequences are measured as zero width and
// preserved, so colored box content still aligns. It is used to build framed
// boxes whose borders must line up on every row regardless of content width.
func fitVis(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		cw := runeWidth(ch)
		if cur+cw > w {
			break
		}
		b.WriteRune(ch)
		cur += cw
	}
	vw := visibleWidth(b.String())
	if vw < w {
		b.WriteString(strings.Repeat(" ", w-vw))
	}
	return b.String()
}

// visibleWidth returns the display width of s, ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		w += runeWidth(r)
	}
	return w
}

// truncVisible truncates s to w visible columns, preserving ANSI sequences and
// appending an ellipsis if anything was cut. A reset code is appended when a
// colored run is cut, so color never bleeds past the frame line.
func truncVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	cut := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		if cur >= w-1 {
			cut = true
			break
		}
		b.WriteRune(ch)
		cur += runeWidth(ch)
	}
	if cut {
		if inEsc {
			b.WriteString("\x1b[0m") // close any open color before the ellipsis
		}
		b.WriteString("…")
	}
	return b.String()
}

// ensureAnim starts a lightweight ticker that repaints the screen while a
// generation is in flight, so the thinking bar keeps sliding even between
// token bursts. It is idempotent.
func (t *TUI) ensureAnim() {
	t.mu.Lock()
	if t.animRunning {
		t.mu.Unlock()
		return
	}
	t.animRunning = true
	t.mu.Unlock()
	go func() {
		for t.streaming || t.running {
			t.mu.Lock()
			if !t.streaming {
				t.animRunning = false
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
			t.scheduleRender()
			time.Sleep(110 * time.Millisecond)
		}
		t.mu.Lock()
		t.streaming = false
		t.animRunning = false
		t.mu.Unlock()
	}()

}

// SetContext records the latest request's prompt-token count and the model's
// context window so the status bar / explorer can show live context usage.
func (t *TUI) SetContext(tokens, window int) {
	t.mu.Lock()
	t.contextTokens = tokens
	t.contextWindow = window
	t.mu.Unlock()
}

// listCwd returns the top-level entries of the current working directory
// (dotfiles and hidden entries skipped), used by the explorer pane.
func listCwd() []string {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 14 {
		names = names[:14]
	}
	return names
}

// statusLine renders the Claude Code-style bottom status bar: a colored model
// dot, provider, token usage, context %, cache hit rate, and running cost.
// All coloring is applied here (the renderer appends the line verbatim, so no
// nested ANSI wrapping occurs). During generation it shows the elapsed time
// and the sliding thinking bar.
func (t *TUI) statusLine() string {
	d := func(s string) string { return t.paint("dim", s) }
	var parts []string
	parts = append(parts, t.paint("green", "●")+" "+t.model)
	if t.provider != "" {
		parts = append(parts, d(t.provider))
	}
	// Security level badge — always visible so the user knows their privacy
	// boundary. Unlike Claude Code, no hidden telemetry or phone-home.
	if t.securityLevel != "" && t.securityLevel != "local" {
		label := permission.SecurityLabel(config.SecurityLevel(t.securityLevel))
		parts = append(parts, label)
	}
	if t.promptTokens > 0 || t.completionTokens > 0 {
		parts = append(parts,
			d("↑"+formatTokens(t.promptTokens)+" ↓"+formatTokens(t.completionTokens)))
	}
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		parts = append(parts, d(fmt.Sprintf("%d%% ctx", pct)))
		// Visual context progress bar
		barW := 10
		filled := pct * barW / 100
		if filled > barW {
			filled = barW
		}
		bar := "["
		for i := 0; i < barW; i++ {
			if i < filled {
				bar += "▓"
			} else {
				bar += "░"
			}
		}
		bar += "]"
		parts = append(parts, d(bar))
	}
	if t.cacheHitRate > 0 {
		parts = append(parts, d(fmt.Sprintf("%.0f%% cache", t.cacheHitRate*100)))
	}
	if t.cost != "" {
		parts = append(parts, d(t.cost))
	}
	// Todo counters — shown when the current session has an active todo
	// list. Pending items appear in dim, in-progress in yellow, completed
	// dim. Zero-list sessions render nothing.
	if t.callback != nil {
		if pending, active, done, total := t.callback.TodoCounts(); total > 0 {
			seg := fmt.Sprintf("☐%d ▶%d ✓%d", pending, active, done)
			if active > 0 {
				seg = t.paint("yellow", seg)
			} else {
				seg = d(seg)
			}
			parts = append(parts, seg)
		}
	}
	if t.streaming {
		if !t.turnStart.IsZero() {
			parts = append(parts, d("⏱ "+formatDuration(time.Since(t.turnStart))))
		}
		// Thinking bar is already shown in the conversation area (thinkingBox),
		// so don't duplicate it here — Claude Code shows the spinner only once.
	}
	// Flash notice (slash command feedback)
	if t.statusNotice != "" {
		parts = append(parts, t.statusNotice)
	}
	return strings.Join(parts, d(" · "))
}

// formatDuration renders a duration compactly: "3.2s" or "1m04s".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", m, s)
}

// modeColor returns the ANSI color name for the current mode indicator,
// matching Claude Code's mode coloring (plan=blue, yolo=red, …).
func modeColor(m Mode) string {
	switch m {
	case ModePlan:
		return "blue"
	case ModeAuto:
		return "green"
	case ModeYOLO:
		return "red"
	default:
		// Agent default — Claude Code's signature orange/red
		return "orange"
	}
}

// drawInputBox renders the Claude Code-style single-line prompt — NO box, just
// `> <input>` on a single row. The hint bar (manual mode · shortcuts · agents)
// and the status bar (max · /effort) sit on the rows below. This is the exact
// layout from the Claude Code v2.x screenshots.
//
//	> <input>                                   row topRow
//	  manual mode on · ? for shortcuts · ↵ for agents   row topRow+1
//	                                          ⊙max · /effort   row topRow+2
func (t *TUI) drawInputBox(W, H int, inputBuf string, cursor int, streaming bool) {
	const inputRows = 3
	topRow := H - inputRows + 1
	if topRow < 1 {
		topRow = 1
	}
	hintRow := topRow + 1
	statusRow := topRow + 2

	// Prompt line: "> <input>"
	prompt := t.paint(modeColor(t.mode), "❯")
	innerW := W - 4
	if innerW < 4 {
		innerW = 4
	}
	content := inputBuf
	if visibleWidth(content) > innerW {
		content = truncVisible(content, innerW)
	}
	line := prompt + " " + content
	if visibleWidth(line) > W {
		line = truncVisible(line, W)
	}

	// Hint row (left side)
	hint := t.paint("dim", "  "+t.tstr("input.hint"))
	if streaming {
		hint = t.paint("dim", "  "+t.tstr("input.hint.streaming"))
	}

	// Status row (right side, model-style)
	effort := t.paint("dim", "⊙") + "max"
	if t.mode == ModeYOLO {
		effort = t.paint("yellow", "⊙") + "yolo"
	}
	rightStatus := effort + t.paint("dim", " · /effort")
	// Right-align
	pad := W - visibleWidth(rightStatus) - 2
	if pad < 0 {
		pad = 0
	}
	statusPadded := strings.Repeat(" ", pad) + rightStatus

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow))
	b.WriteString(line)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", hintRow))
	b.WriteString(hint)
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", statusRow))
	b.WriteString(statusPadded)

	// Position the cursor on the input line, just after the typed prefix.
	runes := []rune(inputBuf)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	vw := visibleWidth(string(runes[:cursor]))
	if vw > innerW {
		vw = innerW
	}
	col := 2 + vw // "❯ "(2) → content starts at column 2
	if col > W {
		col = W
	}
	if col < 1 {
		col = 1
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", topRow, col))
	if streaming {
		b.WriteString("\x1b[?25l")
	} else {
		b.WriteString("\x1b[?25h")
	}
	fmt.Fprint(t.writer, b.String())
}

// ── Scrolling support ───────────────────────────────────────────

// convHeight returns the number of rows available for conversation content.
func (t *TUI) convHeight() int {
	return t.height - 7 // header(1) + hrule(1) + hrule(1) + status(1) + perm(2) + input(4) — rough min
}

// totalConvLines counts all display lines for the current conversation.
func (t *TUI) totalConvLines(msgs []Message, streaming bool, streamContent string, width int) int {
	return len(t.conversationLines(msgs, streaming, streamContent, width))
}

// scrollPgUp scrolls one page up (or to top).
func (t *TUI) scrollPgUp() {
	t.mu.Lock()
	defer t.mu.Unlock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset += bodyH
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.scheduleRender()
}

// scrollPgDn scrolls one page down (or to bottom / auto-follow).
func (t *TUI) scrollPgDn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset -= bodyH
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}

// scrollToTop jumps to the oldest conversation lines.
func (t *TUI) scrollToTop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	msgs := append([]Message{}, t.messages...)
	total := len(t.conversationLines(msgs, t.streaming, t.streamBuf.String(), t.width))
	bodyH := t.convHeight()
	if total > bodyH {
		t.scrollOffset = total - bodyH
	}
	t.scheduleRender()
}

// scrollToBottom resumes auto-follow (scroll to latest content).
func (t *TUI) scrollToBottom() {
	t.mu.Lock()
	t.scrollOffset = 0
	t.mu.Unlock()
	t.scheduleRender()
}

// scrollUpSmall moves up a few lines.
func (t *TUI) scrollUpSmall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset += 3
	t.scheduleRender()
}

// scrollDownSmall moves down a few lines.
func (t *TUI) scrollDownSmall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset -= 3
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}

// ── Line mode (fallback) ─────────────────────────────────────────

func (t *TUI) runLine() error {
	t.writer = os.Stdout
	t.printBanner()
	reader := bufio.NewReader(t.reader)

	for t.running {
		fmt.Fprint(t.writer, t.linePrompt())
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
		if strings.HasPrefix(text, "!") {
			t.execShell(text[1:])
			continue
		}
		if strings.HasPrefix(text, "/") {
			t.handleSlash(text)
			continue
		}
		t.printUser(text)
		if t.callback != nil {
			t.mu.Lock()
			t.streaming = true
			t.streamBuf.Reset()
			t.turnStart = time.Now()
			t.mu.Unlock()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
					}
				}()
				t.callback.OnSend(text)
			}()
			t.drainStream()
		}
	}
	return nil
}

func (t *TUI) linePrompt() string {
	switch t.mode {
	case ModePlan:
		return "plan ⏣ "
	case ModeYOLO:
		return "yolo ⏣ "
	default:
		return "⏣ "
	}
}

func (t *TUI) printBanner() {
	cwd, _ := os.Getwd()
	fmt.Fprintln(t.writer)
	fmt.Fprintln(t.writer, strings.Repeat("─", 60))
	fmt.Fprintf(t.writer, "◆ iCode %s  %s\n", appVersionStr(), shortDir(cwd))
	fmt.Fprintf(t.writer, "  Model: %s  Mode: %s\n", t.model, t.mode)
	fmt.Fprintln(t.writer, strings.Repeat("─", 60))
	fmt.Fprintln(t.writer, "  "+t.tstr("banner.hint"))
	fmt.Fprintln(t.writer)
}

func (t *TUI) printUser(text string) {
	fmt.Fprintf(t.writer, "  ▸ %s\n\n", text)
}

// ── StreamWriter ─────────────────────────────────────────────────

// AddMessage renders a complete message (user/system/error/assistant).
func (t *TUI) AddMessage(role Role, content string) {
	t.mu.Lock()
	switch role {
	case RoleUser:
		t.messages = append(t.messages, Message{Role: RoleUser, Content: content})
	case RoleAssistant:
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: content})
	case RoleSystem:
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: content})
	case RoleError:
		t.messages = append(t.messages, Message{Role: RoleError, Content: content})
	case RoleThinking:
		t.messages = append(t.messages, Message{Role: RoleThinking, Content: content})
	default:
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: content})
	}
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	} else {
		t.printMessage(t.messages[len(t.messages)-1])
	}
}

// AddToolMessage records a tool invocation.
func (t *TUI) AddToolMessage(tool, toolArgs, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleTool, Tool: tool, ToolArgs: toolArgs, Content: content})
	idx := len(t.messages) - 1
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	} else {
		t.printMessage(t.messages[idx])
	}
}

// AppendToolResult appends result text to the most recent tool message.
func (t *TUI) AppendToolResult(content string) {
	t.mu.Lock()
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Role == RoleTool {
			if t.messages[i].Content != "" {
				t.messages[i].Content += "\n"
			}
			t.messages[i].Content += content
			idx := i
			t.mu.Unlock()
			if t.rawMode {
				t.render()
			} else {
				t.printMessage(t.messages[idx])
			}
			return
		}
	}
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	}
}

// printMessage writes a single message to the line-mode writer.
func (t *TUI) printMessage(m Message) {
	switch m.Role {
	case RoleUser:
		fmt.Fprintf(t.writer, "  ▸ %s\n\n", m.Content)
	case RoleAssistant:
		t.printAssistant(m.Content)
	case RoleSystem:
		fmt.Fprintf(t.writer, "  %s\n\n", m.Content)
	case RoleError:
		fmt.Fprintf(t.writer, "  × %s\n\n", m.Content)
	case RoleTool:
		toolArgs := m.ToolArgs
		if toolArgs == "{}" || strings.TrimSpace(toolArgs) == "" {
			toolArgs = ""
		}
		fmt.Fprintf(t.writer, "  » %s %s\n", m.Tool, truncate(toolArgs, 60))
		if m.Content != "" {
			for _, l := range strings.Split(m.Content, "\n") {
				fmt.Fprintf(t.writer, "    %s\n", l)
			}
		}
		fmt.Fprintln(t.writer)
	case RoleThinking:
		// Bordered "thinking" box. Top and bottom span the same 16 columns
		// (┐/┘ at col 15); the middle row carries a closing │ that lines up
		// with them, and the content is truncated to the 12-cell inner width
		// by *visible* columns so CJK text can't push the border out of line.
		const thinkInner = 12
		tc := fitVis(m.Content, thinkInner)
		fmt.Fprintf(t.writer, "  ┌─ thinking ─┐\n  │%s│\n  └────────────┘\n\n", tc)
	}
}

// printAssistant renders assistant text in line mode (◆ prefix).
func (t *TUI) printAssistant(text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(t.writer, "  ◆ %s\n", line)
		} else {
			fmt.Fprintf(t.writer, "    %s\n", line)
		}
	}
	fmt.Fprintln(t.writer)
}

// AppendStream appends assistant text as it streams in.
func (t *TUI) AppendStream(text string) {
	t.mu.Lock()
	t.streamBuf.WriteString(text)
	t.mu.Unlock()
	if t.rawMode {
		t.ensureAnim()
		t.scheduleRender()
	} else {
		fmt.Fprint(t.writer, text)
	}
}

// scheduleRender coalesces full-screen redraws: rapid token bursts collapse
// into at most one repaint per ~25ms window instead of one per chunk. A direct
// t.render() call (e.g. at end-of-stream) bypasses the throttle.
func (t *TUI) scheduleRender() {
	if !t.rawMode {
		return
	}
	t.mu.Lock()
	if t.renderPending {
		t.mu.Unlock()
		return
	}
	t.renderPending = true
	t.mu.Unlock()

	t.renderTimer = time.AfterFunc(25*time.Millisecond, func() {
		t.mu.Lock()
		t.renderPending = false
		t.mu.Unlock()
		t.render()
	})
}

// EndStream finalizes the streaming turn.
func (t *TUI) EndStream() {
	final := strings.TrimSpace(t.streamBuf.String())
	if final != "" {
		t.mu.Lock()
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: final})
		t.mu.Unlock()
	}
	t.streamBuf.Reset()
	select {
	case t.streamDone <- struct{}{}:
	default:
	}
	if t.rawMode {
		t.render()
	}
}

// SetStatus records token usage and cost for the status bar.
func (t *TUI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.promptTokens = input
	t.completionTokens = output
	t.cacheHitRate = cacheHit
	t.cost = cost
}

// ── Slash commands ───────────────────────────────────────────────

func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help":
		var b strings.Builder
		b.WriteString(t.tstr("cmd.help") + ":\n")
		for _, d := range slashDefs {
			b.WriteString(fmt.Sprintf("  %-14s %s\n", d.Name, t.tstr(d.Key)))
		}

		// Append user-defined slash commands (.icode/commands/*.md) so
		// `/help` reflects everything the current session will accept.
		if custom := slashcmd.Load(slashcmd.DefaultDirs()...).List(); len(custom) > 0 {
			b.WriteString("\n自定义命令:\n")
			for _, c := range custom {
				hint := c.ArgumentHint
				if hint != "" {
					hint = " " + hint
				}
				b.WriteString(fmt.Sprintf("  %-14s %s\n", c.Name+hint, c.Description))
			}
		}

		b.WriteString("\n特殊语法:\n")
		b.WriteString("  # <内容>          追加到 ~/.icode/CLAUDE.md\n")
		b.WriteString("  ! <shell>         运行 shell 命令\n")
		b.WriteString("\n" + t.tstr("sc.title") + ":\n")
		b.WriteString("  Ctrl+C           " + t.tstr("sc.ctrlc") + "\n")
		b.WriteString("  Ctrl+L           " + t.tstr("sc.ctrll") + "\n")
		b.WriteString("  Ctrl+P / Ctrl+N  " + t.tstr("sc.history") + "\n")
		b.WriteString("  " + t.tstr("cmd.ac"))
		t.add(RoleSystem, b.String())

	case "/exit", "/quit":
		t.add(RoleSystem, "Goodbye!")
		t.running = false

	case "/expand":
		t.toolFolded = !t.toolFolded
		if t.toolFolded {
			t.add(RoleSystem, "工具输出已折叠（只显示前 8 行），再运行 /expand 展开全部")
		} else {
			t.add(RoleSystem, "工具输出已全部展开")
		}

	case "/model":
		if len(args) > 0 {
			t.model = args[0]
			t.notice("Model → " + args[0])
			t.add(RoleSystem, t.tstr("mode.set")+" → "+args[0])
		}

	case "/mode":
		if len(args) > 0 {
			t.mode = args[0]
			t.add(RoleSystem, "Mode → "+args[0])
		}

	case "/session", "/sessions":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnListSessions())
		}

	case "/resume":
		if len(args) > 0 && t.callback != nil {
			t.add(RoleSystem, t.callback.OnResume(args[0]))
		} else {
			t.add(RoleSystem, "Usage: /resume <session-id>")
		}

	case "/clear":
		t.mu.Lock()
		t.messages = nil
		t.promptTokens = 0
		t.completionTokens = 0
		t.cost = ""
		t.cacheHitRate = 0
		t.mu.Unlock()
		t.add(RoleSystem, "Conversation cleared.")

	case "/compact":
		t.compact()

	case "/export":
		t.exportMarkdown(args)

	case "/diff":
		t.showGitDiff()

	case "/rewind":
		n := 1
		if len(args) > 0 {
			fmt.Sscanf(args[0], "%d", &n)
		}
		if n <= 0 {
			t.add(RoleSystem, "用法: /rewind [N]  — 回滚前 N 步工具调用")
			break
		}
		sessionID := ""
		if t.callback != nil {
			sessionID = t.callback.SessionID()
		}
		if sessionID == "" {
			t.add(RoleSystem, "没有活跃会话。")
			break
		}
		store, err := checkpoint.GetOrOpen(sessionID)
		if err != nil {
			t.add(RoleError, "打开检查点失败: "+err.Error())
			break
		}
		files, err := store.Rewind(context.Background(), n)
		if err != nil {
			t.add(RoleError, "回滚失败: "+err.Error())
			break
		}
		msg := fmt.Sprintf("⏪ 已回滚 %d 步。影响文件:\n", n)
		for _, f := range files {
			msg += "  " + f + "\n"
		}
		t.add(RoleSystem, msg)

	case "/todo":
		var b strings.Builder
		sessionID := ""
		if t.callback != nil {
			if p, a, d, total := t.callback.TodoCounts(); total > 0 {
				fmt.Fprintf(&b, "📋 待办 (%d 待处理 · %d 进行中 · %d 完成)\n\n", p, a, d)
				b.WriteString("（详情可通过 `todo_write` 工具查看 — 当前只在状态栏显示计数）\n")
			} else {
				b.WriteString("当前会话没有待办事项。让模型执行任务时会自动创建。\n")
			}
		}
		_ = sessionID
		t.add(RoleSystem, b.String())

	case "/init":
		cwd, err := os.Getwd()
		if err != nil { t.add(RoleError, err.Error()); break }
		if _, err := os.Stat(filepath.Join(cwd, "ICODE.md")); err == nil {
			t.add(RoleSystem, "ICODE.md 已存在")
			break
		}
		_ = os.WriteFile(filepath.Join(cwd, "ICODE.md"), []byte("# Project Context\n\nEdit this file.\n"), 0o644)
		t.add(RoleSystem, "✓ ICODE.md 已生成")

	case "/agents":
		v := agent.Load(agent.AgentDefaultDirs()...)
		v.RegisterDefaults()
		var a strings.Builder
		a.WriteString("子 agent:\n")
		for _, d := range v.List() { a.WriteString(fmt.Sprintf("  %s — %s\n", d.Name, d.Description)) }
		t.add(RoleSystem, a.String())

	case "/mcp":
		cfg, err := config.Load()
		if err != nil { t.add(RoleSystem, err.Error()); break }
		var a strings.Builder
		a.WriteString("MCP 服务器:\n")
		for _, s := range cfg.MCP { a.WriteString(fmt.Sprintf("  %s: %s\n", s.Name, s.Command)) }
		if a.Len() < 12 { a.WriteString("  未配置\n在 ~/.icode/mcp.json 中添加。\n") }
		t.add(RoleSystem, a.String())

	case "/hooks":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".icode", "hooks.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			permission.GenerateHooks(path)
			t.add(RoleSystem, "✓ "+path)
		} else {
			data, _ := os.ReadFile(path)
			t.add(RoleSystem, fmt.Sprintf("📄 %s\n\n```yaml\n%s\n```", path, string(data)))
		}

	case "/status":
		if t.callback != nil {
			t.add(RoleSystem, t.callback.OnStatus())
		} else {
			t.add(RoleSystem, "引擎未初始化。")
		}

	case "/cost":
		info := fmt.Sprintf("Tokens: %d prompt + %d completion = %d total",
			t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens)
		if t.cost != "" {
			info += " · Cost: " + t.cost
		}
		if t.cacheHitRate > 0 {
			info += fmt.Sprintf(" · Cache: %.0f%%", t.cacheHitRate*100)
		}
		t.add(RoleSystem, info)

	case "/provider":
		if len(args) > 0 {
			t.provider = args[0]
			t.add(RoleSystem, "Provider → "+args[0])
		} else {
			t.add(RoleSystem, "当前 Provider: "+t.provider+"\n用法: /provider <name>")
		}
case "/multiline":
		t.multiline = !t.multiline
		if t.multiline {
			t.add(RoleSystem, "[Multiline ON] Enter=newline, Alt+Enter=send")
		} else {
			t.add(RoleSystem, "[Multiline OFF]")
		}


		cfg, err := config.Load()
		if err != nil {
			t.add(RoleSystem, "无法读取配置: "+err.Error())
			return
		}
		var b strings.Builder
		b.WriteString("API 密钥状态：\n")
		for name, pc := range cfg.Providers {
			st := "未配置"
			if pc.APIKey != "" {
				st = "已配置"
			}
			b.WriteString(fmt.Sprintf("  %-14s %s\n", name, st))
		}
		b.WriteString("\n用 `icode config key <provider> <key>` 或桌面端设置配置。")
		t.add(RoleSystem, b.String())

	case "/models":
		cfg, err := config.Load()
		if err != nil || len(cfg.Models) == 0 {
			t.add(RoleSystem, "暂无自定义模型。\n用 `icode config model add <provider> <model_id> [name]` 新增。")
			return
		}
		var b strings.Builder
		b.WriteString("自定义模型：\n")
		for _, m := range cfg.Models {
			name := m.Name
			if name == "" {
				name = m.ModelID
			}
			b.WriteString(fmt.Sprintf("  %-26s %s / %s\n", m.ID, m.Provider, name))
		}
		t.add(RoleSystem, b.String())

	case "/config":
		cfg, _ := config.Load()
		lang := "zh-CN"
		theme := "auto"
		diff := "unified"
		syntax := "on"
		if cfg != nil {
			lang = cfg.Language
			theme = cfg.TUI.Theme
			diff = cfg.TUI.DiffMode
			if !cfg.TUI.SyntaxHL {
				syntax = "off"
			}
		}
		t.add(RoleSystem, fmt.Sprintf("当前设置：\n  Model:     %s\n  Provider:  %s\n  Mode:      %s\n  Language:  %s\n  Theme:     %s\n  Diff:      %s\n  Security:  %s\n  Syntax:    %s\n\n用 `icode config <key> <value>` 修改，或 `/lang` `/theme`  `/security` 即时切换。",
			t.model, t.provider, t.mode, lang, theme, diff,
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), syntax))

	case "/history":
		if len(t.history) == 0 {
			t.add(RoleSystem, "无历史记录。")
			return
		}
		var b strings.Builder
		for i, h := range t.history {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, h))
		}
		t.add(RoleSystem, b.String())

	case "/theme":
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "auto", "dark", "light":
				t.theme = strings.ToLower(args[0])
				t.persistSetting(func(c *config.Config) { c.TUI.Theme = t.theme })
				t.add(RoleSystem, fmt.Sprintf(t.tstr("theme.set"), t.theme))
			default:
				t.add(RoleSystem, t.tstr("theme.usage"))
			}
		} else {
			t.add(RoleSystem, t.tstr("theme.usage"))
		}

	case "/lang":
		if len(args) > 0 {
			switch args[0] {
			case "zh-CN", "zh-TW", "en":
				t.lang = args[0]
				t.persistSetting(func(c *config.Config) { c.Language = t.lang })
				t.add(RoleSystem, fmt.Sprintf(t.tstr("lang.set"), t.lang))
			default:
				t.add(RoleSystem, t.tstr("lang.usage"))
			}
		} else {
			t.add(RoleSystem, t.tstr("lang.usage"))
		}

	case "/summarize":
		if len(t.messages) == 0 {
			t.add(RoleSystem, t.tstr("cmd.summarize")+": 没有对话内容可总结。")
			return
		}
		var b strings.Builder
		b.WriteString("## 对话总结\n\n")
		msgCount := 0
		for _, m := range t.messages {
			if m.Role == RoleUser || m.Role == RoleAssistant {
				msgCount++
			}
		}
		b.WriteString(fmt.Sprintf("共 %d 条消息，模型: %s，提供商: %s，模式: %s\n\n", msgCount, t.model, t.provider, t.mode))
		if t.promptTokens > 0 || t.completionTokens > 0 {
			b.WriteString(fmt.Sprintf("Token: %d 输入 + %d 输出 = %d 总计", t.promptTokens, t.completionTokens, t.promptTokens+t.completionTokens))
			if t.cost != "" {
				b.WriteString(" · 费用: " + t.cost)
			}
			if t.cacheHitRate > 0 {
				b.WriteString(fmt.Sprintf(" · 缓存: %.0f%%", t.cacheHitRate*100))
			}
			b.WriteString("\n\n")
		}

		// List key topics from user messages
		b.WriteString("### 用户提问\n\n")
		for _, m := range t.messages {
			if m.Role == RoleUser {
				trunc := m.Content
				if len([]rune(trunc)) > 120 {
					trunc = string([]rune(trunc)[:120]) + "…"
				}
				b.WriteString(fmt.Sprintf("- %s\n", trunc))
			}
		}
		t.add(RoleSystem, b.String())

	case "/review":
		t.add(RoleSystem, "🔍 审查模式已启用。请描述你想审查的代码或文件路径，我将分析代码质量、安全性和潜在问题。")

	case "/security":
		if len(args) == 0 {
			t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
				permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
			return
		}
		newLevel := strings.ToLower(args[0])
		valid := map[string]config.SecurityLevel{
			"local":        config.SecLocal,
			"desensitize":  config.SecDesensitize,
			"local-llm":    config.SecLocalLLM,
			"foreign-llm":  config.SecForeignLLM,
			"unrestricted": config.SecUnrestricted,
		}
		level, ok := valid[newLevel]
		if !ok {
			t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
				permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
			return
		}
		t.securityLevel = newLevel
		t.persistSetting(func(c *config.Config) { c.SecurityLevel = level })
		t.add(RoleSystem, fmt.Sprintf(t.tstr("security.set"),
			permission.SecurityLabel(level)))

	case "/welcome":
		t.mu.Lock()
		empty := len(t.messages) == 0
		t.welcomeVisible = !t.welcomeVisible
		vis := t.welcomeVisible
		t.mu.Unlock()
		if vis && empty {
			t.render() // conversation is empty → banner will show
		} else if vis && !empty {
			t.add(RoleSystem, t.tstr("welcome.reopen"))
		} else {
			t.render() // hidden
		}

	default:
		// User-defined slash command? Look it up in .icode/commands/*.md
		// (user + project scope) and expand it into a normal chat message.
		if custom := t.tryCustomSlash(cmd, strings.Join(args, " ")); custom {
			return
		}
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// tryCustomSlash resolves `cmd` (e.g. "/changelog") against the user- and
// project-scoped command registry loaded from .icode/commands/*.md. When a
// match is found, its template is expanded and the result is submitted as a
// regular user message. Returns true if a custom command handled the input.
func (t *TUI) tryCustomSlash(cmd, argStr string) bool {
	reg := slashcmd.Load(slashcmd.DefaultDirs()...)
	c, ok := reg.Get(cmd)
	if !ok {
		return false
	}
	expanded, err := c.Expand(context.Background(), argStr)
	if err != nil {
		t.add(RoleError, fmt.Sprintf("展开 %s 失败: %v", cmd, err))
		return true
	}
	if strings.TrimSpace(expanded) == "" {
		t.add(RoleSystem, fmt.Sprintf("命令 %s 展开为空", cmd))
		return true
	}

	// Feed the expanded text into the normal submit flow so it is displayed
	// as a user message and streamed through the LLM. Bypass the leading
	// prefix scan (! # /) that the raw submit() runs — the expanded body
	// might legitimately start with any of those characters.
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: fmt.Sprintf("(%s) %s", cmd, argStr)})
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(expanded)
		}()
	}
	t.ensureAnim()
	t.drainStream()
	return true
}

// add appends a message and refreshes the screen.
func (t *TUI) add(role Role, content string) {
	t.AddMessage(role, content)
}

// ── Shell mode ───────────────────────────────────────────────────

// appendMemory writes a note to the user memory file (~/.icode/CLAUDE.md)
// via the context loader's helper. Invoked when the user submits input that
// starts with `#`, mirroring Claude Code's quick-memory shortcut.
func (t *TUI) appendMemory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		t.add(RoleSystem, "用法: # <要记录的内容>")
		return
	}
	if err := projectcontext.AppendUserMemory(text); err != nil {
		t.add(RoleError, "追加 memory 失败: "+err.Error())
		return
	}
	path, _ := projectcontext.UserMemoryPath()
	t.add(RoleSystem, fmt.Sprintf("✓ 已记录到 %s", path))
}

func (t *TUI) execShell(cmdStr string) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return
	}
	t.add(RoleTool, "bash "+cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.add(RoleError, err.Error())
	}
	if len(output) > 0 {
		t.AppendToolResult(strings.TrimRight(string(output), "\n"))
	}
}

// ── Compact ──────────────────────────────────────────────────────

func (t *TUI) compact() {
	t.mu.Lock()
	if len(t.messages) < 4 {
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: "Not enough messages to compact."})
		t.mu.Unlock()
		t.render()
		return
	}
	var keep []Message
	var summary strings.Builder
	summary.WriteString("[Compacted] Summary of earlier turns:\n")
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
	t.messages = append(t.messages, Message{Role: RoleSystem, Content: summary.String()})
	t.mu.Unlock()
	t.render()
}

// ── Export ───────────────────────────────────────────────────────

func (t *TUI) exportMarkdown(args []string) {
	filename := "icode-export.md"
	if len(args) > 0 {
		filename = args[0]
	}
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	t.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("# iCode Conversation Export\n\n")
	sb.WriteString(fmt.Sprintf("**Model:** %s  \n", t.model))
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n\n", t.mode))
	sb.WriteString("---\n\n")
	for _, m := range msgs {
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
		t.add(RoleError, "Export failed: "+err.Error())
		return
	}
	t.add(RoleSystem, fmt.Sprintf("Exported to %s (%d messages)", filename, len(msgs)))
}

// ── Git diff ─────────────────────────────────────────────────────

func (t *TUI) showGitDiff() {
	cmd := exec.Command("git", "diff")
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.add(RoleError, "git diff: "+err.Error())
		return
	}
	if len(output) == 0 {
		t.add(RoleSystem, "No unstaged changes.")
		return
	}
	t.AddToolMessage("git_diff", "", t.colorizeDiffStr(strings.TrimRight(string(output), "\n")))
}

// ── Helpers ──────────────────────────────────────────────────────

// persistSetting loads config, applies fn, and writes it back to disk.
// Errors are reported via the UI rather than silently ignored.
func (t *TUI) persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleError, "配置加载失败: "+err.Error())
		return
	}
	fn(cfg)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.add(RoleError, "配置保存失败: "+err.Error())
	}
}

// updateSuggestions recomputes the autocomplete panel based on the current
// input. It opens when the input is empty (showing all commands) or starts
// with "/" (showing commands) or contains "@" (showing file completions).
func (t *TUI) updateSuggestions() {
	if !t.rawMode || t.streaming {
		t.acOpen = false
		t.acItems = nil
		return
	}
	buf := t.inputBuf
	if buf == "" {
		t.acOpen = true
		t.acItems = t.allSuggestions()
		t.acIdx = 0
		return
	}
	if strings.HasPrefix(buf, "/") {
		prefix := strings.TrimSpace(buf)
		var items []acItem
		for _, d := range slashDefs {
			if prefix == "/" || strings.HasPrefix(d.Name, prefix) {
				items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
			}
		}
		if len(items) == 0 {
			t.acOpen = false
			t.acItems = nil
			return
		}
		t.acOpen = true
		t.acItems = items
		if t.acIdx >= len(items) {
			t.acIdx = len(items) - 1
		}
		if t.acIdx < 0 {
			t.acIdx = 0
		}
		return
	}
	// @file completions — matches Claude Code's file-attachment autocomplete.
	if idx := strings.LastIndex(buf, "@"); idx >= 0 {
		prefix := buf[idx+1:] // text after the @
		items := t.filesAutocomplete(prefix)
		if len(items) > 0 {
			t.acOpen = true
			t.acItems = items
			if t.acIdx >= len(items) {
				t.acIdx = len(items) - 1
			}
			if t.acIdx < 0 {
				t.acIdx = 0
			}
			return
		}
	}
	t.acOpen = false
	t.acItems = nil
}

// filesAutocomplete returns files and directories that match the given
// prefix (text after the @ sign). Results are gitignore-aware: directories
// named .git, node_modules, vendor, dist, target, release are excluded.
func (t *TUI) filesAutocomplete(prefix string) []acItem {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	// Determine the directory to search.
	searchDir := cwd
	if strings.Contains(prefix, "/") {
		// User typed a sub-path: "src/foo" → search cwd/src/
		rel := filepath.Dir(prefix)
		searchDir = filepath.Join(cwd, rel)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var items []acItem
	basePattern := strings.TrimPrefix(prefix, filepath.Dir(prefix)+"/")
	if basePattern == filepath.Dir(prefix) {
		basePattern = prefix
	}
	for _, e := range entries {
		name := e.Name()
		if isIgnoredDir(name) {
			continue
		}
		relPath := strings.TrimPrefix(filepath.Join(filepath.Dir(prefix), name), ".")
		relPath = strings.TrimPrefix(relPath, "/")
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(basePattern)) {
			continue
		}
		if e.IsDir() {
			items = append(items, acItem{Name: relPath + "/", Desc: "directory"})
		} else {
			info, _ := e.Info()
			size := ""
			if info != nil {
				size = formatFileSize(info.Size())
			}
			items = append(items, acItem{Name: relPath, Desc: size})
		}
		if len(items) >= 50 {
			break
		}
	}
	return items
}

// isIgnoredDir returns true for directories that should be hidden from
// @file autocomplete (mirrors common .gitignore patterns).
func isIgnoredDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "target", "release",
		"__pycache__", ".venv", ".next", "build", "out", ".icloud":
		return true
	}
	return false
}

// formatFileSize renders a byte count into a human-readable string.
func formatFileSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// expandFileRefs scans text for `@path/to/file` references (not preceded by
// a word character) and replaces them with [file: path]\n<content>\n. This
// lets users quickly attach file context in the TUI. For desktop/clients
// that handle file attachment natively, this is a no-op fallback.
func (t *TUI) expandFileRefs(text string) string {
	var result strings.Builder
	remaining := text
	for {
		idx := strings.Index(remaining, "@")
		if idx < 0 || (idx > 0 && isWordChar(remaining[idx-1])) {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:idx])
		rest := remaining[idx+1:]

		// Extract the path: everything until whitespace or end.
		end := strings.IndexAny(rest, " \t\n")
		path := rest
		if end >= 0 {
			path = rest[:end]
			rest = rest[end:]
		} else {
			rest = ""
		}
		path = strings.TrimSpace(path)
		if path == "" {
			result.WriteString("@")
			remaining = rest
			continue
		}
		// Resolve relative to CWD
		fullPath := path
		if !filepath.IsAbs(path) {
			if cwd, err := os.Getwd(); err == nil {
				fullPath = filepath.Join(cwd, path)
			}
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			result.WriteString("@")
			result.WriteString(path)
			remaining = rest
			continue
		}
		result.WriteString(fmt.Sprintf("[file: %s]\n%s\n", path, strings.TrimSpace(string(data))))
		remaining = rest
	}
	return result.String()
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '.'
}

// allSuggestions returns every slash command as an autocomplete entry.
func (t *TUI) allSuggestions() []acItem {
	items := make([]acItem, 0, len(slashDefs))
	for _, d := range slashDefs {
		items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
	}
	return items
}

// acceptSuggestion replaces the input with the highlighted autocomplete
// entry. For slash commands this inserts the command name. For @file refs
// it replaces the "@prefix" with the file path and the file content.
func (t *TUI) acceptSuggestion() {
	if len(t.acItems) == 0 {
		return
	}
	it := t.acItems[t.acIdx]

	// If this is an @file autocomplete item (name starts with a path
	// separator or "./"), replace the "@prefix" with the file path.
	if strings.Contains(t.inputBuf, "@") && !strings.HasPrefix(it.Name, "/") {
		idx := strings.LastIndex(t.inputBuf, "@")
		path := strings.TrimPrefix(it.Name, "./")
		// Replace the @prefix with the file path
		t.inputBuf = t.inputBuf[:idx] + "@" + path + " "
		t.cursor = len([]rune(t.inputBuf))
		t.acOpen = false
		t.updateSuggestions()
		// When the user sends the message, the frontend will automatically
		// read the @file reference and prepend its content.
		return
	}

	t.inputBuf = it.Name + " "
	t.cursor = len([]rune(t.inputBuf))
	t.acOpen = false
	t.updateSuggestions()
}

// autocompleteLines renders the suggestion panel shown above the input line.
// Returns nil when there is nothing to show.
func (t *TUI) autocompleteLines() []string {
	if !t.rawMode || t.streaming || !t.acOpen || len(t.acItems) == 0 {
		return nil
	}
	var out []string
	out = append(out, t.paint("dim", "  ▾ "+t.tstr("ac.title")+"   ("+t.tstr("ac.hint")+")"))

	const maxShow = 9
	from := 0
	if t.acIdx >= maxShow {
		from = t.acIdx - maxShow + 1
	}
	show := t.acItems
	if from+maxShow < len(show) {
		show = show[from : from+maxShow]
	} else {
		show = show[from:]
	}

	for i, it := range show {
		globalIdx := from + i
		sel := globalIdx == t.acIdx
		name := padEnd(it.Name, 16)
		if sel {
			out = append(out, "  "+t.c("cyan")+"▶ "+name+" "+it.Desc+"\x1b[0m")
		} else {
			out = append(out, "    "+t.paint("dim", name+" "+it.Desc))
		}
	}
	return out
}

func formatTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
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
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// wrapPrefixed wraps text to the terminal width, with a first-line prefix and
// a continuation indent. prefixWidth is the display width of `prefix`.
func wrapPrefixed(prefix, cont, text string, width int) []string {
	contentW := width - runeWidthStr(prefix)
	if contentW < 10 {
		contentW = 10
	}
	wrapped := wrapText(text, contentW)
	if len(wrapped) == 0 {
		return []string{prefix}
	}
	out := make([]string, len(wrapped))
	out[0] = prefix + wrapped[0]
	for i := 1; i < len(wrapped); i++ {
		out[i] = cont + wrapped[i]
	}
	return out
}

// wrapText wraps text to the given display width (counting CJK as width 2).
func wrapText(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		runes := []rune(para)
		var cur []rune
		curW := 0
		for _, r := range runes {
			w := runeWidth(r)
			if curW+w > width && len(cur) > 0 {
				lines = append(lines, string(cur))
				cur = cur[:0]
				curW = 0
			}
			cur = append(cur, r)
			curW += w
		}
		lines = append(lines, string(cur))
	}
	return lines
}

func runeWidthStr(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115F ||
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0x303E) ||
		(r >= 0x3041 && r <= 0x33FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xA000 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6)) {
		return 2
	}
	return 1
}

func thinkingLines(text string, width int) []string {
	inner := width - 4
	if inner < 10 {
		inner = 10
	}
	label := " 思考 "
	pad := inner - runeWidthStr(label)
	if pad < 0 {
		pad = 0
	}
	top := "┌" + label + repeat("─", pad) + "┐"
	var out []string
	out = append(out, "  "+top)
	for _, l := range wrapText(text, inner) {
		out = append(out, "  │ "+fitVis(l, inner-1)+"│")
	}
	out = append(out, "  └"+repeat("─", inner)+"┘")
	return out
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

func padEnd(s string, n int) string {
	w := runeWidthStr(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// hrule returns a full-width horizontal separator line (dim), used to divide
// the banner, messages, and footer regions of the TUI.
func (t *TUI) hrule(width int) string {
	if width < 2 {
		width = 2
	}
	return t.paint("dim", repeat("─", width))
}

// appVersionStr returns the human-readable version shown in the banner.
func appVersionStr() string {
	return "v0.2"
}

// ── Accessors used by the backend callback ───────────────────────

// CurrentModel returns the active model ID.
func (t *TUI) CurrentModel() string { return t.model }

// CurrentProvider returns the active provider name.
func (t *TUI) CurrentProvider() string { return t.provider }

// SetModels updates the available model list for Tab switching.
func (t *TUI) SetModels(models []string) {
	t.models = models
	// Find current model index
	for i, m := range models {
		if m == t.model {
			t.modelIdx = i
			return
		}
	}
}

// notice sets a one-line flash message shown on the next render.
func (t *TUI) notice(msg string) {
	t.statusNotice = " " + t.paint("green", "✓") + " " + msg
}

// LoadSession replaces the visible message list (used by /resume).
func (t *TUI) LoadSession(msgs []Message) {
	t.mu.Lock()
	t.messages = msgs
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	}
}

// PromptPermission shows an interactive approval dialog and blocks until the
// user answers. It is invoked from the engine's permission handler, which runs
// on the streaming goroutine while the main loop is parked in drainStream — so
// we read the decision key directly from the terminal.
func (t *TUI) PromptPermission(prompt string) permission.Decision {
	if !t.rawMode {
		// Non-interactive (piped) — auto-approve to avoid a hang.
		return permission.DecisionAllow
	}

	t.mu.Lock()
	t.permPending = true
	t.permPrompt = prompt
	t.mu.Unlock()
	t.render()

	reader := bufio.NewReader(t.reader)
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			t.clearPerm()
			return permission.DecisionDeny
		}
		switch r {
		case '1', 'y', 'Y', '\r', '\n':
			t.clearPerm()
			return permission.DecisionAllow
		case '2', 'a', 'A':
			t.clearPerm()
			return permission.DecisionAllowAll
		case '3', 'n', 'N', 0x03: // '3', 'n', or Ctrl+C → deny
			t.clearPerm()
			return permission.DecisionDeny
		}
	}
}

// clearPerm dismisses the approval dialog and repaints.
func (t *TUI) clearPerm() {
	t.mu.Lock()
	t.permPending = false
	t.permPrompt = ""
	t.mu.Unlock()
	t.render()
}

// termSize returns the current terminal dimensions, trying both stdin and
// stdout handles. On Windows, GetConsoleScreenBufferInfo requires an output
// handle; term.GetSize is called with stdin, and if that fails, stdout.
func (t *TUI) termSize() (w, h int, ok bool) {
	for _, fd := range []int{int(os.Stdin.Fd()), int(os.Stdout.Fd())} {
		if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
			return w, h, true
		}
	}
	return 0, 0, false
}

// resizeTerminal requests the terminal to resize to a comfortable size for
// the iCode TUI. Uses two approaches:
//
//  1. ANSI escape \x1b[8;H;Wt — supported by Windows Terminal, xterm, iTerm2,
//     GNOME Terminal, etc. Silently ignored by terminals that don't support it.
//  2. On Windows, a fallback using SetConsoleScreenBufferInfo / SetConsoleWindowInfo
//     for legacy conhost / cmd.exe (see resize_windows.go).
//
// The function does nothing if the terminal is already at least 120×36.
func (t *TUI) resizeTerminal() {
	w, h, ok := t.termSize()
	if !ok {
		return
	}

	// Target: at least 120 columns × 36 rows — minimum comfortable for a TUI.
	const wantW, wantH = 120, 36
	if w >= wantW && h >= wantH {
		return // already big enough
	}
	if w < wantW {
		w = wantW
	}
	if h < wantH {
		h = wantH
	}

	// 1. ANSI escape (works in most modern terminals).
	fmt.Fprintf(t.writer, "\x1b[8;%d;%dt", h, w)

	// 2. Windows API fallback (in resize_windows.go, compiled only on Windows).
	resizeTerminalWindows(w, h)
}

// resizeTerminalWindows is defined in resize_windows.go (Windows) and
// resize_stub.go (all other platforms).
