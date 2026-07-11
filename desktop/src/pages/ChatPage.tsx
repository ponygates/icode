import React, { useState, useRef, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { useAppStore, Message } from '../stores/appStore';
import { Send, Plus, Trash2, MessageSquare, Cpu, Shield } from 'lucide-react';
import Markdown from '../components/Markdown';

const ChatPage: React.FC = () => {
  const { t } = useTranslation();
  const [input, setInput] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const [backendUrl, setBackendUrl] = useState<string | null>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Resolve the local backend URL once so we can stream chat directly from the
  // renderer (bypassing the fragile Electron IPC+SSE bridge). Falls back to the
  // IPC bridge when the backend URL is unavailable.
  useEffect(() => {
    if (window.icode && window.icode.getBackendURL) {
      window.icode
        .getBackendURL()
        .then((u: string | null) => {
          if (u) setBackendUrl(u);
        })
        .catch(() => {});
    }
  }, []);

  const {
    sessions, activeSessionId, selectedModel,
    createSession, setActiveSession, deleteSession,
    addMessage, updateMessage, clearMessages, models,
    updateTokenUsage, tokenUsage,
  } = useAppStore();

  const navigate = useNavigate();
  const activeSession = sessions.find((s) => s.id === activeSessionId);
  const currentModel = models.find((m) => m.id === selectedModel);
  const [mode, setMode] = useState<string>('ask');

  useEffect(() => {
    if (window.icode && window.icode.getConfig) {
      window.icode
        .getConfig()
        .then((c: any) => { if (c?.defaults?.mode) setMode(c.defaults.mode); })
        .catch(() => {});
    }
  }, []);

  const openSettings = () => navigate('/settings');

  useEffect(() => {
    if (!activeSessionId) {
      createSession(selectedModel, currentModel?.provider || 'deepseek');
    }
  }, []);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [activeSession?.messages]);

  const handleSend = useCallback(async () => {
    if (!input.trim() || !activeSessionId || isStreaming) return;

    const text = input.trim();
    const userMsg: Message = {
      id: Date.now().toString(36),
      role: 'user',
      content: text,
      timestamp: Date.now(),
    };
    addMessage(activeSessionId, userMsg);
    setInput('');
    setIsStreaming(true);

    const assistantMsg: Message = {
      id: (Date.now() + 1).toString(36),
      role: 'assistant',
      content: '',
      timestamp: Date.now(),
    };
    addMessage(activeSessionId, assistantMsg);

    const model = currentModel?.id || selectedModel || 'deepseek-v4-flash';
    const provider = currentModel?.provider || 'deepseek';

    let accumulated = '';
    let settled = false;
    let unsub = () => {};

    const onEvent = (event: any) => {
      if (settled) return;
      const ty = event?.type;
      if (ty === 'EventText' || ty === 'text') {
        accumulated += event.content || event.Content || '';
        updateMessage(activeSessionId, { ...assistantMsg, content: accumulated });
      } else if (ty === 'EventToolUse' || ty === 'tool_use') {
        const name = event.tool_call?.name || event.ToolCall?.Name || 'tool';
        accumulated += `\n[Tool: ${name}]\n`;
        updateMessage(activeSessionId, { ...assistantMsg, content: accumulated });
      } else if (ty === 'EventDone' || ty === 'done') {
        const usage = event.meta?.usage || event.meta?.Usage || {};
        updateTokenUsage({
          input: usage.prompt_tokens || usage.PromptTokens || 0,
          output: usage.completion_tokens || usage.CompletionTokens || 0,
          cacheHit: usage.cache_hit_tokens || usage.CacheHitTokens || 0,
          cost: estimateCost(usage, currentModel),
        });
        settled = true;
        unsub();
        setIsStreaming(false);
      } else if (ty === 'EventError' || ty === 'error') {
        settled = true;
        unsub();
        setIsStreaming(false);
        const msg = event.content || event.Content || 'unknown error';
        updateMessage(activeSessionId, {
          ...assistantMsg,
          content: (accumulated ? accumulated + '\n' : '') + '[Error] ' + msg,
        });
      }
    };

    try {
      if (backendUrl) {
        // Primary path: stream directly from the renderer to the local backend
        // (CORS is enabled on the server). The request carries model/provider
        // so the backend uses the user's selected model.
        await streamChat(
          backendUrl,
          { session_id: activeSessionId, content: text, model, provider },
          onEvent,
        );
        if (!settled) {
          settled = true;
          setIsStreaming(false);
        }
      } else if (window.icode && window.icode.sendMessage && window.icode.onChatStream) {
        // Fallback: Electron IPC bridge (still works if the URL is unknown).
        unsub = window.icode.onChatStream(onEvent);
        const res = await window.icode.sendMessage(activeSessionId, text);
        if (!settled && res && res.error) {
          settled = true;
          unsub();
          setIsStreaming(false);
          updateMessage(activeSessionId, { ...assistantMsg, content: '[Error] ' + res.error });
        }
      } else {
        // No backend at all — local demo reply so the UI stays usable.
        simulateTyping(
          `iCode Desktop — standalone mode

No backend server detected.

To connect to the AI backend:
1. Build the Go server: \`go build -o icode.exe .\`
2. Start the server: \`./icode.exe server\`
3. Relaunch the desktop app

Or run in CLI mode: \`./icode.exe chat\`

Currently selected: ${currentModel?.name || selectedModel} (${currentModel?.provider || 'unknown'})`,
          assistantMsg,
          activeSessionId,
          updateMessage,
          setIsStreaming,
        );
        return;
      }
    } catch (e: any) {
      if (!settled) {
        settled = true;
        unsub();
        setIsStreaming(false);
        updateMessage(activeSessionId, {
          ...assistantMsg,
          content: '[Error] ' + (e?.message || String(e)),
        });
      }
    }
  }, [input, activeSessionId, isStreaming, selectedModel, currentModel, backendUrl]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '8px 16px', borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ fontSize: 14, fontWeight: 500 }}>
            {activeSession?.title || t('sidebar.chat')}
          </span>
          <span style={{
            fontSize: 11, color: 'var(--text-muted)',
            background: 'var(--bg-tertiary)', padding: '2px 8px',
            borderRadius: 4,
          }}>
            {currentModel?.name || selectedModel}
          </span>
        </div>

        <div style={{ display: 'flex', gap: 8 }}>
          <button
            onClick={() => createSession(selectedModel, currentModel?.provider || 'deepseek')}
            title={t('chat.newSession')}
            style={{
              background: 'none', border: '1px solid var(--border-color)',
              color: 'var(--text-secondary)', padding: '4px 10px',
              borderRadius: 6, cursor: 'pointer', display: 'flex',
              alignItems: 'center', gap: 4, fontSize: 12,
            }}
          >
            <Plus size={14} /> {t('chat.newSession')}
          </button>
          {activeSessionId && (
            <button
              onClick={() => clearMessages(activeSessionId)}
              title={t('chat.clearChat')}
              style={{
                background: 'none', border: '1px solid var(--border-color)',
                color: 'var(--text-secondary)', padding: '4px 10px',
                borderRadius: 6, cursor: 'pointer', display: 'flex',
                alignItems: 'center', gap: 4, fontSize: 12,
              }}
            >
              <Trash2 size={14} />
            </button>
          )}
        </div>
      </div>

      {/* Middle: conversation + session-stats sidebar (Reasonix style) */}
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        {/* Messages */}
        <div style={{
          flex: 1, overflowY: 'auto', padding: '16px 20px',
          display: 'flex', flexDirection: 'column', gap: 16,
        }}>
          {/* Session list when no active session */}
          {!activeSessionId && sessions.length > 0 && (
            <div style={{ textAlign: 'center', padding: 40 }}>
              <h2 style={{ color: 'var(--text-secondary)', marginBottom: 16, fontSize: 15 }}>
                选择或新建会话
              </h2>
              {sessions.map((s) => (
                <button
                  key={s.id}
                  onClick={() => setActiveSession(s.id)}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 10,
                    width: '100%', maxWidth: 400, margin: '4px auto',
                    padding: '10px 14px', background: 'var(--bg-secondary)',
                    border: '1px solid var(--border-color)', borderRadius: 8,
                    color: 'var(--text-primary)', cursor: 'pointer',
                    textAlign: 'left',
                  }}
                >
                  <MessageSquare size={16} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13 }}>{s.title}</div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                      {s.modelId} · {s.messages.length} messages
                    </div>
                  </div>
                  <button
                    onClick={(e) => { e.stopPropagation(); deleteSession(s.id); }}
                    style={{
                      background: 'none', border: 'none', color: 'var(--text-muted)',
                      cursor: 'pointer', padding: 4,
                    }}
                  >
                    <Trash2 size={14} />
                  </button>
                </button>
              ))}
            </div>
          )}

          {/* Chat messages */}
          {activeSession?.messages.map((msg) => (
            <div
              key={msg.id}
              style={{
                display: 'flex', gap: 12,
                justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
              }}
            >
              {msg.role === 'assistant' && (
                <div style={{
                  width: 28, height: 28, borderRadius: 6, background: 'var(--accent)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: 14, fontWeight: 600, color: '#000', flexShrink: 0,
                }}>
                  i
                </div>
              )}
              <div style={{
                maxWidth: '78%', padding: '10px 14px', borderRadius: 10,
                background: msg.role === 'user' ? 'var(--bg-tertiary)' : 'var(--bg-secondary)',
                border: '1px solid var(--border-color)',
                color: 'var(--text-primary)', fontSize: 13,
                lineHeight: 1.7, wordBreak: 'break-word',
              }}>
                {msg.role === 'assistant' ? (
                  msg.content ? <Markdown text={msg.content} /> : (isStreaming ? '...' : '')
                ) : (
                  <span style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</span>
                )}
              </div>
              {msg.role === 'user' && (
                <div style={{
                  width: 28, height: 28, borderRadius: 6,
                  background: 'var(--warning)', display: 'flex',
                  alignItems: 'center', justifyContent: 'center',
                  fontSize: 14, fontWeight: 600, color: '#000', flexShrink: 0,
                }}>
                  U
                </div>
              )}
            </div>
          ))}
          <div ref={messagesEndRef} />
        </div>

        {/* Right sidebar — live session stats */}
        <div style={{
          width: 230, minWidth: 230, borderLeft: '1px solid var(--border-color)',
          background: 'var(--bg-secondary)', padding: '14px 14px', overflowY: 'auto',
          fontSize: 12, color: 'var(--text-secondary)',
        }}>
          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 10, letterSpacing: 0.5 }}>
            会话统计
          </div>
          <Stat label="模型" value={currentModel?.name || selectedModel} />
          <Stat label="提供商" value={currentModel?.provider || 'deepseek'} />
          <Stat label="模式" value={mode} />
          <div style={{ height: 1, background: 'var(--border-color)', margin: '10px 0' }} />
          <Stat label="↑ 输入 Token" value={tokenUsage.input.toLocaleString()} />
          <Stat label="↓ 输出 Token" value={tokenUsage.output.toLocaleString()} />
          <Stat
            label="缓存命中"
            value={tokenUsage.cacheHit > 0
              ? `${tokenUsage.cacheHit.toLocaleString()} (${((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(1)}%)`
              : '0'}
          />
          <Stat label="预估费用" value={tokenUsage.cost} />
        </div>
      </div>

      {/* Status bar — clickable pills (model / provider / mode) + live tokens */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
        padding: '5px 16px', fontSize: 11, color: 'var(--text-muted)',
        background: 'var(--bg-primary)', borderTop: '1px solid var(--border-color)',
      }}>
        <Pill icon={<Cpu size={12} />} label={currentModel?.name || selectedModel} onClick={openSettings} />
        <Pill icon={<span>◈</span>} label={currentModel?.provider || 'deepseek'} onClick={openSettings} />
        <Pill icon={<Shield size={12} />} label={`模式: ${mode}`} onClick={openSettings} />
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 14 }}>
          <span>↑{tokenUsage.input.toLocaleString()}</span>
          <span>↓{tokenUsage.output.toLocaleString()}</span>
          <span style={{ color: 'var(--success)' }}>
            {t('token.cacheHit')}: {tokenUsage.cacheHit > 0
              ? ((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(1) + '%'
              : '0%'}
          </span>
          <span style={{ fontWeight: 500 }}>{t('token.cost')}: {tokenUsage.cost}</span>
        </span>
      </div>

      {/* Input */}
      <div style={{
        padding: '10px 16px', borderTop: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <div style={{
          display: 'flex', gap: 10, alignItems: 'flex-end',
          background: 'var(--bg-primary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: '6px 8px',
        }}>
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t('chat.placeholder')}
            rows={1}
            style={{
              flex: 1, background: 'transparent', border: 'none',
              color: 'var(--text-primary)', resize: 'none',
              outline: 'none', padding: '6px 4px', maxHeight: 200,
              lineHeight: 1.5,
            }}
          />
          <button
            onClick={handleSend}
            disabled={!input.trim() || isStreaming}
            title={t('chat.send')}
            style={{
              background: input.trim() ? 'var(--accent)' : 'var(--border-color)',
              border: 'none', color: input.trim() ? '#000' : 'var(--text-muted)',
              padding: '6px 10px', borderRadius: 8, cursor: input.trim() ? 'pointer' : 'default',
              display: 'flex', alignItems: 'center', transition: 'background 0.15s',
            }}
          >
            <Send size={16} />
          </button>
        </div>
      </div>
    </div>
  );
};

/**
 * streamChat talks to the local backend /api/chat endpoint directly from the
 * renderer and parses the Server-Sent-Events stream. This avoids the multi-hop
 * Electron IPC bridge and is the reliable path for desktop streaming.
 */
async function streamChat(
  baseUrl: string,
  payload: { session_id: string; content: string; model: string; provider: string },
  onEvent: (e: any) => void,
) {
  const res = await fetch(`${baseUrl}/api/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => '');
    onEvent({ type: 'error', content: text || `HTTP ${res.status}` });
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';
    for (const line of lines) {
      const s = line.trim();
      if (!s.startsWith('data:')) continue;
      const p = s.slice(4).trim();
      if (!p) continue;
      try {
        onEvent(JSON.parse(p));
      } catch {
        /* ignore malformed event */
      }
    }
  }
}

function simulateTyping(
  text: string,
  assistantMsg: Message,
  sessionId: string,
  updateMsg: any,
  setStreaming: any
) {
  let i = 0;
  const interval = setInterval(() => {
    i += 1;
    updateMsg(sessionId, {
      ...assistantMsg,
      content: text.slice(0, Math.min(i * 3, text.length)),
    });
    if (i * 3 >= text.length) {
      clearInterval(interval);
      setStreaming(false);
    }
  }, 30);
}

function estimateCost(usage: any, model: any): string {
  if (!usage || (!usage.PromptTokens && !usage.prompt_tokens && !usage.TotalTokens && !usage.total_tokens))
    return '\xA50.00';
  const input = usage.PromptTokens || usage.prompt_tokens || 0;
  const output = usage.CompletionTokens || usage.completion_tokens || 0;
  return '\xA5' + ((input * 0.00014 + output * 0.00028) / 1000).toFixed(4);
}

// Small presentational helpers for the status bar / stats sidebar.
function Pill({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      title="点击打开设置"
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 5,
        background: 'var(--bg-tertiary)', border: '1px solid var(--border-color)',
        color: 'var(--text-secondary)', borderRadius: 12, padding: '2px 10px',
        fontSize: 11, cursor: 'pointer',
      }}
    >
      {icon}
      {label}
    </button>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', padding: '5px 0', borderBottom: '1px dashed var(--border-color)' }}>
      <span style={{ color: 'var(--text-muted)' }}>{label}</span>
      <span style={{ color: 'var(--text-primary)', fontWeight: 500, textAlign: 'right', maxWidth: 130, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {value}
      </span>
    </div>
  );
}

export default ChatPage;
