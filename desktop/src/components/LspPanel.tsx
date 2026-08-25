import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Bug, Search, Loader2, Copy, Check } from 'lucide-react';

interface DiagItem {
  severity: string;
  line: number;
  col: number;
  message: string;
  file: string;
}

// Parse the /lsp diag text output into structured entries.
function parseDiags(output: string, file: string): DiagItem[] {
  const items: DiagItem[] = [];
  for (const line of output.split('\n')) {
    const m = line.match(/^\s*\[(错误|警告|信息|提示)\]\s*(\d+):(\d+)\s+(.*)$/);
    if (m) {
      items.push({ severity: m[1], line: +m[2], col: +m[3], message: m[4], file });
    }
  }
  return items;
}

const sevColor: Record<string, string> = {
  '错误': 'var(--danger)',
  '警告': 'var(--warning)',
  '信息': 'var(--info)',
  '提示': 'var(--text-muted)',
};

// LspPanel exposes on-demand LSP code intelligence from the sidebar. Diagnostic
// results are structured and clickable — clicking a row copies its
// "file:line:col" location so the user can jump to it (paste to the agent or a
// terminal). All queries route through /api/slash (same as the /lsp TUI cmd).
const LspPanel: React.FC = () => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const [file, setFile] = useState('');
  const [result, setResult] = useState('');
  const [diags, setDiags] = useState<DiagItem[]>([]);
  const [copied, setCopied] = useState('');
  const [loading, setLoading] = useState(false);

  const runSlash = async (text: string, diagFile?: string) => {
    if (!backendUrl || loading) return;
    setLoading(true);
    setResult('');
    setDiags([]);
    try {
      const res = await fetch(`${backendUrl}/api/slash`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text, session_id: activeSessionId || '' }),
      });
      const data = await res.json();
      const out = data?.output || '';
      setResult(out);
      if (diagFile) setDiags(parseDiags(out, diagFile));
    } catch {
      setResult(t('lsp.backendError'));
    } finally {
      setLoading(false);
    }
  };

  const copyLoc = (d: DiagItem) => {
    const loc = `${d.file}:${d.line}:${d.col}`;
    navigator.clipboard?.writeText(loc).catch(() => {});
    setCopied(loc);
    setTimeout(() => setCopied(''), 2000);
  };

  const runDiag = () => {
    if (!file.trim()) return;
    runSlash('/lsp diag ' + file.trim(), file.trim());
  };

  return (
    <div className="card" style={{ padding: 14 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
        <Bug size={13} style={{ color: 'var(--accent)' }} /> {t('lsp.title')}
      </div>

      <div style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
        <input
          value={file}
          onChange={e => setFile(e.target.value)}
          placeholder={t('lsp.filePlaceholder')}
          onKeyDown={e => { if (e.key === 'Enter') runDiag(); }}
          style={inputStyle}
        />
        <button onClick={runDiag} disabled={!file.trim() || loading} style={btnStyle}>
          {loading ? <Loader2 size={12} className="spin" /> : <Search size={12} />} {t('lsp.diag')}
        </button>
      </div>
      <button onClick={() => runSlash('/lsp status')} disabled={loading} style={{ ...btnStyle, width: '100%' }}>
        {t('lsp.status')}
      </button>

      {diags.length > 0 && (
        <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 2 }}>
            {diags.length} {t('lsp.issues')} · {t('lsp.clickToCopy')}
          </div>
          {diags.map((d, i) => (
            <button
              key={i}
              onClick={() => copyLoc(d)}
              title={t('lsp.copyLoc')}
              style={{
                display: 'flex', alignItems: 'flex-start', gap: 6, textAlign: 'left',
                padding: '6px 8px', fontSize: 10.5, borderRadius: 6, cursor: 'pointer',
                background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
                color: 'var(--text-secondary)', lineHeight: 1.4,
              }}
            >
              <span style={{ color: sevColor[d.severity] || 'var(--text-muted)', flexShrink: 0, fontWeight: 600 }}>
                [{d.severity}]
              </span>
              <span style={{ color: 'var(--accent)', flexShrink: 0, fontFamily: 'var(--font-mono)' }}>
                {d.line}:{d.col}
              </span>
              <span style={{ flex: 1, wordBreak: 'break-word' }}>{d.message}</span>
              {copied === `${d.file}:${d.line}:${d.col}`
                ? <Check size={11} style={{ color: 'var(--success)', flexShrink: 0 }} />
                : <Copy size={11} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />}
            </button>
          ))}
        </div>
      )}

      {result && diags.length === 0 && (
        <pre style={{
          marginTop: 8, padding: 8, fontSize: 10.5, fontFamily: 'var(--font-mono)',
          background: 'var(--bg-tertiary)', borderRadius: 6, whiteSpace: 'pre-wrap',
          wordBreak: 'break-word', maxHeight: 220, overflowY: 'auto',
          color: 'var(--text-secondary)',
        }}>{result}</pre>
      )}
    </div>
  );
};

const inputStyle: React.CSSProperties = {
  flex: 1, padding: '5px 8px', fontSize: 11, borderRadius: 6,
  background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
  color: 'var(--text-primary)', outline: 'none',
};
const btnStyle: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
  padding: '5px 10px', fontSize: 11, borderRadius: 6, cursor: 'pointer',
  background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
  color: 'var(--text-secondary)',
};

export default LspPanel;
