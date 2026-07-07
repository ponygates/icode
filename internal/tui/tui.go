package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ponygates/icode/internal/agent"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/permissions"
	"github.com/ponygates/icode/internal/provider"
	"github.com/ponygates/icode/internal/provider/optimizer"
	"github.com/ponygates/icode/internal/tools"
)

type lineEntry struct {
	style lipgloss.Style
	text  string
}

type statusBarData struct {
	permMode  string
	privMode  string
	modelKey  string
	price     string
	turns     int
	modelName string
	provider  string
	thinking  bool
}

type model struct {
	cfg        *config.Config
	agent      *agent.Agent
	perm       *permissions.Manager
	reg        *provider.Registry

	viewport   viewport.Model
	lines      []lineEntry
	input      string

	processing bool
	thinking   bool
	turnCount  int
	width      int
	height     int

	permMode   string
}

func initialModel(cfg *config.Config, reg *provider.Registry) *model {
	perm := permissions.New(
		cfg.Permission.Mode,
		cfg.Permission.ReadOnlyDirs,
		cfg.Permission.DenyDirs,
		cfg.Permission.BashDenyCmds,
		".",
	)

	pk := provider.ParseModelKey(cfg.Provider.Default)
	prov := reg.Get(pk.Name)

	toolCfg := tools.Config{
		WorkspaceRoot: ".",
		Permissions:   perm,
		Timeout:       120 * time.Second,
	}
	allTools := []agent.Tool{
		tools.NewReadTool(toolCfg),
		tools.NewWriteTool(toolCfg),
		tools.NewEditTool(toolCfg),
		tools.NewBashTool(toolCfg),
		tools.NewGrepTool(toolCfg),
		tools.NewGlobTool(toolCfg),
	}
	agt := agent.New(prov, allTools, agent.Config{
		SystemPrompt: defaultSystemPrompt,
		MaxTurns:     cfg.Permission.MaxTurns,
		MaxTokens:    4096,
		Model:        pk.Model,
		Profile:      optimizer.ForProvider(pk.Name, pk.Model),
	})

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)
	vp.KeyMap = viewport.KeyMap{}

	return &model{
		cfg:      cfg,
		agent:    agt,
		perm:     perm,
		reg:      reg,
		viewport: vp,
		permMode: cfg.Permission.Mode,
	}
}

// ─── Messages ────────────────────────────────────────────────

type agentEventMsg struct {
	event agent.StreamEvent
}

type agentDoneMsg struct {
	err error
}

// ─── Init ────────────────────────────────────────────────────

func (m *model) Init() tea.Cmd {
	m.addLine(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")), m.banner())
	m.addLine(lipgloss.NewStyle().Foreground(lipgloss.Color("240")), "Ctrl+P/E/A/Y=Mode  Ctrl+L=Clear  Ctrl+C=Quit  /help=Commands")
	m.addLine(lipgloss.NewStyle(), "")
	return nil
}

// ─── Update ──────────────────────────────────────────────────

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 2
		m.viewport.Height = msg.Height - 6
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case agentEventMsg:
		return m.handleAgentEvent(msg)

	case agentDoneMsg:
		m.processing = false
		m.thinking = false
		if msg.err != nil {
			m.addLine(errorStyle, fmt.Sprintf("Error: %v", msg.err))
		}
		m.viewport.GotoBottom()
		return m, nil

	case error:
		m.addLine(errorStyle, fmt.Sprintf("Error: %v", msg))
		m.processing = false
		m.thinking = false
		return m, nil
	}

	return m, nil
}

// ─── Key Handling ────────────────────────────────────────────

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.processing {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEnter:
		return m.handleSubmit()

	case tea.KeyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil

	case tea.KeyRunes:
		m.input += string(msg.Runes)
		return m, nil

	case tea.KeySpace:
		m.input += " "
		return m, nil

	case tea.KeyTab:
		return m, nil

	case tea.KeyUp:
		return m, nil

	case tea.KeyDown:
		return m, nil
	}

	return m.handleCtrlKey(msg)
}

func (m *model) handleCtrlKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+p":
		m.setPermMode("plan")
		return m, nil
	case "ctrl+a":
		m.setPermMode("ask")
		return m, nil
	case "ctrl+e":
		m.setPermMode("auto")
		return m, nil
	case "ctrl+y":
		m.setPermMode("yolo")
		return m, nil
	case "ctrl+l":
		m.lines = nil
		m.agent.ClearHistory()
		m.addLine(infoStyle, "history cleared")
		return m, nil
	}

	return m.handleCommand("/" + msg.String())
}

func (m *model) handleSubmit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input)
	m.input = ""

	if input == "" {
		return m, nil
	}

	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}

	m.processing = true
	m.thinking = true
	m.addLine(userStyle, "> "+input)
	m.viewport.GotoBottom()

	return m, m.runAgent(input)
}

func (m *model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	switch {
	case cmd == "/quit" || cmd == "/exit":
		return m, tea.Quit

	case cmd == "/clear":
		m.lines = nil
		m.agent.ClearHistory()
		m.addLine(infoStyle, "history cleared")
		return m, nil

	case cmd == "/help":
		m.addLine(helpStyle, m.helpText())
		return m, nil

	case cmd == "/providers":
		for _, name := range m.reg.List() {
			p := m.reg.Get(name)
			m.addLine(infoStyle, fmt.Sprintf("  %-12s %s", name, strings.Join(p.Models(), ", ")))
		}
		return m, nil

	case cmd == "/profile":
		prof := m.agent.Profile()
		m.addLine(infoStyle, fmt.Sprintf("Provider:     %s", prof.Provider))
		m.addLine(infoStyle, fmt.Sprintf("Model:        %s", prof.Model))
		m.addLine(infoStyle, fmt.Sprintf("Temperature:  %.1f", prof.Temperature))
		m.addLine(infoStyle, fmt.Sprintf("TopP:         %.1f", prof.TopP))
		m.addLine(infoStyle, fmt.Sprintf("MaxTokens:    %d", prof.MaxTokens))
		m.addLine(infoStyle, fmt.Sprintf("StripThink:   %v", prof.StripThinkTag))
		if plan := optimizer.GetTokenPlan(prof.Provider, prof.Model); plan != nil {
			m.addLine(infoStyle, fmt.Sprintf("Token Price:  %s", plan.FormatPrice()))
			m.addLine(infoStyle, fmt.Sprintf("Token Plan:   %s", plan.FormatTiers()))
		}
		return m, nil

	case cmd == "/plan":
		prof := m.agent.Profile()
		m.addLine(infoStyle, fmt.Sprintf("Current: %s/%s", prof.Provider, prof.Model))
		if plan := optimizer.GetTokenPlan(prof.Provider, prof.Model); plan != nil {
			m.addLine(infoStyle, fmt.Sprintf("Price:      %s", plan.FormatPrice()))
			m.addLine(infoStyle, fmt.Sprintf("Plans:      %s", plan.FormatTiers()))
			if plan.Notes != "" {
				m.addLine(infoStyle, fmt.Sprintf("Notes:      %s", plan.Notes))
			}
		}
		return m, nil

	case cmd == "/plans":
		seen := make(map[string]bool)
		for _, p := range optimizer.ListAllPlans() {
			key := p.Provider + "/" + p.Model
			if seen[key] {
				continue
			}
			seen[key] = true
			fm := ""
			if p.HasFreeTier && p.Input1M == 0 {
				fm = " 🆓"
			}
			pm := ""
			if p.HasCodingPlan {
				pm = " 📋"
			}
			m.addLine(infoStyle, fmt.Sprintf("  %-15s %-25s %s%s%s",
				p.Provider, p.Model, optimizer.FormatPlanShort(p.Provider, p.Model), fm, pm))
		}
		return m, nil

	case cmd == "/mode":
		prof := m.agent.Profile()
		m.addLine(infoStyle, fmt.Sprintf("perm: %s  priv: %s  model: %s/%s",
			m.cfg.Permission.Mode, m.cfg.Privacy.Mode, prof.Provider, prof.Model))
		return m, nil
	}

	if strings.HasPrefix(cmd, "/model ") {
		key := strings.TrimSpace(strings.TrimPrefix(cmd, "/model "))
		pk := provider.ParseModelKey(key)
		prov := m.reg.Get(pk.Name)
		if prov == nil {
			m.addLine(errorStyle, fmt.Sprintf("unknown provider: %s", pk.Name))
			return m, nil
		}
		prof := optimizer.ForProvider(pk.Name, pk.Model)
		m.agent = agent.New(prov, m.createTools(), agent.Config{
			SystemPrompt: defaultSystemPrompt,
			MaxTurns:     m.cfg.Permission.MaxTurns,
			MaxTokens:    4096,
			Model:        pk.Model,
			Profile:      prof,
		})
		m.addLine(infoStyle, fmt.Sprintf("switched to %s/%s", pk.Name, pk.Model))
		return m, nil
	}

	if strings.HasPrefix(cmd, "/plan ") {
		key := strings.TrimSpace(strings.TrimPrefix(cmd, "/plan "))
		parts := strings.SplitN(key, "/", 2)
		pn, mn := parts[0], ""
		if len(parts) == 2 {
			mn = parts[1]
		}
		if mn == "" {
			for _, p := range optimizer.ListProviderPlans(pn) {
				m.addLine(infoStyle, fmt.Sprintf("  %-25s %s", p.Model, p.FormatPrice()))
			}
		} else if plan := optimizer.GetTokenPlan(pn, mn); plan != nil {
			m.addLine(infoStyle, fmt.Sprintf("%s/%s  %s", plan.Provider, plan.Model, plan.FormatPrice()))
			m.addLine(infoStyle, fmt.Sprintf("  Plans: %s", plan.FormatTiers()))
		}
		return m, nil
	}

	m.addLine(errorStyle, fmt.Sprintf("unknown command: %s", cmd))
	return m, nil
}

func (m *model) createTools() []agent.Tool {
	toolCfg := tools.Config{
		WorkspaceRoot: ".",
		Permissions:   m.perm,
		Timeout:       120 * time.Second,
	}
	return []agent.Tool{
		tools.NewReadTool(toolCfg),
		tools.NewWriteTool(toolCfg),
		tools.NewEditTool(toolCfg),
		tools.NewBashTool(toolCfg),
		tools.NewGrepTool(toolCfg),
		tools.NewGlobTool(toolCfg),
	}
}

func (m *model) setPermMode(mode string) {
	m.permMode = mode
	m.cfg.Permission.Mode = mode
	m.perm = permissions.New(
		mode,
		m.cfg.Permission.ReadOnlyDirs,
		m.cfg.Permission.DenyDirs,
		m.cfg.Permission.BashDenyCmds,
		".",
	)
	m.addLine(infoStyle, fmt.Sprintf("mode → %s", strings.ToUpper(mode)))
}

// ─── Agent Runner ────────────────────────────────────────────

func (m *model) runAgent(input string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		done := make(chan struct{})

		m.agent.OnEvent(func(ev agent.StreamEvent) {
			switch ev.Type {
			case "text":
				ev.Type = "stream"
			}
		})

		err := m.agent.Run(ctx, input)
		close(done)

		// Collect all events and rebuild lines based on history
		_ = done

		if err != nil {
			return agentDoneMsg{err: err}
		}

		// After agent is done, update lines from history
		return agentDoneMsg{}
	}
}

// ─── Agent Event Handling ────────────────────────────────────

func (m *model) handleAgentEvent(msg agentEventMsg) (tea.Model, tea.Cmd) {
	switch msg.event.Type {
	case "text", "stream":
		m.addInlineContent(msg.event.Content)
	case "tool_call":
		m.addLine(toolStyle, "🔧 "+msg.event.Content)
	case "tool_result":
		result := msg.event.Content
		if len(result) > 300 {
			result = result[:300]
		}
		m.addLine(resultStyle, "📎 "+result)
	case "done":
		m.processing = false
		m.thinking = false
		m.turnCount++
	}
	m.viewport.GotoBottom()
	return m, nil
}

// ─── View ────────────────────────────────────────────────────

func (m *model) View() string {
	if m.width == 0 {
		m.width = 80
		m.height = 24
	}

	status := m.statusView()
	statusHeight := lipgloss.Height(status)

	msgContent := lipgloss.JoinVertical(lipgloss.Top, m.renderLines()...)
	m.viewport.SetContent(msgContent)
	m.viewport.Height = m.height - statusHeight - 2

	inputView := m.inputView()
	inputHeight := lipgloss.Height(inputView)

	contentHeight := m.height - statusHeight - inputHeight - 2
	if contentHeight < 5 {
		contentHeight = 5
	}
	m.viewport.Height = contentHeight

	return lipgloss.JoinVertical(lipgloss.Top,
		m.viewport.View(),
		inputView,
		status,
	)
}

// ─── Input View ──────────────────────────────────────────────

func (m *model) inputView() string {
	prompt := "> "
	if m.processing {
		prompt = "⋯ "
	}
	inputContent := prompt + m.input
	if m.processing {
		inputContent += " ◌"
	}
	return lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("236")).
		Render(inputContent)
}

// ─── Status Bar ──────────────────────────────────────────────

func (m *model) statusView() string {
	sd := m.statusData()

	left := sd.permBadge() + " " + sd.privBadge() + " " + sd.modelBadge()
	right := sd.priceBadge() + " " + sd.turnBadge()

	bar := lipgloss.NewStyle().
		Padding(0, 1).
		MaxWidth(m.width).
		Render(lipgloss.JoinHorizontal(lipgloss.Top, left, dot, right))

	keyhint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("236")).
		Padding(0, 1).
		Render("Ctrl+P/E/A/Y:mode  Ctrl+L:clear  Ctrl+C:quit  /help")

	return lipgloss.JoinVertical(lipgloss.Top, bar, keyhint)
}

var dot = lipgloss.NewStyle().Foreground(lipgloss.Color("236")).Render(" · ")

func (m *model) statusData() statusBarData {
	pk := provider.ParseModelKey(m.cfg.Provider.Default)
	price := ""
	if plan := optimizer.GetTokenPlan(pk.Name, pk.Model); plan != nil {
		price = optimizer.FormatPlanShort(pk.Name, pk.Model)
	}
	return statusBarData{
		permMode:  m.permMode,
		privMode:  m.cfg.Privacy.Mode,
		modelKey:  m.cfg.Provider.Default,
		price:     price,
		turns:     m.turnCount,
		modelName: pk.Model,
		provider:  pk.Name,
		thinking:  m.processing,
	}
}

func (s statusBarData) permBadge() string {
	var st lipgloss.Style
	var label string
	switch s.permMode {
	case "plan":
		st = badgePlan
		label = "PLAN"
	case "ask":
		st = badgeAsk
		label = "ASK"
	case "auto":
		st = badgeAuto
		label = "AUTO"
	case "yolo":
		st = badgeYOLO
		label = "YOLO"
	default:
		st = badgeAsk
		label = strings.ToUpper(s.permMode)
	}
	if s.thinking {
		label = "◌ " + label
	}
	return st.Render(label)
}

func (s statusBarData) privBadge() string {
	switch s.privMode {
	case "local":
		return badgeLocal.Render("L")
	case "china-trusted":
		return badgeChina.Render("CN")
	case "smart":
		return badgeSmart.Render("SM")
	case "global-audited":
		return badgeGlobal.Render("GL")
	case "full":
		return badgeGlobal.Render("🌍")
	default:
		return badgeSmart.Render(s.privMode[:2])
	}
}

func (s statusBarData) modelBadge() string {
	model := s.modelName
	if len(model) > 20 {
		model = model[:17] + "..."
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("253"))
	return style.Render(model)
}

func (s statusBarData) priceBadge() string {
	if s.price == "" || s.price == "—" {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	return style.Render(s.price)
}

func (s statusBarData) turnBadge() string {
	if s.turns == 0 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(fmt.Sprintf("#%d", s.turns))
}

// ─── Style definitions ───────────────────────────────────────

var (
	badgePlan  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("61")).Padding(0, 1)
	badgeAsk   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("32")).Padding(0, 1)
	badgeAuto  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("220")).Padding(0, 1)
	badgeYOLO  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("196")).Padding(0, 1)

	badgeLocal = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("28")).Padding(0, 1)
	badgeChina = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("130")).Padding(0, 1)
	badgeSmart = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("45")).Padding(0, 1)
	badgeGlobal = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("90")).Padding(0, 1)

	userStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	infoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	resultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
)

const spinner = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// ─── Line Management ─────────────────────────────────────────

func (m *model) addLine(style lipgloss.Style, text string) {
	if text == "" {
		m.lines = append(m.lines, lineEntry{style: style, text: ""})
		return
	}
	m.lines = append(m.lines, lineEntry{style: style, text: text})
}

func (m *model) addInlineContent(content string) {
	if len(m.lines) == 0 {
		emptyStyle := lipgloss.NewStyle()
		m.lines = append(m.lines, lineEntry{style: emptyStyle, text: content})
		return
	}
	last := &m.lines[len(m.lines)-1]
	last.text += content
}

func (m *model) renderLines() []string {
	var result []string
	for _, l := range m.lines {
		if l.text == "" {
			result = append(result, "")
		} else {
			result = append(result, l.style.Render(l.text))
		}
	}
	return result
}

// ─── Banner & Help ───────────────────────────────────────────

func (m *model) banner() string {
	pk := provider.ParseModelKey(m.cfg.Provider.Default)
	price := ""
	if plan := optimizer.GetTokenPlan(pk.Name, pk.Model); plan != nil {
		price = fmt.Sprintf(" (%s)", optimizer.FormatPlanShort(pk.Name, pk.Model))
	}
	return fmt.Sprintf(`  ___   ___  ___  ___  ___
 / _ \ / __|/ _ \| __|/ __|
| (_) | (__|  __/| _| \__ \
 \___/ \___|\___/|___||___|
 iCode v0.1.0 — %s%s`, m.cfg.Provider.Default, price)
}

func (m *model) helpText() string {
	return `Commands:
  /quit, /exit           Exit iCode
  /clear                 Clear history
  /help                  This help
  /providers             List providers
  /model <name>          Switch model
  /profile               Optimization profile
  /plan [/plan <name>]   Token plan details
  /plans                 All token plans
  /mode                  Current modes

Keyboard:
  Ctrl+P   PLAN mode (read-only)
  Ctrl+A   ASK mode (confirm each step)
  Ctrl+E   AUTO mode (auto-execute safe)
  Ctrl+Y   YOLO mode (full auto)
  Ctrl+L   Clear conversation
  Ctrl+C   Quit`
}

const defaultSystemPrompt = `You are iCode, an AI coding assistant powered by a multi-provider LLM engine.

You have access to a set of tools you can use to help the user with their tasks.
Always think through what tools you need and use them sequentially.

Follow these guidelines:
1. First, understand what the user wants.
2. Use tools to explore, read, and modify the codebase.
3. When writing code, follow existing conventions, patterns, and style.
4. Always check what exists before making changes.
5. Use bash for building, testing, and git operations.
6. Be concise and direct in your responses.
7. When making edits, prefer the edit tool for targeted changes.`

// ─── App Entry Point ─────────────────────────────────────────

type App struct {
	cfg *config.Config
	reg *provider.Registry
}

func New(cfg *config.Config, reg *provider.Registry) *App {
	return &App{cfg: cfg, reg: reg}
}

func (a *App) Run() error {
	m := initialModel(a.cfg, a.reg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
