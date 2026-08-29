package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/types"
)

// requireAPIToken guards remote-control API endpoints with the shared Bearer
// apiToken (same token the desktop uses for the main API).
func (s *Server) requireAPIToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != s.apiToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// remotePort extracts the port from a "host:port" listen address.
func remotePort(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return addr
}

// handleRemoteStatus serves GET /api/remote/status — a compact phone-friendly
// snapshot of the machine state (WorkBuddy Claw / ZCode Remote Control parity).
func (s *Server) handleRemoteStatus(w http.ResponseWriter, r *http.Request) {
	mode := ""
	if s.gate != nil {
		mode = string(s.gate.Mode())
	}
	sec := ""
	if s.gate != nil {
		sec = string(s.gate.SecurityLevel())
	}
	auto := []map[string]any{}
	if s.sch != nil {
		for _, t := range s.sch.List() {
			next := ""
			if !t.NextRun.IsZero() {
				next = t.NextRun.Format("01-02 15:04")
			}
			auto = append(auto, map[string]any{
				"id": t.ID, "name": t.Name, "schedule": t.Schedule,
				"enabled": t.Enabled, "next_run": next,
			})
		}
	}
	chunks := -1
	if s.engine != nil {
		if km := s.engine.KnowledgeManager(); km != nil {
			chunks = km.ChunkCount()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      s.version,
		"mode":         mode,
		"security":     sec,
		"bg_agents":    tool.RunningAgentTaskCount(),
		"bg_shells":    tool.RunningShellTaskCount(),
		"automations":  auto,
		"kb_chunks":    chunks,
		"server_time":  time.Now().Format("2006-01-02 15:04:05"),
	})
}

// handleRemotePrompt serves POST /api/remote/prompt — sends a prompt to the
// engine (creating a remote session when needed) and returns the aggregated
// text reply (non-streaming, blocking up to 180s).
func (s *Server) handleRemotePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Content   string `json:"content"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content required"})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not available"})
		return
	}
	sid := strings.TrimSpace(req.SessionID)
	if sid == "" {
		sid = fmt.Sprintf("remote-%d", time.Now().UnixNano())
	}
	if _, err := s.store.Get(sid); err != nil {
		title := firstLine(content, 40)
		_ = s.store.Create(&types.Session{
			ID:           sid,
			ModelID:      "openrouter/free",
			ProviderName: "openrouter",
			Title:        title,
		})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	eventCh, err := s.engine.Send(ctx, sid, content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for ev := range eventCh {
		switch ev.Type {
		case types.EventText:
			sb.WriteString(ev.Content)
		case types.EventError:
			sb.WriteString("\n[错误] " + ev.Content)
		case types.EventDone:
			// drain remaining events
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reply": sb.String(), "session_id": sid})
}

// handleRemoteTasks serves GET /api/remote/tasks (list automations) and
// POST /api/remote/tasks (create one: {name, prompt, schedule}).
func (s *Server) handleRemoteTasks(w http.ResponseWriter, r *http.Request) {
	if s.sch == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "scheduler not available"})
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Name     string `json:"name"`
			Prompt   string `json:"prompt"`
			Schedule string `json:"schedule"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Name == "" || req.Prompt == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and prompt required"})
			return
		}
		if req.Schedule == "" {
			req.Schedule = "idle"
		}
		if _, err := s.sch.Create(req.Name, req.Prompt, req.Schedule); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	out := []map[string]any{}
	for _, t := range s.sch.List() {
		next := ""
		if !t.NextRun.IsZero() {
			next = t.NextRun.Format("01-02 15:04")
		}
		out = append(out, map[string]any{
			"id": t.ID, "name": t.Name, "schedule": t.Schedule,
			"enabled": t.Enabled, "next_run": next,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// handleRemotePage serves the phone-friendly remote-control page.
func (s *Server) handleRemotePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(remotePageHTML))
}

const remotePageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>iCode 远程控制</title>
<style>
  body { background:#0f1115; color:#e6e6e6; font-family:-apple-system,Segoe UI,Roboto,sans-serif; margin:0; padding:14px; }
  .card { background:#171a21; border:1px solid #2a2e3a; border-radius:12px; padding:14px; margin-bottom:12px; }
  h1 { font-size:16px; margin:0 0 8px; color:#7fc3ff; }
  .stat { display:flex; flex-wrap:wrap; gap:8px; font-size:12px; color:#aab; }
  .stat span { background:#1d2433; padding:4px 10px; border-radius:6px; }
  input, textarea { width:100%; box-sizing:border-box; padding:10px; border-radius:8px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; font-size:14px; margin-bottom:8px; }
  button { padding:10px 16px; border-radius:8px; border:none; background:#4f6ef7; color:#fff; font-size:14px; cursor:pointer; }
  #reply { white-space:pre-wrap; word-break:break-word; font-size:13px; line-height:1.6; max-height:40vh; overflow:auto; }
  .task { display:flex; justify-content:space-between; align-items:center; font-size:12px; padding:6px 0; border-bottom:1px solid #232a3a; }
  .task .on { color:#4caf50; }
  .err { color:#e06c6c; font-size:12px; }
</style>
</head>
<body>
<div class="card">
  <h1>📡 iCode 远程控制</h1>
  <div class="stat" id="stat">加载中…</div>
</div>
<div class="card">
  <h1>💬 发送指令</h1>
  <textarea id="msg" rows="3" placeholder="输入消息，回车发送…"></textarea>
  <button onclick="sendMsg()">发送</button>
  <div id="reply" style="margin-top:10px;"></div>
</div>
<div class="card">
  <h1>⏱ 自动化任务</h1>
  <input id="tname" placeholder="任务名">
  <textarea id="tprompt" rows="2" placeholder="任务内容"></textarea>
  <select id="tsched" style="width:100%; padding:10px; border-radius:8px; border:1px solid #2a2e3a; background:#0f1115; color:#e6e6e6; margin-bottom:8px;">
    <option value="idle">闲时（0 点后）</option>
    <option value="rrule:FREQ=SECONDLY;INTERVAL=60">每 60 秒</option>
    <option value="daily:02:00">每天 02:00</option>
  </select>
  <button onclick="createTask()">创建任务</button>
  <div id="tasks" style="margin-top:10px;"></div>
</div>
<script>
  var token = new URLSearchParams(location.search).get('token') || '';
  function hdr() { return token ? { 'Authorization': 'Bearer ' + token } : {}; }
  function setStat(s) {
    var el = document.getElementById('stat');
    var bg = '后台 ' + s.bg_agents + ' 代理 / ' + s.bg_shells + ' 命令';
    el.innerHTML = '<span>v' + s.version + '</span><span>模式 ' + (s.mode||'-') + '</span><span>安全 ' + (s.security||'-') + '</span><span>' + bg + '</span><span>任务 ' + (s.automations||[]).length + '</span><span>知识库 ' + s.kb_chunks + '</span><span>' + s.server_time + '</span>';
    renderTasks(s.automations || []);
  }
  function refresh() {
    fetch('/api/remote/status', { headers: hdr() }).then(function(r){ return r.json(); }).then(setStat).catch(function(e){ document.getElementById('stat').textContent = '连接失败: ' + e; });
  }
  function sendMsg() {
    var msg = document.getElementById('msg');
    var t = msg.value.trim(); if (!t) return;
    var reply = document.getElementById('reply');
    reply.textContent = '⏳ 生成中…';
    msg.value = '';
    fetch('/api/remote/prompt', { method:'POST', headers: Object.assign({'Content-Type':'application/json'}, hdr()), body: JSON.stringify({ content: t }) })
      .then(function(r){ return r.json(); })
      .then(function(d){ reply.textContent = d.reply || ('⚠ ' + (d.error || '无回复')); refresh(); })
      .catch(function(e){ reply.textContent = '请求失败: ' + e; });
  }
  function renderTasks(list) {
    var el = document.getElementById('tasks');
    if (!list.length) { el.innerHTML = '<div style="color:#888;font-size:12px;">暂无任务</div>'; return; }
    el.innerHTML = list.map(function(t){
      return '<div class="task"><span><span class="' + (t.enabled?'on':'') + '">●</span> ' + t.name + ' <span style="color:#888">(' + t.schedule + (t.next_run ? ' · 下次 ' + t.next_run : '') + ')</span></span></div>';
    }).join('');
  }
  function createTask() {
    var name = document.getElementById('tname').value.trim();
    var prompt = document.getElementById('tprompt').value.trim();
    var sched = document.getElementById('tsched').value;
    if (!name || !prompt) return;
    fetch('/api/remote/tasks', { method:'POST', headers: Object.assign({'Content-Type':'application/json'}, hdr()), body: JSON.stringify({ name: name, prompt: prompt, schedule: sched }) })
      .then(function(r){ return r.json(); })
      .then(function(d){ if (d.ok) { document.getElementById('tname').value=''; document.getElementById('tprompt').value=''; } refresh(); })
      .catch(function(e){});
  }
  document.getElementById('msg').addEventListener('keydown', function(e){ if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMsg(); } });
  refresh(); setInterval(refresh, 5000);
</script>
</body>
</html>`
