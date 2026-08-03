import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { RotateCcw, History } from 'lucide-react';

interface CheckpointEntry {
  Hash: string;
  Message: string;
  When: string;
}

const CheckpointPanel: React.FC = () => {
  const { t } = useTranslation();
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const backendUrl = useAppStore(s => s.backendUrl);
  const [entries, setEntries] = useState<CheckpointEntry[]>([]);
  const [rewinding, setRewinding] = useState(false);

  useEffect(() => {
    if (!backendUrl || !activeSessionId) return;
    let alive = true;
    const poll = async () => {
      try {
        const res = await fetch(`${backendUrl}/api/checkpoints/${activeSessionId}`);
        if (res.ok && alive) {
          const data = await res.json();
          setEntries(data || []);
        }
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => { alive = false; clearInterval(interval); };
  }, [backendUrl, activeSessionId]);

  const handleRewind = async (steps: number) => {
    if (!backendUrl || !activeSessionId || rewinding) return;
    if (!window.confirm(t('checkpoint.confirmRewind', { steps }))) return;
    setRewinding(true);
    try {
      const res = await fetch(`${backendUrl}/api/checkpoints/rewind`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: activeSessionId, steps }),
      });
      if (res.ok) {
        const data = await res.json();
        const files = data.files || [];
        alert(`${t('checkpoint.rewound', { steps })}\n${files.length > 0 ? t('checkpoint.restoredFiles') + files.join(', ') : ''}`);
        const r2 = await fetch(`${backendUrl}/api/checkpoints/${activeSessionId}`);
        if (r2.ok) setEntries(await r2.json());
      }
    } catch {}
    setRewinding(false);
  };

  if (entries.length === 0) return null;

  return (
    <div style={{ marginTop: 8 }}>
      <div style={{
        fontSize: 11, color: 'var(--text-muted)', marginBottom: 6,
        letterSpacing: 0.5, fontWeight: 500, display: 'flex',
        justifyContent: 'space-between', alignItems: 'center',
      }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <History size={11} /> {t('checkpoint.title')} ({entries.length})
        </span>
        {entries.length > 0 && (
          <button
            onClick={() => handleRewind(1)}
            disabled={rewinding}
            title={t('checkpoint.rewindStep')}
            style={{
              background: 'transparent', border: '1px solid var(--border-color)',
              borderRadius: 3, cursor: 'pointer', padding: '0 4px',
              fontSize: 9, color: 'var(--text-muted)',
              display: 'flex', alignItems: 'center', gap: 2,
            }}>
            <RotateCcw size={9} /> {t('checkpoint.rewind')}
          </button>
        )}
      </div>
      {entries.slice(0, 8).map((e, i) => (
        <div key={e.Hash} style={{
          display: 'flex', alignItems: 'center', gap: 4,
          padding: '2px 0', fontSize: 10,
          color: i === 0 ? 'var(--text-primary)' : 'var(--text-muted)',
          fontFamily: 'var(--font-mono)',
        }}>
          <span style={{ color: 'var(--accent)', flexShrink: 0, fontSize: 9 }}>
            ●
          </span>
          <span style={{
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            flex: 1, lineHeight: 1.3,
          }}>
            {e.Message || e.Hash.slice(0, 7)}
          </span>
          {i > 0 && (
            <button
              onClick={() => handleRewind(i)}
              disabled={rewinding}
              title={t('checkpoint.confirmRewind', { steps: i })}
              style={{
                background: 'transparent', border: 'none', cursor: 'pointer',
                color: 'var(--text-muted)', fontSize: 9, padding: '0 2px',
                flexShrink: 0, opacity: 0.6,
              }}
              onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
              onMouseLeave={e => (e.currentTarget.style.opacity = '0.6')}
            >
              <RotateCcw size={9} />
            </button>
          )}
        </div>
      ))}
      {entries.length > 8 && (
        <div style={{ fontSize: 9, color: 'var(--text-muted)', textAlign: 'center' }}>
          +{entries.length - 8} {t('checkpoint.more')}
        </div>
      )}
    </div>
  );
};

export default CheckpointPanel;
