import React from 'react';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';
import { useAppStore } from '../stores/appStore';
import Markdown from './Markdown';

/**
 * SessionViewer is a read-only side-by-side comparison pane (D1 split-pane
 * minimal version): it renders another session's messages next to the active
 * conversation so the user can cross-reference two sessions without switching
 * tabs. It is intentionally read-only — no input, no streaming — so it needs
 * none of ChatPage's interactive state; the full two-way split (SessionPane
 * parameterization) is tracked in docs/split_design.md.
 */
const SessionViewer: React.FC<{ sessionId: string; onClose: () => void }> = ({ sessionId, onClose }) => {
  const { t } = useTranslation();
  const session = useAppStore(s => s.sessions.find(x => x.id === sessionId));
  const messages = session?.messages || [];

  return (
    <div style={{
      width: '42%', minWidth: 320, borderLeft: '0.5px solid var(--border-color)',
      background: 'var(--bg-primary)', display: 'flex', flexDirection: 'column',
      minHeight: 0, flexShrink: 0,
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '8px 12px', borderBottom: '0.5px solid var(--border-color)',
        background: 'var(--bg-secondary)', flexShrink: 0,
      }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          👁 {session?.title || t('split.viewer')}
        </span>
        <button onClick={onClose} title={t('split.close')} style={{
          background: 'transparent', border: 'none', cursor: 'pointer',
          color: 'var(--text-muted)', padding: 2, display: 'flex', borderRadius: 4,
        }}>
          <X size={14} />
        </button>
      </div>
      <div style={{ flex: 1, overflowY: 'auto', padding: '12px 14px' }}>
        {messages.length === 0 ? (
          <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '12px 0' }}>{t('split.empty')}</div>
        ) : messages.map((m, i) => {
          const isUser = m.role === 'user';
          return (
            <div key={i} style={{
              marginBottom: 12,
              padding: '8px 10px',
              borderRadius: 8,
              background: isUser ? 'var(--accent-soft)' : 'var(--bg-secondary)',
              border: '0.5px solid var(--border-color)',
            }}>
              <div style={{
                fontSize: 10, fontWeight: 600, letterSpacing: '.5px',
                textTransform: 'uppercase', color: isUser ? 'var(--accent)' : 'var(--text-muted)',
                marginBottom: 4,
              }}>
                {isUser ? 'User' : 'Assistant'}
              </div>
              <div style={{ fontSize: 12.5, color: 'var(--text-primary)', lineHeight: 1.65 }}>
                <Markdown text={m.content || ''} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default SessionViewer;
