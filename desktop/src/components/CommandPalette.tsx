import React, { useState, useEffect, useRef, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAppStore, Model, Session } from '../stores/appStore';

interface Action {
  id: string;
  label: string;
  description: string;
  icon: string;
  action: () => void;
}

const CommandPalette: React.FC<{ onClose: () => void }> = ({ onClose }) => {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [selectedIdx, setSelectedIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();

  // Precise selectors: the palette re-renders only when its data sources
  // (models list, sessions list, current selection) change.
  const models = useAppStore(s => s.models);
  const sessions = useAppStore(s => s.sessions);
  const selectedModel = useAppStore(s => s.selectedModel);
  const setSelectedModel = useAppStore(s => s.setSelectedModel);
  const createSession = useAppStore(s => s.createSession);
  const setActiveSession = useAppStore(s => s.setActiveSession);
  const deleteSession = useAppStore(s => s.deleteSession);
  const clearMessages = useAppStore(s => s.clearMessages);

  // Build actions list
  const actions: Action[] = [
    {
      id: 'settings',
      label: t('commandPalette.openSettings'),
      description: t('commandPalette.openSettingsDesc'),
      icon: '⚙',
      action: () => { onClose(); window.dispatchEvent(new CustomEvent('icode:open-settings')); },
    },
    {
      id: 'clear',
      label: t('commandPalette.clearChat'),
      description: t('commandPalette.clearChatDesc'),
      icon: '🗑',
      action: () => { const sid = useAppStore.getState().activeSessionId; if (sid) clearMessages(sid); onClose(); },
    },
    {
      id: 'new-session',
      label: t('commandPalette.newSession'),
      description: t('commandPalette.newSessionDesc'),
      icon: '✚',
      action: () => {
        const st = useAppStore.getState();
        const m = st.models?.find((x) => x.id === st.selectedModel);
        createSession(selectedModel, m?.provider || 'openrouter');
        onClose();
      },
    },
    {
      id: 'export',
      label: t('commandPalette.exportChat'),
      description: t('commandPalette.exportChatDesc'),
      icon: '⬇',
      action: () => {
        const sess = sessions.find(s => s.id === useAppStore.getState().activeSessionId);
        if (sess) {
          const md = sess.messages.map(m => `## ${m.role}\n\n${m.content}`).join('\n\n---\n\n');
          const blob = new Blob([md], { type: 'text/markdown' });
          const url = URL.createObjectURL(blob);
          const a = document.createElement('a');
          a.href = url; a.download = `icode-${sess.title || 'chat'}.md`; a.click();
          URL.revokeObjectURL(url);
        }
        onClose();
      },
    },
    ...models.map((m: Model) => ({
      id: `model:${m.id}`,
      label: t('commandPalette.switchModel', { name: m.name || m.id }),
      description: `${m.provider} — ${m.id}`,
      icon: '🧠',
      action: () => { setSelectedModel(m.id); onClose(); },
    })),
    ...sessions.slice(0, 10).map((s: Session) => ({
      id: `session:${s.id}`,
      label: s.title || s.id.slice(0, 8),
      description: t('commandPalette.sessionMessages', { count: s.messages.length }),
      icon: '💬',
      action: () => { setActiveSession(s.id); onClose(); },
    })),
  ];

  const filtered = query
    ? actions.filter(a =>
        a.label.toLowerCase().includes(query.toLowerCase()) ||
        a.description.toLowerCase().includes(query.toLowerCase())
      )
    : actions;

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    setSelectedIdx(0);
  }, [query]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') { onClose(); return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); setSelectedIdx(i => Math.min(i + 1, filtered.length - 1)); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); setSelectedIdx(i => Math.max(i - 1, 0)); return; }
    if (e.key === 'Enter' && filtered[selectedIdx]) {
      filtered[selectedIdx].action();
    }
  }, [filtered, selectedIdx, onClose]);

  return (
    <div
      style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.5)',
        display: 'flex', alignItems: 'flex-start', justifyContent: 'center',
        paddingTop: '12vh', zIndex: 2000,
      }}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="elevated" style={{
        width: 520, maxWidth: '90vw', maxHeight: '60vh',
        borderRadius: 12, boxShadow: '0 20px 60px rgba(0,0,0,0.5)',
        display: 'flex', flexDirection: 'column',
        animation: 'scaleIn 0.18s ease-out',
      }}>
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('commandPalette.placeholder')}
          style={{
            width: '100%', boxSizing: 'border-box',
            padding: '14px 16px', fontSize: 14,
            border: 'none', borderBottom: '1px solid var(--border-color)',
            background: 'transparent', color: 'var(--text-primary)',
            outline: 'none', borderRadius: '12px 12px 0 0',
          }}
        />
        <div style={{ overflowY: 'auto', flex: 1 }}>
          {filtered.length === 0 && (
            <div style={{ padding: '20px', textAlign: 'center', color: 'var(--text-muted)', fontSize: 13 }}>
              {t('commandPalette.noMatch')}
            </div>
          )}
          {filtered.map((item, i) => (
            <div
              key={item.id}
              onClick={() => item.action()}
              style={{
                display: 'flex', alignItems: 'center', gap: 10,
                padding: '10px 16px', cursor: 'pointer',
                background: i === selectedIdx ? 'var(--bg-tertiary)' : 'transparent',
                borderLeft: i === selectedIdx ? '2px solid var(--accent)' : '2px solid transparent',
              }}
              onMouseEnter={() => setSelectedIdx(i)}
            >
              <span style={{ fontSize: 16, width: 24, textAlign: 'center' }}>{item.icon}</span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, color: 'var(--text-primary)' }}>{item.label}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{item.description}</div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};

// Global Ctrl+K listener hook
export function useCommandPalette() {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setOpen(o => !o);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);

  return { open, setOpen };
}

export default CommandPalette;
