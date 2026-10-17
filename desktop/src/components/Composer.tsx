// Composer — the input bar at the bottom of a chat pane.
//
// Boundary chosen deliberately:
//   - Input history, slash-menu highlight index and draft are *inside* the
//     composer: they describe how typing feels, and no caller has ever needed
//     to read them.
//   - Attachments, streaming state, voice capture and plan confirmation stay
//     *controlled* by the page: the send path reads attachments, and voice
//     owns an AudioContext + backend call that doesn't belong to a text box.
//
// Extracted from ChatPage.tsx (2,345 lines) so keyboard behaviour — Enter vs
// Shift+Enter, slash-menu Tab completion, history recall, Tab mode cycling —
// can be exercised without mounting the whole transcript.

import React, { useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Send, Square, Mic, Plus, FileText } from 'lucide-react';
import { useAppStore } from '../stores/appStore';
import { filterSlash } from '../lib/slashCommands';

export interface AttachmentDraft {
  mime: string;
  data: string;
  name?: string;
}

export interface ComposerProps {
  value: string;
  onChange: (next: string) => void;
  /** Called with the exact text to send; the composer records it in history. */
  onSubmit: (text: string) => void;
  onStop: () => void;
  isStreaming: boolean;
  attachments: AttachmentDraft[];
  onAttachmentsChange: (next: AttachmentDraft[]) => void;
  /** The '@' shortcut opens the page-level file picker. */
  onOpenPicker: () => void;
  voiceActive: boolean;
  voiceBusy: boolean;
  onToggleVoice: () => void;
  planPending: boolean;
  onConfirmPlan: () => void;
  onDiscardPlan: () => void;
  ctxWindow: number;
  ctxWindowLabel: string;
  modelLabel: string;
  dragOver: boolean;
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onDrop: (e: React.DragEvent) => void;
  inputRef: React.RefObject<HTMLTextAreaElement>;
}

/** Reads a File into the base64 shape the chat payload expects. */
function readAsDraft(file: File, mime: string): Promise<AttachmentDraft> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve({
      mime,
      data: String(reader.result).split(',')[1],
      name: file.name || undefined,
    });
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

const MAX_HISTORY = 200;

export const Composer: React.FC<ComposerProps> = ({
  value, onChange, onSubmit, onStop, isStreaming,
  attachments, onAttachmentsChange, onOpenPicker,
  voiceActive, voiceBusy, onToggleVoice,
  planPending, onConfirmPlan, onDiscardPlan,
  ctxWindow, ctxWindowLabel, modelLabel,
  dragOver, onDragOver, onDragLeave, onDrop, inputRef,
}) => {
  const { t } = useTranslation();
  const tokenUsage = useAppStore((s) => s.tokenUsage);
  const mode = useAppStore((s) => s.mode);
  const setMode = useAppStore((s) => s.setMode);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [slashSel, setSlashSel] = useState(0);
  // ↑/↓ recall: -1 means "live draft", 0 = most recent submission.
  const historyRef = useRef<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState(-1);
  const draftRef = useRef('');

  // Rebuilt from the current text; empty unless the input starts with '/'.
  const slashMenu = useMemo(() => filterSlash(value), [value]);

  const submit = useCallback((text: string) => {
    const trimmed = text.trim();
    // A pasted screenshot with no caption is a valid send — the send path on
    // the page allows it too (`!text && attachedFiles.length === 0` returns).
    if (!trimmed && attachments.length === 0) return;
    if (trimmed) {
      historyRef.current.push(trimmed);
      if (historyRef.current.length > MAX_HISTORY) historyRef.current.shift();
    }
    setHistoryIdx(-1);
    onSubmit(trimmed);
  }, [onSubmit, attachments.length]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(value);
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
        onChange('/' + slashMenu[Math.min(slashSel, slashMenu.length - 1)].name + ' ');
        setSlashSel(0);
        return;
      }
    }
    // Tab / Shift+Tab cycle the agent mode (Claude Code style): Tab moves to
    // the next mode, Shift+Tab to the previous. Order matches the TUI's
    // cycleMode exactly (plan → agent → yolo → auto). The slash-menu
    // completion above already claims Tab while a slash command is open.
    if (e.key === 'Tab' && !e.altKey) {
      e.preventDefault();
      const modes = ['plan', 'agent', 'yolo', 'auto'];
      const idx = modes.indexOf(mode);
      setMode(modes[(idx + (e.shiftKey ? -1 : 1) + modes.length) % modes.length]);
      return;
    }
    // ↑/↓ browse the input history — only in single-line input, where the
    // arrows don't need to move the caret (multi-line keeps native behaviour).
    if (!e.shiftKey && !e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
      if (value.includes('\n')) return;
      const list = historyRef.current;
      if (e.key === 'ArrowUp') {
        if (list.length === 0) return;
        e.preventDefault();
        if (historyIdx === -1) draftRef.current = value;
        const next = Math.min(historyIdx + 1, list.length - 1);
        setHistoryIdx(next);
        onChange(list[list.length - 1 - next]);
        return;
      }
      if (historyIdx === -1) return;
      e.preventDefault();
      if (historyIdx === 0) {
        setHistoryIdx(-1);
        onChange(draftRef.current);
        return;
      }
      const next = historyIdx - 1;
      setHistoryIdx(next);
      onChange(list[list.length - 1 - next]);
      return;
    }
    if (e.key === '@' && value.trim() === '') {
      onOpenPicker();
    }
  };

  const addFiles = async (files: File[]) => {
    const drafts = await Promise.all(
      files.map((f) => readAsDraft(f, f.type || 'application/octet-stream')),
    );
    if (drafts.length) onAttachmentsChange([...attachments, ...drafts]);
  };

  const pct = Math.min((tokenUsage.input / ctxWindow) * 100, 100);
  // Attachments alone are sendable (image with no caption).
  const canSend = value.trim().length > 0 || attachments.length > 0;

  return (
    <>
      {attachments.length > 0 && (
        <div style={{
          padding: '8px 16px', display: 'flex', gap: 8, flexWrap: 'wrap',
          borderTop: '1px solid var(--border-color)', background: 'var(--bg-secondary)',
        }}>
          {attachments.map((img, i) => (
            <div key={i} style={{ position: 'relative', display: 'inline-block' }}>
              {img.mime === 'application/pdf' ? (
                <div style={{
                  height: 64, minWidth: 120, maxWidth: 200, padding: '0 10px',
                  borderRadius: 6, border: '1px solid var(--border-color)',
                  display: 'flex', alignItems: 'center', gap: 6,
                  background: 'var(--bg-primary)', fontSize: 11, color: 'var(--text-secondary)',
                }}>
                  <FileText size={16} aria-hidden />
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {img.name || 'PDF'}
                  </span>
                </div>
              ) : (
                <img
                  src={`data:${img.mime};base64,${img.data}`}
                  alt={img.name || 'pasted'}
                  style={{ height: 64, borderRadius: 6, border: '1px solid var(--border-color)' }}
                />
              )}
              <button
                type="button"
                aria-label={t('chat.removeAttachment', '移除附件')}
                onClick={() => onAttachmentsChange(attachments.filter((_, j) => j !== i))}
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

      <div
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        style={{
          padding: '12px 24px', borderTop: '0.5px solid var(--border-color)',
          background: dragOver ? 'var(--accent-soft)' : 'var(--bg-secondary)',
        }}
      >
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
              type="button"
              onClick={onConfirmPlan}
              style={{
                background: 'var(--grad-accent)', border: 'none', color: '#fff',
                padding: '4px 12px', borderRadius: 6, fontSize: 12, cursor: 'pointer',
              }}
            >
              {t('chat.planAccept', '接受并执行')}
            </button>
            <button
              type="button"
              onClick={onDiscardPlan}
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

        {/* Context bar — Claude Code style context meter right above the input */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          marginBottom: 6, padding: '0 4px',
        }}>
          <span style={{ fontSize: 10, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>ctx</span>
          <div style={{
            flex: 1, height: 3, borderRadius: 999,
            background: 'var(--border-color)', overflow: 'hidden',
          }}>
            <div style={{
              width: `${pct}%`,
              height: '100%', borderRadius: 999,
              background: pct > 80 ? 'var(--error)' : 'var(--success)',
              transition: 'width 0.3s ease',
            }} />
          </div>
          <span style={{ fontSize: 10, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
            {pct.toFixed(0)}% · {ctxWindowLabel}
          </span>
        </div>

        <div style={{
          display: 'flex', gap: 10, alignItems: 'flex-end',
          background: 'var(--bg-primary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: '6px 8px',
        }} className="composer">
          <div style={{ position: 'relative', flex: 1 }}>
            {slashMenu.length > 0 && (
              <div
                role="listbox"
                aria-label={t('chat.slashMenuAriaLabel', '斜杠命令')}
                style={{
                  position: 'absolute', bottom: '100%', left: 0, right: 0, marginBottom: 4,
                  background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
                  borderRadius: 8, boxShadow: '0 8px 24px rgba(0,0,0,0.25)', zIndex: 50,
                  maxHeight: 240, overflowY: 'auto', padding: 4,
                }}
              >
                {slashMenu.map((c, i) => (
                  <div
                    key={c.name}
                    role="option"
                    aria-selected={i === slashSel}
                    onMouseEnter={() => setSlashSel(i)}
                    onClick={() => { onChange('/' + c.name + ' '); setSlashSel(0); }}
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
              value={value}
              onChange={(e) => onChange(e.target.value)}
              onKeyDown={handleKeyDown}
              onPaste={(e) => {
                const items = e.clipboardData?.items;
                if (!items) return;
                const picked: File[] = [];
                for (let i = 0; i < items.length; i++) {
                  const mime = items[i].type;
                  if (!mime.startsWith('image/') && mime !== 'application/pdf') continue;
                  const file = items[i].getAsFile();
                  if (file) picked.push(file);
                }
                if (picked.length) void addFiles(picked);
              }}
              placeholder={t('chat.placeholder')}
              aria-label={t('chat.inputAriaLabel', '消息输入框')}
              aria-multiline="true"
              rows={1}
              style={{
                width: '100%', background: 'transparent', border: 'none',
                color: 'var(--text-primary)', resize: 'none',
                outline: 'none', padding: '6px 4px', maxHeight: 200,
                lineHeight: 1.5,
              }}
            />
          </div>
          <button
            type="button"
            onClick={onToggleVoice}
            disabled={voiceBusy}
            aria-pressed={voiceActive}
            aria-label={voiceActive ? t('chat.voiceStop') : t('chat.voice')}
            title={voiceActive ? t('chat.voiceStop') : t('chat.voice')}
            style={{
              background: voiceActive ? 'var(--error)' : 'transparent',
              border: 'none', color: voiceActive ? '#fff' : 'var(--text-muted)',
              padding: '6px 10px', borderRadius: 8, cursor: 'pointer',
              display: 'flex', alignItems: 'center',
            }}
          >
            <Mic size={16} aria-hidden />
          </button>
          {isStreaming ? (
            <button
              type="button"
              onClick={onStop}
              aria-label={t('chat.stop')}
              title={t('chat.stop')}
              style={{
                background: 'var(--error)', border: 'none', color: '#fff',
                padding: '6px 10px', borderRadius: 8, cursor: 'pointer',
                display: 'flex', alignItems: 'center',
              }}
            >
              <Square size={16} aria-hidden />
            </button>
          ) : (
            <button
              type="button"
              onClick={() => submit(value)}
              disabled={!canSend}
              aria-label={t('chat.send')}
              title={t('chat.send')}
              style={{
                background: canSend ? 'var(--grad-accent)' : 'var(--border-color)',
                border: 'none', color: canSend ? '#fff' : 'var(--text-muted)',
                padding: '7px 12px', borderRadius: 999, cursor: canSend ? 'pointer' : 'default',
                display: 'flex', alignItems: 'center', transition: 'filter 0.15s, opacity 0.15s',
              }}
              onMouseEnter={(e) => { if (canSend) e.currentTarget.style.filter = 'brightness(1.1)'; }}
              onMouseLeave={(e) => { e.currentTarget.style.filter = ''; }}
            >
              <Send size={16} aria-hidden />
            </button>
          )}
        </div>

        {/* Mode row — iCode's own mode selector (a labelled toggle group) */}
        <div role="group" aria-label={t('chat.modeGroupAriaLabel', '运行模式')} style={{
          display: 'flex', alignItems: 'center', gap: 4, marginTop: 6,
          fontSize: 10, color: 'var(--text-muted)',
        }}>
          <button
            type="button"
            className="interactive"
            aria-label={t('chat.addAttachmentAriaLabel', '添加附件')}
            onClick={() => fileInputRef.current?.click()}
            style={{
              padding: '3px 8px', borderRadius: 4, fontSize: 10,
              background: 'transparent', border: 'none', color: 'var(--text-muted)',
              display: 'flex', alignItems: 'center', gap: 3,
            }}>
            <Plus size={10} aria-hidden />
          </button>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*,application/pdf"
            multiple
            aria-label={t('chat.addAttachmentAriaLabel', '添加附件')}
            style={{ display: 'none' }}
            onChange={(e) => {
              const files = Array.from(e.target.files || []);
              e.target.value = '';
              void addFiles(files);
            }}
          />
          {[
            { v: 'plan', label: t('chat.modePlan') },
            { v: 'auto', label: t('chat.modeNormal') },
            { v: 'ask',  label: t('chat.modeAsk') },
            { v: 'yolo', label: t('chat.modeYolo') },
          ].map(m => (
            <button
              type="button"
              key={m.v}
              aria-pressed={mode === m.v}
              className={mode === m.v ? 'nav-item active' : 'nav-item'}
              style={{
                padding: '3px 10px', borderRadius: 4, fontSize: 10, fontWeight: mode === m.v ? 600 : 400,
              }}
              onClick={() => setMode(m.v)}
            >{m.label}</button>
          ))}
          <div style={{ flex: 1 }} />
          <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>
            {modelLabel} · {mode}
          </span>
        </div>

        {/* Live token/cost — updates on every streamed token (session totals) */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 14, marginTop: 6,
          fontSize: 10, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)',
        }}>
          <span>↑ {(tokenUsage.input / 1000).toFixed(1)}K</span>
          <span>↓ {(tokenUsage.output / 1000).toFixed(1)}K</span>
          {tokenUsage.cacheHit > 0 && (
            <span style={{ color: 'var(--success)' }}>
              cache {((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(0)}%
            </span>
          )}
          <span style={{ color: 'var(--accent)' }}>{tokenUsage.cost}</span>
          <div style={{ flex: 1 }} />
          {isStreaming && (
            <span style={{ color: 'var(--text-secondary)', display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              {t('chat.generatingShort')}
              <span className="typing-dots"><span /><span /><span /></span>
            </span>
          )}
        </div>
      </div>
    </>
  );
};

export default Composer;
