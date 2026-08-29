import React, { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { useAppStore } from '../stores/appStore';
import { Search, ArrowLeft } from 'lucide-react';

// Standalone skill market page (D7): the market browser previously lived as a
// tab inside the Settings modal. This route gives it full-page breathing room
// with a search filter over name/description/triggers, mirroring WorkBuddy's
// dedicated market surface.

interface MarketSkill {
  name: string;
  description?: string;
  triggers?: string[];
  installed?: boolean;
}

const SkillMarketPage: React.FC = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const backendUrl = useAppStore((s) => s.backendUrl);
  const [market, setMarket] = useState<MarketSkill[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [notice, setNotice] = useState('');

  const loadMarket = async () => {
    if (!backendUrl) return;
    setLoading(true);
    try {
      const r = await fetch(`${backendUrl}/api/skills/market`);
      if (r.ok) { const d = await r.json(); setMarket(d.market || []); }
    } catch {}
    setLoading(false);
  };

  useEffect(() => {
    loadMarket();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [backendUrl]);

  const install = async (name: string) => {
    if (!backendUrl) return;
    setBusy(name);
    setNotice('');
    try {
      const r = await fetch(`${backendUrl}/api/skills/market/install`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      if (r.ok) {
        setMarket((prev) => prev.map((m) => m.name === name ? { ...m, installed: true } : m));
        setNotice(`${name} ✓`);
      } else {
        const d = await r.json().catch(() => ({} as Record<string, unknown>));
        setNotice(String(d.error || 'failed'));
      }
    } catch { setNotice('failed'); }
    setBusy(null);
  };

  const uninstall = async (name: string) => {
    if (!backendUrl) return;
    if (!confirm(t('settings.skillMarket.uninstallConfirm'))) return;
    setBusy(name);
    setNotice('');
    try {
      const r = await fetch(`${backendUrl}/api/skills/market/${encodeURIComponent(name)}`, { method: 'DELETE' });
      if (r.ok) setMarket((prev) => prev.map((m) => m.name === name ? { ...m, installed: false } : m));
    } catch {}
    setBusy(null);
  };

  const q = query.trim().toLowerCase();
  const filtered = market.filter((m) =>
    !q || m.name.toLowerCase().includes(q)
      || (m.description || '').toLowerCase().includes(q)
      || (m.triggers || []).some((tr) => tr.toLowerCase().includes(q)),
  );

  const cardStyle: React.CSSProperties = {
    display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12,
    background: 'var(--bg-primary)', borderRadius: 8, border: '1px solid var(--border-color)',
    padding: '12px 14px',
  };

  return (
    <div style={{ maxWidth: 860, margin: '0 auto', padding: '32px 24px' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
        <button
          onClick={() => navigate('/')}
          title={t('shortcuts.close')}
          style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: 4, display: 'flex', borderRadius: 6 }}
        ><ArrowLeft size={18} /></button>
        <div>
          <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--text-primary)', letterSpacing: '-0.02em' }}>
            {t('settings.skillMarket.title')}
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 2 }}>
            {t('settings.skillMarket.desc')}
          </div>
        </div>
      </div>

      {/* Search box */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, margin: '18px 0',
        background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
        borderRadius: 8, padding: '8px 12px',
      }}>
        <Search size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('settings.skillMarket.searchPlaceholder')}
          style={{
            flex: 1, background: 'transparent', border: 'none', outline: 'none',
            color: 'var(--text-primary)', fontSize: 13,
          }}
        />
        {q && <span style={{ fontSize: 11, color: 'var(--text-muted)', flexShrink: 0 }}>{filtered.length}</span>}
      </div>

      {notice && (
        <div style={{ fontSize: 12, color: 'var(--success)', marginBottom: 10 }}>{notice}</div>
      )}

      {/* Market list */}
      {loading ? (
        <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-muted)', fontSize: 12 }}>
          {t('settings.skillMarket.loading')}
        </div>
      ) : filtered.length === 0 ? (
        <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-muted)', fontSize: 12 }}>
          {q ? t('settings.skillMarket.noMatch') : t('settings.skillMarket.empty')}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {filtered.map((m) => (
            <div key={m.name} style={cardStyle}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {m.name}
                  {m.installed && (
                    <span style={{
                      marginLeft: 8, fontSize: 10, padding: '1px 8px', borderRadius: 10,
                      background: 'var(--accent-soft)', color: 'var(--accent)',
                    }}>{t('settings.skillMarket.installed')}</span>
                  )}
                </div>
                <div style={{ fontSize: 11.5, color: 'var(--text-muted)', marginTop: 2 }}>{m.description || ''}</div>
                {m.triggers && m.triggers.length > 0 && (
                  <div style={{ fontSize: 10, color: 'var(--text-muted)', marginTop: 4 }}>
                    {t('settings.skillMarket.triggers')}: {m.triggers.join('、')}
                  </div>
                )}
              </div>
              <button
                disabled={busy === m.name}
                onClick={() => m.installed ? uninstall(m.name) : install(m.name)}
                style={{
                  flexShrink: 0, padding: '7px 16px', borderRadius: 6, fontSize: 11.5,
                  cursor: busy === m.name ? 'default' : 'pointer',
                  border: '1px solid ' + (m.installed ? 'var(--border-color)' : 'var(--accent)'),
                  background: m.installed ? 'transparent' : 'var(--accent)',
                  color: m.installed ? 'var(--text-primary)' : '#fff',
                }}
              >
                {busy === m.name ? '…' : m.installed ? t('settings.skillMarket.uninstall') : t('settings.skillMarket.install')}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default SkillMarketPage;
