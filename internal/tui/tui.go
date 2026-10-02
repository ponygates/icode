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
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/core/voice"
	"github.com/ponygates/icode/internal/notify"
	"github.com/ponygates/icode/internal/types"
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

// antiPoisonInterval is how often (in renders) the periodic full repaint
// fires. During streaming the animation tick renders ~10x/s, so a stray row
// painted into the console buffer by an outsider survives at most ~2s; while
// idle there are no renders, but the next keystroke triggers one, so a stray
// row disappears on the next key press.
const antiPoisonInterval = 20

type Message struct {
	Role     Role
	Content  string
	Tool     string
	ToolArgs string
	// Folded is the per-block collapse state for tool messages (opencode
	// style): a folded card shows the status line plus a short excerpt, an
	// expanded card shows the full output. Non-tool messages ignore it.
	Folded bool
	// LiveTail holds the volatile in-flight tool output streamed via
	// tool_progress (bash live stdout/stderr). It is kept OUT of Content so
	// the final AppendToolResult never duplicates what already streamed —
	// and render() shows a Claude Code-style scrolling tail window while the
	// tool runs. Cleared as soon as the result arrives.
	LiveTail string
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
	OnSend(text string, attachments []types.Attachment)
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
	// TodoActiveText returns the in-progress todo's display text ("" if none)
	// for the Claude Code-style status bar segment.
	TodoActiveText() string
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
	// OnSetAskUser registers the interactive multiple-choice asker (Claude
	// Code AskUserQuestion parity): the backend engine calls fn when the
	// ask_user_question tool fires, so the TUI can render options and read
	// the user's choice.
	OnSetAskUser(fn func(question string, options []string) (int, error))
	// OnSetAskUserForm registers the multi-question wizard asker (opencode
	// AskQuestion parity): the engine calls fn when ask_user_form fires, so
	// the TUI can render the form and collect all answers.
	OnSetAskUserForm(fn func(questions []tool.FormQuestion) ([]tool.FormAnswer, error))
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
	// OnPermissionNote delivers the reason the user attached to a rejected
	// permission prompt (Tab note). The agent sees it on the next turn.
	OnPermissionNote(toolPrompt, note string)
	// OnMCPApply applies an MCP server change ("add"/"remove") to the live
	// shared pool (connect/disconnect + registry refresh), so /mcp add|remove
	// take effect immediately without a restart (Claude Code parity).
	// Returns a human-readable status line.
	OnMCPApply(action string, name string, cfg config.MCPServerCfg) string
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

// SetOnConfigChanged installs the host hook invoked after every successful
// persistSetting (config write from any TUI slash command).
func (t *TUI) SetOnConfigChanged(fn func(*config.Config)) {
	t.mu.Lock()
	t.onConfigChanged = fn
	t.mu.Unlock()
}

// noteRecentCmd records a dispatched slash command for recency-ranked
// autocomplete. Keeps at most 8 entries, most recent first. Also bumps the
// persisted usage counter (C8) so frequently-used commands rank first.
func (t *TUI) noteRecentCmd(name string) {
	if !strings.HasPrefix(name, "/") {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, 9)
	out = append(out, name)
	for _, c := range t.recentCmds {
		if c != name {
			out = append(out, c)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	t.recentCmds = out
	if t.cmdUsage == nil {
		t.cmdUsage = map[string]int{}
	}
	t.cmdUsage[name]++
	saveCmdUsage(t.cmdUsage)
}

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
	// onConfigChanged, when installed by the host (cmd layer), fires after
	// every persistSetting so engine-side config derivatives — the system
	// prompt carrying the language directive — refresh live.
	onConfigChanged func(*config.Config)
	// recentCmds tracks slash-command usage for recency-ranked autocomplete.
	recentCmds []string
	// cmdUsage persists slash-command usage counts across sessions
	// (~/.icode/command_usage.json). Autocomplete ranks frequently-used
	// commands first — Claude Code style history-based recommendation (C8).
	cmdUsage map[string]int
	// curTool is the tool currently executing while streaming ("⚙ bash" in
	// the status bar). Set by AddToolMessage, cleared by AppendToolResult.
	curTool string
	// zenMode hides tool-output noise — all tool cards render folded so the
	// user sees only the conversation (OpenCode Zen parity).
	zenMode bool
	// voiceRec is the live CLI microphone capture (/voice toggle).
	voiceRec *voice.Recorder
	// gitBranch caches the workspace branch for the status bar; refreshed
	// lazily (branchChecked) so no git exec happens per frame.
	gitBranch   string
	branchCheck time.Time

	// prBadge caches the current GitHub PR status for the status bar, e.g.
	// "#123 OPEN". Refreshed lazily (prCheck) via `gh pr view` so no gh exec
	// happens on the render hot path. Empty when there is no PR for the branch
	// or `gh` is unavailable.
	prBadge string
	prCheck time.Time

	// lastActivity / lastAutoRecap power the "auto-recommend after 3 min away"
	// feature (Claude Code parity): when the user has been idle for 3 minutes
	// with no streaming in flight, iCode runs a lightweight /recap automatically
	// (cooled down so it never spams). lastActivity is bumped on every key
	// event, on submit, and when a turn finishes (EndStream).
	lastActivity  time.Time
	lastAutoRecap time.Time

	// input autocomplete state (raw mode)
	acOpen  bool
	acItems []acItem
	acIdx   int

	// tool output folding (Claude Code-style)
	toolFolded bool

	// toolHeadRows maps the last-rendered screen row (0-based, row of the
	// header line) → index into t.messages for each tool card. Filled by
	// render(), consumed by handleMouse() so a click on a card header toggles
	// that block's fold state.
	toolHeadRows map[int]int

	// planPending is set when a plan-mode reply finished and awaits the user's
	// go/no-go: Enter confirms and executes, Esc cancels.
	planPending bool

	mu       sync.Mutex
	messages []Message

	streaming  bool
	streamBuf  strings.Builder
	streamDone chan struct{}

	// Streaming-time message queue (Claude Code parity): typed-ahead input
	// holds queued messages; Enter moves input there; the oldest auto-sends
	// as the next turn when the current one finishes. ↑ recalls the oldest.
	queue []string

	// askPending is the in-flight interactive multiple-choice question
	// (Claude Code AskUserQuestion parity): set while the ask_user_question
	// tool waits; main loop routes 1-9/Enter/Esc into it; render draws it.
	askPending *askState
	// askForm is the in-flight multi-question wizard (opencode AskQuestion
	// parity): set while ask_user_form waits; main loop routes digits/
	// Enter/Tab/Esc into it; render draws the form overlay.
	askForm *askFormState

	// Pasted-block folding (Claude Code parity): large clipboard/bracketed
	// pastes collapse to a "[粘贴 N 行 #k]" placeholder in the input box so a
	// 200-line dump doesn't spam the one-line prompt; the real content lives in
	// pasteBlocks and is expanded back at submit time (so the model still gets
	// the full text). pasteSeq numbers each folded block uniquely.
	pasteBlocks map[string]string
	pasteSeq    int

	// Double-Esc state (Claude Code parity): lastEscAt timestamps the previous
	// lone Esc; rewindArmed arms the rewind on the first double-tap so a
	// second double-tap is required to actually roll back (防误触).
	lastEscAt   time.Time
	rewindArmed bool

	// stashBuf holds the Ctrl+S-stashed prompt (Claude Code parity): with text
	// in the box Ctrl+S stashes it and clears the input; with an empty box it
	// restores the stashed text back into the input.
	stashBuf string

	// backgrounded is set by Ctrl+B: the agent keeps running while the UI
	// returns to the prompt (Claude Code parity). Messages typed meanwhile are
	// queued and auto-sent when the background turn finishes.
	backgrounded bool
	// rawState remembers the cooked-mode terminal state so Ctrl+G ($EDITOR)
	// can suspend raw mode and restore it afterwards.
	rawState *term.State

	// thinkingOn tracks the Alt+T extended-thinking toggle so the shortcut
	// flips state instead of only reporting it (Claude Code parity).
	thinkingOn bool

	// transcriptVerbose tracks Ctrl+O: when true every tool block is expanded
	// to its full arguments/output (Claude Code's transcript viewer).
	transcriptVerbose bool

	// permNoteOpen / permNoteBuf power the Tab "explain why" field on the
	// permission prompt (Claude Code parity): the note is delivered with the
	// decision so the agent learns the reason it was rejected.
	permNoteOpen bool
	permNoteBuf  string
	// pendingQueueFlush asks the MAIN loop to send the oldest queued message
	// once a backgrounded turn has completed (never sent from the engine
	// goroutine, which must not touch keyCh).
	pendingQueueFlush bool

	// undoStack snapshots (inputBuf, cursor) before each edit so Ctrl+_ can
	// restore the previous input state (readline-style undo).
	undoStack []struct {
		buf    string
		cursor int
	}

	// ansiPending holds a trailing incomplete ANSI escape from the previous
	// streamed chunk (one escape can be split across two chunks); it is
	// prepended to the next chunk before sanitising.
	ansiPending string

	// renderPending / renderTimer coalesce full-screen redraws so a burst of
	// streamed tokens doesn't trigger one expensive redraw per chunk.
	renderPending bool
	renderTimer   *time.Timer

	// turnStart timestamps when a generation begins (for the status bar clock).
	turnStart time.Time
	// bellOn toggles the task-completion bell (Claude Code parity): when a
	// turn ran longer than bellMinTurnSec the CLI emits BEL (\x07) so the
	// terminal flashes its tab / raises a notification. Defaults to true.
	bellOn bool

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

	// keyCh receives every rune the single key-pump goroutine reads from the
	// terminal in raw mode. The main loop, drainStream and — via permKeyCh —
	// the permission prompt consume from these channels, so exactly one
	// goroutine ever touches t.reader (no concurrent reads, no stolen keys).
	keyCh chan rune
	// byteCh carries RAW bytes from rawBytePump (the only goroutine touching
	// t.reader) to keyPump, which aggregates escape sequences and decodes
	// UTF-8. The split lets keyPump wait on a byte WITH a timeout — bufio
	// has no deadline reads — which is what makes sequence reassembly (see
	// pumpEscape in raw_input.go) possible.
	byteCh        chan byte
	permKeyCh     chan rune
	keyStop       chan struct{} // close to stop the key pump on session exit
	keyReaderDone chan struct{} // closed when the key pump exits (EOF/error)

	// inputTrace is a small ring of the most recent raw stdin bytes (see
	// traceByte / dumpInputTrace in raw_input.go). When the key parser hits
	// an anomaly — an unknown CSI that had to be drained, a truncated X10
	// mouse report, an invalid UTF-8 burst — the ring is dumped to cli.log
	// so the exact byte stream that produced a "garbled input" report can be
	// reconstructed from the user's machine without a live debugger.
	inputTrace    [256]byte
	inputTracePos int

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
	// permSeverity colours the approval box by risk: "high" red, "medium"
	// yellow, "low" cyan (see permission.RiskSeverity).
	permSeverity string

	// titleDir is the cached short workdir name for the dynamic terminal
	// title (see setTermTitle), captured once when the raw loop starts.
	titleDir string

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

	// prompt is the modal one-line question used by /login and /logout. While
	// it is set, handleKey routes every key to it and render draws the prompt
	// box instead of the input line; a secret prompt masks its echo so an API
	// key never reaches the input buffer, history, or the transcript.
	prompt *promptState

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

	// replayOpen enables the /replay checkpoint timeline overlay (Claude
	// Code /replay parity): it lists the session's shadow-git checkpoints
	// newest-first; ↑/↓ move, Enter shows what THAT step changed (a one-step
	// diff), r rewinds the workspace back to that checkpoint (first press
	// previews the revert diff, second press confirms), Esc closes.
	replayOpen  bool
	replayIdx   int
	replayTop   int
	replayList  []checkpoint.Entry
	replayArmed bool // first r previews the revert; a second r executes it

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

	// settingsOpen shows the settings overlay panel.
	settingsOpen   bool
	settingsCursor int // highlighted row in the settings panel
	// settingsCfg caches the config snapshot shown in the settings panel. It
	// is (re)loaded once when the panel opens — render() runs many times per
	// second while the panel is open (typing, animations, resize), and calling
	// config.Load() (disk read + YAML parse) on every frame is wasteful.
	settingsCfg *config.Config

	// mouseOn tracks whether SGR mouse tracking is enabled. When off the
	// terminal regains native click-drag text selection; Shift+drag always
	// selects regardless. Toggled by /mouse (session-scoped, not persisted).
	mouseOn bool

	// lastRenderW, lastRenderH track the dimensions used in the last frame
	// so render() can detect a size change and issue a full clear.
	lastRenderW int
	lastRenderH int
	// lastFrame caches the previously written conversation rows so render()
	// can do incremental repaints (only rows whose content changed are
	// rewritten) — this is what eliminates the visible flicker on Win10
	// conhost when streaming tokens repaint the whole screen every frame.
	lastFrame []string

	// renderCount ticks once per render; every antiPoisonInterval frames the
	// next render is forced to repaint EVERY row. Rationale: incremental
	// repaint never rewrites "unchanged" rows, so anything written into the
	// console buffer by an outsider (a revived conhost ECHO, an IME writing
	// its composition straight into the buffer, a zombie second instance)
	// would stay pinned on screen forever. A periodic full repaint washes
	// such stray rows off within a couple of seconds, single-flush and
	// content-identical, so it is invisible to the user.
	renderCount int

	// fullRepaintSoon forces the next render() to start from a blank screen:
	// used when conhost revived ECHO and echoed stray characters into the
	// conversation area (incremental repaint would never cover them).
	fullRepaintPending bool

	// sessionTitle is the active session's title, shown in the status line
	// (set by autoTitle and /rename).
	sessionTitle string

	// skillCount caches the number of installed skills (lazily computed once,
	// never on the render hot path).
	skillCount       int
	skillCountLoaded bool
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
	zenMode := false
	statusVisible := true
	bellOn := true
	mouseOn := true
	if c, err := config.Load(); err == nil {
		if c.SecurityLevel != "" {
			secLvl = string(c.SecurityLevel)
		}
		vimMode = c.TUI.Vim
		zenMode = c.TUI.Zen
		if c.TUI.ShowStatusLine != nil {
			statusVisible = *c.TUI.ShowStatusLine
		}
		if c.TUI.Bell != nil {
			bellOn = *c.TUI.Bell
		}
		if c.TUI.Mouse != nil {
			mouseOn = *c.TUI.Mouse
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
		mouseOn:       mouseOn, // /mouse toggle, persisted via config.TUI.Mouse
		streamDone:    make(chan struct{}, 1),
		keyCh:         make(chan rune, 32),
		byteCh:        make(chan byte, 512),
		permKeyCh:     make(chan rune, 8),
		keyStop:       make(chan struct{}),
		keyReaderDone: make(chan struct{}),
		width:         80,
		height:        24,
		lastRenderW:   80,
		lastRenderH:   24,
		histIdx:       -1,
		dirEntries:    listCwd(),
		cmdUsage:      loadCmdUsage(),
		lastActivity:  time.Now(),

		welcomeVisible: true, // show the startup banner on a fresh session
		vimMode:        vimMode,
		vimInsert:      true,
		zenMode:        zenMode,
		statusVisible:  statusVisible,
		bellOn:         bellOn,
	}
}

// ── Lifecycle ────────────────────────────────────────────────────

// Run selects the best available rendering mode and starts the loop.
func (t *TUI) Run() error {
	t.running = true

	// Register the interactive multiple-choice asker so the engine's
	// ask_user_question tool can render options and read the user's choice
	// (Claude Code AskUserQuestion parity). Armed here — not NewTUI — so unit
	// tests never inject the asker into a shared engine.
	if t.callback != nil {
		t.callback.OnSetAskUser(t.askUserInteractive)
		t.callback.OnSetAskUserForm(t.askUserFormInteractive)
	}

	// Arm input-history persistence and restore previous prompts so ↑ recalls
	// them across sessions (Claude Code parity). Armed here rather than in
	// NewTUI so unit tests — which build a TUI and call pushHistory directly —
	// never touch ~/.icode/input_history.json.
	historyPersistActive = historyPersistEnabled()
	if h := loadInputHistory(); len(h) > 0 {
		t.history = h
	}

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		// Full-screen rendering is only safe when the terminal actually
		// parses ANSI positioning/clear sequences. On legacy Windows conhost
		// (cmd.exe / PowerShell windows) that requires the
		// ENABLE_VIRTUAL_TERMINAL_PROCESSING bit, which cmd's fixConsole-
		// Codepage enables — but on pre-Win10-1511 consoles (or when that
		// call silently failed) the bit stays off and every escape sequence
		// is dropped: frames stream out as plain text, the physical cursor
		// wanders mid-screen, and typed characters land in the log area
		// (the "input text runs up the screen" bug). Degrade to line mode —
		// plain, but never misplaced.
		if !vtProcessingActive() {
			return t.runLine()
		}
		if state, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, state)
			// Belt and braces: some Windows console configurations keep the
			// ECHO/LINE bits alive after MakeRaw — ConHost then echoes keys at
			// the physical cursor (text landing in the log area).
			hardenConsoleInput()
			diagInputMode("after-MakeRaw")
			// Remember the cooked-mode state so Ctrl+G can hand the terminal
			// to $EDITOR and take it back afterwards (suspendRaw/resumeRaw).
			t.rawState = state
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
		text = t.expandPasteBlocks(text)
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
				sentText, atts := t.expandFileRefs(text)
				t.callback.OnSend(sentText, atts)
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

// Messages returns a snapshot copy of the visible message list. Hosts (CLI
// callbacks, desktop bridge, tests) use it to inspect what the TUI rendered
// without reaching into the guarded slice.
func (t *TUI) Messages() []Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Message, len(t.messages))
	copy(out, t.messages)
	return out
}

// PromptPermission shows an interactive approval dialog and blocks until the
// user answers. It is invoked from the engine's permission handler, which runs
// on the streaming goroutine while the main loop is parked in drainStream — so
// we read the decision key directly from the terminal.
// submitPermNote finalises a permission decision that carries a reason. The
// note is handed to the agent through the callback so it sees WHY the action
// was rejected; an empty note behaves as a plain deny.
func (t *TUI) submitPermNote() permission.Decision {
	t.mu.Lock()
	note := strings.TrimSpace(t.permNoteBuf)
	permPrompt := t.permPrompt
	t.mu.Unlock()
	t.clearPerm()
	if note != "" && t.callback != nil {
		t.callback.OnPermissionNote(permPrompt, note)
	}
	return permission.DecisionDeny
}

func (t *TUI) PromptPermission(prompt, severity string) permission.Decision {
	if !t.rawMode {
		// Non-interactive (piped) — auto-approve to avoid a hang. This is a
		// silent security footgun (Claude Code refuses or needs an explicit
		// flag), so at least surface a warning to the user's terminal.
		fmt.Fprintln(t.writer, "[iCode] 非交互模式下自动批准工具权限请求（如需限制请用 /mode 或 --dangerously-skip-permissions 类似机制）")
		return permission.DecisionAllow
	}

	t.mu.Lock()
	t.permPending = true
	t.permPrompt = prompt
	t.permSeverity = severity
	t.mu.Unlock()
	t.render()

	// Waiting-for-approval toast (Claude Code parity): the whole turn is
	// BLOCKED until the user answers, so a backgrounded/tabbed-away terminal
	// must be called back — this is the single most important notification
	// moment. One toast per prompt (not repeated), gated on /bell.
	if t.bellOn {
		title := "iCode 正在等待你的批准"
		if severity == permission.SeverityHigh {
			title = "⚠ iCode 高危操作待确认"
		}
		firstLine := strings.SplitN(prompt, "\n", 2)[0]
		notify.Notify(title, truncVisible(firstLine, 60))
	}
	// Tab title flips to the waiting state — highest priority prefix, since
	// the turn is parked until the user answers.
	t.setTermTitle("⚠")

	// Decision keys come from permKeyCh, which the single key-pump goroutine
	// fills while permPending is set. Reading here (instead of from t.reader
	// directly, as the old code did) guarantees the pump is the only reader of
	// the terminal, so a streaming drainStream goroutine can never steal the
	// decision keys.
	for {
		select {
		case r, ok := <-t.permKeyCh:
			if !ok {
				// Key pump exited (stdin EOF) — deny rather than hang.
				t.clearPerm()
				return permission.DecisionDeny
			}
			traceKeySite("perm", r)
			switch r {
			case 0x09: // Tab — open the "explain why" note field (Claude Code)
				if !t.permNoteOpen {
					t.permNoteOpen = true
					t.permNoteBuf = ""
					t.render()
					continue
				}
				// Second Tab closes the field without a note.
				t.permNoteOpen = false
				t.permNoteBuf = ""
				t.render()
				continue
			case '1', 'y', 'Y', '\r', '\n':
				// With the note field open, Enter submits "deny + reason".
				if t.permNoteOpen {
					return t.submitPermNote()
				}
				t.clearPerm()
				return permission.DecisionAllow
			case '2', 'a', 'A':
				if t.permNoteOpen {
					return t.submitPermNote()
				}
				t.clearPerm()
				return permission.DecisionAllowAll
			case '3', 'n', 'N': // '3' or 'n' → deny (with note when open)
				return t.submitPermNote()
			case 0x03, 0x1b: // Ctrl+C or Esc → deny
				// 0x1b may be the start of an escape sequence (mouse wheel,
				// arrow keys) rather than a real Esc press. The sequence's
				// printable tail used to be fed back as DECISION keys — a
				// wheel report contains coordinate DIGITS, so scrolling
				// could silently answer '1' (allow) or '2' (allow-all)!
				// The key pump now delivers sequences as one atomic burst,
				// so a follow-up byte that arrives immediately marks a
				// sequence: swallow it whole and keep waiting. Only a
				// follow-up timeout (genuine lone Esc) denies.
				if r == 0x1b {
					select {
					case u, ok2 := <-t.permKeyCh:
						if !ok2 {
							t.clearPerm()
							return permission.DecisionDeny
						}
						traceKeySite("perm", u)
						if u == '[' {
							if c2, ok3 := readRuneTimeout(t.permKeyCh, escSwallowWait); ok3 {
								t.swallowCSI(t.permKeyCh, c2)
							}
							continue // sequence swallowed — not a decision
						}
						if u == 'O' {
							readRuneTimeout(t.permKeyCh, escSwallowWait)
							continue // SS3 swallowed
						}
						continue // Alt+key swallowed
					case <-time.After(escSwallowWait):
						// No follow-up — a genuine lone Esc: fall through.
						// escSwallowWait (not escFollowTimeout) so a ConPTY-split
						// sequence still reassembles instead of denying.
					}
				}
				if t.permNoteOpen {
					// Esc closes the note field first (Claude Code behaviour);
					// Ctrl+C still denies outright.
					if r == 0x1b {
						t.permNoteOpen = false
						t.permNoteBuf = ""
						t.render()
						continue
					}
				}
				t.clearPerm()
				return permission.DecisionDeny
			default:
				// Printable characters go into the note while it's open.
				if t.permNoteOpen && r >= 0x20 && r != 0x7f {
					t.permNoteBuf += string(r)
					t.render()
					continue
				}
				if t.permNoteOpen && (r == 0x7f || r == 0x08) {
					if rs := []rune(t.permNoteBuf); len(rs) > 0 {
						t.permNoteBuf = string(rs[:len(rs)-1])
						t.render()
					}
					continue
				}
			}
		case <-t.keyReaderDone:
			// Terminal closed while waiting — deny instead of blocking forever.
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
	t.permSeverity = ""
	streaming := t.streaming
	t.mu.Unlock()
	t.render()
	// The ⚠ title prefix was set on entering the approval wait; restore the
	// turn state (still generating, or idle).
	if streaming {
		t.setTermTitle("⏳")
	} else {
		t.setTermTitle("")
	}
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

// ── Multi-line input support (Claude Code parity) ─────────────────────────

// inputLineCount returns how many display lines the input buffer occupies
// (content is split on "\n" — Ctrl+J inserts new lines).
func (t *TUI) inputLineCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Count(t.inputBuf, "\n") + 1
}

// queueRows is 1 when the streaming-time queue indicator should show (typed
// buffer or queued messages present), 0 otherwise.
func (t *TUI) queueRows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) > 0 {
		return 1
	}
	return 0
}

// queueLine renders the queue indicator line shown above the input box while
// the agent is streaming and the user has typed ahead.
func (t *TUI) queueLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) > 0 {
		return t.paint("dim", fmt.Sprintf("⏳ 已排队 (%d): %s", len(t.queue), t.queue[0]))
	}
	return ""
}

// inputVisibleLines is the max number of input-box rows shown at once. The
// box grows with content up to this cap and scrolls internally afterwards
// (tail-anchored, cursor kept visible) — same feel as Claude Code.
func (t *TUI) inputVisibleLines(H int) int {
	rows := 1 // prompt area margin
	if t.statusVisible {
		rows++
	}
	t.mu.Lock()
	hasCtx := t.contextWindow > 0 && t.contextTokens >= 0
	t.mu.Unlock()
	if hasCtx {
		rows++
	}
	v := H - rows - 1
	if v > 8 {
		v = 8
	}
	if v < 1 {
		v = 1
	}
	return v
}

// inputCursorPos maps an absolute rune cursor to (lineIndex, colIndex) within
// the input buffer's lines.
func inputCursorPos(inputBuf string, cursor int) (int, int) {
	lines := strings.Split(inputBuf, "\n")
	if cursor < 0 {
		cursor = 0
	}
	pos := 0
	lineIdx, colIdx := 0, 0
	for i, ln := range lines {
		rl := len([]rune(ln))
		if cursor <= pos+rl {
			lineIdx, colIdx = i, cursor-pos
			return lineIdx, colIdx
		}
		pos += rl + 1 // +1 for the newline itself
		lineIdx, colIdx = i, rl
	}
	return lineIdx, colIdx
}

// inputAbsCursor converts (lineIndex, colIndex) back to an absolute rune
// offset, clamped to the target line's length.
func inputAbsCursor(inputBuf string, lineIdx, colIdx int) int {
	lines := strings.Split(inputBuf, "\n")
	if lineIdx < 0 {
		lineIdx = 0
	}
	if lineIdx >= len(lines) {
		lineIdx = len(lines) - 1
	}
	pos := 0
	for i := 0; i < lineIdx; i++ {
		pos += len([]rune(lines[i])) + 1
	}
	rl := len([]rune(lines[lineIdx]))
	if colIdx < 0 {
		colIdx = 0
	}
	if colIdx > rl {
		colIdx = rl
	}
	return pos + colIdx
}
