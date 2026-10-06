import React, { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { useAppStore } from '../stores/appStore';
import { Search, ArrowLeft, Globe } from 'lucide-react';

// Standalone skill market page (D7): the market browser previously lived as a
// tab inside the Settings modal. This route gives it full-page breathing room
// with a search filter over name/description/triggers, mirroring WorkBuddy's
// dedicated market surface.

interface MarketSkill {
  name: string;
  description?: string;
  category?: string; // coding | office | general — server groups the market by it
  triggers?: string[];
  installed?: boolean;
}

// A skill discovered inside a remote source (A2). `path` is the repo-internal
// path the server returned from the list call and is echoed back on install.
interface RemoteSkill extends MarketSkill {
  path: string;
}

// A saved remote source entry (A3), persisted in ~/.icode/skill_sources.json.
interface SkillSource {
  source: string;
  alias?: string;
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

  // Remote source (A2): probe a GitHub repo or a direct SKILL.md URL, then
  // install skills from the discovered listing.
  const [remoteSource, setRemoteSource] = useState('');
  const [remoteSkills, setRemoteSkills] = useState<RemoteSkill[] | null>(null);
  const [remoteLoading, setRemoteLoading] = useState(false);
  const [remoteError, setRemoteError] = useState('');
  const [remoteBusy, setRemoteBusy] = useState<string | null>(null);
  const [sources, setSources] = useState<SkillSource[]>([]);

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
    loadSources();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [backendUrl]);

  const loadSources = async () => {
    if (!backendUrl) return;
    try {
      const r = await fetch(`${backendUrl}/api/skills/sources`);
      if (r.ok) { const d = await r.json(); setSources(d.sources || []); }
    } catch {}
  };

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

  // Probe a remote skill source: GitHub repo (owner/repo), GitHub tree URL or
  // a direct SKILL.md URL. The server handles marketplace.json / skills-dir
  // discovery and returns the installable listing. Takes the source as a
  // parameter (not from state) so chips can probe without waiting for a
  // state update to land.
  const probeSourceOf = async (rawSource: string) => {
    if (!backendUrl) return;
    const source = rawSource.trim();
    if (!source) return;
    setRemoteLoading(true);
    setRemoteError('');
    setRemoteSkills(null);
    try {
      const r = await fetch(`${backendUrl}/api/skills/source/list`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source }),
      });
      const d = await r.json().catch(() => ({} as Record<string, unknown>));
      if (r.ok) {
        setRemoteSkills((d.skills || []) as RemoteSkill[]);
        if (!d.skills || (d.skills as RemoteSkill[]).length === 0) {
          setRemoteError(t('settings.skillMarket.remoteNotFound'));
        }
      } else {
        setRemoteError(String(d.error || 'failed'));
      }
    } catch {
      setRemoteError('failed');
    }
    setRemoteLoading(false);
  };

  const probeSource = () => probeSourceOf(remoteSource);

  // Install one skill from the probed remote source. Direct-URL sources
  // ignore `path` server-side.
  const installRemote = async (skill: RemoteSkill) => {
    if (!backendUrl) return;
    setRemoteBusy(skill.name);
    setRemoteError('');
    try {
      const r = await fetch(`${backendUrl}/api/skills/source/install`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source: remoteSource.trim(), path: skill.path || '' }),
      });
      const d = await r.json().catch(() => ({} as Record<string, unknown>));
      if (r.ok) {
        setRemoteSkills((prev) => prev
          ? prev.map((s) => s.name === skill.name ? { ...s, installed: true } : s)
          : prev);
        setNotice(`${String(d.installed || skill.name)} ✓`);
        // Refresh the built-in market so the installed badge stays truthful
        // when a remote skill shadows a catalog entry.
        loadMarket();
      } else {
        setRemoteError(String(d.error || 'failed'));
      }
    } catch {
      setRemoteError('failed');
    }
    setRemoteBusy(null);
  };

  // Save the current input as a persistent source, then probe it right away
  // (saving implies interest, and probing gives immediate feedback).
  const saveSource = async () => {
    if (!backendUrl) return;
    const source = remoteSource.trim();
    if (!source) return;
    try {
      const r = await fetch(`${backendUrl}/api/skills/sources`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source }),
      });
      if (r.ok) {
        await loadSources();
        probeSourceOf(source);
      } else {
        const d = await r.json().catch(() => ({} as Record<string, unknown>));
        setRemoteError(String(d.error || 'failed'));
      }
    } catch {
      setRemoteError('failed');
    }
  };

  const removeSource = async (source: string) => {
    if (!backendUrl) return;
    try {
      const r = await fetch(`${backendUrl}/api/skills/sources/${encodeURIComponent(source)}`, { method: 'DELETE' });
      if (r.ok) setSources((prev) => prev.filter((s) => s.source !== source));
    } catch {}
  };

  const q = query.trim().toLowerCase();
  const filtered = market.filter((m) =>
    !q || m.name.toLowerCase().includes(q)
      || (m.description || '').toLowerCase().includes(q)
      || (m.triggers || []).some((tr) => tr.toLowerCase().includes(q)),
  );

  // Group the market by category (coding / office / general), keeping the
  // display stable regardless of the order the server returns.
  const catOrder = ['coding', 'office', 'general'];
  const catLabel = (c: string) =>
    c === 'coding' ? t('settings.skillMarket.catCoding')
      : c === 'office' ? t('settings.skillMarket.catOffice')
        : t('settings.skillMarket.catGeneral');
  const grouped = catOrder
    .map((cat) => ({ cat, items: filtered.filter((m) => (m.category || 'general') === cat) }))
    .filter((g) => g.items.length > 0);

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
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          {grouped.map((g) => (
            <div key={g.cat} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--text-primary)', letterSpacing: '0.04em', textTransform: 'uppercase' }}>{catLabel(g.cat)}</span>
                <span style={{ fontSize: 10.5, color: 'var(--text-muted)' }}>{g.items.length}</span>
                <div style={{ flex: 1, height: '0.5px', background: 'var(--border-color)' }} />
              </div>
              {g.items.map((m) => (
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
          ))}
        </div>
      )}

      {/* Remote source (A2): install skills from a GitHub repo or a direct
          SKILL.md URL — compatible with the Claude skill ecosystem. */}
      <div style={{
        marginTop: 32, paddingTop: 22, borderTop: '0.5px solid var(--border-color)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <Globe size={14} style={{ color: 'var(--accent)', flexShrink: 0 }} />
          <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--text-primary)' }}>
            {t('settings.skillMarket.remoteTitle')}
          </span>
        </div>
        <div style={{ fontSize: 11.5, color: 'var(--text-muted)', marginBottom: 12 }}>
          {t('settings.skillMarket.remoteDesc')}
        </div>

        {/* Saved source chips: click to probe, × to forget. */}
        {sources.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 10 }}>
            {sources.map((s) => (
              <span
                key={s.source}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 6,
                  padding: '3px 6px 3px 10px', borderRadius: 12, fontSize: 11,
                  background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
                  color: 'var(--text-primary)',
                }}
              >
                <button
                  onClick={() => { setRemoteSource(s.source); setRemoteSkills(null); setRemoteError(''); probeSourceOf(s.source); }}
                  title={s.source}
                  style={{
                    background: 'none', border: 'none', padding: 0, cursor: 'pointer',
                    color: 'var(--text-primary)', fontSize: 11,
                  }}
                >
                  {s.alias || s.source}
                </button>
                <button
                  onClick={() => removeSource(s.source)}
                  title={t('settings.skillMarket.sourceRemove')}
                  style={{
                    background: 'none', border: 'none', padding: 0, cursor: 'pointer',
                    color: 'var(--text-muted)', fontSize: 11, lineHeight: 1,
                  }}
                >×</button>
              </span>
            ))}
          </div>
        )}

        <div style={{ display: 'flex', gap: 8 }}>
          <input
            value={remoteSource}
            onChange={(e) => setRemoteSource(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') probeSource(); }}
            placeholder={t('settings.skillMarket.remotePlaceholder')}
            style={{
              flex: 1, padding: '8px 12px', borderRadius: 8,
              border: '0.5px solid var(--border-color)', background: 'var(--bg-tertiary)',
              color: 'var(--text-primary)', fontSize: 12.5, outline: 'none',
            }}
          />
          <button
            onClick={saveSource}
            disabled={!remoteSource.trim()}
            title={t('settings.skillMarket.sourceSave')}
            style={{
              flexShrink: 0, padding: '8px 14px', borderRadius: 8, fontSize: 12,
              cursor: remoteSource.trim() ? 'pointer' : 'default',
              border: '0.5px solid var(--border-color)', background: 'transparent',
              color: 'var(--text-primary)',
            }}
          >＋</button>
          <button
            onClick={probeSource}
            disabled={remoteLoading || !remoteSource.trim()}
            style={{
              flexShrink: 0, padding: '8px 18px', borderRadius: 8, fontSize: 12,
              cursor: remoteLoading || !remoteSource.trim() ? 'default' : 'pointer',
              border: 'none', background: 'var(--accent)', color: '#fff',
            }}
          >
            {remoteLoading ? t('settings.skillMarket.remoteProbing') : t('settings.skillMarket.remoteProbe')}
          </button>
        </div>

        {remoteError && (
          <div style={{ fontSize: 11.5, color: '#e5484d', marginTop: 10 }}>{remoteError}</div>
        )}

        {remoteSkills && remoteSkills.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 12 }}>
            {remoteSkills.map((s) => (
              <div key={s.name} style={cardStyle}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                    {s.name}
                    {s.installed && (
                      <span style={{
                        marginLeft: 8, fontSize: 10, padding: '1px 8px', borderRadius: 10,
                        background: 'var(--accent-soft)', color: 'var(--accent)',
                      }}>{t('settings.skillMarket.installed')}</span>
                    )}
                  </div>
                  <div style={{ fontSize: 11.5, color: 'var(--text-muted)', marginTop: 2 }}>{s.description || ''}</div>
                </div>
                <button
                  disabled={remoteBusy === s.name}
                  onClick={() => installRemote(s)}
                  style={{
                    flexShrink: 0, padding: '6px 16px', borderRadius: 6, fontSize: 11.5,
                    cursor: remoteBusy === s.name ? 'default' : 'pointer',
                    border: '1px solid ' + (s.installed ? 'var(--border-color)' : 'var(--accent)'),
                    background: s.installed ? 'transparent' : 'var(--accent)',
                    color: s.installed ? 'var(--text-primary)' : '#fff',
                  }}
                >
                  {remoteBusy === s.name ? '…' : s.installed ? t('settings.skillMarket.installed') : t('settings.skillMarket.install')}
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

export default SkillMarketPage;
