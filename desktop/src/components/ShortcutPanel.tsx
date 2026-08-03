import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';

// Keyboard chip — matches the Reasonix design system (hairline border, no shadow).
const kbd: React.CSSProperties = {
  display: 'inline-block',
  padding: '2px 7px',
  borderRadius: 5,
  border: '0.5px solid var(--border-strong)',
  background: 'var(--bg-tertiary)',
  color: 'var(--text-primary)',
  fontFamily: 'var(--font-mono)',
  fontSize: 11,
  lineHeight: 1.6,
  whiteSpace: 'nowrap',
};

interface ShortcutItem {
  keys: string[];
  label: string;
}
interface ShortcutGroup {
  title: string;
  items: ShortcutItem[];
}

/**
 * ShortcutPanel — a workbuddy-style "?" overlay listing every keyboard
 * shortcut the desktop app supports. Additive-only: mounted globally in
 * App.tsx, toggled by `?`, closed by Esc or clicking the backdrop.
 */
export default function ShortcutPanel({ visible, onClose }: { visible: boolean; onClose: () => void }) {
  const { t } = useTranslation();

  // Esc closes (registered only while visible).
  useEffect(() => {
    if (!visible) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [visible, onClose]);

  if (!visible) return null;

  const groups: ShortcutGroup[] = [
    {
      title: t('shortcuts.global'),
      items: [
        { keys: ['Ctrl', ','], label: t('shortcuts.openSettings') },
        { keys: ['Ctrl', 'K'], label: t('shortcuts.commandPalette') },
        { keys: ['?'], label: t('shortcuts.shortcutPanel') },
        { keys: ['Esc'], label: t('shortcuts.closeOrInterrupt') },
      ],
    },
    {
      title: t('shortcuts.chat'),
      items: [
        { keys: ['Enter'], label: t('shortcuts.send') },
        { keys: ['Shift', 'Enter'], label: t('shortcuts.newline') },
        { keys: ['@'], label: t('shortcuts.mentionFile') },
        { keys: ['#'], label: t('shortcuts.appendMemory') },
        { keys: ['!'], label: t('shortcuts.runShell') },
      ],
    },
    {
      title: t('shortcuts.palette'),
      items: [
        { keys: ['↑', '↓'], label: t('shortcuts.select') },
        { keys: ['Enter'], label: t('shortcuts.run') },
        { keys: ['Esc'], label: t('shortcuts.close') },
      ],
    },
    {
      title: t('shortcuts.editing'),
      items: [
        { keys: ['Enter'], label: t('shortcuts.confirmRename') },
        { keys: ['Esc'], label: t('shortcuts.cancel') },
      ],
    },
  ];

  return (
    <div
      style={{
        position: 'fixed', inset: 0, zIndex: 2500,
        display: 'flex', alignItems: 'flex-start', justifyContent: 'center',
        paddingTop: '10vh',
        background: 'rgba(0,0,0,0.35)',
        backdropFilter: 'blur(6px)',
        WebkitBackdropFilter: 'blur(6px)',
      }}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div
        style={{
          width: 460, maxWidth: '92vw', maxHeight: '70vh', overflowY: 'auto',
          background: 'var(--bg-secondary)',
          border: '0.5px solid var(--border-color)',
          borderRadius: 'var(--r-lg)',
          padding: 'var(--s5) var(--s6)',
          animation: 'scaleIn var(--t-slow) ease-out',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 'var(--s4)' }}>
          <span style={{ fontSize: 'var(--text-lg)', fontWeight: 700, color: 'var(--text-primary)', letterSpacing: '-0.01em' }}>
            {t('shortcuts.title')}
          </span>
          <span style={{ fontSize: 'var(--text-xs)', color: 'var(--text-muted)' }}>{t('shortcuts.hint')}</span>
        </div>
        {groups.map((g) => (
          <div key={g.title} style={{ marginBottom: 'var(--s4)' }}>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--text-muted)', marginBottom: 'var(--s2)', fontWeight: 600, letterSpacing: '0.04em' }}>
              {g.title}
            </div>
            {g.items.map((it, i) => (
              <div
                key={i}
                style={{
                  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                  padding: '6px 0',
                  borderTop: i === 0 ? 'none' : '0.5px solid var(--border-color)',
                }}
              >
                <span style={{ fontSize: 'var(--text-base)', color: 'var(--text-primary)' }}>{it.label}</span>
                <span style={{ display: 'inline-flex', gap: 4, alignItems: 'center' }}>
                  {it.keys.map((k, ki) => (
                    <React.Fragment key={ki}>
                      {ki > 0 && <span style={{ color: 'var(--text-muted)', fontSize: 11 }}>+</span>}
                      <span style={kbd}>{k}</span>
                    </React.Fragment>
                  ))}
                </span>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
