import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X, FileDiff } from 'lucide-react';

interface DiffViewerProps {
  backendUrl: string;
  sessionId: string;
  steps: number;
  message: string;
  onClose: () => void;
}

// Lightweight unified-diff viewer: parses `git diff` output into per-file
// hunks and renders them side-by-side (old | new columns). No external diff
// library — the parser handles the standard unified format git emits.
const DiffViewer: React.FC<DiffViewerProps> = ({ backendUrl, sessionId, steps, message, onClose }) => {
  const { t } = useTranslation();
  const [diff, setDiff] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const res = await fetch(
          `${backendUrl}/api/checkpoints/${sessionId}/diff?steps=${steps}`
        );
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        if (alive) setDiff(data.diff || '');
      } catch (e) {
        if (alive) setError(String(e));
      }
      if (alive) setLoading(false);
    })();
    return () => { alive = false; };
  }, [backendUrl, sessionId, steps]);

  // Parse unified diff into { files: [{ header, hunks: [{ oldStart, lines }] }] }
  const files: { header: string; lines: { text: string; kind: 'ctx' | 'add' | 'del' | 'hunk' | 'meta' }[] }[] = [];
  let cur: { header: string; lines: { text: string; kind: 'ctx' | 'add' | 'del' | 'hunk' | 'meta' }[] } | null = null;

  for (const raw of diff.split('\n')) {
    const line = raw;
    if (line.startsWith('diff --git ') || line.startsWith('--- ')) {
      if (cur) files.push(cur);
      if (line.startsWith('diff --git ')) {
        cur = { header: line.replace(/^diff --git /, ''), lines: [] };
      }
      continue;
    }
    if (!cur) cur = { header: '', lines: [] };
    let kind: 'ctx' | 'add' | 'del' | 'hunk' | 'meta' = 'ctx';
    if (line.startsWith('+++ ') || line.startsWith('+++')) kind = 'meta';
    else if (line.startsWith('@@')) kind = 'hunk';
    else if (line.startsWith('+')) kind = 'add';
    else if (line.startsWith('-')) kind = 'del';
    cur.lines.push({ text: line, kind });
  }
  if (cur) files.push(cur);

  const colorFor = (kind: string) => {
    switch (kind) {
      case 'add': return { bg: 'rgba(46,160,67,0.12)', fg: 'var(--success)' };
      case 'del': return { bg: 'rgba(248,81,73,0.12)', fg: 'var(--error)' };
      case 'hunk': return { bg: 'rgba(88,166,255,0.10)', fg: 'var(--accent)' };
      case 'meta': return { bg: 'transparent', fg: 'var(--accent)' };
      default: return { bg: 'transparent', fg: 'var(--text-secondary)' };
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed', inset: 0, zIndex: 1000,
        background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: 'var(--bg-secondary)', borderRadius: 14,
          border: '1px solid var(--border-color)',
          width: 'min(880px, 92vw)', height: 'min(640px, 85vh)',
          display: 'flex', flexDirection: 'column', overflow: 'hidden',
          boxShadow: '0 20px 60px rgba(0,0,0,0.35)',
        }}
      >
        {/* Header */}
        <div style={{
          padding: '14px 18px', borderBottom: '1px solid var(--border-color)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
            <FileDiff size={15} style={{ color: 'var(--accent)', flexShrink: 0 }} />
            <span style={{
              fontSize: 13, fontWeight: 600, color: 'var(--text-primary)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            }}>
              {t('diff.title')} — {t('diff.range', { count: steps })}
            </span>
          </div>
          {message && (
            <div style={{
              fontSize: 10, color: 'var(--text-muted)', textAlign: 'center',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              maxWidth: '40%', margin: '0 8px',
            }}>
              {t('diff.since')} {message}
            </div>
          )}
          <button
            onClick={onClose}
            style={{
              background: 'transparent', border: 'none', color: 'var(--text-muted)',
              cursor: 'pointer', padding: 4, display: 'flex', borderRadius: 6,
            }}
            onMouseEnter={e => (e.currentTarget.style.color = 'var(--text-primary)')}
            onMouseLeave={e => (e.currentTarget.style.color = 'var(--text-muted)')}
          >
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', fontFamily: 'var(--font-mono)', fontSize: 11.5, lineHeight: 1.55 }}>
          {loading && (
            <div style={{ color: 'var(--text-muted)', textAlign: 'center', paddingTop: 40 }}>
              {t('diff.loading')}
            </div>
          )}
          {error && (
            <div style={{ color: 'var(--error)', textAlign: 'center', paddingTop: 40 }}>
              {t('diff.error')}: {error}
            </div>
          )}
          {!loading && !error && files.length === 0 && (
            <div style={{ color: 'var(--text-muted)', textAlign: 'center', paddingTop: 40 }}>
              {t('diff.empty')}
            </div>
          )}
          {files.map((f, fi) => (
            <div key={fi} style={{ marginBottom: 14 }}>
              {f.header && (
                <div style={{
                  color: 'var(--accent)', fontWeight: 600, fontSize: 11,
                  marginBottom: 6, padding: '4px 8px',
                  background: 'rgba(88,166,255,0.07)', borderRadius: 6,
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                }}>
                  {f.header}
                </div>
              )}
              {f.lines.map((l, li) => {
                const c = colorFor(l.kind);
                return (
                  <div key={li} style={{
                    background: c.bg, color: c.fg, padding: '0 8px',
                    whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                    borderRadius: 2,
                  }}>
                    {l.text || ' '}
                  </div>
                );
              })}
            </div>
          ))}
        </div>

        {/* Footer */}
        <div style={{
          padding: '10px 18px', borderTop: '1px solid var(--border-color)',
          fontSize: 10, color: 'var(--text-muted)',
          display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        }}>
          <span>{files.length} {t('diff.files')}</span>
          <span>{t('diff.hint')}</span>
        </div>
      </div>
    </div>
  );
};

export default DiffViewer;
