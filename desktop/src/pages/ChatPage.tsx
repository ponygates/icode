import React, { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../i18n';
import { useAppStore, Message, Attachment, type Model } from '../stores/appStore';
import { Send, Plus, Trash2, MessageSquare, Cpu, Shield, Square, ShieldAlert, GitBranch, FileText, RefreshCw, Folder, Edit3, Download, FileJson, Upload, ChevronDown } from 'lucide-react';
import Markdown from '../components/Markdown';
import CommandPalette, { useCommandPalette } from '../components/CommandPalette';
import TodoPanel from '../components/TodoPanel';
import TokenBar from '../components/TokenBar';
import TabBar from '../components/TabBar';
import CheckpointPanel from '../components/CheckpointPanel';
import FilePicker from '../components/FilePicker';
import ModelPicker from '../components/ModelPicker';
import PlumBlossom from '../components/PlumBlossom';
import { executeSlash, filterSlash, type SlashCommand } from '../lib/slashCommands';

// Shorten a path to its last 2 segments for display.
function shortDir(p: string): string {
  if (!p) return '';
  const parts = p.replace(/\\/g, '/').split('/').filter(Boolean);
  if (parts.length <= 2) return parts.join('/');
  return parts.slice(-2).join('/');
}

// A permission prompt surfaced from the engine's tool gate while a session is
// blocked waiting on the user's decision.
interface PermissionRequest {
  request_id: string;
  tool: string;
  prompt?: string;
  sid?: string;
}

// Token usage as reported by the backend (snake_case) or a provider (PascalCase).
interface UsageInfo {
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_hit_tokens?: number;
  PromptTokens?: number;
  CompletionTokens?: number;
  TotalTokens?: number;
  total_tokens?: number;
}

interface CostParts { input: number; output: number; }

interface PlanInfo {
  type?: string;
  name?: string;
  cost?: CostParts;
}

// A single server-sent event from /api/chat's SSE stream.
type ChatEvent =
  | { type: 'text'; content: string }
  | { type: 'thinking'; content: string }
  | { type: 'system'; content: string }
  | { type: 'tool_use'; tool_call?: { name: string; arguments?: string }; ToolCall?: { Name: string; Arguments?: string } }
  | { type: 'permission'; permission?: PermissionRequest; Permission?: PermissionRequest }
  | { type: 'plan_proposal' }
  | { type: 'done'; meta?: { usage?: UsageInfo } }
  | { type: 'error'; content: string };

// Request body for POST /api/chat (and the slash-command re-entry path).
interface ChatPayload {
  session_id: string;
  content: string;
  model: string;
  provider: string;
  attachments?: Array<{ type: string; mime: string; data: string }>;
}

// Fields worth surfacing in a tool-call chip — keeps the bubble informative
// without dumping the full (often huge) JSON argument payload into the stream.
const TOOL_ARG_FIELDS = ['path', 'command', 'pattern', 'query', 'file', 'directory', 'url', 'name', 'content'];

function summarizeToolArgs(raw?: string): string {
  if (!raw) return '';
  try {
    const obj = typeof raw === 'string' ? JSON.parse(raw) : raw;
    const parts: string[] = [];
    for (const k of TOOL_ARG_FIELDS) {
      const v = obj?.[k];
      if (v == null) continue;
      const s = String(v).replace(/\s+/g, ' ').trim();
      parts.push(`${k}=${s.length > 60 ? s.slice(0, 57) + '…' : s}`);
    }
    return parts.length ? ' ' + parts.join(' ') : '';
  } catch {
    return '';
  }
}

// Renders inline multimodal attachments (image thumbnails / file chips) inside a chat bubble.
function AttachmentView({ items, onZoom }: { items: Attachment[]; onZoom: (src: string) => void }) {  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
      {items.map((a, i) => {
        const isImage = a.type === 'image' || (a.mime || '').startsWith('image/');
        const src = a.data ? `data:${a.mime || 'image/png'};base64,${a.data}` : a.url;
        if (!src) return null;
        if (isImage) {
          return (
            <img
              key={i}
              src={src}
              alt={a.alt_text || 'image'}
              onClick={() => onZoom(src)}
              style={{
                maxWidth: 240, maxHeight: 240, borderRadius: 8, cursor: 'pointer',
                border: '1px solid var(--border)', objectFit: 'cover',
              }}
            />
          );
        }
        return (
          <a
            key={i}
            href={src}
            download={a.alt_text || 'file'}
            style={{
              display: 'inline-flex', alignItems: 'center', gap: 6, padding: '6px 10px',
              borderRadius: 8, border: '1px solid var(--border)', color: 'var(--text-primary)',
              textDecoration: 'none', fontSize: 12,
            }}
          >
            📎 {a.alt_text || a.mime || 'file'}
          </a>
        );
      })}
    </div>
  );
}

// Memoized message list. Isolates message rendering from ChatPage's local
// state (typing) and from unrelated store updates (backend health pings,
// token usage, …) so the list only re-renders when messages actually change.
const MessageList = React.memo(({ messages, isStreaming, onRegenerate, onZoom }: {
  messages: Message[];
  isStreaming: boolean;
  onRegenerate: (id: string) => void;
  onZoom: (src: string) => void;
}) => {
  const { t } = useTranslation();
  return (
    <>
      {messages.map((msg, idx) => {
        // Only the final assistant message is "streaming" — passing this down
        // lets Markdown skip expensive highlightAuto on every token frame and
        // do it once on the final render instead.
        const isLast = idx === messages.length - 1;
        const msgStreaming = isStreaming && isLast && msg.role === 'assistant';
        return (
        <div
          key={msg.id}
          style={{
            display: 'flex', gap: 10, padding: '6px 24px',
            justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
            alignItems: 'flex-start',
            // content-visibility: auto lets the browser skip rendering work
            // for messages outside the viewport — the cheapest form of list
            // virtualisation, with zero scroll/height regressions (unlike a
            // full virtualiser with dynamic heights). contain-intrinsic-size
            // gives the browser a height hint so the scrollbar stays stable
            // before a row scrolls into view and is measured.
            contentVisibility: 'auto',
            containIntrinsicSize: 'auto 140px',
          }}
        >
          {msg.role === 'assistant' && (
            <div className="grad-avatar" style={{
              width: 30, height: 30, borderRadius: '50%',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 13, fontWeight: 600, flexShrink: 0,
              boxShadow: '0 2px 8px rgba(88,166,255,0.25)',
            }}>i</div>
          )}
          <div className={msg.role === 'assistant' ? 'msg-bubble' : 'msg-bubble msg-user'} style={{
            maxWidth: '75%', padding: '12px 16px',
            color: 'var(--text-primary)', fontSize: 13,
            lineHeight: 1.7, wordBreak: 'break-word',
            position: 'relative',
          }}>
            {msg.role === 'assistant' ? (
              msg.content ? (
                msg.content.startsWith('[Thinking]') ? (
                  <details style={{ fontSize: 12, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
                    <summary style={{ cursor: 'pointer', color: 'var(--accent)', fontWeight: 500 }}>
                      🧠 {t('chat.thinking')}
                    </summary>
                    <pre style={{ whiteSpace: 'pre-wrap', margin: '4px 0 0', color: 'var(--text-muted)' }}>
                      {msg.content.slice(msg.content.indexOf('\n') + 1)}
                    </pre>
                  </details>
                ) : msg.content.startsWith('[Tool:') ? (
                  (() => {
                    const tm = msg.content.match(/^\[Tool: ([^\]]+)\]/);
                    const name = tm?.[1] || t('chat.toolCall');
                    const afterHeader = msg.content.slice(tm?.[0].length || 0);
                    const nl = afterHeader.indexOf('\n');
                    const detail = (nl < 0 ? afterHeader : afterHeader.slice(0, nl)).trim();
                    const output = nl < 0 ? '' : afterHeader.slice(nl + 1);
                    return (
                      <div style={{ fontSize: 12, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
                        <div style={{ color: 'var(--accent)', fontWeight: 500, marginBottom: 2 }}>
                          ⏺ {name}
                        </div>
                        {detail && (
                          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 2, wordBreak: 'break-all' }}>
                            {detail}
                          </div>
                        )}
                        {output && (
                          <pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{output}</pre>
                        )}
                      </div>
                    );
                  })()
                ) : (
                  <>
                    <Markdown text={msg.content} streaming={msgStreaming} />
                    {/* Action buttons — hidden until bubble hover */}
                    <div className="action-hidden" style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                      <ActionBtn icon="📋" label={t('chat.copy')} title={t('chat.copyTitle')}
                        onClick={() => navigator.clipboard.writeText(msg.content)} />
                      <ActionBtn icon="🔄" label={t('chat.regenerate')} title={t('chat.regenerateTitle')}
                        onClick={() => onRegenerate(msg.id)} />
                    </div>
                  </>
                )
              ) : (isStreaming ? (
                <span style={{ color: 'var(--text-muted)' }}>
                  {t('chat.generatingShort')}<span style={{ animation: 'pulse 1.5s infinite' }}>...</span>
                </span>
              ) : '')
            ) : (
              <span style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</span>
            )}
            {msg.attachments && msg.attachments.length > 0 && (
              <AttachmentView items={msg.attachments} onZoom={onZoom} />
            )}
          </div>
          {msg.role === 'user' && (
            <div className="grad-avatar" style={{
              width: 30, height: 30, borderRadius: '50%',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 13, fontWeight: 600, flexShrink: 0,
              boxShadow: '0 2px 8px rgba(210,153,29,0.3)',
            }}>U</div>
          )}
        </div>
        );
      })}
    </>
  );
});

const ChatPage: React.FC = () => {
  const palette = useCommandPalette();
  const { t } = useTranslation();
  const [input, setInput] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  // Set when a plan-mode turn finished and the plan awaits confirmation.
  const [planPending, setPlanPending] = useState(false);
  // Interactive permission request pending an answer from the user. When set,
  // the conversation engine is blocked server-side until we respond.
  const [pendingPermission, setPendingPermission] = useState<PermissionRequest | null>(null);
  // Zoomed image attachment (lightbox)
  const [lightbox, setLightbox] = useState<string | null>(null);
  const [attachedImages, setAttachedImages] = useState<{ mime: string; data: string }[]>([]);
  const [gitBranch, setGitBranch] = useState('');
  // Session runtime is measured from the session's createdAt; tick once a second
  // so the "runtime" card stays live without re-rendering the message list.
  const [now, setNow] = useState(() => Date.now());
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // Per-session input history for ↑/↓ browsing (mirrors the TUI). The draft
  // holds whatever the user typed before starting to browse so ↓ can restore it.
  const historyRef = useRef<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState(-1);
  const draftRef = useRef('');
  // Slash-command autocomplete (visible while the input starts with "/" and no
  // args have been typed yet). Selected index for ↑/↓/Tab navigation.
  const [slashSel, setSlashSel] = useState(0);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const stickToBottomRef = useRef(true);
  const scrollRafRef = useRef<number | null>(null);

  // Resolve the local backend URL once so we can stream chat directly from the
  // renderer (bypassing the fragile Electron IPC+SSE bridge). Falls back to the
  // IPC bridge when the backend URL is unavailable.
  useEffect(() => {
    checkBackend();
  }, []);

  // Precise selectors: subscribe only to the slices this component needs, so
  // unrelated store updates (e.g. settings changes, tokenUsage ticks on other
  // pages) don't re-render the whole ChatPage. Actions are stable refs.
  const sessions = useAppStore(s => s.sessions);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const selectedModel = useAppStore(s => s.selectedModel);
  const models = useAppStore(s => s.models);
  const tokenUsage = useAppStore(s => s.tokenUsage);
  const backendUrl = useAppStore(s => s.backendUrl);
  const openTabIds = useAppStore(s => s.openTabIds);
  const activeWorkspaceId = useAppStore(s => s.activeWorkspaceId);
  const workspaces = useAppStore(s => s.workspaces);
  // Individual selectors (not a fresh object literal) so zustand's Object.is
  // comparison keeps unrelated store updates (e.g. tokenUsage ticks) from
  // re-rendering this component on every frame.
  const createSession = useAppStore(s => s.createSession);
  const setActiveSession = useAppStore(s => s.setActiveSession);
  const deleteSession = useAppStore(s => s.deleteSession);
  const closeTab = useAppStore(s => s.closeTab);
  const addMessage = useAppStore(s => s.addMessage);
  const updateMessage = useAppStore(s => s.updateMessage);
  const clearMessages = useAppStore(s => s.clearMessages);
  const updateTokenUsage = useAppStore(s => s.updateTokenUsage);
  const checkBackend = useAppStore(s => s.checkBackend);
  const setBackendUrl = useAppStore(s => s.setBackendUrl);
  const setSelectedModel = useAppStore(s => s.setSelectedModel);
  const setMode = useAppStore(s => s.setMode);
  const refreshModels = useAppStore(s => s.refreshModels);

  const abortRef = useRef<AbortController | null>(null);
  const activeSession = sessions.find((s) => s.id === activeSessionId);

  // Latest handleSend, kept in a ref so handleRegenerate can trigger a resend
  // without depending on `input` (which would re-render the memoized list on
  // every keystroke). Assigned right after handleSend is defined below.
  const handleSendRef = useRef<(() => Promise<void>) | null>(null);

  // Stable handler so the memoized <MessageList> doesn't re-render on every
  // keystroke or unrelated store update. Truncates the session at the user
  // message preceding the assistant turn, then re-sends it (true regenerate,
  // not just a prefill).
  const handleRegenerate = useCallback((id: string) => {
    const msgs = activeSession?.messages || [];
    const idx = msgs.findIndex((m) => m.id === id);
    if (idx <= 0) return;
    const userMsg = msgs[idx - 1];
    if (!userMsg || userMsg.role !== 'user') return;
    const sid = activeSessionId;
    if (!sid) return;
    // Keep only what precedes this user message — it will be re-sent.
    const keep = msgs.slice(0, idx - 1);
    useAppStore.setState(prev => ({
      sessions: prev.sessions.map(s => s.id === sid ? { ...s, messages: keep } : s),
    }));
    setInput(userMsg.content);
    // Let the store update flush, then send (same pattern as the toolbar buttons).
    setTimeout(() => handleSendRef.current?.(), 60);
  }, [activeSession, activeSessionId, setInput]);

  // Multi-tab state lives in the store (openTabIds) so it survives route
  // changes and restarts. Filter the open tabs by the active workspace for
  // deep session↔workspace binding: switching workspace switches the strip.
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId);
  const visibleTabIds = activeWs
    ? activeWs.session_ids.length > 0
      ? activeWs.session_ids.filter((id) => openTabIds.includes(id))
      : openTabIds
    : openTabIds;
  const visibleTabs = visibleTabIds
    .map((id) => {
      const s = sessions.find((x) => x.id === id);
      return { id, title: s?.title || t('chat.session') };
    })
    .filter((tab) => tab.id);

  // Derived: files the agent has touched, recomputed only when messages change
  // (was an inline IIFE re-running every render → expensive during streaming).
  const fileActions = useMemo(() => {
    const files = new Map<string, string>();
    const patterns = [
      /read_file\b.*?["'`]([^"'`\n]+?)["'`]/g,
      /write_file\b.*?["'`]([^"'`\n]+?)["'`]/g,
      /\b(?:edit|bash)\b.*?["'`]([^"'`\n]+?)["'`]/g,
      /\b([\w\/\\\.\-]+\.(?:tsx?|jsx?|go|py|rs|java|rb|html|css|json|yaml|yml|md|toml))\b/g,
    ];
    const scan = (content: string) => {
      // Cheap pre-filter so we don't run 4 regexes over every (often huge)
      // message on every streaming chunk: skip anything that can't reference a
      // file path.
      if (!/[.]\w{1,6}$|read_file|write_file|\bedit\b|\bbash\b/.test(content)) return;
      const capped = content.length > 20000 ? content.slice(0, 20000) : content;
      patterns.forEach((re) => {
        let m: RegExpExecArray | null;
        while ((m = re.exec(capped)) !== null) {
          const path = m[1].replace(/\\/g, '/');
          if (path.length > 2 && !path.startsWith('http') && !files.has(path)) {
            const action = capped.includes('write_file') ? 'write'
              : capped.includes('edit') ? 'edit' : 'read';
            files.set(path, action);
          }
        }
      });
    };
    (activeSession?.messages || []).forEach((msg) => { if (msg.content) scan(msg.content); });
    return Array.from(files.entries());
    // Memoize on session id + message COUNT, not the whole messages array:
    // during streaming the array reference changes every chunk, so keying on
    // it would re-scan every message on every token. File chips only change
    // when messages are added/removed (tool calls), not as text streams in.
  }, [activeSession?.id, activeSession?.messages?.length]);

  const handleTabSelect = (id: string) => {
    setActiveSession(id);
  };

  const handleTabClose = (id: string) => {
    closeTab(id);
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
    if (!stickToBottomRef.current) return;
    const el = messagesEndRef.current;
    if (!el) return;
    if (scrollRafRef.current == null) {
      scrollRafRef.current = requestAnimationFrame(() => {
        scrollRafRef.current = null;
        el.scrollIntoView({ behavior: 'auto' });
      });
    }
  }, [activeSession?.messages]);

  // Keep the runtime card ticking while a session is open (1 Hz, resets the
  // baseline whenever the active session switches).
  useEffect(() => {
    setNow(Date.now());
    if (!activeSessionId) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [activeSessionId]);

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

  const handleSend = useCallback(async (override?: string) => {
    const text = (override ?? input).trim();
    if (!text || isStreaming) return;

    // Record the submitted text in the input history (↑/↓ browser).
    historyRef.current.push(text);
    if (historyRef.current.length > 200) historyRef.current.shift();
    setHistoryIdx(-1);

    // Handle # memory append (like TUI)
    if (text.startsWith('#')) {
      let memoryText = text.slice(1).trim();
      // `#user: ...` targets the cross-project memory file (~/.icode);
      // plain `#` targets the project ICODE.md — mirrors the TUI shortcut.
      let scope = 'project';
      if (/^user\s*:/i.test(memoryText)) {
        scope = 'user';
        memoryText = memoryText.replace(/^user\s*:/i, '').trim();
      }
      if (memoryText && backendUrl) {
        try {
          await fetch(`${backendUrl}/api/memory`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ text: memoryText, scope }),
          });
          addMessage(activeSessionId || '', {
            id: Date.now().toString(36),
            role: 'system',
            content: `📝 ${t('chat.copied')}: ${memoryText}`,
            timestamp: Date.now(),
          });
        } catch {}
      }
      setInput('');
      return;
    }

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

    const userMsg: Message = {
      id: Date.now().toString(36),
      role: 'user', content: text, timestamp: Date.now(),
      attachments: attachedImages.length > 0
        ? attachedImages.map(img => ({ type: 'image', mime: img.mime, data: img.data }))
        : undefined,
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
    let rafId: number | null = null;
    // Thinking deltas arrive first and are folded into a collapsible box; they
    // are shown but never persisted at full length or sent back to the model
    // (iCode keeps them out of the paid context — a token saver, not a leak).
    let thinkingBuf = '';

    // Coalesce token updates into at most one store write per animation frame.
    // This prevents the whole message list + sidebar from re-rendering on every
    // single streamed token (the main source of the "laggy" feel).
    const flushNow = () => {
      if (rafId != null) { cancelAnimationFrame(rafId); rafId = null; }
      updateMessage(sid, { ...assistantMsg, content: accumulated });
    };
    const scheduleFlush = () => {
      if (rafId == null) {
        rafId = requestAnimationFrame(() => { rafId = null; flushNow(); });
      }
    };

    const onEvent = (event: ChatEvent) => {
      if (settled) return;
      const ty = event?.type;
      if (ty === 'thinking') {
        thinkingBuf += event.content || '';
        if (thinkingBuf.length > 3000) thinkingBuf = thinkingBuf.slice(0, 3000);
        return;
      }
      if (ty === 'system') {
        // Engine notice (e.g. budget guard) — standalone system message,
        // never folded into the assistant reply.
        addMessage(sid, {
          id: (Date.now() + 1).toString(36) + 's',
          role: 'system', content: event.content || '', timestamp: Date.now(),
        });
        return;
      }
      if (ty === 'plan_proposal') {
        // Plan-mode turn finished — arm the confirmation bar so the user can
        // accept the plan (switch to auto and start executing) or discard it.
        setPlanPending(true);
        return;
      }
      if (ty === 'text') {
        if (thinkingBuf) {
          accumulated += '\n[Thinking]\n' + thinkingBuf + '\n';
          thinkingBuf = '';
        }
        accumulated += event.content || '';
        scheduleFlush();
      } else if (ty === 'tool_use') {
        const name = event.tool_call?.name || event.ToolCall?.Name || 'tool';
        const argsRaw = event.tool_call?.arguments || event.ToolCall?.Arguments;
        accumulated += `\n[Tool: ${name}]${summarizeToolArgs(argsRaw)}\n`;
        scheduleFlush();
      } else if (ty === 'permission') {
        const req = event.permission || event.Permission;
        if (req?.request_id) setPendingPermission(req);
      } else if (ty === 'done') {
        settled = true;
        if (thinkingBuf) {
          accumulated += '\n[Thinking]\n' + thinkingBuf + '\n';
          thinkingBuf = '';
        }
        flushNow();
        setIsStreaming(false);
        setPendingPermission(null);
        const u = event.meta?.usage;
        updateTokenUsage({
          input: u?.prompt_tokens || u?.PromptTokens || 0,
          output: u?.completion_tokens || u?.CompletionTokens || 0,
          cacheHit: u?.cache_hit_tokens || 0,
          cost: estimateCost(u, currentModel),
        });
      } else if (ty === 'error') {
        settled = true;
        accumulated += '\n❌ ' + (event.content || t('chat.unknownError'));
        flushNow();
        setIsStreaming(false);
        setPendingPermission(null);
      }
    };

    // `! <shell>` — run a shell command and surface the output as a tool block
    // (zero tokens: the result is shown but never auto-fed to the model).
    if (text.startsWith('!')) {
      const cmd = text.slice(1).trim();
      setInput('');
      if (!cmd || !url || !sid) { onEvent({ type: 'error', content: t('chat.backendError') }); return; }
      const toolMsg: Message = {
        id: (Date.now() + 1).toString(36),
        role: 'assistant', content: `[Tool: bash]\n$ ${cmd}`, timestamp: Date.now(),
      };
      addMessage(sid, toolMsg);
      try {
        const res = await fetch(`${url}/api/shell`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ cmd }),
        });
        const data = await res.json();
        let detail = (data?.output || '(无输出)');
        if (data?.error) detail += `\n❌ ${data.error}`;
        if (data?.exit_code && data.exit_code !== 0) detail += `\n[exit ${data.exit_code}]`;
        updateMessage(sid, { ...toolMsg, content: `[Tool: bash]\n$ ${cmd}\n${detail}` });
      } catch (e) {
        updateMessage(sid, { ...toolMsg, content: `[Tool: bash]\n$ ${cmd}\n❌ 执行失败: ${String(e)}` });
      }
      return;
    }

    // Client-side slash first (local commands + natural-language rewrites).
    // Unknown commands fall through to the backend slash layer below — the two
    // vocabularies are intentionally disjoint, not duplicated.
    if (text.startsWith('/') && url && sid) {
      const outcome = await executeSlash(text);
      if (outcome.type === 'rewrite') {
        const payload: ChatPayload = { session_id: sid, content: outcome.text, model, provider };
        if (attachedImages.length > 0) {
          payload.attachments = attachedImages.map(img => ({ type: 'image', mime: img.mime, data: img.data }));
          setAttachedImages([]);
        }
        await streamChat(url, payload, onEvent, abortRef);
        setInput('');
        return;
      }
      if (outcome.type === 'handled') {
        setInput('');
        return;
      }
      // passthrough → backend /api/slash below
    }

    // CLI-parity slash commands — routed to /api/slash (the same vocabulary
    // as the TUI, implemented once server-side in internal/core/slashui).
    if (text.startsWith('/') && url && sid) {
      try {
        const res = await fetch(`${url}/api/slash`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            text,
            session_id: sid,
            model,
            provider,
            mode,
          }),
        });
        if (res.ok) {
          const data = await res.json();
          if (data?.new_session) {
            createSession(selectedModel, currentModel?.provider || 'openrouter');
          } else if (data?.clear_session) {
            clearMessages(sid);
          }
          if (data?.model && data.model !== selectedModel) setSelectedModel(data.model);
          if (data?.provider) {
            setSelectedModel(`${data.provider}/${currentModel?.id?.split('/')[1] || model.split('/')[1] || 'free'}`);
          }
          if (data?.mode && data.mode !== mode) setMode(data.mode);

          if (data?.chat) {
            // Command expands into a model turn (e.g. /review) — stream it.
            const payload: ChatPayload = { session_id: sid, content: data.content || text, model, provider };
            if (attachedImages.length > 0) {
              payload.attachments = attachedImages.map(img => ({ type: 'image', mime: img.mime, data: img.data }));
              setAttachedImages([]);
            }
            await streamChat(url, payload, onEvent, abortRef);
          } else {
            const out = data?.output || '';
            updateMessage(sid, { ...assistantMsg, content: out || '(no output)' });
            setIsStreaming(false);
          }
          if (!settled) { settled = true; }
          setInput('');
          return;
        }
      } catch { /* fall through to normal chat if slash endpoint unavailable */ }
    }

    if (url) {
      const payload: ChatPayload = { session_id: sid, content: text, model, provider };
      if (attachedImages.length > 0) {
        payload.attachments = attachedImages.map(img => ({ type: 'image', mime: img.mime, data: img.data }));
        setAttachedImages([]);
      }
      console.log('[iCode] sending to', url, 'model:', model, 'provider:', provider);
      await streamChat(url, payload, onEvent, abortRef);
    } else {
      onEvent({ type: 'error', content: t('chat.backendError') });
    }

    if (!settled) { settled = true; setIsStreaming(false); }
  }, [input, activeSessionId, isStreaming, selectedModel, currentModel, attachedImages]);
  handleSendRef.current = handleSend;

  // Accept a plan-mode proposal: leave read-only plan mode and start executing
  // the plan in the continuation turn.
  const confirmPlan = useCallback(() => {
    setPlanPending(false);
    useAppStore.getState().setMode('auto');
    handleSend('计划已确认。请按上述计划立即开始执行，不要再重复或重新规划，直接动手。');
  }, [handleSend]);

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
      window.icode.stopChat(activeSessionId).catch(() => {});
    } else if (backendUrl) {
      fetch(`${backendUrl}/api/chat/stop`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: activeSessionId }),
      }).catch(() => {});
    }
  }, [activeSessionId, backendUrl]);

  // Global shortcuts dispatched from App.tsx: Ctrl+N / Ctrl+L / Esc.
  useEffect(() => {
    const newSession = () => createSession(selectedModel, currentModel?.provider || 'openrouter');
    const focusInput = () => { inputRef.current?.focus(); };
    const stopChat = () => { if (isStreaming) handleStop(); };
    window.addEventListener('icode:new-session', newSession);
    window.addEventListener('icode:focus-input', focusInput);
    window.addEventListener('icode:stop-chat', stopChat);
    return () => {
      window.removeEventListener('icode:new-session', newSession);
      window.removeEventListener('icode:focus-input', focusInput);
      window.removeEventListener('icode:stop-chat', stopChat);
    };
  }, [createSession, selectedModel, currentModel, isStreaming, handleStop]);

  // File picker state - triggered by @ in input
  const [pickerOpen, setPickerOpen] = useState(false);
  // Model picker popover - triggered by clicking the model name in the header.
  const [modelPickerOpen, setModelPickerOpen] = useState(false);

  // Slash autocomplete list — derived from the input, empty once args are typed.
  const slashMenu = useMemo(() => filterSlash(input), [input]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
      return;
    }
    // While the slash menu is open, ↑/↓ navigate it (instead of history) and
    // Tab accepts the highlighted command.
    if (slashMenu.length > 0) {
      if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
        e.preventDefault();
        setSlashSel((i) => (i + (e.key === 'ArrowDown' ? 1 : -1) + slashMenu.length) % slashMenu.length);
        return;
      }
      if (e.key === 'Tab') {
        e.preventDefault();
        setInput('/' + slashMenu[slashSel].name + ' ');
        setSlashSel(0);
        return;
      }
    }
    // Tab / Shift+Tab cycle the agent mode (Claude Code style): Tab moves to
    // the next mode, Shift+Tab to the previous. The slash-menu completion
    // above already claims Tab while a slash command is open.
    if (e.key === 'Tab' && !e.altKey) {
      e.preventDefault();
      const modes = ['plan', 'auto', 'ask', 'yolo'];
      const idx = modes.indexOf(mode);
      const next = modes[(idx + (e.shiftKey ? -1 : 1) + modes.length) % modes.length];
      useAppStore.getState().setMode(next);
      return;
    }
    // ↑/↓ browse the input history — only in single-line input, where the
    // arrows don't need to move the caret (multi-line keeps native behaviour).
    if (!e.shiftKey && !e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
      if (input.includes('\n')) return;
      const list = historyRef.current;
      if (e.key === 'ArrowUp') {
        if (list.length === 0) return;
        e.preventDefault();
        if (historyIdx === -1) draftRef.current = input;
        const next = Math.min(historyIdx + 1, list.length - 1);
        setHistoryIdx(next);
        setInput(list[list.length - 1 - next]);
        return;
      }
      if (historyIdx === -1) return;
      e.preventDefault();
      if (historyIdx === 0) {
        setHistoryIdx(-1);
        setInput(draftRef.current);
        return;
      }
      const next = historyIdx - 1;
      setHistoryIdx(next);
      setInput(list[list.length - 1 - next]);
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
    <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
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
              const title = prompt(t('chat.sessionTitle'), activeSession?.title || '');
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
          <button
            onClick={() => setModelPickerOpen(true)}
            className="interactive"
            title={t('shortcuts.switchModel', '切换模型')}
            style={{
              fontSize: 11, color: 'var(--text-muted)', fontWeight: 500,
              background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
              padding: '3px 10px', borderRadius: 'var(--r-full)', cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: 4,
            }}
          >
            {currentModel?.name || selectedModel}
            <ChevronDown size={10} />
          </button>
          <button className="interactive" title={t('shortcuts.refreshModels', '刷新模型列表')}
            onClick={async () => {
              const btn = document.getElementById('hdr-refresh-btn');
              if (btn) btn.style.opacity = '0.5';
              await refreshModels();
              if (btn) btn.style.opacity = '1';
            }}
            id="hdr-refresh-btn"
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-muted)', padding: '3px 6px', borderRadius: 6,
              display: 'flex', alignItems: 'center', fontSize: 11, cursor: 'pointer',
            }}>
            <RefreshCw size={12} />
          </button>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {/* Open folder */}
          <button className="interactive" title={t('chat.openDir')}
            onClick={() => {
              // Use the Electron shell API if available
              if (window.icode?.openFolder) {
                window.icode.openFolder('.').catch(() => {});
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
          <button className="interactive" title={t('chat.export')}
            onClick={() => {
              if (!activeSession?.messages?.length) return;
              const md = activeSession.messages.map(m =>
                `### ${m.role === 'user' ? '👤 ' + t('chat.user') : '🤖 iCode'}\n\n${m.content}\n`
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
          {/* Export session as JSON */}
          <button className="interactive" title={t('chat.exportJson')}
            onClick={async () => {
              if (!activeSessionId || !backendUrl) return;
              try {
                const res = await fetch(`${backendUrl}/api/sessions/${activeSessionId}`);
                if (!res.ok) return;
                const data = await res.json();
                const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = `icode-${data.title || 'session'}.json`;
                a.click();
                URL.revokeObjectURL(url);
              } catch { /* ignore */ }
            }}
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-muted)', padding: '4px 8px', borderRadius: 6,
              display: 'flex', alignItems: 'center', gap: 4, fontSize: 11,
            }}>
            <FileJson size={12} />
          </button>
          {/* Import session from JSON */}
          <button className="interactive" title={t('chat.import')}
            onClick={() => {
              const input = document.createElement('input');
              input.type = 'file';
              input.accept = '.json,application/json';
              input.onchange = async () => {
                const file = input.files?.[0];
                if (!file || !backendUrl) return;
                try {
                  const body = JSON.parse(await file.text());
                  const res = await fetch(`${backendUrl}/api/sessions/import`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(body),
                  });
                  const result = await res.json();
                  if (res.ok && result?.session?.id) {
                    await useAppStore.getState().loadSessions();
                    useAppStore.getState().setActiveSession(result.session.id);
                  } else {
                    alert(result?.error || t('chat.importFailed'));
                  }
                } catch {
                  alert(t('chat.importFailed'));
                }
              };
              input.click();
            }}
            style={{
              background: 'none', border: '0.5px solid var(--border-color)',
              color: 'var(--text-muted)', padding: '4px 8px', borderRadius: 6,
              display: 'flex', alignItems: 'center', gap: 4, fontSize: 11,
            }}>
            <Upload size={12} />
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
              title={t('chat.fork')}
              className="interactive"
              style={{
                background: 'none', border: '0.5px solid var(--border-color)',
                color: 'var(--text-secondary)', padding: '4px 10px',
                borderRadius: 6, display: 'flex',
                alignItems: 'center', gap: 4, fontSize: 12,
              }}>
              <GitBranch size={14} /> {t('chat.forkLabel')}
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
        tabs={visibleTabs}
        activeId={activeSessionId}
        onSelect={handleTabSelect}
        onClose={handleTabClose}
        onNew={handleTabNew}
      />

      {/* Middle: conversation + session-stats sidebar (Reasonix style) */}
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        {/* Messages */}
        <div
          ref={scrollContainerRef}
          onScroll={() => {
            const el = scrollContainerRef.current;
            if (!el) return;
            const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
            stickToBottomRef.current = distance <= 80;
          }}
          style={{
            flex: 1, overflowY: 'auto', padding: '24px 24px',
            display: 'flex', flexDirection: 'column', gap: 4,
          }}
        >
          {/* Welcome — Apple-style large title with plum blossom logo */}
          {activeSession?.messages.length === 0 && !isStreaming && (
            <div style={{
              flex: 1, display: 'flex', flexDirection: 'column',
              alignItems: 'center', justifyContent: 'center',
              padding: 60, color: 'var(--text-muted)',
            }}>
              {/* Plum blossom brand mark + iCode wordmark (Apple-style) */}
              <PlumBlossom
                size={76}
                style={{ marginBottom: 4, filter: 'drop-shadow(0 4px 12px rgba(230,111,168,0.28))' }}
              />
              <div style={{
                fontSize: 34, fontWeight: 700, letterSpacing: '-0.03em',
                color: 'var(--text-primary)', lineHeight: 1.1,
              }}>
                iCode
              </div>
              <div className="page-subtitle" style={{ fontSize: 15, marginBottom: 32, marginTop: 6 }}>
                {t('chat.yourPartner')}
              </div>
              <div style={{
                display: 'flex', gap: 10, flexWrap: 'wrap', justifyContent: 'center',
              }}>
                {[
                  { label: t('chat.promptReact'), icon: '⚛' },
                  { label: t('chat.promptExplain'), icon: '🔍' },
                  { label: t('chat.promptRefactor'), icon: '🔄' },
                  { label: t('chat.promptDebug'), icon: '🐛' },
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
                {t('chat.inputHint')}
              </div>
            </div>
          )}

          {/* Session list when no active session */}
          {!activeSessionId && sessions.length > 0 && (
            <div style={{ textAlign: 'center', padding: 40 }}>
              <h2 style={{ color: 'var(--text-secondary)', marginBottom: 16, fontSize: 15 }}>
                {t('chat.selectSession')}
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
          <MessageList
            messages={activeSession?.messages || []}
            isStreaming={isStreaming}
            onRegenerate={handleRegenerate}
            onZoom={setLightbox}
          />
          <div ref={messagesEndRef} />
        </div>

        {/* Lightbox — zoomed image attachment */}
        {lightbox && (
          <div
            onClick={() => setLightbox(null)}
            style={{
              position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.82)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              zIndex: 1000, cursor: 'zoom-out',
            }}
          >
            <img src={lightbox} style={{ maxWidth: '92vw', maxHeight: '92vh', borderRadius: 12, boxShadow: '0 8px 40px rgba(0,0,0,0.5)' }} />
          </div>
        )}

        {/* Right sidebar — iCode overview panels */}
        <div style={{
          width: 250, minWidth: 250, borderLeft: '0.5px solid var(--border-color)',
          background: 'var(--bg-secondary)', padding: '16px 14px', overflowY: 'auto',
          fontSize: 12, color: 'var(--text-secondary)', display: 'flex', flexDirection: 'column', gap: 14,
        }}>
          {/* Files touched by agent */}
          {fileActions.length > 0 && (
              <div className="card" style={{ padding: 12 }}>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 8, fontWeight: 500 }}>
                  {t('chat.fileActions')} · {fileActions.length}
                </div>
                {fileActions.map(([path, action]) => (
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
          )}

          {/* Card 1: Context Window */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)' }}>{t('chat.contextWindow')}</span>
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
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('chat.used')}</div>
                <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {(tokenUsage.input / 1000).toFixed(1)}K
                </div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)', marginTop: 4 }}>{t('chat.nearLimit200K')}</div>
              </div>
            </div>
          </div>

          {/* Card 2: Session Metrics */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10 }}>
              {t('chat.sessionMetrics')}
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{t('token.cacheHit')}</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--success)' }}>
                  {tokenUsage.cacheHit > 0
                    ? ((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(1) + '%'
                    : '0%'}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{t('chat.runTime')}</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {activeSession?.createdAt
                    ? Math.max(0, Math.floor((now - activeSession.createdAt) / 1000)) + 's'
                    : '0s'}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{t('chat.totalTokens')}</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {(tokenUsage.input + tokenUsage.output).toLocaleString()}
                </div>
              </div>
              <div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{t('chat.estCost')}</div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--accent)' }}>
                  {tokenUsage.cost}
                </div>
              </div>
            </div>
          </div>

          {/* Card 3: Usage Breakdown */}
          <div className="card" style={{ padding: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10 }}>
              {t('chat.usageBreakdown')}
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
              <div>● <span style={{ color: 'var(--accent)' }}>{t('token.input')}</span> {tokenUsage.input.toLocaleString()}</div>
              <div>● <span style={{ color: 'var(--success)' }}>{t('token.cacheHit')}</span> {tokenUsage.cacheHit.toLocaleString()}</div>
              <div>● <span style={{ color: 'var(--warning)' }}>{t('token.output')}</span> {tokenUsage.output.toLocaleString()}</div>
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
            <GitBranch size={12} /> {t('chat.forkSession')}
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
            <FileText size={12} /> {t('chat.summarize')}
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
            <RefreshCw size={12} /> {t('chat.review')}
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
        {planPending && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10,
            marginBottom: 10, padding: '8px 12px', borderRadius: 8,
            background: 'var(--accent-soft)', border: '1px solid var(--accent)',
          }}>
            <span style={{ fontSize: 12, color: 'var(--text-primary)', flex: 1 }}>
              📋 {t('chat.planReady', '计划已生成')} — {t('chat.planHint', '接受后开始执行')}
            </span>
            <button
              onClick={confirmPlan}
              style={{
                background: 'var(--grad-accent)', border: 'none', color: '#fff',
                padding: '4px 12px', borderRadius: 6, fontSize: 12, cursor: 'pointer',
              }}
            >
              {t('chat.planAccept', '接受并执行')}
            </button>
            <button
              onClick={() => setPlanPending(false)}
              style={{
                background: 'transparent', border: '1px solid var(--border-color)',
                color: 'var(--text-muted)', padding: '4px 10px', borderRadius: 6,
                fontSize: 12, cursor: 'pointer',
              }}
            >
              {t('chat.planDiscard', '放弃')}
            </button>
          </div>
        )}
        <div style={{
          display: 'flex', gap: 10, alignItems: 'flex-end',
          background: 'var(--bg-primary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: '6px 8px',
        }} className="composer">
          <div style={{ position: 'relative', flex: 1 }}>
            {slashMenu.length > 0 && (
              <div style={{
                position: 'absolute', bottom: '100%', left: 0, right: 0, marginBottom: 4,
                background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
                borderRadius: 8, boxShadow: '0 8px 24px rgba(0,0,0,0.25)', zIndex: 50,
                maxHeight: 240, overflowY: 'auto', padding: 4,
              }}>
                {slashMenu.map((c, i) => (
                  <div
                    key={c.name}
                    onMouseEnter={() => setSlashSel(i)}
                    onClick={() => { setInput('/' + c.name + ' '); setSlashSel(0); }}
                    style={{
                      padding: '5px 10px', borderRadius: 6, cursor: 'pointer',
                      background: i === slashSel ? 'var(--accent-soft)' : 'transparent',
                    }}
                  >
                    <span style={{ fontWeight: 600, fontSize: 12, color: 'var(--accent)' }}>/{c.name}</span>
                    {c.usage && <span style={{ color: 'var(--text-muted)', fontSize: 11 }}> {c.usage}</span>}
                    <span style={{ color: 'var(--text-muted)', fontSize: 11, marginLeft: 8 }}>{t(c.descKey)}</span>
                  </div>
                ))}
              </div>
            )}
          <textarea
            ref={inputRef}
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
              width: '100%', background: 'transparent', border: 'none',
              color: 'var(--text-primary)', resize: 'none',
              outline: 'none', padding: '6px 4px', maxHeight: 200,
              lineHeight: 1.5,
            }}
          />
          </div>
          {isStreaming ? (
            <button
              onClick={handleStop}
              title={t('chat.stop')}
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
              onClick={() => handleSend()}
              disabled={!input.trim()}
              title={t('chat.send')}
              style={{
                background: input.trim() ? 'var(--grad-accent)' : 'var(--border-color)',
                border: 'none', color: input.trim() ? '#fff' : 'var(--text-muted)',
                padding: '6px 10px', borderRadius: 8, cursor: input.trim() ? 'pointer' : 'default',
                display: 'flex', alignItems: 'center', transition: 'filter 0.15s, opacity 0.15s',
              }}
              onMouseEnter={(e) => { if (input.trim()) (e.currentTarget as HTMLElement).style.filter = 'brightness(1.1)'; }}
              onMouseLeave={(e) => { (e.currentTarget as HTMLElement).style.filter = ''; }}
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
            { v: 'plan', label: t('chat.modePlan') },
            { v: 'auto', label: t('chat.modeNormal') },
            { v: 'ask',  label: t('chat.modeAsk') },
            { v: 'yolo', label: t('chat.modeYolo') },
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
                {t('permission.title')}
              </div>
            </div>
            <div style={{
              fontSize: 13, color: 'var(--text-secondary)', lineHeight: 1.7,
              background: 'var(--bg-primary)', borderRadius: 8, padding: '10px 12px',
              marginBottom: 16, wordBreak: 'break-word',
            }}>
              <div style={{ color: 'var(--text-muted)', fontSize: 11, marginBottom: 4 }}>
                {t('permission.tool')}: <span style={{ color: 'var(--accent)' }}>{pendingPermission.tool}</span>
              </div>
              <div>{pendingPermission.prompt || t('permission.confirm')}</div>
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
                {t('permission.deny')}
              </button>
              <button
                onClick={() => respondPermission(pendingPermission.request_id, 'allow_all')}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
                  color: 'var(--text-secondary)',
                }}
              >
                {t('permission.allowAll')}
              </button>
              <button
                onClick={async () => {
                  // Allow this tool AND remember for this session
                  const sid = pendingPermission.sid || activeSessionId;
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
                {t('permission.allowToolAlways')}
              </button>
              <button
                onClick={() => respondPermission(pendingPermission.request_id, 'allow')}
                style={{
                  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                  border: 'none', background: 'var(--accent)', color: '#000', fontWeight: 500,
                }}
              >
                {t('permission.allow')}
              </button>
            </div>
          </div>
        </div>
      )}

      {palette.open && <CommandPalette onClose={() => palette.setOpen(false)} />}
      <FilePicker visible={pickerOpen} onClose={() => setPickerOpen(false)} onSelect={handleFileSelect} />
  <ModelPicker open={modelPickerOpen} onClose={() => setModelPickerOpen(false)} />
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
  payload: ChatPayload,
  onEvent: (e: ChatEvent) => void,
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
      onEvent({ type: 'error', content: i18n.t('chat.noResponse') });
    }
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      onEvent({ type: 'error', content: i18n.t('chat.timeoutError') });
    } else {
      onEvent({ type: 'error', content: e instanceof Error ? e.message : String(e) });
    }
  } finally {
    clearTimeout(t);
    if (abortRef) abortRef.current = null;
  }
}

function estimateCost(usage: UsageInfo | null | undefined, model: Model | null | undefined): string {
  if (!usage || (!usage.PromptTokens && !usage.prompt_tokens && !usage.TotalTokens && !usage.total_tokens))
    return '\xA50.00';
  const input = usage.PromptTokens || usage.prompt_tokens || 0;
  const output = usage.CompletionTokens || usage.completion_tokens || 0;
  // Use model's actual pricing if available
  const plans = (model?.plans as PlanInfo[] | undefined) ||
    (model as { Plans?: PlanInfo[] } | undefined)?.Plans ||
    [];
  const plan = plans.find((p) => p.type === 'token' || (p.name ? /token|coding/i.test(p.name) : false));
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
      title={i18n.t('chat.openSettings')}
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
      {done ? '✓' : icon} {done ? i18n.t('chat.copied') : label}
    </button>
  );
}

export default ChatPage;
