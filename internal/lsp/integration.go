package lsp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
