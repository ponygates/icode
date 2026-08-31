package lsp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager manages LSP connections and provides code intelligence.
type Manager struct {
	mu      sync.Mutex
	clients map[string]*Client
	rootURI string
}

// NewManager creates an LSP manager for the given workspace root.
func NewManager(rootPath string) *Manager {
	rootURI := "file://" + filepath.ToSlash(rootPath)
	return &Manager{
		clients: make(map[string]*Client),
		rootURI: rootURI,
	}
}

// RootURI returns the root URI.
func (m *Manager) RootURI() string { return m.rootURI }

// StartLanguageServer starts a language server for the given language.
func (m *Manager) StartLanguageServer(ctx context.Context, languageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clients[languageID]; ok {
		return nil
	}

	cmd, args, err := getLanguageServerCommand(languageID)
	if err != nil {
		return fmt.Errorf("lsp %s: %w", languageID, err)
	}

	client, err := NewClient(ctx, m.rootURI, cmd, args...)
	if err != nil {
		return fmt.Errorf("lsp %s start: %w", languageID, err)
	}

	m.clients[languageID] = client
	log.Printf("[iCode LSP] Started %s language server (%s)", languageID, cmd)
	return nil
}

// CloseAll shuts down all language servers.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for lang, client := range m.clients {
		_ = client.Close()
		delete(m.clients, lang)
	}
}

// GetClient returns the LSP client for a language.
func (m *Manager) GetClient(languageID string) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clients[languageID]
}

// OpenFile notifies the language server about an open file.
func (m *Manager) OpenFile(filePath, languageID string) error {
	client := m.GetClient(languageID)
	if client == nil {
		return fmt.Errorf("no LSP client for %s", languageID)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	docURI := "file://" + filepath.ToSlash(absPath)
	return client.OpenTextDocument(docURI, languageID, string(data))
}

// BuildContextInfo collects LSP information for the conversation context.
func (m *Manager) BuildContextInfo(filePaths []string, languageID string) string {
	client := m.GetClient(languageID)
	if client == nil {
		return ""
	}

	var sb strings.Builder
	for _, fp := range filePaths {
		symbols, err := client.WorkspaceSymbols(filepath.Base(fp))
		if err != nil || len(symbols) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n### %s\n", filepath.Base(fp)))
		sb.WriteString("Symbols:\n")
		count := 0
		for _, s := range symbols {
			if count >= 15 {
				sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(symbols)-count))
				break
			}
			if strings.Contains(s.URI, filepath.Base(fp)) {
				sb.WriteString(fmt.Sprintf("  - %s (%s)\n", s.Name, s.Kind))
				count++
			}
		}
	}

	return sb.String()
}

// DetectLanguage determines the programming language for a file.
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".ts", ".jsx", ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".cs":
		return "csharp"
	case ".cpp", ".cc", ".cxx", ".c":
		return "cpp"
	default:
		return ""
	}
}

// DetectProjectLanguages inspects the project root for well-known manifest
// files and returns the language IDs to eagerly start (OpenCode parity:
// auto-load the right LSP from the project, no manual auto_start config).
// Order is stable and de-duplicated.
func DetectProjectLanguages(cwd string) []string {
	if cwd == "" {
		cwd = "."
	}
	type probe struct {
		file string
		lang string
	}
	probes := []probe{
		{"go.mod", "go"},
		{"package.json", "typescript"},
		{"Cargo.toml", "rust"},
		{"pyproject.toml", "python"},
		{"requirements.txt", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"build.gradle.kts", "java"},
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range probes {
		if seen[p.lang] {
			continue
		}
		if _, err := os.Stat(filepath.Join(cwd, p.file)); err == nil {
			out = append(out, p.lang)
			seen[p.lang] = true
		}
	}
	return out
}

// ensureClient starts the language server for a file's language (idempotent)
// and returns the client, or an error when the server is unavailable.
func (m *Manager) ensureClient(ctx context.Context, filePath string) (*Client, string, error) {
	lang := DetectLanguage(filePath)
	if lang == "" {
		return nil, "", fmt.Errorf("不支持的文件类型: %s", filepath.Ext(filePath))
	}
	if err := m.StartLanguageServer(ctx, lang); err != nil {
		return nil, "", err
	}
	c := m.GetClient(lang)
	if c == nil {
		return nil, "", fmt.Errorf("LSP 客户端未就绪: %s", lang)
	}
	return c, lang, nil
}

// openFile opens the file in the language server (idempotent) and returns the
// document URI.
func (m *Manager) openFile(ctx context.Context, c *Client, filePath, lang string) (string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	docURI := "file://" + filepath.ToSlash(abs)
	_ = c.OpenTextDocument(docURI, lang, "")
	return docURI, nil
}

// DiagnosticsForFile returns diagnostics (compile errors/warnings) for a file.
func (m *Manager) DiagnosticsForFile(ctx context.Context, filePath string) ([]Diagnostic, error) {
	c, lang, err := m.ensureClient(ctx, filePath)
	if err != nil {
		return nil, err
	}
	uri, err := m.openFile(ctx, c, filePath, lang)
	if err != nil {
		return nil, err
	}
	return c.Diagnostics(uri)
}

// HoverAt returns hover info at a 1-based line/character position.
func (m *Manager) HoverAt(ctx context.Context, filePath string, line, char int) (string, error) {
	c, lang, err := m.ensureClient(ctx, filePath)
	if err != nil {
		return "", err
	}
	uri, err := m.openFile(ctx, c, filePath, lang)
	if err != nil {
		return "", err
	}
	return c.Hover(uri, line-1, char-1)
}

// DefinitionAt returns the definition location (file path, 1-based line/char)
// for a symbol at the given position.
func (m *Manager) DefinitionAt(ctx context.Context, filePath string, line, char int) (string, int, int, error) {
	c, lang, err := m.ensureClient(ctx, filePath)
	if err != nil {
		return "", 0, 0, err
	}
	uri, err := m.openFile(ctx, c, filePath, lang)
	if err != nil {
		return "", 0, 0, err
	}
	loc, l, ch, err := c.Definition(uri, line-1, char-1)
	if err != nil {
		return "", 0, 0, err
	}
	// loc is a file:// URI; strip the prefix for display.
	return strings.TrimPrefix(loc, "file://"), l + 1, ch + 1, nil
}

// ReferencesAt returns references (file paths) for a symbol at a position.
func (m *Manager) ReferencesAt(ctx context.Context, filePath string, line, char int) ([]string, error) {
	c, lang, err := m.ensureClient(ctx, filePath)
	if err != nil {
		return nil, err
	}
	uri, err := m.openFile(ctx, c, filePath, lang)
	if err != nil {
		return nil, err
	}
	refs, err := c.References(uri, line-1, char-1)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, strings.TrimPrefix(r, "file://"))
	}
	return out, nil
}

// Symbols returns workspace symbols matching the query.
func (m *Manager) Symbols(ctx context.Context, lang, query string) ([]SymbolInfo, error) {
	if err := m.StartLanguageServer(ctx, lang); err != nil {
		return nil, err
	}
	c := m.GetClient(lang)
	if c == nil {
		return nil, fmt.Errorf("LSP 客户端未就绪: %s", lang)
	}
	return c.WorkspaceSymbols(query)
}

// AvailableServers reports which language servers are discoverable on PATH,
// for the /lsp status command.
func AvailableServers() []string {
	var out []string
	checks := []struct {
		lang, bin string
	}{
		{"go", "gopls"},
		{"typescript", "typescript-language-server"},
		{"python", "pyright-langserver"},
		{"rust", "rust-analyzer"},
		{"java", "jdtls"},
	}
	for _, c := range checks {
		if _, _, err := findExecutable(c.bin); err == nil {
			out = append(out, c.lang)
		}
	}
	return out
}

// QueryReport runs an on-demand LSP query for the /lsp slash command and
// returns a formatted, human-readable report. It is the single shared
// implementation used by both the TUI and the HTTP slash layer, so the
// command behaves identically on all three ends.
//
// Subcommands: status | diag <file> | syms <lang> <query> |
// hover/def/refs <file>:<line>:<col>.
func (m *Manager) QueryReport(sub string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch sub {
	case "status":
		servers := AvailableServers()
		if len(servers) == 0 {
			return "LSP 已启用，但未检测到可用的语言服务器。\n可安装：gopls（Go）、pyright（Python）、typescript-language-server（TS/JS）、rust-analyzer（Rust）。", nil
		}
		return "LSP 状态：已启用 ✅\n检测到语言服务器: " + strings.Join(servers, "、") +
			"\n\n用法:\n  /lsp diag <文件>              诊断（编译错误/警告）\n  /lsp syms <语言> <查询>       工作区符号搜索\n  /lsp hover <文件>:<行>:<列>    悬停信息\n  /lsp def <文件>:<行>:<列>      跳转定义\n  /lsp refs <文件>:<行>:<列>     查找引用", nil

	case "diag":
		if len(args) == 0 {
			return "", fmt.Errorf("用法: /lsp diag <文件路径>")
		}
		diags, err := m.DiagnosticsForFile(ctx, args[0])
		if err != nil {
			return "", fmt.Errorf("诊断失败: %w", err)
		}
		if len(diags) == 0 {
			return "✅ 未发现诊断问题: " + args[0], nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("诊断 %s（%d 条）:\n", args[0], len(diags)))
		for _, d := range diags {
			sev := "警告"
			switch d.Severity {
			case 1:
				sev = "错误"
			case 3:
				sev = "信息"
			case 4:
				sev = "提示"
			}
			sb.WriteString(fmt.Sprintf("  [%s] %d:%d  %s\n", sev, d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message))
		}
		return sb.String(), nil

	case "syms":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: /lsp syms <语言> <查询>（如 /lsp syms go Engine）")
		}
		syms, err := m.Symbols(ctx, args[0], args[1])
		if err != nil {
			return "", fmt.Errorf("符号搜索失败: %w", err)
		}
		if len(syms) == 0 {
			return "未找到匹配符号。", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("符号匹配 %q（%d 个）:\n", args[1], len(syms)))
		for i, s := range syms {
			if i >= 30 {
				sb.WriteString(fmt.Sprintf("  ... 及 %d 个\n", len(syms)-i))
				break
			}
			sb.WriteString(fmt.Sprintf("  %s (%s) @ %s:%d\n", s.Name, s.Kind, strings.TrimPrefix(s.URI, "file://"), s.Line+1))
		}
		return sb.String(), nil

	case "hover", "def", "refs":
		if len(args) == 0 {
			return "", fmt.Errorf("用法: /lsp %s <文件>:<行>:<列>", sub)
		}
		file, line, col, err := ParsePos(args[0])
		if err != nil {
			return "", fmt.Errorf("位置格式错误（应为 文件:行:列）: %w", err)
		}
		switch sub {
		case "hover":
			h, err := m.HoverAt(ctx, file, line, col)
			if err != nil {
				return "", fmt.Errorf("hover 失败: %w", err)
			}
			if h == "" {
				return "无悬停信息。", nil
			}
			return h, nil
		case "def":
			f, l, c, err := m.DefinitionAt(ctx, file, line, col)
			if err != nil {
				return "", fmt.Errorf("定义跳转失败: %w", err)
			}
			return fmt.Sprintf("定义: %s:%d:%d", f, l, c), nil
		default:
			refs, err := m.ReferencesAt(ctx, file, line, col)
			if err != nil {
				return "", fmt.Errorf("引用查询失败: %w", err)
			}
			if len(refs) == 0 {
				return "未找到引用。", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("引用（%d 处）:\n", len(refs)))
			for i, r := range refs {
				if i >= 30 {
					sb.WriteString(fmt.Sprintf("  ... 及 %d 处\n", len(refs)-i))
					break
				}
				sb.WriteString("  " + r + "\n")
			}
			return sb.String(), nil
		}

	default:
		return "", fmt.Errorf("未知 /lsp 子命令: %s（可用: status/diag/syms/hover/def/refs）", sub)
	}
}

// ParsePos parses "file:line:col" (or "file:line") into components
// (line/col 1-based). A colon at index 1 following a single ASCII letter is
// treated as a Windows drive colon (C:), not a separator.
func ParsePos(s string) (string, int, int, error) {
	var seps []int
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i == 1 && len(s) >= 3 && isASCIILetter(s[0]) && (s[2] == '\\' || s[2] == '/') {
				continue // drive colon
			}
			seps = append(seps, i)
		}
	}
	if len(seps) == 0 {
		return "", 0, 0, fmt.Errorf("缺少行号")
	}
	file := s[:seps[0]]
	lineStr := s[seps[0]+1:]
	colStr := ""
	if len(seps) >= 2 {
		lineStr = s[seps[0]+1 : seps[1]]
		colStr = s[seps[1]+1:]
	}
	line, err := strconv.Atoi(lineStr)
	if err != nil {
		return "", 0, 0, fmt.Errorf("无效行号 %q", lineStr)
	}
	col := 1
	if colStr != "" {
		if col, err = strconv.Atoi(colStr); err != nil {
			return "", 0, 0, fmt.Errorf("无效列号 %q", colStr)
		}
	}
	return file, line, col, nil
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func getLanguageServerCommand(languageID string) (string, []string, error) {
	switch languageID {
	case "go":
		return findExecutable("gopls")
	case "typescript", "javascript":
		return findExecutable("typescript-language-server", "--stdio")
	case "python":
		return findExecutable("pyright-langserver", "--stdio")
	case "rust":
		return findExecutable("rust-analyzer")
	case "java":
		return findExecutable("jdtls")
	default:
		return "", nil, fmt.Errorf("unsupported language: %s", languageID)
	}
}

func findExecutable(name string, extraArgs ...string) (string, []string, error) {
	path, err := execLookPath(name)
	if err != nil {
		return "", nil, fmt.Errorf("%s not found in PATH", name)
	}
	var args []string
	if len(extraArgs) > 0 {
		args = extraArgs
	}
	return path, args, nil
}

var execLookPath = func(name string) (string, error) {
	if _, err := os.Stat(name); err == nil {
		return filepath.Abs(name)
	}
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return full, nil
		}
		if filepath.Ext(name) == "" {
			if fi, err := os.Stat(full + ".exe"); err == nil && !fi.IsDir() {
				return full + ".exe", nil
			}
		}
	}
	return "", fmt.Errorf("not found")
}
