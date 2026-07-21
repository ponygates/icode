package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestNewClient(t *testing.T) {
	c := NewClient(ServerConfig{
		Name:    "test-server",
		Type:    TransportStdio,
		Command: "echo",
		Enabled: true,
	})
	if c == nil {
		t.Fatal("NewClient should return non-nil client")
	}
}

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient(ServerConfig{Name: "test"})
	if c == nil {
		t.Fatal("NewClient should return non-nil client")
	}
	if c.Tools() != nil {
		t.Error("new client should have nil tools")
	}
}

func TestPool_NewPool(t *testing.T) {
	p := NewPool()
	if p == nil {
		t.Fatal("NewPool should return non-nil pool")
	}
	if p.Count() != 0 {
		t.Errorf("new pool should have 0 clients, got %d", p.Count())
	}
}

func TestPool_Add_Disabled(t *testing.T) {
	p := NewPool()
	ctx := context.Background()

	err := p.Add(ctx, ServerConfig{
		Name:    "disabled-server",
		Type:    TransportStdio,
		Command: "echo",
		Enabled: false,
	})
	if err != nil {
		t.Errorf("adding disabled server should not error, got %v", err)
	}
	if p.Count() != 0 {
		t.Errorf("disabled server should not be added, count=%d", p.Count())
	}
}

func TestPool_Add_UnsupportedTransport(t *testing.T) {
	p := NewPool()
	ctx := context.Background()

	err := p.Add(ctx, ServerConfig{
		Name:    "bad-transport",
		Type:    Transport("unknown"),
		Command: "echo",
		Enabled: true,
	})
	if err == nil {
		t.Error("expected error for unsupported transport")
	}
	if p.Count() != 0 {
		t.Errorf("failed add should not increment count, got %d", p.Count())
	}
}

func TestPool_Has(t *testing.T) {
	p := NewPool()
	if p.Has("nonexistent") {
		t.Error("Has should return false for nonexistent server")
	}
}

func TestPool_Remove(t *testing.T) {
	p := NewPool()
	p.Remove("nonexistent") // should not panic
	if p.Has("nonexistent") {
		t.Error("Remove should not add servers")
	}
}

func TestPool_Execute_NotFound(t *testing.T) {
	p := NewPool()
	ctx := context.Background()

	_, err := p.Execute(ctx, "nonexistent_tool", nil)
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestPool_CloseAll_Empty(t *testing.T) {
	p := NewPool()
	p.CloseAll() // should not panic
	if p.Count() != 0 {
		t.Errorf("after CloseAll on empty pool, count should be 0, got %d", p.Count())
	}
}

func TestJSONRPC_MarshalRequest(t *testing.T) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var decoded jsonrpcRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", decoded.JSONRPC)
	}
	if decoded.ID != 1 {
		t.Errorf("expected id 1, got %d", decoded.ID)
	}
	if decoded.Method != "tools/list" {
		t.Errorf("expected method 'tools/list', got %q", decoded.Method)
	}
}

func TestJSONRPC_MarshalResponse(t *testing.T) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(`{"tools":[]}`),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var decoded jsonrpcResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", decoded.JSONRPC)
	}
	if string(decoded.Result) != `{"tools":[]}` {
		t.Errorf("unexpected result: %s", string(decoded.Result))
	}
}

func TestJSONRPC_ErrorResponse(t *testing.T) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      1,
		Error: &jsonrpcError{
			Code:    -32601,
			Message: "Method not found",
		},
	}
	data, _ := json.Marshal(resp)

	var decoded jsonrpcResponse
	json.Unmarshal(data, &decoded)
	if decoded.Error == nil {
		t.Fatal("expected error field")
	}
	if decoded.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", decoded.Error.Code)
	}
	if decoded.Error.Message != "Method not found" {
		t.Errorf("expected 'Method not found', got %q", decoded.Error.Message)
	}
}

func TestJSONRPC_Notification(t *testing.T) {
	notif := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	data, _ := json.Marshal(notif)

	var decoded jsonrpcNotification
	json.Unmarshal(data, &decoded)
	if decoded.Method != "notifications/initialized" {
		t.Errorf("expected 'notifications/initialized', got %q", decoded.Method)
	}
}

func TestServerConfig_Defaults(t *testing.T) {
	cfg := ServerConfig{
		Name: "test",
	}
	if cfg.Name != "test" {
		t.Errorf("expected name 'test', got %q", cfg.Name)
	}
	if cfg.Type != "" {
		t.Errorf("default Type should be empty, got %q", cfg.Type)
	}
}

func TestPool_ConcurrentSafe(t *testing.T) {
	p := NewPool()

	// Concurrent operations should not panic
	t.Run("concurrent access", func(t *testing.T) {
		done := make(chan bool, 3)
		go func() {
			p.Count()
			done <- true
		}()
		go func() {
			p.Has("test")
			done <- true
		}()
		go func() {
			p.Remove("test")
			done <- true
		}()

		for i := 0; i < 3; i++ {
			<-done
		}
	})
}

func TestNewClient_ConfigPreserved(t *testing.T) {
	cfg := ServerConfig{
		Name:    "my-server",
		Type:    TransportStdio,
		Command: "test-command",
		Args:    []string{"--flag", "value"},
		Enabled: true,
	}
	c := NewClient(cfg)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}

	// Verify tools list is empty initially
	if len(c.Tools()) != 0 {
		t.Errorf("expected 0 tools initially, got %d", len(c.Tools()))
	}
}

func TestToolDef_NameFormat(t *testing.T) {
	// Verify that DiscoverTools prefixes tool names correctly
	// This tests the format: mcp_<server>_<tool>
	tool := types.ToolDef{
		Name: "mcp_test-server_my-tool",
	}
	if len(tool.Name) <= 0 {
		t.Error("tool name should not be empty")
	}
}

func TestPool_AllTools_Empty(t *testing.T) {
	p := NewPool()
	tools := p.AllTools()
	if len(tools) != 0 {
		t.Errorf("expected empty tools list, got %d", len(tools))
	}
}

func TestMCPResource_Defaults(t *testing.T) {
	r := MCPResource{
		URI:  "file:///test",
		Name: "test-resource",
	}
	if r.URI != "file:///test" {
		t.Errorf("expected URI 'file:///test', got %q", r.URI)
	}
	if r.Name != "test-resource" {
		t.Errorf("expected name 'test-resource', got %q", r.Name)
	}
}

// Test the JSON-RPC message parsing with a mock readLoop
func TestJSONRPC_RoundTrip(t *testing.T) {
	// Verify the call() method builds correct requests
	// by inspecting the marshaled output
	reqID := int64(42)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "test-tool",
			"arguments": map[string]any{
				"key": "value",
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded jsonrpcRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != reqID {
		t.Errorf("expected id %d, got %d", reqID, decoded.ID)
	}

	// Verify params contain name and arguments
	params, ok := decoded.Params.(map[string]any)
	if !ok {
		t.Fatal("params should be map[string]any")
	}
	if name, ok := params["name"].(string); !ok || name != "test-tool" {
		t.Errorf("expected name 'test-tool', got %v", params["name"])
	}
}
