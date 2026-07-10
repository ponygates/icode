import React, { useState, useRef, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore, Message } from '../stores/appStore';
import { Send, Plus, Trash2, MessageSquare } from 'lucide-react';
import TokenBar from '../components/TokenBar';

const ChatPage: React.FC = () => {
  const { t } = useTranslation();
  const [input, setInput] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const {
    sessions, activeSessionId, selectedModel,
    createSession, setActiveSession, deleteSession,
    addMessage, updateMessage, clearMessages, models,
    updateTokenUsage,
  } = useAppStore();

  const activeSession = sessions.find((s) => s.id === activeSessionId);
  const currentModel = models.find((m) => m.id === selectedModel);

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

    // No backend bridge available — show a local demo reply.
    if (!window.icode || !window.icode.sendMessage || !window.icode.onChatStream) {
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

    let accumulated = '';
    let settled = false;

    const unsub = window.icode.onChatStream((event: any) => {
      if (settled) return;
      const t = event?.type;
      if (t === 'EventText' || t === 'text') {
        accumulated += event.content || event.Content || '';
        updateMessage(activeSessionId, { ...assistantMsg, content: accumulated });
      } else if (t === 'EventToolUse' || t === 'tool_use') {
        const name = event.tool_call?.name || event.ToolCall?.Name || 'tool';
        accumulated += `\n[Tool: ${name}]`;
        updateMessage(activeSessionId, { ...assistantMsg, content: accumulated });
      } else if (t === 'EventDone' || t === 'done') {
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
      } else if (t === 'EventError' || t === 'error') {
        settled = true;
        unsub();
        setIsStreaming(false);
        const msg = event.content || event.Content || 'unknown error';
        updateMessage(activeSessionId, {
          ...assistantMsg,
          content: (accumulated ? accumulated + '\n' : '') + '[Error] ' + msg,
        });
      }
    });

    try {
      const res = await window.icode.sendMessage(activeSessionId, text);
      if (!settled && res && res.error) {
        settled = true;
        unsub();
        setIsStreaming(false);
        updateMessage(activeSessionId, { ...assistantMsg, content: '[Error] ' + res.error });
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
  }, [input, activeSessionId, isStreaming, selectedModel, currentModel]);

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
              maxWidth: '75%', padding: '10px 14px', borderRadius: 10,
              background: msg.role === 'user' ? 'var(--bg-tertiary)' : 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              color: 'var(--text-primary)', fontSize: 13,
              lineHeight: 1.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            }}>
              {msg.content || (isStreaming ? '...' : '')}
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

      {/* Token Bar */}
      <TokenBar />

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

export default ChatPage;
