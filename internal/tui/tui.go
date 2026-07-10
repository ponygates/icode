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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
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

// Callback bridges user input / slash commands back to the backend.
type Callback interface {
	OnSend(text string)
	OnSlashCommand(cmd string, args []string)
	OnPermissionResponse(decision string)
	// OnListSessions returns a formatted list of past sessions.
	OnListSessions() string
	// OnResume loads a past session's messages; returns a status line.
	OnResume(id string) string
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
	mode     Mode
	model    string
	provider string
	lang     string
	theme    string
	callback Callback

	// input autocomplete state (raw mode)
	acOpen  bool
	acItems []acItem
	acIdx   int

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
	reader  io.Reader
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

	// pending permission prompt (agent mode, interactive approval)
	permPending bool
	permPrompt  string
}

// New creates a TUI instance.
func New(cfg Config) *TUI {
	if cfg.Mode == "" {
		cfg.Mode = ModeAgent
	}
	return &TUI{
		mode:       cfg.Mode,
		model:      cfg.Model,
		provider:   cfg.Provider,
		lang:       cfg.Lang,
		theme:      cfg.Theme,
		callback:   cfg.Callback,
		reader:     os.Stdin,
		writer:     os.Stdout,
		streamDone: make(chan struct{}, 1),
		width:      80,
		height:     24,
		histIdx:    -1,
		dirEntries: listCwd(),
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
			if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
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
	// Hide cursor while rendering; re-show at the input prompt.
	fmt.Fprint(t.writer, "\x1b[?25l")
	defer fmt.Fprint(t.writer, "\x1b[?25h")

	t.render()
	reader := bufio.NewReader(t.reader)

	for t.running {
		t.render()
		r, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			continue
		}
		if !t.handleKey(r) {
			break
		}
	}
	return nil
}

// handleKey processes a single input rune in raw mode.
// Returns false to signal the loop should exit.
func (t *TUI) handleKey(r rune) bool {
	switch r {
	case 0x03: // Ctrl+C
		if t.streaming {
			return true // streaming cancelled via engine Stop (best effort)
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
	case 0x09: // Tab — accept highlighted suggestion
		if t.acOpen && len(t.acItems) > 0 {
			t.acceptSuggestion()
			return true
		}
		return true
	case 0x1b: // Esc — dismiss suggestions
		if t.acOpen {
			t.acOpen = false
			t.acItems = nil
			return true
		}
		return true
	case '\r', '\n':
		text := strings.TrimSpace(t.inputBuf)
		t.inputBuf = ""
		t.cursor = 0
		if text == "" {
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
	// Slash command
	if strings.HasPrefix(text, "/") {
		t.handleSlash(text)
		return
	}

	// User message
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: text})
	t.mu.Unlock()

	if t.callback != nil {
		t.mu.Lock()
		t.streaming = true
		t.streamBuf.Reset()
		t.turnStart = time.Now()
		t.mu.Unlock()
		go t.callback.OnSend(text)
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
	// re-locking t.mu.
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
	t.mu.Unlock()

	if !rawMode {
		return // line mode renders incrementally, not full-screen
	}

	W := t.width
	H := t.height
	if W < 20 {
		W = 20
	}
	if H < 10 {
		H = 10
	}

	// Bottom status bar: a single compact line, truncated to the terminal
	// width so it stays a thin strip at the bottom (never wraps to several
	// lines, which would eat vertical space).
	statusLine := status
	if visibleWidth(statusLine) > W {
		statusLine = truncVisible(statusLine, W)
	}
	statusW := []string{statusLine}

	// Overlays drawn above the input line.
	acLines := t.autocompleteLines()
	permLines := []string{}
	if permPending {
		permLines = append(permLines,
			t.paint("yellow", "  ⏸ "+t.tstr("perm.title")+": "+truncate(permPrompt, W-14)),
			t.paint("dim", "     [y] "+t.tstr("perm.allow")+"   [a] "+t.tstr("perm.all")+"   [n/^C] "+t.tstr("perm.deny")),
		)
	}

	// Body height = rows between the top header/hrule and the bottom (status
	// separator + status + input + overlays).
	bodyH := H - 1 /*header*/ - 1 /*hrule*/ - 1 /*status sep*/ - len(statusW) - 1 /*input*/ - len(permLines) - len(acLines)
	if bodyH < 3 {
		bodyH = 3
	}

	// Pane widths. The left explorer pane is hidden on narrow terminals so the
	// conversation never gets squeezed.
	paneW := 0
	switch {
	case W >= 110:
		paneW = 36
	case W >= 92:
		paneW = 30
	case W >= 76:
		paneW = 24
	case W >= 64:
		paneW = 20
	}
	rightW := W - paneW
	if paneW > 0 {
		rightW -= 3 // " │ "
	}
	if rightW < 20 {
		rightW = 20
	}

	leftLines := t.leftPaneLines(paneW)
	rightAll := t.conversationLines(msgs, streaming, streamContent, rightW)
	if len(rightAll) > bodyH {
		rightAll = rightAll[len(rightAll)-bodyH:]
	}

	// Compose the split body row-by-row so the vertical frame line stays
	// continuous down the whole height.
	var body []string
	for i := 0; i < bodyH; i++ {
		var left, right string
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightAll) {
			right = rightAll[i]
		}
		if paneW > 0 {
			body = append(body, fit(left, paneW)+t.paint("dim", " │ ")+fit(right, rightW))
		} else {
			body = append(body, fit(right, rightW))
		}
	}

	// Assemble the full screen (everything except the final input line).
	var out []string
	out = append(out, t.headerLine())
	out = append(out, t.hrule(W))
	out = append(out, body...)
	out = append(out, t.hrule(W)) // status bar separator
	for _, sl := range statusW {
		out = append(out, t.paint("dim", sl))
	}
	for _, pl := range permLines {
		out = append(out, pl)
	}
	for _, al := range acLines {
		out = append(out, al)
	}
	maxLines := H - 1
	if len(out) > maxLines {
		out = out[len(out)-maxLines:]
	}

	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
	for _, ln := range out {
		fmt.Fprintln(t.writer, ln)
	}
	for i := len(out); i < maxLines; i++ {
		fmt.Fprintln(t.writer)
	}
	// Input line (no trailing newline).
	inputLine := t.inputRenderFor(inputBuf)
	fmt.Fprint(t.writer, inputLine)
	runes := []rune(inputBuf)
	if !streaming && cursor < len(runes) {
		fmt.Fprintf(t.writer, "\x1b[%dD", len(runes)-cursor)
	}
	fmt.Fprint(t.writer, "\x1b[?25h") // show cursor at prompt
}

// headerLine renders the compact top bar: app name, working directory, mode.
func (t *TUI) headerLine() string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	modeLabel := t.mode
	if modeLabel == "" {
		modeLabel = ModeAgent
	}
	return t.paint("cyan", "◆ iCode") + " " + appVersionStr() +
		t.paint("dim", "  ·  ") + short +
		t.paint("dim", "  ·  mode: ") + modeLabel
}

func (t *TUI) messageLinesW(m Message, width int) []string {
	switch m.Role {
	case RoleThinking:
		return thinkingLines(m.Content, width)
	case RoleUser:
		return wrapPrefixed("  ▸ ", "    ", m.Content, width)
	case RoleAssistant:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "  ◆ ", "    ", width)
		}
		return wrapPrefixed("  ◆ ", "    ", m.Content, width)
	case RoleSystem:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "  ", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleError:
		return wrapPrefixed("  × ", "    ", m.Content, width)
	case RoleTool:
		var out []string
		head := "  » " + m.Tool
		if m.ToolArgs != "" {
			head += " " + truncate(m.ToolArgs, 60)
		}
		out = append(out, t.paint("yellow", head))
		if m.Content != "" {
			for _, l := range wrapPrefixed("    ", "    ", m.Content, width) {
				out = append(out, t.paint("dim", l))
			}
		}
		return out
	}
	return wrapPrefixed("  ", "  ", m.Content, width)
}

// conversationLines builds the right pane: every message (+ the in-flight
// stream), wrapped to the right-pane width. While the model is "thinking"
// (stream started but no tokens yet) a sliding-bar indicator is shown.
func (t *TUI) conversationLines(msgs []Message, streaming bool, streamContent string, width int) []string {
	var lines []string
	all := append([]Message{}, msgs...)
	if streaming {
		all = append(all, Message{Role: RoleAssistant, Content: streamContent})
	}
	for i, m := range all {
		// Replace the empty in-flight assistant message with the thinking bar.
		if streaming && i == len(all)-1 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		if i > 0 {
			lines = append(lines, "") // blank separator between turns
		}
		lines = append(lines, t.messageLinesW(m, width)...)
	}
	if streaming && strings.TrimSpace(streamContent) == "" {
		lines = append(lines, t.paint("dim", "  ◆ "+t.tstr("status.gen"))+"  "+t.thinkingBar())
	}
	return lines
}

// leftPaneLines builds the explorer pane as a clean file tree: the current
// working directory as the root, a divider, then top-level entries with
// folder/file markers. The model/context/cost/mode info lives in the bottom
// status bar instead, which keeps this pane uncluttered.
func (t *TUI) leftPaneLines(paneW int) []string {
	inner := paneW - 2
	if inner < 6 {
		inner = 6
	}
	var L []string
	// Panel title.
	L = append(L, t.paint("cyan", "◆ 文件"))
	// Current directory (tree root).
	if cwd, err := os.Getwd(); err == nil {
		L = append(L, "  "+truncate(shortDir(cwd), inner))
	}
	L = append(L, t.paint("dim", "  "+repeat("─", inner-2)))
	// Entries as a compact tree.
	if len(t.dirEntries) == 0 {
		L = append(L, "  "+t.paint("dim", "(空目录)"))
	}
	for i, f := range t.dirEntries {
		if i >= 14 {
			break
		}
		name := strings.TrimSuffix(f, "/")
		if strings.HasSuffix(f, "/") {
			L = append(L, "  "+t.paint("blue", "▾")+" "+truncate(name, inner-3))
		} else {
			L = append(L, "  "+t.paint("dim", "•")+" "+truncate(name, inner-3))
		}
	}
	return L
}

// contextBar renders a "NN% ▓▓░░" usage meter of width w (display columns).
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
	return fmt.Sprintf("%2d%% ", pct) + strings.Repeat("▓", filled) + strings.Repeat("░", barW-filled)
}

// thinkingBar is a left↔right sliding block that animates while the model
// generates, giving Claude Code's signature "thinking" motion.
func (t *TUI) thinkingBar() string {
	const n = 12
	elapsed := time.Since(t.turnStart)
	frame := int(elapsed.Milliseconds() / 120)
	if frame < 0 {
		frame = 0
	}
	frame %= (2*n - 2)
	pos := frame
	if pos >= n {
		pos = 2*n - 2 - pos
	}
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < n; i++ {
		if i == pos {
			b.WriteString("█")
		} else {
			b.WriteString("░")
		}
	}
	b.WriteString("]")
	return b.String()
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
		for {
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
	}()
}

// SetContext records the latest request's prompt-token count and the model's
// context window so the status bar / explorer can show live context usage.
func (t *TUI) SetContext(tokens, window int) {
	t.contextTokens = tokens
	t.contextWindow = window
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

func (t *TUI) statusLine() string {
	var parts []string
	parts = append(parts, t.mode)
	if t.provider != "" {
		parts = append(parts, t.provider)
	}
	parts = append(parts, t.model)
	if t.promptTokens > 0 || t.completionTokens > 0 {
		parts = append(parts,
			"↑"+formatTokens(t.promptTokens)+" ↓"+formatTokens(t.completionTokens))
	}
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		parts = append(parts, fmt.Sprintf("%d%% ctx", pct))
	}
	if t.cacheHitRate > 0 {
		parts = append(parts, fmt.Sprintf("%.0f%% cache", t.cacheHitRate*100))
	}
	if t.cost != "" {
		parts = append(parts, t.cost)
	}
	if t.streaming {
		if !t.turnStart.IsZero() {
			parts = append(parts, "⏱ "+formatDuration(time.Since(t.turnStart)))
		}
		parts = append(parts, t.thinkingBar()+" "+t.tstr("status.gen"))
	}
	return strings.Join(parts, " · ")
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

func (t *TUI) inputRenderFor(inputBuf string) string {
	prefix := "❯ "
	switch t.mode {
	case ModePlan:
		prefix = t.paint("blue", "plan") + " ❯ "
	case ModeYOLO:
		prefix = t.paint("red", "yolo") + " ❯ "
	case ModeDefault:
		prefix = t.paint("dim", "default") + " ❯ "
	}
	if t.streaming {
		return prefix
	}
	return prefix + inputBuf
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
			go t.callback.OnSend(text)
			t.drainStream()
		}
	}
	return nil
}

func (t *TUI) linePrompt() string {
	switch t.mode {
	case ModePlan:
		return "plan ❯ "
	case ModeYOLO:
		return "yolo ❯ "
	default:
		return "❯ "
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
		fmt.Fprintf(t.writer, "  » %s %s\n", m.Tool, truncate(m.ToolArgs, 60))
		if m.Content != "" {
			for _, l := range strings.Split(m.Content, "\n") {
				fmt.Fprintf(t.writer, "    %s\n", l)
			}
		}
		fmt.Fprintln(t.writer)
	case RoleThinking:
		fmt.Fprintf(t.writer, "  ┌─ thinking ─┐\n  │ %s\n  └──────────┘\n\n", truncate(m.Content, 200))
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
		b.WriteString("\n" + t.tstr("sc.title") + ":\n")
		b.WriteString("  !<command>       " + t.tstr("sc.shell") + "\n")
		b.WriteString("  Ctrl+C           " + t.tstr("sc.ctrlc") + "\n")
		b.WriteString("  Ctrl+L           " + t.tstr("sc.ctrll") + "\n")
		b.WriteString("  Ctrl+P / Ctrl+N  " + t.tstr("sc.history") + "\n")
		b.WriteString("  " + t.tstr("cmd.ac"))
		t.add(RoleSystem, b.String())

	case "/exit", "/quit":
		t.add(RoleSystem, "Goodbye!")
		t.running = false

	case "/model":
		if len(args) > 0 {
			t.model = args[0]
			t.add(RoleSystem, "Model → "+args[0])
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

	case "/keys":
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
		t.add(RoleSystem, fmt.Sprintf("当前设置：\n  Model:    %s\n  Provider: %s\n  Mode:     %s\n  Language: %s\n  Theme:    %s\n  Diff:     %s\n  Syntax:   %s\n\n用 `icode config <key> <value>` 修改，或 `/lang` `/theme` 即时切换。",
			t.model, t.provider, t.mode, lang, theme, diff, syntax))

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

	default:
		if t.callback != nil {
			t.callback.OnSlashCommand(cmd, args)
		}
	}
}

// add appends a message and refreshes the screen.
func (t *TUI) add(role Role, content string) {
	t.AddMessage(role, content)
}

// ── Shell mode ───────────────────────────────────────────────────

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
	t.AddToolMessage("git_diff", "", strings.TrimRight(string(output), "\n"))
}

// ── Helpers ──────────────────────────────────────────────────────

// persistSetting loads config, applies fn, and writes it back to disk.
// Errors are silently ignored — the UI setting still takes effect in-session.
func (t *TUI) persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	fn(cfg)
	_ = cfg.Save(config.DefaultPath())
}

// updateSuggestions recomputes the autocomplete panel based on the current
// input. It opens when the input is empty (showing all commands) or starts
// with "/", and filters as the user types.
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
	t.acOpen = false
	t.acItems = nil
}

// allSuggestions returns every slash command as an autocomplete entry.
func (t *TUI) allSuggestions() []acItem {
	items := make([]acItem, 0, len(slashDefs))
	for _, d := range slashDefs {
		items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
	}
	return items
}

// acceptSuggestion replaces the input with the highlighted command + a space.
func (t *TUI) acceptSuggestion() {
	if len(t.acItems) == 0 {
		return
	}
	it := t.acItems[t.acIdx]
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
		out = append(out, "  │ "+padEnd(l, inner)+"│")
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
		case 'y', 'Y', '\r', '\n':
			t.clearPerm()
			return permission.DecisionAllow
		case 'a', 'A':
			t.clearPerm()
			return permission.DecisionAllowAll
		case 'n', 'N', 0x03: // 'n' or Ctrl+C → deny
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
