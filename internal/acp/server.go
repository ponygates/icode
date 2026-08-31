// Package acp implements an Agent Client Protocol (ACP) server over stdio,
// letting Zed / Neovim and other ACP-compatible editors drive iCode as a
// subprocess agent (Reasonix `reasonix acp` parity). See docs/acp_design.md.
//
// Baseline methods: initialize, session/new, session/prompt. The stream from
// engine.Send is mapped to session/update notifications (message chunks +
// tool-call status) and the turn ends with a session/prompt response carrying
// stop_reason. Permission routing (session/request_permission) is a P2 item.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/types"
)

// protocolVersion is the ACP protocol version we speak.
const protocolVersion = 1

// rpcRequest is a JSON-RPC 2.0 request, notification, or response (a response
// has no method and carries result instead of params).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response (id null for notifications).
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is an ACP agent backed by the iCode engine.
type Server struct {
	Engine *conversation.Engine
	Gate   *permission.Gate
	Store  types.SessionStore

	out *json.Encoder
	in  *json.Decoder

	// permPending tracks in-flight session/request_permission requests keyed
	// by the JSON-RPC id we assigned; the response carries the chosen optionId.
	permMu      sync.Mutex
	permPending map[string]chan string
	permSeq     int
}

// Run serves ACP requests on stdin/stdout until EOF or ctx cancellation.
// It blocks; call from the `icode acp` command's RunE.
func Run(ctx context.Context, eng *conversation.Engine, gate *permission.Gate, store types.SessionStore) error {
	s := &Server{
		Engine:      eng,
		Gate:        gate,
		Store:       store,
		in:          json.NewDecoder(bufio.NewReader(os.Stdin)),
		out:         json.NewEncoder(os.Stdout),
		permPending: map[string]chan string{},
	}
	// Route the engine's permission flow through the ACP client (editor): the
	// editor shows the prompt and its answer is fed back as the decision.
	if eng != nil {
		eng.SetPermissionHandler(s.requestPermission)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var req rpcRequest
		if err := s.in.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("acp: decode: %w", err)
		}
		s.dispatch(ctx, req)
	}
}

// dispatch routes one request/notification. Notifications (no id) never get
// a response per JSON-RPC 2.0. A request with no method whose id is a pending
// permission request id is the editor's answer to session/request_permission.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) {
	isNotify := len(req.ID) == 0 || string(req.ID) == "null"
	if req.Method == "" && !isNotify {
		s.resolvePermission(req.ID, req)
		return
	}
	switch req.Method {
	case "initialize":
		s.respond(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"agentCapabilities": map[string]any{
				"loadSession": true,
				"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": false},
			},
		}, nil)
	case "session/new":
		s.handleSessionNew(req, isNotify)
	case "session/prompt":
		s.handleSessionPrompt(ctx, req, isNotify)
	case "session/load":
		s.handleSessionLoad(req, isNotify)
	case "session/set_mode":
		s.handleSessionSetMode(req, isNotify)
	case "session/cancel":
		// notification — cancel the in-flight turn if tracked (P1).
	default:
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
}

func (s *Server) respond(id json.RawMessage, result any, rpcErr *rpcError) {
	if id == nil {
		return
	}
	_ = s.out.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
}

// notify sends a one-way JSON-RPC notification.
func (s *Server) notify(method string, params any) {
	_ = s.out.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// sessionNewParams mirrors ACP session/new.
type sessionNewParams struct {
	Cwd         string `json:"cwd"`
	MCPFilters  any    `json:"mcpServers,omitempty"`
	ClientName  string `json:"clientName,omitempty"`
}

func (s *Server) handleSessionNew(req rpcRequest, isNotify bool) {
	var p sessionNewParams
	_ = json.Unmarshal(req.Params, &p)
	if s.Store == nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: "no session store"})
		}
		return
	}
	sess := &types.Session{}
	if err := s.Store.Create(sess); err != nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
		}
		return
	}
	if !isNotify {
		s.respond(req.ID, map[string]any{"sessionId": sess.ID}, nil)
	}
}

// promptParams mirrors ACP session/prompt.
type promptParams struct {
	SessionID string          `json:"sessionId"`
	Prompt    json.RawMessage `json:"prompt"`
}

func (s *Server) handleSessionPrompt(ctx context.Context, req rpcRequest, isNotify bool) {
	var p promptParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
		}
		return
	}
	text := promptText(p.Prompt)
	if s.Engine == nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: "no engine"})
		}
		return
	}
	ch, err := s.Engine.Send(ctx, p.SessionID, text)
	if err != nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
		}
		return
	}
	// Stream events → session/update notifications (message chunks).
	for ev := range ch {
		switch ev.Type {
		case types.EventText:
			s.notify("session/update", map[string]any{
				"sessionId": p.SessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": ev.Content},
				},
			})
		case types.EventError:
			s.notify("session/update", map[string]any{
				"sessionId": p.SessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "\n[error] " + ev.Content},
				},
			})
		}
	}
	if !isNotify {
		result := map[string]any{"stopReason": "end_turn"}
		// Authoritative total token count for the session (Reasonix parity:
		// ACP exposes totalTokens so editors show usage).
		if s.Engine != nil {
			if st := s.Engine.SessionStats(p.SessionID); st != nil {
				result["totalTokens"] = st.TotalTokens
			}
		}
		s.respond(req.ID, result, nil)
	}
}

// loadParams mirrors ACP session/load.
type loadParams struct {
	SessionID string `json:"sessionId"`
}

// handleSessionLoad resumes an existing session (requires loadSession
// capability, which we advertise in initialize).
func (s *Server) handleSessionLoad(req rpcRequest, isNotify bool) {
	var p loadParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
		}
		return
	}
	if s.Store == nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: "no session store"})
		}
		return
	}
	sess, err := s.Store.Get(p.SessionID)
	if err != nil || sess == nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32602, Message: "session not found: " + p.SessionID})
		}
		return
	}
	if !isNotify {
		s.respond(req.ID, map[string]any{"sessionId": sess.ID}, nil)
	}
}

// setModeParams mirrors ACP session/set_mode.
type setModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// handleSessionSetMode switches the permission gate mode for the session
// (plan / agent / auto / yolo — iCode's vocabulary).
func (s *Server) handleSessionSetMode(req rpcRequest, isNotify bool) {
	var p setModeParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ModeID == "" {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
		}
		return
	}
	if s.Gate == nil {
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32603, Message: "no permission gate"})
		}
		return
	}
	// Map ACP's mode vocabulary onto iCode's; unknown modes are rejected.
	mode := permission.Mode(p.ModeID)
	switch mode {
	case permission.ModePlan, permission.ModeAgent, permission.ModeAuto, permission.ModeYOLO:
	default:
		if !isNotify {
			s.respond(req.ID, nil, &rpcError{Code: -32602, Message: "unsupported mode: " + p.ModeID})
		}
		return
	}
	s.Gate.SetMode(mode)
	if !isNotify {
		s.respond(req.ID, map[string]any{"modeId": p.ModeID}, nil)
	}
}

// requestPermission implements conversation.PermissionHandler: it forwards
// the gate's ask decision to the ACP client (editor) via
// session/request_permission and waits for the answer, mapping the option
// kind back to an iCode decision (allow_once/allow_always → allow;
// reject_* / cancelled → deny).
func (s *Server) requestPermission(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
	// Only forward when the gate actually asked; read/auto-approved calls
	// never reach here (they short-circuit before the handler).
	s.permMu.Lock()
	s.permSeq++
	id := fmt.Sprintf("perm-%d", s.permSeq)
	ch := make(chan string, 1)
	s.permPending[id] = ch
	s.permMu.Unlock()

	s.out.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": sessionID,
			"toolCall":  map[string]any{"toolCallId": "tool-" + req.Tool, "title": req.Tool, "kind": "other"},
			"options": []map[string]any{
				{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
				{"optionId": "allow-always", "name": "Allow always", "kind": "allow_always"},
				{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
			},
		},
	})

	// Wait for the editor's answer (or a close of the channel on shutdown).
	option, ok := <-ch
	s.permMu.Lock()
	delete(s.permPending, id)
	s.permMu.Unlock()
	if !ok {
		return permission.DecisionDeny
	}
	switch option {
	case "allow-always":
		return permission.DecisionAllowAll
	case "allow-once":
		return permission.DecisionAllow
	default:
		return permission.DecisionDeny
	}
}

// resolvePermission feeds the editor's response back to the waiting handler.
func (s *Server) resolvePermission(id json.RawMessage, req rpcRequest) {
	var key string
	_ = json.Unmarshal(id, &key) // strip JSON string quotes
	s.permMu.Lock()
	ch, ok := s.permPending[key]
	s.permMu.Unlock()
	if !ok {
		return
	}
	option := ""
	var resp struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if json.Unmarshal(req.Result, &resp) == nil {
		option = resp.Outcome.OptionID
	}
	ch <- option
}

// promptText flattens an ACP prompt content list into plain text.
func promptText(raw json.RawMessage) string {
	var list []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		out := ""
		for _, c := range list {
			if c.Type == "text" {
				out += c.Text
			}
		}
		if out != "" {
			return out
		}
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
