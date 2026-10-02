// Command fakeserver is a minimal, dependency-free MCP server used by the
// client integration tests. It speaks line-delimited JSON-RPC 2.0 over stdio
// and deliberately implements only what the tests exercise:
//
//	initialize / notifications/initialized / tools/list / tools/call /
//	resources/list / resources/read / prompts/list / prompts/get
//
// Tools:
//
//	echo      — returns the arguments it was given plus the name the *server*
//	            was called with, so tests can prove the mcp_<slug>_ prefix is
//	            stripped before the wire request.
//	whoami    — returns the server name, used by the collision-routing test.
//	boom      — exits the process, so the restart supervisor gets exercised.
//	grow      — pushes notifications/tools/list_changed and adds a tool.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type server struct {
	mu    sync.Mutex
	name  string
	tools []tool
	out   *bufio.Writer
}

func (s *server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.out.WriteString(string(b))
	s.out.WriteString("\n")
	s.out.Flush()
}

func (s *server) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *server) toolList() []tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tool, len(s.tools))
	copy(out, s.tools)
	return out
}

func main() {
	name := os.Getenv("FAKE_SERVER_NAME")
	if name == "" {
		name = "fake"
	}
	s := &server{
		name: name,
		out:  bufio.NewWriter(os.Stdout),
		tools: []tool{
			{Name: "echo", Description: "echo arguments", InputSchema: map[string]any{"type": "object"}},
			{Name: "whoami", Description: "report server name", InputSchema: map[string]any{"type": "object"}},
			{Name: "boom", Description: "exit the process", InputSchema: map[string]any{"type": "object"}},
			{Name: "grow", Description: "add a tool and announce it", InputSchema: map[string]any{"type": "object"}},
		},
	}

	// stdout must carry JSON-RPC only; diagnostics go to stderr.
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		s.handle(req)
	}
}

func (s *server) handle(req rpcRequest) {
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return // notification
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": s.name, "version": "test"},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolList()}
	case "tools/call":
		resp.Result = s.callTool(req.Params)
	case "resources/list":
		resp.Result = map[string]any{
			"resources": []map[string]any{
				{"uri": "file:///notes.txt", "name": "notes", "mimeType": "text/plain"},
			},
		}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		resp.Result = map[string]any{
			"contents": []map[string]any{
				{"uri": p.URI, "mimeType": "text/plain", "text": "hello from " + s.name},
			},
		}
	case "prompts/list":
		resp.Result = map[string]any{
			"prompts": []map[string]any{
				{"name": "review", "description": "review a diff", "arguments": []map[string]any{{"name": "path", "required": true}}},
			},
		}
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		resp.Result = map[string]any{
			"messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": fmt.Sprintf("review %s (%s)", p.Arguments["path"], p.Name)}},
			},
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	s.write(resp)
}

func (s *server) callTool(raw json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(raw, &p)

	switch p.Name {
	case "echo":
		args, _ := json.Marshal(p.Arguments)
		return map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": fmt.Sprintf(`{"server":%q,"received_name":%q,"args":%s}`, s.name, p.Name, args),
			}},
		}
	case "whoami":
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s.name}}}
	case "grow":
		s.mu.Lock()
		s.tools = append(s.tools, tool{Name: "extra", Description: "added later", InputSchema: map[string]any{"type": "object"}})
		s.mu.Unlock()
		s.notify("notifications/tools/list_changed", map[string]any{})
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "grew"}}}
	case "boom":
		// The caller writes the response; we die shortly after so it sees an
		// EOF on stdout and the supervisor has to restart us.
		go func() {
			time.Sleep(50 * time.Millisecond)
			os.Exit(1)
		}()
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "crashing"}}}
	default:
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "unknown tool " + p.Name}}}
	}
}
