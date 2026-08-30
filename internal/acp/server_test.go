package acp

import (
	"encoding/json"
	"testing"
)

func TestPromptText(t *testing.T) {
	// ACP content list form.
	raw := json.RawMessage(`[{"type":"text","text":"hello "},{"type":"text","text":"world"}]`)
	if got := promptText(raw); got != "hello world" {
		t.Errorf("got %q", got)
	}
	// Plain string fallback.
	if got := promptText(json.RawMessage(`"hi"`)); got != "hi" {
		t.Errorf("got %q", got)
	}
	// Empty list.
	if got := promptText(json.RawMessage(`[]`)); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestRPCTypes(t *testing.T) {
	// initialize params round-trip.
	var req rpcRequest
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "initialize" || string(req.ID) != "1" {
		t.Errorf("bad decode: %+v", req)
	}

	// Response serializes with the correct envelope.
	resp := rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("1"), Result: map[string]any{"protocolVersion": protocolVersion}}
	b, _ := json.Marshal(resp)
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["jsonrpc"] != "2.0" || back["result"] == nil {
		t.Errorf("bad response: %s", b)
	}
}

func TestSetModeParamsDecode(t *testing.T) {
	var p setModeParams
	if err := json.Unmarshal([]byte(`{"sessionId":"s1","modeId":"auto"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "s1" || p.ModeID != "auto" {
		t.Errorf("bad decode: %+v", p)
	}
}
