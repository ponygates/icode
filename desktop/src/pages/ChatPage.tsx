import React, { useState, useRef, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore, Message } from '../stores/appStore';
import { Send, Plus, Trash2, MessageSquare, Cpu, Shield, Square, ShieldAlert, GitBranch, FileText, RefreshCw, Folder, Edit3, Download } from 'lucide-react';
import Markdown from '../components/Markdown';
import CommandPalette, { useCommandPalette } from '../components/CommandPalette';
import TodoPanel from '../components/TodoPanel';
import TokenBar from '../components/TokenBar';
import TabBar from '../components/TabBar';
import CheckpointPanel from '../components/CheckpointPanel';
import FilePicker from '../components/FilePicker';

// Shorten a path to its last 2 segments for display.
function shortDir(p: string): string {
  if (!p) return '';
  const parts = p.replace(/\\/g, '/').split('/').filter(Boolean);
  if (parts.length <= 2) return parts.join('/');
  return parts.slice(-2).join('/');
}

const ChatPage: React.FC = () => {
  const palette = useCommandPalette();
  const { t } = useTranslation();
  const [input, setInput] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  // Interactive permission request pending an answer from the user. When set,
  // the conversation engine is blocked server-side until we respond.
  const [pendingPermission, setPendingPermission] = useState<any>(null);
  const [attachedImages, setAttachedImages] = useState<{ mime: string; data: string }[]>([]);
  const [gitBranch, setGitBranch] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Resolve the local backend URL once so we can stream chat directly from the
  // renderer (bypassing the fragile Electron IPC+SSE bridge). Falls back to the
  // IPC bridge when the backend URL is unavailable.
  useEffect(() => {
    checkBackend();
  }, []);

  const {
    sessions, activeSessionId, selectedModel,
    createSession, setActiveSession, deleteSession,
    addMessage, updateMessage, clearMessages, models,
    updateTokenUsage, tokenUsage, checkBackend, backendUrl, setBackendUrl,
  } = useAppStore();

  const abortRef = useRef<AbortController | null>(null);
  const activeSession = sessions.find((s) => s.id === activeSessionId);

  // Multi-tab state: track open tabs in addition to the store's active session
  const [openTabs, setOpenTabs] = useState<{ id: string; title: string }[]>(() => {
    if (activeSessionId) return [{ id: activeSessionId, title: activeSession?.title || '会话' }];
    return [];
  });

  // Sync tab state when store's active session changes
  useEffect(() => {
    if (activeSessionId && !openTabs.find(t => t.id === activeSessionId)) {
      setOpenTabs(prev => [...prev, { id: activeSessionId, title: activeSession?.title || `会话 ${prev.length + 1}` }]);
    }
  }, [activeSessionId]);

  const handleTabSelect = (id: string) => {
    setActiveSession(id);
  };

  const handleTabClose = (id: string) => {
    setOpenTabs(prev => {
      const remaining = prev.filter(t => t.id !== id);
      if (remaining.length === 0) {
        // Don't close last tab — create a new one instead
        createSession(selectedModel, currentModel?.provider || 'openrouter');
        return prev;
      }
      if (id === activeSessionId && remaining.length > 0) {
        setActiveSession(remaining[remaining.length - 1].id);
      }
      return remaining;
    });
  };

  const handleTabNew = () => {
    createSession(selectedModel, currentModel?.provider || 'openrouter');
  };

  // File drag-drop state
  const [dragOver, setDragOver] = useState(false);
  const [dropFiles, setDropFiles] = useState<string[]>([]);

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);
    const dt = e.dataTransfer;
    const files: File[] = Array.from(dt.files || []);
    if (files.length > 0) {
      const names = files.map(f => f.name);
      setDropFiles(prev => [...prev, ...names]);
      // Read text files and append @file references
      const refs = names.map(n => `@${n}`).join(' ');
      setInput(prev => prev ? `${prev} ${refs}` : refs);
      // Read file contents for context
      files.forEach(file => {
        const reader = new FileReader();
        reader.onload = () => {
          const text = reader.result as string;
          setInput(prev => {
            const fileRef = `\n\n--- File: ${file.name} ---\n${text.slice(0, 8000)}`;
            return prev + fileRef;
          });
        };
        reader.readAsText(file);
      });
    }
  };
  const currentModel = models.find((m) => m.id === selectedModel);
  // Mode is synced from Zustand store (bidirectional with backend)
  const mode = useAppStore((s) => s.mode);

  const openSettings = () => {
    window.dispatchEvent(new CustomEvent('icode:open-settings'));
  };

  // Ensure a session exists immediately. Using createSession's async
  // side-effect (IPC) is fine — the Zustand store sets activeSessionId
  // synchronously before awaiting the backend, so the value is available
  // for handleSend on the very next render.
  useEffect(() => {
    if (!activeSessionId && sessions.length === 0) {
      createSession(selectedModel, currentModel?.provider || 'openrouter');
    }
  }, [activeSessionId, sessions.length, selectedModel, currentModel]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [activeSession?.messages]);

  // Fetch git branch for the status bar
  useEffect(() => {
    const fetchBranch = async () => {
      if (!backendUrl) return;
      try {
        const res = await fetch(`${backendUrl}/api/status`);
        if (res.ok) {
          const data = await res.json();
          setGitBranch(data.git_branch || '');
        }
      } catch {}
    };
    fetchBranch();
    const interval = setInterval(fetchBranch, 10000);
    return () => clearInterval(interval);
  }, [backendUrl]);

  const handleSend = useCallback(async () => {
    if (!input.trim() || isStreaming) return;

    // Ensure we have a session and a backend URL
    let sid = activeSessionId;
    if (!sid) {
      createSession(selectedModel, currentModel?.provider || 'openrouter');
      sid = useAppStore.getState().activeSessionId;
      if (!sid) return;
    }
    // Force a fresh health-check to discover the backend if needed
    const current = useAppStore.getState();
    let url = current.backendUrl;
    if (!url) {
      // Try same-origin discovery inline
      const origin = window.location.origin;
      if (origin && origin !== 'null' && !origin.startsWith('file://')) {
        try {
          const hr = await fetch(`${origin}/api/health`);
          if (hr.ok) { url = origin; setBackendUrl(origin); }
        } catch {}
      }
      if (!url) {
        // Last ditch: try localhost ports
        for (const p of [57356, 57357, 8080, 3000, 9090]) {
          try {
            const hr = await fetch(`http://127.0.0.1:${p}/api/health`);
            if (hr.ok) { url = `http://127.0.0.1:${p}`; setBackendUrl(url); break; }
          } catch {}
        }
      }
    }

    const text = input.trim();
    const userMsg: Message = {
      id: Date.now().toString(36),
      role: 'user', content: text, timestamp: Date.now(),
    };
    addMessage(sid, userMsg);
    setInput('');
    setIsStreaming(true);

    const assistantMsg: Message = {
      id: (Date.now() + 1).toString(36),
      role: 'assistant', content: '', timestamp: Date.now(),
    };
    addMessage(sid, assistantMsg);

    const model = currentModel?.id || selectedModel || 'openrouter/free';
    const provider = currentModel?.provider || 'openrouter';
    let accumulated = '';
    let settled = false;

    const onEvent = (event: any) => {
      if (settled) return;
      const ty = event?.type;
      console.log('[iCode stream] event:', ty, event);
      if (ty === 'text') {
        accumulated += event.content || '';
        updateMessage(sid, { ...assistantMsg, content: accumulated });
      } else if (ty === 'tool_use') {
        const name = event.tool_call?.name || event.ToolCall?.Name || 'tool';
        accumulated += `\n⏺ ${name}\n`;
        updateMessage(sid, { ...assistantMsg, content: accumulated });
      } else if (ty === 'permission') {
        const req = event.permission || event.Permission;
        if (req?.request_id) setPendingPermission(req);
      } else if (ty === 'done') {
        settled = true;
        setIsStreaming(false);
        setPendingPermission(null);
        const u = event.meta?.usage || {};
        updateTokenUsage({
          input: u.prompt_tokens || 0,
          output: u.completion_tokens || 0,
          cacheHit: u.cache_hit_tokens || 0,
          cost: estimateCost(u, currentModel),
        });
      } else if (ty === 'error') {
        settled = true;
        setIsStreaming(false);
        setPendingPermission(null);
        updateMessage(sid, {
          ...assistantMsg,
          content: (accumulated || '') + '\n❌ ' + (event.content || '未知错误'),
        });
      }
    };

    if (url) {
      const payload: any = { session_id: sid, content: text, model, provider };
      if (attachedImages.length > 0) {
        payload.attachments = attachedImages.map(img => ({ type: 'image', mime: img.mime, data: img.data }));
        setAttachedImages([]);
      }
      console.log('[iCode] sending to', url, 'model:', model, 'provider:', provider);
      await streamChat(url, payload, onEvent, abortRef);
    } else {
      onEvent({ type: 'error', content: '无法连接到后端服务。\n请确保 iCode 后端已启动，或检查网络连接。\n如果后端在运行，请尝试刷新页面。' });
    }

    if (!settled) { settled = true; setIsStreaming(false); }
  }, [input, activeSessionId, isStreaming, selectedModel, currentModel, attachedImages]);

  const respondPermission = useCallback(async (requestId: string, decision: string) => {
    setPendingPermission(null);
    try {
      if (window.icode?.respondPermission) {
        await window.icode.respondPermission(requestId, decision);
      } else if (backendUrl) {
        await fetch(`${backendUrl}/api/permission/respond`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ request_id: requestId, decision }),
        });
      }
    } catch { /* backend unreachable */ }
  }, [backendUrl]);

  const handleStop = useCallback(() => {
    if (!activeSessionId) return;
    abortRef.current?.abort();
    setPendingPermission(null);
    setIsStreaming(false);
    // Try Electron IPC first, then HTTP
    if (window.icode?.stopChat) {
      window.icode.stopChat(activeSessionId);
    } else if (backendUrl) {
      fetch(`${backendUrl}/api/chat/stop`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: activeSessionId }),
      }).catch(() => {});
    }
  }, [activeSessionId, backendUrl]);

  // File picker state - triggered by @ in input
  const [pickerOpen, setPickerOpen] = useState(false);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
      return;
    }
    if (e.key === '@' && (input || '').trim() === '') {
      setPickerOpen(true);
    }
  };

  const handleFileSelect = (path: string) => {
    setInput(prev => prev ? `${prev} @${path}` : `@${path}`);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      {/* Header — Apple style */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '14px 24px', borderBottom: '0.5px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 14 }}>
          <span style={{ fontSize: 18, fontWeight: 700, color: 'var(--text-primary)', letterSpacing: '-0.02em' }}>
            {activeSession?.title || t('sidebar.chat')}
          </span>
          <button
            onClick={() => {
              const title = prompt('会话标题', activeSession?.title || '');
              if (title && activeSessionId) {
                // Update session title
                useAppStore.setState(prev => ({
                  sessions: prev.sessions.map(s =>
                    s.id === activeSessionId ? { ...s, title } : s
                  ),
                }));
                // Persist to backend
                if (backendUrl) {
                  fetch(`${backendUrl}/api/sessions/${activeSessionId}`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ title }),
                  }).catch(() => {});
                }
              }
            }}
            className="interactive"
            style={{
              background: 'none', border: 'none', color: 'var(--text-muted)',
              padding: 2, display: 'flex', alignItems: 'center', borderRadius: 4,
            }}
          >
            <Edit3 size={14} />
          </button>
          <span style={{
            fontSize: 11, color: 'var(--text-muted)', fontWeight: 500,
            background: 'var(--bg-tertiary)', padding: '3px 10px',
            borderRadius: 'var(--r-full)',
          }}>
            {currentModel?.name || selectedModel}
          </span>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {/* Open folder */}
          <button className="interactive" title="打开项目目录"
            onClick={() => {
              // Use the Electron shell API if available
              const api = window.icode as any;
              if (api?.openFolder) {
                api.openFolder('.');
              }
            }}
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-muted)', padding: '4px 8px', borderRadius: 6,
              display: 'flex', alignItems: 'center', gap: 4, fontSize: 11,
            }}>
            <Folder size={12} />
          </button>
          {/* Export session */}
          <button className="interactive" title="导出会话"
            onClick={() => {
              if (!activeSession?.messages?.length) return;
              const md = activeSession.messages.map(m =>
                `### ${m.role === 'user' ? '👤 用户' : '🤖 iCode'}\n\n${m.content}\n`
              ).join('\n---\n\n');
              const blob = new Blob([md], { type: 'text/markdown' });
              const url = URL.createObjectURL(blob);
              const a = document.createElement('a');
              a.href = url;
              a.download = `icode-${activeSession.title || 'session'}.md`;
              a.click();
              URL.revokeObjectURL(url);
            }}
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-muted)', padding: '4px 8px', borderRadius: 6,
              display: 'flex', alignItems: 'center', gap: 4, fontSize: 11,
            }}>
            <Download size={12} />
          </button>
          {/* Branch session */}
          {activeSessionId && activeSession?.messages && activeSession.messages.length > 0 && (
            <button
              onClick={() => {
              // Fork: copy current session messages into a new session
              const { createSession, addMessage } = useAppStore.getState();
              createSession(selectedModel, currentModel?.provider || 'openrouter');
              setTimeout(() => {
                const st = useAppStore.getState();
                const newId = st.sessions[st.sessions.length - 1]?.id;
                if (newId && activeSession?.messages) {
                  activeSession.messages.forEach((m: Message) => {
                    addMessage(newId, { ...m, id: Math.random().toString(36).slice(2) });
                  });
                }
              }, 100);
            }}
              title="分支会话"
              className="interactive"
              style={{
                background: 'none', border: '0.5px solid var(--border-color)',
                color: 'var(--text-secondary)', padding: '4px 10px',
                borderRadius: 6, display: 'flex',
                alignItems: 'center', gap: 4, fontSize: 12,
              }}>
              <GitBranch size={14} /> 分支
            </button>
          )}
          <button
            onClick={() => createSession(selectedModel, currentModel?.provider || 'openrouter')}
            className="interactive"
            title={t('chat.newSession')}
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-secondary)', padding: '4px 10px',
              borderRadius: 6, display: 'flex',
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

      {/* Tab bar for multi-session switching */}
      <TabBar
        tabs={openTabs}
        activeId={activeSessionId}
        onSelect={handleTabSelect}
        onClose={handleTabClose}
        onNew={handleTabNew}
      />

      {/* Middle: conversation + session-stats sidebar (Reasonix style) */}
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        {/* Messages */}
        <div style={{
          flex: 1, overflowY: 'auto', padding: '24px 24px',
          display: 'flex', flexDirection: 'column', gap: 4,
        }}>
          {/* Welcome — Apple-style large title */}
          {activeSession?.messages.length === 0 && !isStreaming && (
            <div style={{
              flex: 1, display: 'flex', flexDirection: 'column',
              alignItems: 'center', justifyContent: 'center',
              padding: 60, color: 'var(--text-muted)',
            }}>
              <div className="page-title" style={{ fontSize: 32, textAlign: 'center', marginBottom: 6, color: 'var(--accent)' }}>
                iCode
              </div>
              <div className="page-subtitle" style={{ fontSize: 15, marginBottom: 32 }}>
                你的 AI 编程伙伴
              </div>
              <div style={{
                display: 'flex', gap: 10, flexWrap: 'wrap', justifyContent: 'center',
              }}>
                {[
                  { label: '写一个 React 组件', icon: '⚛' },
                  { label: '解释这段代码', icon: '🔍' },
                  { label: '重构这个函数', icon: '🔄' },
                  { label: '帮我 Debug', icon: '🐛' },
                ].map((s) => (
                  <button
                    key={s.label}
                    onClick={() => { setInput(s.label); }}
                    className="interactive"
                    style={{
                      padding: '10px 18px', borderRadius: 'var(--r-full)',
                      background: 'var(--bg-secondary)', border: '0.5px solid var(--border-color)',
                      color: 'var(--text-secondary)', fontSize: 13,
                      display: 'flex', alignItems: 'center', gap: 6,
                    }}
                  >
                    <span>{s.icon}</span>
                    {s.label}
                  </button>
                ))}
              </div>
              <div style={{ marginTop: 32, fontSize: 11, color: 'var(--text-muted)', lineHeight: 2, textAlign: 'center' }}>
                输入问题开始对话 · /help 查看命令 · Ctrl+K 命令面板 · @ 引用文件
              </div>
            </div>
          )}

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
                display: 'flex', gap: 10, padding: '6px 24px',
                justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
                alignItems: 'flex-start',
              }}
            >
              {msg.role === 'assistant' && (
                <div style={{
                  width: 30, height: 30, borderRadius: '50%', background: 'var(--accent)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: 13, fontWeight: 600, color: '#fff', flexShrink: 0,
                }}>i</div>
              )}
              <div className={msg.role === 'assistant' ? 'msg-bubble' : ''} style={{
                maxWidth: '75%', padding: '12px 16px',
                background: msg.role === 'user' ? 'var(--accent-soft)' : undefined,
                border: msg.role === 'user' ? '0.5px solid var(--accent)' : undefined,
                borderRadius: msg.role === 'user' ? 'var(--r-xl)' : undefined,
                color: 'var(--text-primary)', fontSize: 13,
                lineHeight: 1.7, wordBreak: 'break-word',
                position: 'relative',
              }}>
                {msg.role === 'assistant' ? (
                  msg.content ? (
                    msg.content.startsWith('[Tool:') ? (
                      <div style={{ fontSize: 12, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
                        <div style={{ color: 'var(--accent)', fontWeight: 500, marginBottom: 4 }}>
                          ⏺ {msg.content.match(/\[Tool: ([^\]]+)\]/)?.[1] || '工具调用'}
                        </div>
                        <pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{msg.content.replace(/\[Tool: [^\]]+\]\n?/, '')}</pre>
                      </div>
                    ) : (
                      <>
                        <Markdown text={msg.content} />
                        {/* Action buttons — hidden until bubble hover */}
                        <div className="action-hidden" style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                          <ActionBtn icon="📋" label="复制" title="复制回复"
                            onClick={() => navigator.clipboard.writeText(msg.content)} />
                          <ActionBtn icon="🔄" label="重新生成" title="重新生成此回复"
                            onClick={() => {
                              const msgs = activeSession?.messages || [];
                              const idx = msgs.findIndex(m => m.id === msg.id);
                              if (idx > 0) {
                                const userMsg = msgs[idx - 1];
                                if (userMsg.role === 'user') setInput(userMsg.content);
                              }
                            }} />
                        </div>
                      </>
                    )
                  ) : (isStreaming ? (
                    <span style={{ color: 'var(--text-muted)' }}>
                      生成中<span style={{ animation: 'pulse 1.5s infinite' }}>...</span>
                    </span>
                  ) : '')
                ) : (
                  <span style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</span>
                )}
              </div>
              {msg.role === 'user' && (
                <div style={{
                  width: 30, height: 30, borderRadius: '50%',
                  background: 'var(--warning)', display: 'flex',
                  alignItems: 'center', justifyContent: 'center',
                  fontSize: 13, fontWeight: 600, color: '#fff', flexShrink: 0,
                }}>U</div>
              )}
            </div>
          ))}
          <div ref={messagesEndRef} />
        </div>

        {/* Right sidebar — iCode overview panels */}
        <div style={{
          width: 250, minWidth: 250, borderLeft: '0.5px solid var(--border-color)',
          background: 'var(--bg-secondary)', padding: '16px 14px', overflowY: 'auto',
          fontSize: 12, color: 'var(--text-secondary)', display: 'flex', flexDirection: 'column', gap: 14,
        }}>
          {/* Files touched by agent */}
          {(() => {
            const files = new Map<string, string>();
            (activeSession?.messages || []).forEach(msg => {
              if (!msg.content) return;
              const patterns = [
                /read_file\b.*?["'`]([^"'`\n]+?)["'`]/g,
                /write_file\b.*?["'`]([^"'`\n]+?)["'`]/g,
                /\b(?:edit|bash)\b.*?["'`]([^"'`\n]+?)["'`]/g,
                /\b([\w\/\\\.\-]+\.(?:tsx?|jsx?|go|py|rs|java|rb|html|css|json|yaml|yml|md|toml))\b/g,
              ];
              patterns.forEach(re => {
                let m: RegExpExecArray | null;
                while ((m = re.exec(msg.content)) !== null) {
                  const path = m[1].replace(/\\/g, '/');
                  if (path.length > 2 && !path.startsWith('http') && !files.has(path)) {
                    const action = msg.content.includes('write_file') ? 'write' :
                                   msg.content.includes('edit') ? 'edit' : 'read';
                    files.set(path, action);
                  }
                }
              });
            });
            const fileList = Array.from(files.entries());
            if (fileList.length === 0) return null;
            return (
              <div className="card" style={{ padding: 12 }}>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 8, fontWeight: 500 }}>
                  文件操作 · {fileList.length}
                </div>
                {fileList.map(([path, action]) => (
                  <div key={path} style={{
                    display: 'flex', alignItems: 'center', gap: 6,
                    padding: '3px 0', fontSize: 11, fontFamily: 'var(--font-mono)',
                  }}>
                    <span style={{
                      color: action === 'write' || action === 'edit' ? 'var(--accent)' : 'var(--text-muted)',
                      fontSize: 10, flexShrink: 0,
                    }}>
                      {action === 'write' ? '✎' : action === 'edit' ? '✏' : '☷'}
                    </span>
                    <span style={{
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                      color: action !== 'read' ? 'var(--text-primary)' : 'var(--text-muted)',
                    }}>
                      {path.split('/').pop() || path}
                    </span>
                  </div>
                ))}
              </div>
            );
          })()}

          {/* Card 1: Context Window */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)' }}>上下文窗口</span>
              <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>200K</span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              {/* Circular progress */}
              <svg width="56" height="56" viewBox="0 0 56 56">
                <circle cx="28" cy="28" r="24" fill="none" stroke="var(--border-color)" strokeWidth="3" />
                <circle
                  cx="28" cy="28" r="24" fill="none"
                  stroke="var(--success)" strokeWidth="3"
                  strokeDasharray={`${Math.min(tokenUsage.input / 200000, 1) * 150.8} 150.8`}
                  strokeLinecap="round"
                  transform="rotate(-90 28 28)"
                />
                <text x="28" y="32" textAnchor="middle" fontSize="11" fontWeight="600" fill="var(--text-primary)">
                  {Math.min((tokenUsage.input / 200000) * 100, 100).toFixed(0)}%
                </text>
              </svg>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>已用</div>
                <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {(tokenUsage.input / 1000).toFixed(1)}K
                </div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)', marginTop: 4 }}>距上限 200K</div>
              </div>
            </div>
          </div>

          {/* Card 2: Session Metrics */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10 }}>
              会话指标
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>缓存命中</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--success)' }}>
                  {tokenUsage.cacheHit > 0
                    ? ((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(1) + '%'
                    : '0%'}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>运行时间</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {Math.floor((Date.now() - (activeSessionId ? 0 : Date.now())) / 1000)}s
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>累计 Tokens</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {(tokenUsage.input + tokenUsage.output).toLocaleString()}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>预估费用</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--accent)' }}>
                  {tokenUsage.cost}
                </div>
              </div>
            </div>
          </div>

          {/* Card 3: Usage Breakdown */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10 }}>
              用量构成
            </div>
            <div style={{ display: 'flex', gap: 4, height: 6, borderRadius: 3, overflow: 'hidden', marginBottom: 8 }}>
              {(() => {
                const total = tokenUsage.input + tokenUsage.output;
                if (total === 0) return <div style={{ flex: 1, background: 'var(--bg-tertiary)' }} />;
                const inputPct = (tokenUsage.input / total) * 100;
                const outputPct = (tokenUsage.output / total) * 100;
                const cachePct = tokenUsage.cacheHit > 0 ? Math.min((tokenUsage.cacheHit / total) * 100, 30) : 0;
                return (
                  <>
                    <div style={{ width: `${inputPct - cachePct/2}%`, background: 'var(--accent)' }} />
                    <div style={{ width: `${cachePct}%`, background: 'var(--success)' }} />
                    <div style={{ width: `${outputPct}%`, background: 'var(--warning)' }} />
                  </>
                );
              })()}
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 4, fontSize: 10, color: 'var(--text-muted)' }}>
              <div>● <span style={{ color: 'var(--accent)' }}>输入</span> {tokenUsage.input.toLocaleString()}</div>
              <div>● <span style={{ color: 'var(--success)' }}>缓存</span> {tokenUsage.cacheHit.toLocaleString()}</div>
              <div>● <span style={{ color: 'var(--warning)' }}>输出</span> {tokenUsage.output.toLocaleString()}</div>
            </div>
          </div>

          {/* Todo & Checkpoint panels */}
          <TodoPanel />
          <CheckpointPanel />
        </div>
      </div>

      {/* Status bar — full metrics like Reasonix */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
        padding: '5px 16px', fontSize: 11, color: 'var(--text-muted)',
        background: 'var(--bg-primary)', borderTop: '0.5px solid var(--border-color)',
      }}>
        <Pill icon={<Cpu size={12} />} label={currentModel?.name || selectedModel} onClick={openSettings} />
        <Pill icon={<span>◈</span>} label={currentModel?.provider || 'openrouter'} onClick={openSettings} />
        <Pill icon={<Shield size={12} />} label={mode} onClick={openSettings} />
        {/* CWD + Git branch */}
        <Pill icon={<Folder size={12} />} label={shortDir('~')} onClick={() => {}} />
        <Pill icon={<GitBranch size={12} />} label={gitBranch || '—'} onClick={() => {}} />
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

      {/* Action toolbar — iCode session actions */}
      {activeSession?.messages && activeSession.messages.length > 0 && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '8px 24px', background: 'var(--bg-secondary)',
          borderTop: '0.5px solid var(--border-color)',
        }}>
          <button className="interactive" onClick={() => {
              const { createSession, addMessage } = useAppStore.getState();
              createSession(selectedModel, currentModel?.provider || 'openrouter');
              setTimeout(() => {
                const st = useAppStore.getState();
                const newId = st.sessions[st.sessions.length - 1]?.id;
                if (newId && activeSession?.messages) {
                  activeSession.messages.forEach((m: Message) => {
                    addMessage(newId, { ...m, id: Math.random().toString(36).slice(2) });
                  });
                }
              }, 100);
            }} style={{
            padding: '5px 10px', borderRadius: 6, fontSize: 11,
            background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
            color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 4,
          }}>
            <GitBranch size={12} /> 分叉会话
          </button>
          <button className="interactive" onClick={() => {
              // Compact the session
              setInput('/compact');
              setTimeout(() => handleSend(), 100);
            }} style={{
            padding: '5px 10px', borderRadius: 6, fontSize: 11,
            background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
            color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 4,
          }}>
            <FileText size={12} /> 总结
          </button>
          <button className="interactive" onClick={() => {
              // Show git diff
              setInput('/diff');
              setTimeout(() => handleSend(), 100);
            }} style={{
            padding: '5px 10px', borderRadius: 6, fontSize: 11,
            background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
            color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 4,
          }}>
            <RefreshCw size={12} /> 回顾
          </button>
        </div>
      )}

      {/* Attached image previews */}
      {attachedImages.length > 0 && (
        <div style={{
          padding: '8px 16px', display: 'flex', gap: 8, flexWrap: 'wrap',
          borderTop: '1px solid var(--border-color)', background: 'var(--bg-secondary)',
        }}>
          {attachedImages.map((img, i) => (
            <div key={i} style={{ position: 'relative', display: 'inline-block' }}>
              <img
                src={`data:${img.mime};base64,${img.data}`}
                alt="pasted"
                style={{ height: 64, borderRadius: 6, border: '1px solid var(--border-color)' }}
              />
              <button
                onClick={() => setAttachedImages(prev => prev.filter((_, j) => j !== i))}
                style={{
                  position: 'absolute', top: -6, right: -6, width: 18, height: 18,
                  borderRadius: '50%', border: 'none', background: 'var(--error)',
                  color: '#fff', fontSize: 11, cursor: 'pointer', lineHeight: '18px',
                  textAlign: 'center', padding: 0,
                }}
              >×</button>
            </div>
          ))}
        </div>
      )}

      {/* Input — Apple-style clean input bar */}
      <div
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        style={{
        padding: '12px 24px', borderTop: '0.5px solid var(--border-color)',
        background: dragOver ? 'var(--accent-soft)' : 'var(--bg-secondary)',
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
            onPaste={(e) => {
              const items = e.clipboardData?.items;
              if (!items) return;
              for (let i = 0; i < items.length; i++) {
                if (items[i].type.startsWith('image/')) {
                  const file = items[i].getAsFile();
                  if (!file) continue;
                  const reader = new FileReader();
                  reader.onload = () => {
                    const base64 = (reader.result as string).split(',')[1];
                    setAttachedImages(prev => [...prev, { mime: items[i].type, data: base64 }]);
                  };
                  reader.readAsDataURL(file);
                }
              }
            }}
            placeholder={t('chat.placeholder')}
            rows={1}
            style={{
              flex: 1, background: 'transparent', border: 'none',
              color: 'var(--text-primary)', resize: 'none',
              outline: 'none', padding: '6px 4px', maxHeight: 200,
              lineHeight: 1.5,
            }}
          />
          {isStreaming ? (
            <button
              onClick={handleStop}
              title={t('chat.stop') || '停止'}
              style={{
                background: 'var(--error)', border: 'none', color: '#fff',
                padding: '6px 10px', borderRadius: 8, cursor: 'pointer',
                display: 'flex', alignItems: 'center',
              }}
            >
              <Square size={16} />
            </button>
          ) : (
            <button
              onClick={handleSend}
              disabled={!input.trim()}
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
          )}
        </div>
        {/* Mode row — iCode's own mode selector */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 4, marginTop: 6,
          fontSize: 10, color: 'var(--text-muted)',
        }}>
          <button className="interactive" style={{
            padding: '3px 8px', borderRadius: 4, fontSize: 10,
            background: 'transparent', border: 'none', color: 'var(--text-muted)',
            display: 'flex', alignItems: 'center', gap: 3,
          }}>
            <Plus size={10} />
          </button>
          {[
            { v: 'plan', label: '规划' },
            { v: 'auto', label: '常规' },
            { v: 'ask',  label: '询问' },
            { v: 'yolo', label: 'Yolo' },
          ].map(m => (
            <button key={m.v} className={mode === m.v ? 'nav-item active' : 'nav-item'} style={{
              padding: '3px 10px', borderRadius: 4, fontSize: 10, fontWeight: mode === m.v ? 600 : 400,
            }} onClick={() => useAppStore.getState().setMode(m.v)}>{m.label}</button>
          ))}
          <div style={{ flex: 1 }} />
          <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>
            {currentModel?.name || selectedModel} · {mode}
          </span>
        </div>
      </div>

      {/* Permission request modal — the engine is paused server-side until the
          user answers. Respond via the backend /api/permission/respond call. */}
      {pendingPermission && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.55)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
        }}>
          <div style={{
            width: 440, maxWidth: '90vw', background: 'var(--bg-secondary)',
            border: '1px solid var(--border-color)', borderRadius: 12,
            padding: 20, boxShadow: '0 20px 60px rgba(0,0,0,0.45)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
              <ShieldAlert size={20} style={{ color: 'var(--warning)' }} />
              <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>
                {t('permission.title') || '权限确认'}
              </div>
            </div>
            <div style={{
              fontSize: 13, color: 'var(--text-secondary)', lineHeight: 1.7,
              background: 'var(--bg-primary)', borderRadius: 8, padding: '10px 12px',
              marginBottom: 16, wordBreak: 'break-word',
            }}>
              <div style={{ color: 'var(--text-muted)', fontSize: 11, marginBottom: 4 }}>
                {t('permission.tool') || '工具'}: <span style={{ color: 'var(--accent)' }}>{pendingPermission.tool}</span>
              </div>
              <div>{pendingPermission.prompt || (t('permission.confirm') || '是否允许该操作执行？')}</div>
            </div>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button
                onClick={() => respondPermission(pendingPermission.request_id, 'deny')}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
                  color: 'var(--text-secondary)',
                }}
              >
                {t('permission.deny') || '拒绝'}
              </button>
              <button
                onClick={() => respondPermission(pendingPermission.request_id, 'allow_all')}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
                  color: 'var(--text-secondary)',
                }}
              >
                {t('permission.allowAll') || '本会话全部允许'}
              </button>
              <button
                onClick={async () => {
                  // Allow this tool AND remember for this session
                  const sid = (pendingPermission as any)?.sid || activeSessionId;
                  if (backendUrl && sid) {
                    await fetch(`${backendUrl}/api/permission/allow-tool`, {
                      method: 'POST',
                      headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify({ session_id: sid, tool: pendingPermission.tool }),
                    }).catch(() => {});
                  }
                  respondPermission(pendingPermission.request_id, 'allow');
                }}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: '1px solid var(--accent)', background: 'var(--accent-soft)',
                  color: 'var(--accent)', fontWeight: 500,
                }}
              >
                始终允许此工具
              </button>
              <button
                onClick={() => respondPermission(pendingPermission.request_id, 'allow')}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: 'none', background: 'var(--accent)', color: '#000', fontWeight: 500,
                }}
              >
                {t('permission.allow') || '允许'}
              </button>
            </div>
          </div>
        </div>
      )}

      {palette.open && <CommandPalette onClose={() => palette.setOpen(false)} />}
      <FilePicker visible={pickerOpen} onClose={() => setPickerOpen(false)} onSelect={handleFileSelect} />
      <TokenBar />
    </div>
  );
};

/**
 * streamChat — POST /api/chat, read SSE stream, fire onEvent per message.
 * Includes 120s timeout, empty-stream guard, and AbortError handling.
 */
async function streamChat(
  baseUrl: string,
  payload: Record<string, any>,
  onEvent: (e: any) => void,
  abortRef?: React.MutableRefObject<AbortController | null>,
) {
  const ctrl = new AbortController();
  if (abortRef) abortRef.current = ctrl;
  const t = setTimeout(() => ctrl.abort(), 120_000);

  try {
    const res = await fetch(`${baseUrl}/api/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: ctrl.signal,
    });

    if (!res.ok || !res.body) {
      const text = await res.text().catch(() => '');
      onEvent({ type: 'error', content: text || `HTTP ${res.status}` });
      return;
    }

    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    let fired = false;

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split('\n');
      buf = lines.pop() || '';
      for (const line of lines) {
        const s = line.trim();
        if (!s.startsWith('data:')) continue;
        const json = s.slice(5).trim();
        if (!json) continue;
        try { fired = true; onEvent(JSON.parse(json)); } catch {}
      }
    }

    if (!fired) {
      onEvent({ type: 'error', content: '后端无响应 — 请在设置中配置 API Key（Ctrl+,）' });
    }
  } catch (e: any) {
    if (e?.name === 'AbortError') {
      onEvent({ type: 'error', content: '请求超时（120s）— 模型响应时间过长' });
    } else {
      onEvent({ type: 'error', content: e?.message || String(e) });
    }
  } finally {
    clearTimeout(t);
    if (abortRef) abortRef.current = null;
  }
}

function estimateCost(usage: any, model: any): string {
  if (!usage || (!usage.PromptTokens && !usage.prompt_tokens && !usage.TotalTokens && !usage.total_tokens))
    return '\xA50.00';
  const input = usage.PromptTokens || usage.prompt_tokens || 0;
  const output = usage.CompletionTokens || usage.completion_tokens || 0;
  // Use model's actual pricing if available
  const plans = model?.plans || model?.Plans || [];
  const plan = plans.find((p: any) => p.type === 'token' || (p.name && /token|coding/i.test(p.name)));
  if (plan?.cost) {
    const ip = plan.cost.input || 0;
    const op = plan.cost.output || 0;
    return '\xA5' + ((input * ip + output * op) / 1000).toFixed(4);
  }
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

function ActionBtn({ icon, label, title, onClick }: { icon: string; label: string; title: string; onClick: () => void }) {
  const [done, setDone] = useState(false);
  return (
    <button title={title} onClick={() => { onClick(); setDone(true); setTimeout(() => setDone(false), 1500); }}
      style={{
        background: 'transparent', border: '1px solid var(--border-color)',
        borderRadius: 4, cursor: 'pointer', padding: '2px 8px',
        fontSize: 11, color: done ? 'var(--success)' : 'var(--text-muted)',
        display: 'flex', alignItems: 'center', gap: 3,
      }}>
      {done ? '✓' : icon} {done ? '已复制' : label}
    </button>
  );
}

export default ChatPage;
