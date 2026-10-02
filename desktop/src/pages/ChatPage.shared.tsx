// Shared building blocks for ChatPage — types, pure helpers, and the
// presentational components that only depend on props. Split out of the
// former 2800-line ChatPage.tsx so the main file keeps just state + wiring.
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../i18n';
import { useAppStore, type Message, type Attachment } from '../stores/appStore';
import Markdown from '../components/Markdown';
import ChangedFilesBar from '../components/ChangedFilesBar';
import type { MessageWindow } from '../lib/messageWindow';

// A permission prompt surfaced from the engine's tool gate while a session is
// blocked waiting on the user's decision.
export interface PermissionRequest {
  request_id: string;
  tool: string;
  prompt?: string;
  sid?: string;
  // Risk level from the backend's permission.RiskSeverity: "high" (rm -rf,
  // git push --force) renders the dialog red, "low" (read-tier confirm)
  // calm accent, "medium"/unset keeps the familiar warning yellow.
  severity?: string;
  // Graded-auth escalation progress (Claude Code parity) surfaced by the
  // engine: consecutive ask/deny count and the threshold that forces manual.
  strikes?: number;
  threshold?: number;
}

// Token usage as reported by the backend (snake_case) or a provider (PascalCase).
export interface UsageInfo {
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_hit_tokens?: number;
  PromptTokens?: number;
  CompletionTokens?: number;
  TotalTokens?: number;
  total_tokens?: number;
}

export interface CostParts { input: number; output: number; }

export interface PlanInfo {
  type?: string;
  name?: string;
  cost?: CostParts;
}

// A single server-sent event from /api/chat's SSE stream.
export type ChatEvent =
  | { type: 'text'; content: string }
  | { type: 'thinking'; content: string }
  | { type: 'system'; content: string }
  | { type: 'tool_use'; tool_call?: { name: string; arguments?: string }; ToolCall?: { Name: string; Arguments?: string } }
  | { type: 'tool_progress'; content: string }
  | { type: 'permission'; permission?: PermissionRequest; Permission?: PermissionRequest }
  | { type: 'plan_proposal' }
  | { type: 'done'; meta?: { usage?: UsageInfo } }
  | { type: 'error'; content: string };

// Request body for POST /api/chat (and the slash-command re-entry path).
export interface ChatPayload {
  session_id: string;
  content: string;
  model: string;
  provider: string;
  attachments?: Array<{ type: string; mime: string; data: string }>;
}

// Fields worth surfacing in a tool-call chip — keeps the bubble informative
// without dumping the full (often huge) JSON argument payload into the stream.
const TOOL_ARG_FIELDS = ['path', 'command', 'pattern', 'query', 'file', 'directory', 'url', 'name', 'content'];

export function summarizeToolArgs(raw?: string): string {
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

// msgTime renders a message timestamp as a compact HH:MM (e.g. "14:05").
export function msgTime(ts?: number): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

// Renders inline multimodal attachments (image thumbnails / file chips) inside a chat bubble.
export function AttachmentView({ items, onZoom }: { items: Attachment[]; onZoom: (src: string) => void }) {  return (
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
export const MessageList = React.memo(({ messages, isStreaming, sessionId, onRegenerate, onEditResend, onFork, onZoom, win }: {
  messages: Message[];
  isStreaming: boolean;
  sessionId: string;
  onRegenerate: (id: string) => void;
  onEditResend: (id: string) => void;
  onFork: (id: string) => void;
  onZoom: (src: string) => void;
  // Visible slice + spacers from the parent's windowing hook; null renders all
  // rows (used before the first measurement, so nothing blanks out).
  win: MessageWindow | null;
}) => {
  const { t } = useTranslation();
  // Model display name for the per-message label row (Claude Code-style
  // "Claude Sonnet" header). Selector returns a stable string so the memo
  // stays effective.
  const modelName = useAppStore((s) => {
    const m = s.models.find((x) => x.id === s.selectedModel);
    return m?.name || '';
  });
  const start = win ? win.first : 0;
  const end = win ? win.last : messages.length - 1;
  return (
    <>
{win && <div aria-hidden style={{ height: win.padTop, flexShrink: 0 }} />}
{messages.slice(start, end + 1).map((msg, i) => {
        const idx = start + i;
        // Only the final assistant message is "streaming" — passing this down
        // lets Markdown skip expensive highlightAuto on every token frame and
        // do it once on the final render instead.
        const isLast = idx === messages.length - 1;
        const msgStreaming = isStreaming && isLast && msg.role === 'assistant';
        // System messages (engine notices) render as a centered, muted banner —
        // not a side-aligned bubble like a user/assistant turn.
        if (msg.role === 'system') {
          return (
            <div key={msg.id} data-mi={idx} className="msg-system" style={{
              display: 'flex', justifyContent: 'center', padding: '6px 24px',
            }}>
              <div style={{
                maxWidth: '85%', textAlign: 'center',
                fontSize: 12, lineHeight: 1.6, wordBreak: 'break-word',
                color: 'var(--text-muted)',
                background: 'var(--bg-tertiary)',
                border: '0.5px dashed var(--border-color)',
                borderRadius: 'var(--r-full)',
                padding: '5px 14px',
                animation: 'fadeIn var(--t-slow) ease-out',
              }}>{msg.content}</div>
            </div>
          );
        }
        return (
        <div
          key={msg.id}
          data-mi={idx}
          style={{
            display: 'flex', gap: 10, padding: '6px 24px',
            justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
            alignItems: 'flex-start',
            animation: 'fadeIn var(--t-slow) ease-out',
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
                    {/* Per-message model label (Claude Code-style header) */}
                    <div style={{
                      display: 'flex', alignItems: 'center', gap: 6,
                      fontSize: 10, color: 'var(--text-muted)', fontWeight: 500,
                      marginBottom: 6, letterSpacing: '0.01em',
                    }}>
                      <span style={{ color: 'var(--accent)' }}>{modelName || 'iCode'}</span>
                      <span style={{ opacity: 0.6 }}>·</span>
                      <span style={{ fontWeight: 400, opacity: 0.8 }}>{msgTime(msg.timestamp)}</span>
                    </div>
                    <Markdown text={msg.content} streaming={msgStreaming} />
                    {/* Turn review bar — file chips + diff + rewind, shown once
                        the turn settled with file-modifying tools. */}
                    {!msgStreaming && msg.changedFiles && msg.changedFiles.length > 0 && (
                      <ChangedFilesBar sessionId={sessionId} msgId={msg.id} files={msg.changedFiles} />
                    )}
                    {/* Action buttons — hidden until bubble hover */}
                    <div className="action-hidden" style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                      <ActionBtn icon="📋" label={t('chat.copy')} title={t('chat.copyTitle')}
                        onClick={() => navigator.clipboard.writeText(msg.content)} />
                      <ActionBtn icon="🔄" label={t('chat.regenerate')} title={t('chat.regenerateTitle')}
                        onClick={() => onRegenerate(msg.id)} />
                      <ActionBtn icon="⑂" label={t('chat.forkFrom')} title={t('chat.forkFromTitle')}
                        onClick={() => onFork(msg.id)} />
                    </div>
                  </>
                )
              ) : (isStreaming ? (
                <span style={{ color: 'var(--text-muted)', display: 'inline-flex', alignItems: 'center', gap: 8 }}>
                  {t('chat.generatingShort')}
                  <span className="typing-dots"><span /><span /><span /></span>
                </span>
              ) : '')
            ) : (
              <span style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</span>
            )}
            {msg.role === 'user' && (
              <div className="action-hidden" style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                <ActionBtn icon="📋" label={t('chat.copy')} title={t('chat.copyTitle')}
                  onClick={() => navigator.clipboard.writeText(msg.content)} />
                <ActionBtn icon="✏️" label={t('chat.editResend')} title={t('chat.editResendTitle')}
                  onClick={() => onEditResend(msg.id)} />
                <ActionBtn icon="⑂" label={t('chat.forkFrom')} title={t('chat.forkFromTitle')}
                  onClick={() => onFork(msg.id)} />
              </div>
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
{win && <div aria-hidden style={{ height: win.padBottom, flexShrink: 0 }} />}
    </>
  );
});

// Small presentational helpers for the status bar / stats sidebar.
export function Pill({ icon, label, onClick, title }: { icon: React.ReactNode; label: string; onClick: () => void; title?: string }) {
  return (
    <button
      onClick={onClick}
      title={title || i18n.t('chat.openSettings')}
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

export function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', padding: '5px 0', borderBottom: '1px dashed var(--border-color)' }}>
      <span style={{ color: 'var(--text-muted)' }}>{label}</span>
      <span style={{ color: 'var(--text-primary)', fontWeight: 500, textAlign: 'right', maxWidth: 130, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {value}
      </span>
    </div>
  );
}

export function ActionBtn({ icon, label, title, onClick }: { icon: string; label: string; title: string; onClick: () => void }) {
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
