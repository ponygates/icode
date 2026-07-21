import React, { useState } from 'react';
import { useAppStore, Model } from '../stores/appStore';
import { ArrowLeftRight, Zap, DollarSign, Brain, Eye, Maximize2 } from 'lucide-react';

const card: React.CSSProperties = {
  background: 'var(--bg-secondary)',
  borderRadius: 10,
  border: '1px solid var(--border-color)',
  padding: 16,
};

const label: React.CSSProperties = {
  fontSize: 11,
  color: 'var(--text-muted)',
  marginBottom: 2,
};

const value: React.CSSProperties = {
  fontSize: 14,
  fontWeight: 500,
  color: 'var(--text-primary)',
};

const ModelCompare: React.FC = () => {
  const { models } = useAppStore();
  const [left, setLeft] = useState<string>(models[0]?.id || '');
  const [right, setRight] = useState<string>(models[1]?.id || '');

  const leftModel = models.find((m) => m.id === left);
  const rightModel = models.find((m) => m.id === right);

  const features: { key: string; icon: React.ReactNode; label: string; get: (m: Model) => string }[] = [
    { key: 'provider', icon: <Zap size={14} />, label: '提供商', get: (m) => m.provider },
    { key: 'plan', icon: <DollarSign size={14} />, label: '方案', get: (m) => {
      const plans = m.plans || [];
      const inputP = plans.find((p: any) => p.type === 'input' || (p.name && /input|prompt/i.test(p.name)));
      const outputP = plans.find((p: any) => p.type === 'output' || (p.name && /output|completion/i.test(p.name)));
      const ip = inputP?.inputPrice || 0;
      const op = outputP?.outputPrice || 0;
      if (ip || op) {
        return `\xA5${(ip + op).toFixed(2)}/1M`;
      }
      return m.plan;
    }},
    { key: 'capabilities', icon: <Brain size={14} />, label: '能力', get: (m) => {
      const names = (Array.isArray(m.plans) ? m.plans.map((p: any) => p.name || '').join(' ') : '') + ' ' + (m.name || '');
      const lower = names.toLowerCase();
      if (lower.includes('reasoning') || lower.includes('pro')) return '推理';
      if (lower.includes('flash') || lower.includes('turbo')) return '快速';
      return '通用';
    }},
    { key: 'vision', icon: <Eye size={14} />, label: '视觉', get: (m) => {
      const id = (m.id || '').toLowerCase();
      return (id.includes('vision') || id.includes('4o') || id.includes('gemini') || id.includes('claude'))
        ? '✅' : '—';
    }},
    { key: 'ctx', icon: <Maximize2 size={14} />, label: '上下文', get: (m) => {
      const id = (m.id || '').toLowerCase();
      if (id.includes('1m') || id.includes('1000k')) return '1M';
      if (id.includes('200k')) return '200K';
      if (id.includes('128k')) return '128K';
      if (id.includes('32k')) return '32K';
      if (id.includes('8k')) return '8K';
      return '128K';
    }},
  ];

  const uniqueProviders = Array.from(new Set(models.map((m) => m.provider)));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      <div style={{
        padding: '12px 20px', borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)', display: 'flex',
        alignItems: 'center', gap: 10,
      }}>
        <ArrowLeftRight size={16} />
        <span style={{ fontSize: 15, fontWeight: 500, color: 'var(--text-primary)' }}>
          模型对比
        </span>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
        {/* Model selectors */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr auto 1fr', gap: 16, marginBottom: 20, alignItems: 'center' }}>
          <div style={card}>
            <div style={label}>左侧模型</div>
            <select
              value={left}
              onChange={(e) => setLeft(e.target.value)}
              style={{
                width: '100%', marginTop: 6, padding: '8px 10px', fontSize: 13,
                borderRadius: 6, border: '1px solid var(--border-color)',
                background: 'var(--bg-primary)', color: 'var(--text-primary)',
              }}
            >
              {uniqueProviders.map((p) => (
                <optgroup key={p} label={p}>
                  {models.filter((m) => m.provider === p).map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>

          <div style={{ fontSize: 20, color: 'var(--text-muted)' }}>VS</div>

          <div style={card}>
            <div style={label}>右侧模型</div>
            <select
              value={right}
              onChange={(e) => setRight(e.target.value)}
              style={{
                width: '100%', marginTop: 6, padding: '8px 10px', fontSize: 13,
                borderRadius: 6, border: '1px solid var(--border-color)',
                background: 'var(--bg-primary)', color: 'var(--text-primary)',
              }}
            >
              {uniqueProviders.map((p) => (
                <optgroup key={p} label={p}>
                  {models.filter((m) => m.provider === p).map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>
        </div>

        {leftModel && rightModel && (
          <div style={{ ...card, marginBottom: 16 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 0 }}>
              {/* Header row */}
              <div style={{ borderBottom: '1px solid var(--border-color)', padding: '10px 12px' }}>
                <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>{leftModel.name}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{leftModel.id}</div>
              </div>
              <div style={{ borderBottom: '1px solid var(--border-color)', padding: '10px 12px' }}>
                <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>{rightModel.name}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{rightModel.id}</div>
              </div>

              {/* Feature rows */}
              {features.map((f) => {
                const lv = f.get(leftModel);
                const rv = f.get(rightModel);
                const same = lv === rv;
                return (
                  <React.Fragment key={f.key}>
                    <div style={{
                      padding: '10px 12px', borderBottom: '1px solid var(--border-color)',
                      display: 'flex', alignItems: 'center', gap: 6,
                      color: same ? 'var(--text-primary)' : 'var(--accent)',
                      fontWeight: same ? 400 : 500,
                    }}>
                      {f.icon}
                      <div>
                        <div style={label}>{f.label}</div>
                        <div style={value}>{lv}</div>
                      </div>
                    </div>
                    <div style={{
                      padding: '10px 12px', borderBottom: '1px solid var(--border-color)',
                      display: 'flex', alignItems: 'center', gap: 6,
                      color: same ? 'var(--text-primary)' : 'var(--accent)',
                      fontWeight: same ? 400 : 500,
                    }}>
                      {f.icon}
                      <div>
                        <div style={label}>{f.label}</div>
                        <div style={value}>{rv}</div>
                      </div>
                    </div>
                  </React.Fragment>
                );
              })}
            </div>
          </div>
        )}

        {/* Model list overview */}
        <div style={card}>
          <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--text-primary)', marginBottom: 12 }}>
            全部模型 ({models.length})
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 8 }}>
            {models.map((m) => (
              <div key={m.id} style={{
                padding: '8px 10px', borderRadius: 6,
                background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                fontSize: 12,
              }}>
                <div style={{ color: 'var(--text-primary)', fontWeight: 500 }}>{m.name}</div>
                <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>{m.provider} · {m.plan}</div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};

export default ModelCompare;