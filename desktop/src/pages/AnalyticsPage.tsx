import React, { useEffect, useState } from 'react';
import { BarChart, TrendingUp, DollarSign, Cpu } from 'lucide-react';
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
  compactions_done: number;
  tokens_saved: number;
  rounds: RoundStat[];
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
  const { activeSessionId, backendUrl } = useAppStore();
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      try {
        let url: string | null = null;
        let sessionId: string | null = activeSessionId;
        // Try Electron IPC first
        if (window.icode?.getActiveSession) {
          sessionId = await window.icode.getActiveSession();
        }
        if (window.icode?.getBackendURL) {
          url = await window.icode.getBackendURL();
        }
        // HTTP fallback from store
        if (!url) url = backendUrl || '';
        if (!sessionId) sessionId = activeSessionId || '';
        if (url && sessionId) {
          const res = await fetch(`${url}/api/analytics/${sessionId}`);
          if (res.ok) setStats(await res.json());
        }
      } catch { /* ignore */ }
      setLoading(false);
    })();
  }, [activeSessionId, backendUrl]);

  const fmt = (n: number) => n.toLocaleString();

  if (loading) return <div style={{ padding: 24, color: 'var(--text-muted)' }}>加载中…</div>;
  if (!stats) return (
    <div style={{ padding: 24 }}>
      <h2 style={{ color: 'var(--text-primary)', marginBottom: 12 }}>Token 分析</h2>
      <div style={card}>
        <p style={{ color: 'var(--text-muted)' }}>尚无数据。发送一些消息后再来查看。</p>
      </div>
    </div>
  );

  const cachePct = (stats.cache_hit_rate * 100).toFixed(1);
  const savedCost = ((stats.estimated_cost / (1 - stats.cache_hit_rate + 0.001)) - stats.estimated_cost).toFixed(4);

  return (
    <div style={{ padding: 24 }}>
      <h2 style={{ color: 'var(--text-primary)', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
        <BarChart size={20} /> Token 分析
      </h2>

      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12, marginBottom: 16 }}>
        <div style={card}>
          <div style={label}>总 Token</div>
          <div style={value}>{fmt(stats.total_tokens)}</div>
        </div>
        <div style={card}>
          <div style={label}>缓存命中率</div>
          <div style={{ ...value, color: parseFloat(cachePct) > 60 ? 'var(--success)' : 'var(--warning)' }}>
            {cachePct}%
          </div>
        </div>
        <div style={card}>
          <div style={label}>预估成本</div>
          <div style={value}>¥{stats.estimated_cost.toFixed(4)}</div>
        </div>
        <div style={card}>
          <div style={label}>已压缩次数</div>
          <div style={value}>{stats.compactions_done}</div>
        </div>
        <div style={card}>
          <div style={label}>节省 Token</div>
          <div style={{ ...value, color: 'var(--success)' }}>{fmt(stats.tokens_saved)}</div>
        </div>
        <div style={card}>
          <div style={label}>节省金额 (估算)</div>
          <div style={{ ...value, color: 'var(--success)' }}>¥{savedCost}</div>
        </div>
      </div>

      {/* Per-round breakdown */}
      <div style={card}>
        <h3 style={{ color: 'var(--text-primary)', fontSize: 14, marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
          <TrendingUp size={16} /> 每轮明细
        </h3>
        {stats.rounds.length === 0 ? (
          <div style={{ color: 'var(--text-muted)', fontSize: 13, padding: 8 }}>暂无轮次数据。</div>
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
                    <td style={{ ...tdStyle, color: r.cache_hit > 0 ? 'var(--success)' : 'var(--text-muted)' }}>
                      {fmt(r.cache_hit)}
                    </td>
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
          缓存命中率 = cache_hit_tokens / (prompt_tokens + cache_write_tokens)。压缩轮次标记为 "C"。
        </p>
      </div>
    </div>
  );
};

const thStyle: React.CSSProperties = { padding: '6px 8px', textAlign: 'right', fontWeight: 400 };
const tdStyle: React.CSSProperties = { padding: '5px 8px', textAlign: 'right', whiteSpace: 'nowrap' };

export default AnalyticsPage;
