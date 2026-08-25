import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { BookOpen, Search, Loader2 } from 'lucide-react';

// KnowledgePanel exposes the local document knowledge base (RAG) from the
// sidebar: type a query and see the top passages. Routes through the shared
// /api/slash layer (/kb), matching the TUI command.
const KnowledgePanel: React.FC = () => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const [query, setQuery] = useState('');
  const [result, setResult] = useState('');
  const [loading, setLoading] = useState(false);

  const runSearch = async (q: string) => {
    if (!backendUrl || loading || !q.trim()) return;
    setLoading(true);
    setResult('');
    try {
      const res = await fetch(`${backendUrl}/api/slash`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: '/kb ' + q.trim(), session_id: activeSessionId || '' }),
      });
      const data = await res.json();
      setResult(data?.output || t('kb.noOutput'));
    } catch {
      setResult(t('kb.backendError'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="card" style={{ padding: 14 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
        <BookOpen size={13} style={{ color: 'var(--accent)' }} /> {t('kb.title')}
      </div>

      <div style={{ display: 'flex', gap: 6 }}>
        <input
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder={t('kb.placeholder')}
          onKeyDown={e => { if (e.key === 'Enter') runSearch(query); }}
          style={inputStyle}
        />
        <button onClick={() => runSearch(query)} disabled={!query.trim() || loading} style={btnStyle}>
          {loading ? <Loader2 size={12} className="spin" /> : <Search size={12} />} {t('kb.search')}
        </button>
      </div>

      {result && (
        <div style={{
          marginTop: 8, padding: 8, fontSize: 10.5, fontFamily: 'var(--font-mono)',
          background: 'var(--bg-tertiary)', borderRadius: 6, whiteSpace: 'pre-wrap',
          wordBreak: 'break-word', maxHeight: 240, overflowY: 'auto',
          color: 'var(--text-secondary)',
        }}>{result}</div>
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

export default KnowledgePanel;
