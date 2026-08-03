import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { BarChart, TrendingUp, Cpu, Globe2 } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

interface RoundStat {
  turn: number;
  prompt: number;
  completion: number;
  cache_hit: number;
  cost: number;
  duration: number;
}

interface Stats {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cache_hit_tokens: number;
  cache_write_tokens: number;
  cache_hit_rate: number;
  estimated_cost: number;
  estimated_saved_cost: number;
  compactions_done: number;
  tokens_saved: number;
  rounds: RoundStat[];
}

interface DayAgg {
  day: string;
  tokens_saved: number;
  saved_cost: number;
}

interface GlobalAgg {
  sessions: number;
  total_tokens_saved: number;
  total_cache_hit_tokens: number;
  total_cost: number;
  total_saved_cost: number;
  avg_cache_hit_rate: number;
  trend: DayAgg[];
}

const card: React.CSSProperties = {
  background: 'var(--bg-secondary)', borderRadius: 10,
  border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
};
const label: React.CSSProperties = {
  fontSize: 11, color: 'var(--text-muted)', marginBottom: 2,
};
const value: React.CSSProperties = {
  fontSize: 16, fontWeight: 600, color: 'var(--text-primary)',
};

const AnalyticsPage: React.FC = () => {
  const { t } = useTranslation();
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const backendUrl = useAppStore(s => s.backendUrl);
  const [view, setView] = useState<'session' | 'global'>('session');
  const [stats, setStats] = useState<Stats | null>(null);
  const [global, setGlobal] = useState<GlobalAgg | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      setLoading(true);
      let url: string | null = null;
      let sessionId: string | null = activeSessionId;
      if (window.icode?.getActiveSession) {
        sessionId = await window.icode.getActiveSession();
      }
      if (window.icode?.getBackendURL) {
        url = await window.icode.getBackendURL();
      }
      if (!url) url = backendUrl || '';
      if (!sessionId) sessionId = activeSessionId || '';

      try {
        if (view === 'global') {
          if (url) {
            const res = await fetch(`${url}/api/analytics/global`);
            if (res.ok) setGlobal(await res.json());
          }
          setStats(null);
        } else {
          if (url && sessionId) {
            const res = await fetch(`${url}/api/analytics/${sessionId}`);
            if (res.ok) setStats(await res.json());
          }
          setGlobal(null);
        }
      } catch { /* ignore */ }
      setLoading(false);
    })();
  }, [view, activeSessionId, backendUrl]);

  const fmt = (n: number) => n.toLocaleString();

  if (loading) return <div style={{ padding: 24, color: 'var(--text-muted)' }}>{t('analytics.loading')}</div>;

  const tabBar = (
    <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
      {(['session', 'global'] as const).map((k) => (
        <button
          key={k}
          onClick={() => setView(k)}
          style={{
            padding: '6px 14px', borderRadius: 8, fontSize: 12, fontWeight: 500,
            cursor: 'pointer', border: '1px solid var(--border-color)',
            background: view === k ? 'var(--accent)' : 'var(--bg-secondary)',
            color: view === k ? '#fff' : 'var(--text-primary)',
          }}
        >
          {k === 'session' ? t('analytics.title') : t('analytics.globalTitle')}
        </button>
      ))}
    </div>
  );

  return (
    <div style={{ padding: 24 }}>
      {tabBar}

      {view === 'global' ? (
        // ── Global dashboard ──
        global ? (() => {
          const gCachePct = (global.avg_cache_hit_rate * 100).toFixed(1);
          const maxDay = global.trend.reduce((m, d) => Math.max(m, d.tokens_saved), 1);
          return (
            <>
              <h2 style={{ color: 'var(--text-primary)', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
                <Globe2 size={20} /> {t('analytics.globalTitle')}
                <span style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 400 }}>
                  ({global.sessions} {t('analytics.sessions')})
                </span>
              </h2>

              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12, marginBottom: 16 }}>
                <div style={card}>
                  <div style={label}>{t('analytics.totalSavedTokens')}</div>
                  <div style={{ ...value, color: 'var(--success)' }}>{fmt(global.total_tokens_saved)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.totalSavedAmount')}</div>
                  <div style={{ ...value, color: 'var(--success)' }}>¥{global.total_saved_cost.toFixed(2)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.avgCacheRate')}</div>
                  <div style={{ ...value, color: parseFloat(gCachePct) > 60 ? 'var(--success)' : 'var(--warning)' }}>{gCachePct}%</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.totalCacheHits')}</div>
                  <div style={value}>{fmt(global.total_cache_hit_tokens)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.totalCost')}</div>
                  <div style={value}>¥{global.total_cost.toFixed(2)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.sessions')}</div>
                  <div style={value}>{global.sessions}</div>
                </div>
              </div>

              <div style={card}>
                <h3 style={{ color: 'var(--text-primary)', fontSize: 14, marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
                  <TrendingUp size={16} /> {t('analytics.dailyTrend')}
                </h3>
                {global.trend.length === 0 ? (
                  <div style={{ color: 'var(--text-muted)', fontSize: 13, padding: 8 }}>{t('analytics.noRounds')}</div>
                ) : (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {global.trend.map((d) => (
                      <div key={d.day} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <div style={{ width: 84, fontSize: 11, color: 'var(--text-muted)', flexShrink: 0 }}>{d.day}</div>
                        <div style={{ flex: 1, height: 14, background: 'var(--bg-primary)', borderRadius: 4, overflow: 'hidden' }}>
                          <div style={{
                            width: `${Math.max(2, (d.tokens_saved / maxDay) * 100)}%`,
                            height: '100%',
                            background: 'linear-gradient(90deg, var(--accent), var(--success))',
                          }} />
                        </div>
                        <div style={{ width: 110, textAlign: 'right', fontSize: 11, color: 'var(--text-primary)', flexShrink: 0 }}>
                          {fmt(d.tokens_saved)} · ¥{d.saved_cost.toFixed(2)}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              <div style={card}>
                <p style={{ color: 'var(--text-muted)', fontSize: 11, lineHeight: 1.6 }}>
                  <Cpu size={12} style={{ verticalAlign: 'middle', marginRight: 4 }} />
                  {t('analytics.cacheHint')}
                </p>
              </div>
            </>
          );
        })() : (
          <div style={card}><p style={{ color: 'var(--text-muted)' }}>{t('analytics.globalNoData')}</p></div>
        )
      ) : (
        // ── Per-session dashboard ──
        stats ? (() => {
          const cachePct = (stats.cache_hit_rate * 100).toFixed(1);
          const savedCost = stats.estimated_saved_cost; // computed server-side, reliable
          return (
            <>
              <h2 style={{ color: 'var(--text-primary)', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
                <BarChart size={20} /> {t('analytics.title')}
              </h2>

              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12, marginBottom: 16 }}>
                <div style={card}>
                  <div style={label}>{t('analytics.totalTokens')}</div>
                  <div style={value}>{fmt(stats.total_tokens)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.cacheRate')}</div>
                  <div style={{ ...value, color: parseFloat(cachePct) > 60 ? 'var(--success)' : 'var(--warning)' }}>{cachePct}%</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.estCost')}</div>
                  <div style={value}>¥{stats.estimated_cost.toFixed(4)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.compactions')}</div>
                  <div style={value}>{stats.compactions_done}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.savedTokens')}</div>
                  <div style={{ ...value, color: 'var(--success)' }}>{fmt(stats.tokens_saved)}</div>
                </div>
                <div style={card}>
                  <div style={label}>{t('analytics.savedAmount')}</div>
                  <div style={{ ...value, color: 'var(--success)' }}>¥{savedCost.toFixed(4)}</div>
                </div>
              </div>

              <div style={card}>
                <h3 style={{ color: 'var(--text-primary)', fontSize: 14, marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
                  <TrendingUp size={16} /> {t('analytics.roundDetails')}
                </h3>
                {stats.rounds.length === 0 ? (
                  <div style={{ color: 'var(--text-muted)', fontSize: 13, padding: 8 }}>{t('analytics.noRounds')}</div>
                ) : (
                  <div style={{ overflowX: 'auto' }}>
                    <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                      <thead>
                        <tr style={{ color: 'var(--text-muted)', borderBottom: '1px solid var(--border-color)' }}>
                          <th style={thStyle}>#</th>
                          <th style={thStyle}>Prompt</th>
                          <th style={thStyle}>Completion</th>
                          <th style={thStyle}>Cache Hit</th>
                          <th style={thStyle}>Cost</th>
                          <th style={thStyle}>Duration</th>
                        </tr>
                      </thead>
                      <tbody>
                        {stats.rounds.map((r, i) => (
                          <tr key={i} style={{ borderBottom: '1px solid var(--border-color)', color: 'var(--text-primary)' }}>
                            <td style={tdStyle}>{r.turn > 0 ? r.turn : 'C'}</td>
                            <td style={tdStyle}>{fmt(r.prompt)}</td>
                            <td style={tdStyle}>{fmt(r.completion)}</td>
                            <td style={{ ...tdStyle, color: r.cache_hit > 0 ? 'var(--success)' : 'var(--text-muted)' }}>{fmt(r.cache_hit)}</td>
                            <td style={tdStyle}>¥{r.cost.toFixed(4)}</td>
                            <td style={tdStyle}>{(r.duration / 1e9).toFixed(1)}s</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>

              <div style={card}>
                <p style={{ color: 'var(--text-muted)', fontSize: 11, lineHeight: 1.6 }}>
                  <Cpu size={12} style={{ verticalAlign: 'middle', marginRight: 4 }} />
                  {t('analytics.cacheHint')}
                </p>
              </div>
            </>
          );
        })() : (
          <div style={card}><p style={{ color: 'var(--text-muted)' }}>{t('analytics.noData')}</p></div>
        )
      )}
    </div>
  );
};

const thStyle: React.CSSProperties = { padding: '6px 8px', textAlign: 'right', fontWeight: 400 };
const tdStyle: React.CSSProperties = { padding: '5px 8px', textAlign: 'right', whiteSpace: 'nowrap' };

export default AnalyticsPage;
