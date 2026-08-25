import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Target, CheckCircle2, Loader2, Trash2 } from 'lucide-react';

// GoalPanel exposes the long-goal / acceptance-check mode from the sidebar
// (对标 ZCode Goal 模式): view the active goal + acceptance command, set a new
// one (with optional --verify), or clear it. Routes through /api/slash so it
// matches the /goal command on every end.
const GoalPanel: React.FC = () => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const [goal, setGoal] = useState('');
  const [verify, setVerify] = useState('');
  const [current, setCurrent] = useState<{ goal: string; verify: string } | null>(null);
  const [loading, setLoading] = useState(false);

  const runSlash = async (text: string): Promise<string> => {
    if (!backendUrl) return '';
    const res = await fetch(`${backendUrl}/api/slash`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, session_id: activeSessionId || '' }),
    });
    const data = await res.json();
    return data?.output || '';
  };

  const refresh = async () => {
    if (!backendUrl || !activeSessionId) return;
    const out = await runSlash('/goal show');
    // Parse "目标：xxx\n验收命令：yyy" loosely.
    const g = /当前目标[^：]*：?\s*([\s\S]*?)(?:\n验收命令：|$)/.exec(out)?.[1]?.trim() || '';
    const v = /验收命令：\s*([\s\S]*)/.exec(out)?.[1]?.trim() || '';
    if (g && !g.startsWith('当前没有目标')) {
      setCurrent({ goal: g, verify: v });
    } else {
      setCurrent(null);
    }
  };

  useEffect(() => { refresh(); /* eslint-disable-next-line */ }, [backendUrl, activeSessionId]);

  const handleSet = async () => {
    if (!goal.trim() || loading) return;
    setLoading(true);
    const text = verify.trim() ? `/goal set ${goal.trim()} --verify ${verify.trim()}` : `/goal set ${goal.trim()}`;
    await runSlash(text);
    setGoal('');
    setVerify('');
    setLoading(false);
    refresh();
  };

  const handleClear = async () => {
    setLoading(true);
    await runSlash('/goal clear');
    setCurrent(null);
    setLoading(false);
  };

  return (
    <div className="card" style={{ padding: 14 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
        <Target size={13} style={{ color: 'var(--accent)' }} /> {t('goal.title')}
      </div>

      {current ? (
        <div style={{ marginBottom: 8, fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
          <div style={{ display: 'flex', gap: 6, alignItems: 'flex-start' }}>
            <CheckCircle2 size={12} style={{ color: 'var(--success)', flexShrink: 0, marginTop: 2 }} />
            <span style={{ wordBreak: 'break-word' }}>{current.goal}</span>
          </div>
          {current.verify && (
            <div style={{ marginTop: 4, fontSize: 10, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
              {t('goal.verify')} {current.verify}
            </div>
          )}
          <button onClick={handleClear} disabled={loading} style={{ ...btnStyle, marginTop: 6 }}>
            <Trash2 size={11} /> {t('goal.clear')}
          </button>
        </div>
      ) : (
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 8 }}>{t('goal.none')}</div>
      )}

      <input
        value={goal}
        onChange={e => setGoal(e.target.value)}
        placeholder={t('goal.goalPlaceholder')}
        style={inputStyle}
      />
      <input
        value={verify}
        onChange={e => setVerify(e.target.value)}
        placeholder={t('goal.verifyPlaceholder')}
        style={{ ...inputStyle, marginTop: 6 }}
      />
      <button onClick={handleSet} disabled={!goal.trim() || loading} style={{ ...btnStyle, width: '100%', marginTop: 6 }}>
        {loading ? <Loader2 size={12} className="spin" /> : <Target size={12} />} {t('goal.set')}
      </button>
    </div>
  );
};

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '5px 8px', fontSize: 11, borderRadius: 6,
  background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
  color: 'var(--text-primary)', outline: 'none',
};
const btnStyle: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
  padding: '5px 10px', fontSize: 11, borderRadius: 6, cursor: 'pointer',
  background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
  color: 'var(--text-secondary)',
};

export default GoalPanel;
