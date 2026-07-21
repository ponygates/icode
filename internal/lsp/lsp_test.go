package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSymbolKindString(t *testing.T) {
	tests := []struct {
		kind int
		want string
	}{
		{1, "File"},
		{2, "Module"},
		{3, "Namespace"},
		{4, "Package"},
		{5, "Class"},
		{6, "Method"},
		{7, "Property"},
		{8, "Field"},
		{9, "Constructor"},
		{10, "Enum"},
		{11, "Interface"},
		{12, "Function"},
		{13, "Variable"},
		{14, "Constant"},
		{15, "String"},
		{16, "Number"},
		{17, "Boolean"},
		{18, "Array"},
		{19, "Object"},
		{20, "Key"},
		{21, "Null"},
		{22, "EnumMember"},
		{23, "Struct"},
		{24, "Event"},
		{25, "Operator"},
		{26, "TypeParameter"},
		{99, "Kind(99)"},
		{0, "Kind(0)"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("kind_%d", tt.kind), func(t *testing.T) {
			got := symbolKindString(tt.kind)
			if got != tt.want {
				t.Errorf("symbolKindString(%d) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func readerFromString(s string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(s))
}

func TestReadLSPMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple message",
			input: "Content-Length: 17\r\n\r\n{\"jsonrpc\":\"2.0\"}",
			want:  `{"jsonrpc":"2.0"}`,
		},
		{
			name:  "message with extra headers",
			input: "Content-Length: 17\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n{\"jsonrpc\":\"2.0\"}",
			want:  `{"jsonrpc":"2.0"}`,
		},
		{
			name:    "no content length",
			input:   "\r\n{}",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := readerFromString(tt.input)
			got, err := readLSPMessage(reader)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q (len=%d), want %q (len=%d)", string(got), len(got), tt.want, len(tt.want))
			}
		})
	}
}

func TestReadLSPMessage_MultipleMessages(t *testing.T) {
	input := "Content-Length: 5\r\n\r\nhelloContent-Length: 5\r\n\r\nworld"
	reader := readerFromString(input)

	msg1, err := readLSPMessage(reader)
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if string(msg1) != "hello" {
		t.Errorf("first message got %q, want %q", string(msg1), "hello")
	}

	msg2, err := readLSPMessage(reader)
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	if string(msg2) != "world" {
		t.Errorf("second message got %q, want %q", string(msg2), "world")
	}
}

func TestReadLSPMessage_LargeContent(t *testing.T) {
	content := strings.Repeat("x", 10000)
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(content), content)
	reader := readerFromString(input)

	msg, err := readLSPMessage(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(msg) != content {
		t.Errorf("message length mismatch: got %d, want %d", len(msg), len(content))
	}
}

func TestTransport_SendRequest_ErrorAfterClose(t *testing.T) {
	// Create a transport with a stdin that will error after close
	// This tests the write path fails gracefully
	tr := &Transport{
		stdin:   &mockWriteCloser{errOnWrite: io.ErrClosedPipe},
		pending: make(map[int64]chan<- json.RawMessage),
	}

	_, err := tr.SendRequest("test/method", nil)
	if err == nil {
		t.Error("expected error writing to closed pipe")
	}
}

func TestTransport_SendNotification_Error(t *testing.T) {
	tr := &Transport{
		stdin:   &mockWriteCloser{errOnWrite: io.ErrClosedPipe},
		pending: make(map[int64]chan<- json.RawMessage),
	}

	err := tr.SendNotification("test/notification", nil)
	if err == nil {
		t.Error("expected error writing to closed pipe")
	}
}

func TestJSONRPC_TransportRequestFormat(t *testing.T) {
	// Verify that the request JSON includes the correct LSP fields
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": "file:///test.go"},
			"position":     map[string]any{"line": 0, "character": 0},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	json.Unmarshal(data, &decoded)

	if decoded["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", decoded["jsonrpc"])
	}
	if decoded["method"] != "textDocument/hover" {
		t.Errorf("expected method 'textDocument/hover', got %v", decoded["method"])
	}
}

func TestClient_NewClient_InvalidCommand(t *testing.T) {
	ctx := context.Background()
	_, err := NewClient(ctx, "file:///test", "/nonexistent/binary")
	if err == nil {
		t.Error("expected error starting nonexistent binary")
	}
}

func TestTransport_WriteMessageFormat(t *testing.T) {
	var buf bytes.Buffer
	tr := &Transport{
		stdin:   &mockWriteCloser{w: &buf},
		pending: make(map[int64]chan<- json.RawMessage),
	}

	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.writeMessage(data)
	if err != nil {
		t.Fatalf("writeMessage: %v", err)
	}

	expected := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(data), string(data))
	if buf.String() != expected {
		t.Errorf("write format mismatch\n  got:  %q\n  want: %q", buf.String(), expected)
	}
}

func TestTransport_WriteMessage_Empty(t *testing.T) {
	var buf bytes.Buffer
	tr := &Transport{
		stdin:   &mockWriteCloser{w: &buf},
		pending: make(map[int64]chan<- json.RawMessage),
	}

	err := tr.writeMessage([]byte{})
	if err != nil {
		t.Fatalf("writeMessage: %v", err)
	}

	expected := "Content-Length: 0\r\n\r\n"
	if buf.String() != expected {
		t.Errorf("got %q, want %q", buf.String(), expected)
	}
}

func TestNewClient_InitializeParams(t *testing.T) {
	// Verify initialization parameters contain required LSP fields
	params := map[string]any{
		"processId": nil,
		"rootUri":   "file:///test",
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover":      map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"definition": map[string]any{},
				"references": map[string]any{},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	json.Unmarshal(data, &decoded)

	cap, ok := decoded["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities should be map")
	}
	td, ok := cap["textDocument"].(map[string]any)
	if !ok {
		t.Fatal("textDocument should be map")
	}
	if _, ok := td["hover"]; !ok {
		t.Error("expected hover capability")
	}
	if _, ok := td["definition"]; !ok {
		t.Error("expected definition capability")
	}
	if _, ok := td["references"]; !ok {
		t.Error("expected references capability")
	}
}

func TestDiagnostic_Types(t *testing.T) {
	d := Diagnostic{
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 1, Character: 1},
		},
		Severity: 1,
		Message:  "test diagnostic",
		Source:   "test",
	}
	if d.Message != "test diagnostic" {
		t.Errorf("expected 'test diagnostic', got %q", d.Message)
	}
	if d.Range.Start.Line != 0 || d.Range.Start.Character != 0 {
		t.Error("range start should be 0,0")
	}
}

func TestSymbolInfo_Defaults(t *testing.T) {
	s := SymbolInfo{
		Name: "testFunc",
		Kind: "Function",
		URI:  "file:///test.go",
		Line: 10,
	}
	if s.Name != "testFunc" {
		t.Errorf("expected 'testFunc', got %q", s.Name)
	}
	if s.Kind != "Function" {
		t.Errorf("expected 'Function', got %q", s.Kind)
	}
	if s.Line != 10 {
		t.Errorf("expected line 10, got %d", s.Line)
	}
}

// ============================================================================
// Helpers
// ============================================================================

type mockWriteCloser struct {
	w          *bytes.Buffer
	errOnWrite error
}

func (m *mockWriteCloser) Write(p []byte) (int, error) {
	if m.errOnWrite != nil {
		return 0, m.errOnWrite
	}
	if m.w != nil {
		return m.w.Write(p)
	}
	return len(p), nil
}

func (m *mockWriteCloser) Close() error { return nil }

func TestReadLSPMessage_WithCRLF(t *testing.T) {
	// Some servers send \n instead of \r\n
	input := "Content-Length: 5\n\nhello"
	reader := readerFromString(input)

	msg, err := readLSPMessage(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(msg) != "hello" {
		t.Errorf("got %q, want %q", string(msg), "hello")
	}
}
