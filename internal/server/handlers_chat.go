package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// sseHeartbeatInterval is how often the chat SSE stream emits a keepalive
// comment frame while it has no events to send. A variable rather than a
// constant so tests can shorten it.
var sseHeartbeatInterval = 15 * time.Second

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Catch any panic in the chat handler so it gets logged instead of
	// silently killing the response (the http.Server recovers panics, but
	// its default recovery log goes to stderr which is redirected to the
	// log file — this makes the trace explicit and easy to find).
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("[server] chat: PANIC recovered: %v", rv)
			errData, _ := json.Marshal(types.StreamEvent{
				Type:    types.EventError,
				Content: fmt.Sprintf("内部错误: %v", rv),
			})
			fmt.Fprintf(w, "data: %s\n\n", errData)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID   string             `json:"session_id"`
		Content     string             `json:"content"`
		Model       string             `json:"model"`
		Provider    string             `json:"provider"`
		Attachments []types.Attachment `json:"attachments,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not available"})
		return
	}

	log.Printf("[server] chat: session=%s model=%s provider=%s", req.SessionID, req.Model, req.Provider)

	// Ensure a session exists for this conversation.
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("web-%d", time.Now().UnixNano())
	}
	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		sess = &types.Session{
			ID:           req.SessionID,
			ModelID:      orDefault(req.Model, "openrouter/free"),
			ProviderName: orDefault(req.Provider, "openrouter"),
			Title:        firstLine(req.Content, 40),
		}
		_ = s.store.Create(sess)
	} else {
		// Reflect any model/provider change from the client.
		changed := false
		if req.Model != "" && sess.ModelID != req.Model {
			sess.ModelID = req.Model
			changed = true
		}
		if req.Provider != "" && sess.ProviderName != req.Provider {
			sess.ProviderName = req.Provider
			changed = true
		}
		if changed {
			_ = s.store.Update(sess)
		}
	}

	log.Printf("[server] chat: session resolved: %s model=%s provider=%s",
		req.SessionID, sess.ModelID, sess.ProviderName)

	// SSE streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Resolve the model to verify Provider + credentials exist.
	// NOTE: We deliberately do NOT call prov.Health() here. The CLI path
	// never pre-checks — it just calls engine.Send and lets real errors
	// surface via the stream. Health() does GET /models which on OpenRouter
	// returns a multi-MB JSON body; calling it on every chat request risks
	// rate-limiting or a long blocking timeout that makes the desktop app
	// appear "stuck" while the CLI works fine.
	prov, _, resolveErr := s.reg.ResolveModel(sess.ModelID)
	if resolveErr != nil {
		log.Printf("[server] chat: resolve model %q failed: %v", sess.ModelID, resolveErr)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		errData, _ := json.Marshal(types.StreamEvent{
			Type:    types.EventError,
			Content: fmt.Sprintf("模型 %q 未找到，请检查设置中的模型配置", sess.ModelID),
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}
	log.Printf("[server] chat: resolved provider=%s, calling engine.Send", prov.Name())

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Derive the chat context purely from the client connection so an agentic
	// turn lives as long as the browser is attached — r.Context() is cancelled
	// on disconnect, which the engine reads as a user interrupt (partial output
	// is persisted, Claude Code parity). We deliberately do NOT clamp the turn
	// with a short absolute timeout: a 25-round turn (model stream + go test /
	// npm build per round) routinely exceeds 2 minutes, and the previous 115s
	// cap killed every long desktop turn mid-flight. The engine's own
	// maxToolRounds bound (default 25) prevents unbounded work, and provider
	// HTTP clients catch hung upstreams, so no backstop is needed here.
	chatCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	eventCh, err := s.engine.Send(chatCtx, req.SessionID, req.Content, req.Attachments)
	if err != nil {
		log.Printf("[server] chat: engine.Send failed session=%s model=%s err=%v", req.SessionID, sess.ModelID, err)
		errData, _ := json.Marshal(types.StreamEvent{
			Type:    types.EventError,
			Content: err.Error(),
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}
	log.Printf("[server] chat: engine.Send OK, streaming events...")

	// Keepalive: emit an SSE comment frame whenever the stream goes quiet. A
	// long tool round (`go test` / `npm build` can run 60s+) produces no
	// events, and a byte-silent connection gets dropped or stalled by
	// intermediaries — after which the desktop UI cannot distinguish "the
	// model is still working" from "the socket died". Comment frames start
	// with ':' and are ignored by EventSource, so they keep the pipe warm
	// without polluting the event stream.
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	firstEvent := true
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				log.Printf("[server] chat: stream ended (channel closed) session=%s", req.SessionID)
				return
			}
			if firstEvent {
				log.Printf("[server] chat: first event type=%s session=%s", event.Type, req.SessionID)
				firstEvent = false
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			if event.Type == types.EventDone || event.Type == types.EventError {
				log.Printf("[server] chat: stream end event=%s session=%s", event.Type, req.SessionID)
				return
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			log.Printf("[server] chat: client disconnected session=%s", req.SessionID)
			return
		}
	}
}

func (s *Server) handleChatStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = r.Body.Close()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if s.engine != nil {
		s.engine.Stop(req.SessionID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
