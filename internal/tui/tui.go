package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/searchreplace"
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

// SessionInfo is a lightweight session descriptor for the /resume picker.
type SessionInfo struct {
	ID      string
	Title   string
	Model   string
	Updated string // human-readable relative/absolute time
}

// Callback bridges user input / slash commands back to the backend.
type Callback interface {
	OnSend(text string)
	OnSlashCommand(cmd string, args []string)
	OnPermissionResponse(decision string)
	// OnPlanConfirm is called when the user accepts a plan-mode proposal:
	// the implementation switches the gate out of read-only plan mode and
	// starts executing the plan.
	OnPlanConfirm()
	// OnInterrupt is called when the user presses Esc or Ctrl+C during
	// streaming to cancel the current LLM turn.
	OnInterrupt()
	// OnListSessions returns a formatted list of past sessions.
	OnListSessions() string
	// OnListSessionsStructured returns up to limit past sessions (id, title,
	// model) for the interactive /resume picker. Empty when none exist.
	OnListSessionsStructured(limit int) []SessionInfo
	// OnResume loads a past session's messages; returns a status line.
	OnResume(id string) string
	// OnCompactSummarize asks the backend to semantically summarize the active
	// session's older turns using the model (Claude Code /compact parity).
	// Returns "" when not applicable or on failure, so the caller falls back
	// to the free local trim. The instruction optionally steers what to keep.
	OnCompactSummarize(instruction string) string
	// TodoCounts returns the current session's todo counts for the status
	// bar. Returns all zeros when there is no active session or no list.
	TodoCounts() (pending, active, done, total int)
	// SessionID returns the active session ID, or "".
	SessionID() string
	// OnStatus returns a formatted system status report (providers, keys,
	// MCP servers, cache stats, etc.).
	OnStatus() string
	// OnTokenStats returns a formatted token-saving report (tokens saved,
	// cache hit rate, compactions, total tokens, estimated cost). Surfaces
	// iCode's core "super token-saving" mechanism so users can see it working.
	OnTokenStats() string
	// OnOutputStyle applies a new answer style (concise|normal|verbose) live
	// and persists it. Returns a status line.
	OnOutputStyle(style string) string
	// OnAddDir registers an extra working directory (/add-dir), persists it,
	// and re-applies the composed system prompt. Returns a status line.
	OnAddDir(dir string) string
	// OnUpdateModels refreshes provider model catalogs (/update) and returns a
	// per-provider report. The TUI model list is refreshed as a side effect.
	OnUpdateModels() string
	// OnSetMode switches the backend permission gate's mode (plan/agent/auto/
	// yolo) so the TUI's displayed mode and the actual gate stay in sync.
	// Returns a human-readable confirmation.
	OnSetMode(mode string) string
	// OnRenameSession retitles the active session (/rename) in the backend
	// store. Returns an error message, or "" on success.
	OnRenameSession(title string) string
	// OnAddCustomModel persists and live-registers a user-defined model
	// (/models add <provider> <model_id> [name]). Returns a status line, or an
	// error message prefixed with "ERROR" on failure.
	OnAddCustomModel(provider, modelID, name string) string
	// OnRemoveCustomModel removes a user-defined model (/models rm <id>).
	// Returns a status line, or an error message on failure.
	OnRemoveCustomModel(id string) string
	// LSPQuery runs an on-demand LSP code-intelligence query for /lsp
	// (subcommand: status/diag/syms/hover/def/refs). Returns a formatted
	// report, or an error message when LSP is unavailable.
	LSPQuery(sub string, args []string) string
	// KnowledgeQuery searches the local document knowledge base (/kb).
	// Returns formatted passages, or a status/error message.
	KnowledgeQuery(query string) string
	// CreateIdleTask creates an off-peak task (/idle <name> <prompt>) that runs
	// in the idle window. Returns a status/error message.
	CreateIdleTask(name, prompt string) string
}

// StreamWriter is the surface the backend uses to push data into the UI.
type StreamWriter interface {
	AddMessage(role Role, content string)
	AddToolMessage(tool, toolArgs, content string)
	AppendToolResult(content string)
	AppendStream(text string)
	AppendToolProgress(content string)
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
	Version  string // app version (injected from ldflags)
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
	version       string
	callback      Callback

	// input autocomplete state (raw mode)
	acOpen  bool
	acItems []acItem
	acIdx   int

	// tool output folding (Claude Code-style)
	toolFolded bool

	// planPending is set when a plan-mode reply finished and awaits the user's
	// go/no-go: Enter confirms and executes, Esc cancels.
	planPending bool

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
	reader  io.Reader // raw mode: *bufio.Reader
	writer  io.Writer

	// raw-mode state
	rawMode  bool
	color    bool
	osc8     bool // OSC 8 hyperlink support (Windows Terminal / WezTerm / kitty…)
	width    int
	height   int
	inputBuf string
	cursor   int
	history  []string
	histIdx  int

	// vim normal-mode state (real key bindings, toggled by /vim)
	vimInsert    bool   // true = insert mode (default); false = normal mode
	vimUndo      string // snapshot for `u` in normal mode
	vimUndoValid bool

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

	// helpVisible toggles the keyboard-shortcut help overlay (opened with `?`
	// on an empty input, dismissed with any key).
	helpVisible bool

	// verbose toggles verbose output (full tool args, raw diffs). Mirrors
	// Claude Code's /verbose command.
	verbose bool

	// vimMode toggles vi-style key bindings in the input (Claude Code /vim).
	vimMode bool

	// statusVisible toggles the bottom status bar (Claude Code /statusline).
	statusVisible bool

	// searchMode enables the Ctrl+R reverse-history-search overlay (Claude
	// Code-style). While active, printable keys filter history, ↑/↓ cycle
	// matches, Enter/Tab accepts, Esc/Ctrl+G cancels.
	searchMode    bool
	searchBuf     string
	searchIdx     int
	searchMatches []string
	restoreInput  string // input buffer to restore when search is cancelled

	// modelPickerOpen enables the interactive /model selector (Claude Code
	// style): ↑/↓ move the highlight, Enter confirms, Esc cancels, a digit
	// jumps to that line. The picker is rendered as a FIXED overlay (like the
	// help / permission boxes) so it is always fully visible regardless of the
	// conversation scroll position or how many models there are. modelPickerIdx
	// is the highlighted row; modelPickerTop is the first visible row of the
	// model list (an internal scroll window that keeps the highlight on screen
	// when the list is taller than the viewport).
	modelPickerOpen bool
	modelPickerIdx  int
	modelPickerTop  int

	// resumePickerOpen enables the interactive /resume session selector
	// (opened by /resume with no arguments in raw mode). Same overlay
	// interaction pattern as the model picker: ↑/↓ move, Enter resumes,
	// Esc cancels.
	resumePickerOpen bool
	resumePickerIdx  int
	resumePickerTop  int
	resumeSessions   []SessionInfo

	// scrollbar geometry cached from the last render so mouse handlers can map
	// a click/drag to a scroll offset without recomputing the conversation.
	sbMaxOff int
	sbTop    int
	sbBottom int

	// statusNotice is a one-line flash message shown in the status bar (e.g.
	// "✓ Model switched to deepseek-v4-flash"), cleared after the next render.
	statusNotice string

	// diffBoxOpen enables the staged-edits diff review overlay (opened
	// automatically when staged edits exist, or via `/review`). While active,
	// printable keys dismiss the overlay, Enter accepts all edits, Ctrl+Z
	// rejects them.
	diffBoxOpen bool
	diffEdits   []searchreplace.StagedEdit // staged edits to review
	diffIdx     int                        // highlighted row in the box

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
	if cfg.Version != "" {
		tuiVersion = cfg.Version
	}
	secLvl := "local"
	vimMode := false
	statusVisible := true
	if c, err := config.Load(); err == nil {
		if c.SecurityLevel != "" {
			secLvl = string(c.SecurityLevel)
		}
		vimMode = c.TUI.Vim
		if c.TUI.ShowStatusLine != nil {
			statusVisible = *c.TUI.ShowStatusLine
		}
	}
	return &TUI{
		mode:          cfg.Mode,
		model:         cfg.Model,
		provider:      cfg.Provider,
		lang:          cfg.Lang,
		theme:         cfg.Theme,
		securityLevel: secLvl,
		version:       cfg.Version,
		callback:      cfg.Callback,
		reader:        os.Stdin,
		writer:        os.Stdout,
		streamDone:    make(chan struct{}, 1),
		width:         80,
		height:        24,
		lastRenderW:   80,
		lastRenderH:   24,
		histIdx:       -1,
		dirEntries:    listCwd(),

		welcomeVisible: true, // show the startup banner on a fresh session
		vimMode:        vimMode,
		vimInsert:      true,
		statusVisible:  statusVisible,
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
			// OSC 8 hyperlinks need a modern terminal; Windows conhost's legacy
			// VT path may garble the sequence, so gate on TERM_PROGRAM / WT_SESSION
			// / KITTY_WINDOW_ID / WEZTERM_PANE / TMUX (which forwards OSC 8).
			t.osc8 = osc8Supported()
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

// osc8Supported reports whether the terminal advertises OSC 8 hyperlink
// support. Conservative: unknown terminals get plain underline links.
func osc8Supported() bool {
	env := os.Getenv("TERM_PROGRAM")
	if env == "iTerm.app" || env == "WezTerm" || env == "Hyper" || env == "vscode" {
		return true
	}
	if os.Getenv("WT_SESSION") != "" || os.Getenv("KITTY_WINDOW_ID") != "" ||
		os.Getenv("WEZTERM_PANE") != "" || os.Getenv("TERM_PROGRAM_VERSION") != "" {
		return true
	}
	if t := os.Getenv("TERM"); strings.Contains(t, "xterm-kitty") ||
		strings.Contains(t, "wezterm") || strings.Contains(t, "foot") ||
		strings.Contains(t, "tmux") || strings.Contains(t, "screen") {
		return true
	}
	// Windows Terminal exposes WT_SESSION; legacy conhost does not.
	return false
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
		t.pushHistory(text)
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
		return "plan > "
	case ModeYOLO:
		return "yolo > "
	default:
		return "> "
	}
}

func (t *TUI) printBanner() {
	cwd, _ := os.Getwd()
	fmt.Fprintln(t.writer)
	fmt.Fprintln(t.writer, strings.Repeat("─", 60))
	fmt.Fprintf(t.writer, "* iCode %s  %s\n", appVersionStr(), shortDir(cwd))
	fmt.Fprintf(t.writer, "  Model: %s  Mode: %s\n", t.model, t.mode)
	fmt.Fprintln(t.writer, strings.Repeat("─", 60))
	fmt.Fprintln(t.writer, "  "+t.tstr("banner.hint"))
	fmt.Fprintln(t.writer)
}

func (t *TUI) printUser(text string) {
	fmt.Fprintf(t.writer, "  > %s\n\n", text)
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
	t.statusNotice = " " + t.paint("green", "[x]") + " " + msg
}

// SetPlanPending marks a plan-mode reply as awaiting confirmation. While set,
// the next Enter confirms the plan (and starts execution) and Esc cancels it.
func (t *TUI) SetPlanPending(pending bool) {
	t.mu.Lock()
	t.planPending = pending
	t.mu.Unlock()
	if pending {
		t.notice("计划已生成 — Enter 确认执行 · Esc 放弃")
	} else {
		t.notice("计划已放弃")
	}
	if t.rawMode {
		t.render()
	}
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
