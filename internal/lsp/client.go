// Package lsp provides Language Server Protocol integration for iCode.
//
// LSP (Language Server Protocol) enables the AI to understand code semantics:
//   - Type information & diagnostics (error/warning annotations)
//   - Go-to-definition & find-references navigation
//   - Hover documentation & symbol search
//   - Workspace-wide symbol indexing
//
// This package implements a lightweight LSP client that communicates with
// language servers via JSON-RPC over stdin/stdout.
//
// Supported servers:
//   - Go: gopls (built-in)
//   - LSP discovery: auto-detects from .lsp.json config or falls back to gopls
//
// Architecture (mirrors MCP client design):
//   Client ↔ JSON-RPC (stdio) ↔ Language Server (gopls, pyright, etc.)

package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Transport manages the JSON-RPC communication with a language server.
type Transport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu      sync.Mutex
	reqID   atomic.Int64
	pending map[int64]chan<- json.RawMessage
	cancel  context.CancelFunc
}

// NewTransport starts a language server process and returns a transport.
func NewTransport(ctx context.Context, command string, args ...string) (*Transport, error) {
	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("lsp start: %w", err)
	}

	t := &Transport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan<- json.RawMessage),
		cancel:  cancel,
	}

	go t.readLoop()
	return t, nil
}

// SendRequest sends a JSON-RPC request and returns the response.
func (t *Transport) SendRequest(method string, params any) (json.RawMessage, error) {
	id := t.reqID.Add(1)
	ch := make(chan json.RawMessage, 1)

	t.mu.Lock()
	t.pending[id] = ch
	t.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("lsp marshal: %w", err)
	}

	if err := t.writeMessage(data); err != nil {
		return nil, err
	}

	result := <-ch
	return result, nil
}

// SendNotification sends a JSON-RPC notification (no response expected).
func (t *Transport) SendNotification(method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("lsp marshal: %w", err)
	}

	return t.writeMessage(data)
}

// Close shuts down the language server.
func (t *Transport) Close() error {
	t.cancel()
	_ = t.SendNotification("shutdown", nil)
	_ = t.SendNotification("exit", nil)
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}

func (t *Transport) writeMessage(data []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	msg := append([]byte(header), data...)
	_, err := t.stdin.Write(msg)
	return err
}

func (t *Transport) readLoop() {
	reader := bufio.NewReader(t.stdout)
	for {
		content, err := readLSPMessage(reader)
		if err != nil {
			return
		}

		var base struct {
			ID     int64            `json:"id,omitempty"`
			Method string           `json:"method,omitempty"`
			Result json.RawMessage  `json:"result,omitempty"`
			Error  *json.RawMessage `json:"error,omitempty"`
		}
		if err := json.Unmarshal(content, &base); err != nil {
			continue
		}

		if base.ID > 0 {
			// Response to a request
			t.mu.Lock()
			ch, ok := t.pending[base.ID]
			delete(t.pending, base.ID)
			t.mu.Unlock()
			if ok && ch != nil {
				ch <- base.Result
				close(ch)
			}
		}
	}
}

// readLSPMessage reads a single LSP message from the reader.
// Format: "Content-Length: N\r\n\r\n{JSON of N bytes}"
func readLSPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = line[:len(line)-1] // strip \n
		if line == "" || line == "\r" {
			break // end of headers
		}
		var n int
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &n); err == nil {
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("lsp: no content length")
	}
	content := make([]byte, contentLength)
	_, err := io.ReadFull(reader, content)
	return content, err
}

// ============================================================================
// LSP Client — high-level wrapper
// ============================================================================

// Client provides high-level LSP operations.
type Client struct {
	transport *Transport
	rootURI   string
}

// ServerCapabilities holds the capabilities of the language server.
type ServerCapabilities struct {
	TextDocumentSync int `json:"textDocumentSync,omitempty"`
	HoverProvider    bool `json:"hoverProvider,omitempty"`
	DefinitionProvider bool `json:"definitionProvider,omitempty"`
	ReferencesProvider bool `json:"referencesProvider,omitempty"`
	DiagnosticsProvider bool `json:"diagnosticsProvider,omitempty"`
	WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider,omitempty"`
}

// NewClient creates and initializes an LSP client.
func NewClient(ctx context.Context, rootURI, command string, args ...string) (*Client, error) {
	transport, err := NewTransport(ctx, command, args...)
	if err != nil {
		return nil, fmt.Errorf("lsp transport: %w", err)
	}

	client := &Client{
		transport: transport,
		rootURI:   rootURI,
	}

	// Initialize session
	initParams := map[string]any{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover":                map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"definition":           map[string]any{},
				"references":           map[string]any{},
				"completion":           map[string]any{},
				"diagnostics":          map[string]any{},
				"documentSymbol":       map[string]any{},
				"codeAction":           map[string]any{},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
		},
	}

	initResult, err := transport.SendRequest("initialize", initParams)
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("lsp init: %w", err)
	}

	// Parse capabilities
	var initResp struct {
		Capabilities ServerCapabilities `json:"capabilities"`
	}
	json.Unmarshal(initResult, &initResp)

	// Send initialized notification
	_ = transport.SendNotification("initialized", map[string]any{})

	return client, nil
}

// Close shuts down the LSP client.
func (c *Client) Close() error {
	if c.transport != nil {
		return c.transport.Close()
	}
	return nil
}

// OpenTextDocument notifies the server that a document is open.
func (c *Client) OpenTextDocument(uri, languageID, text string) error {
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	}
	return c.transport.SendNotification("textDocument/didOpen", params)
}

// CloseTextDocument notifies the server that a document is closed.
func (c *Client) CloseTextDocument(uri string) error {
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
	}
	return c.transport.SendNotification("textDocument/didClose", params)
}

// Hover returns hover information for a position in a document.
func (c *Client) Hover(uri string, line, character int) (string, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	}

	result, err := c.transport.SendRequest("textDocument/hover", params)
	if err != nil {
		return "", err
	}

	var hoverResp struct {
		Contents struct {
			Kind   string `json:"kind"`
			Value  string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(result, &hoverResp); err != nil {
		return "", nil
	}

	return hoverResp.Contents.Value, nil
}

// Definition returns the location of a symbol's definition.
func (c *Client) Definition(uri string, line, character int) (string, int, int, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	}

	result, err := c.transport.SendRequest("textDocument/definition", params)
	if err != nil {
		return "", 0, 0, err
	}

	var loc struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(result, &loc); err != nil {
		return "", 0, 0, nil
	}

	return loc.URI, loc.Range.Start.Line, loc.Range.Start.Character, nil
}

// References returns all references to a symbol.
func (c *Client) References(uri string, line, character int) ([]string, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
		"context":      map[string]any{"includeDeclaration": true},
	}

	result, err := c.transport.SendRequest("textDocument/references", params)
	if err != nil {
		return nil, err
	}

	var locations []struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(result, &locations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal references: %w", err)
	}

	refs := make([]string, 0, len(locations))
	for _, loc := range locations {
		refs = append(refs, fmt.Sprintf("%s:%d:%d", loc.URI, loc.Range.Start.Line, loc.Range.Start.Character))
	}
	return refs, nil
}

// WorkspaceSymbols searches for symbols across the workspace.
func (c *Client) WorkspaceSymbols(query string) ([]SymbolInfo, error) {
	params := map[string]any{
		"query": query,
	}

	result, err := c.transport.SendRequest("workspace/symbol", params)
	if err != nil {
		return nil, err
	}

	var symbols []struct {
		Name     string `json:"name"`
		Kind     int    `json:"kind"`
		Location struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
			} `json:"range"`
		} `json:"location"`
	}
	if err := json.Unmarshal(result, &symbols); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workspace symbols: %w", err)
	}

	info := make([]SymbolInfo, 0, len(symbols))
	for _, s := range symbols {
		info = append(info, SymbolInfo{
			Name:      s.Name,
			Kind:      symbolKindString(s.Kind),
			URI:       s.Location.URI,
			Line:      s.Location.Range.Start.Line,
			Character: s.Location.Range.Start.Character,
		})
	}
	return info, nil
}

// Diagnostics requests the current diagnostics for a document.
func (c *Client) Diagnostics(uri string) ([]Diagnostic, error) {
	// Force publishDiagnostics by sending a didChange
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
	}
	_ = c.transport.SendNotification("textDocument/didChange", params)
	return nil, fmt.Errorf("diagnostics not implemented")
}

// SymbolInfo describes a workspace symbol.
type SymbolInfo struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URI       string `json:"uri"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
}

// Diagnostic represents a language server diagnostic.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// Range represents a range in a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position represents a position in a document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// symbolKindString converts LSP symbol kind numbers to readable strings.
func symbolKindString(kind int) string {
	kinds := map[int]string{
		1:  "File", 2: "Module", 3: "Namespace", 4: "Package", 5: "Class",
		6:  "Method", 7: "Property", 8: "Field", 9: "Constructor",
		10: "Enum", 11: "Interface", 12: "Function", 13: "Variable",
		14: "Constant", 15: "String", 16: "Number", 17: "Boolean",
		18: "Array", 19: "Object", 20: "Key", 21: "Null",
		22: "EnumMember", 23: "Struct", 24: "Event", 25: "Operator",
		26: "TypeParameter",
	}
	if name, ok := kinds[kind]; ok {
		return name
	}
	return fmt.Sprintf("Kind(%d)", kind)
}
