import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { RefreshCw, Search, Sparkles, Zap, Shield } from 'lucide-react';

const ModelsPage: React.FC = () => {
  const { t } = useTranslation();
  const { models, selectedModel, setSelectedModel, refreshModels } = useAppStore();
  const [search, setSearch] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const filtered = models.filter((m) =>
    m.name.toLowerCase().includes(search.toLowerCase()) ||
    m.provider.toLowerCase().includes(search.toLowerCase()) ||
    m.id.toLowerCase().includes(search.toLowerCase())
  );

  const providers = Array.from(new Set(models.map((m) => m.provider)));

  const handleRefresh = async () => {
    setRefreshing(true);
    await refreshModels();
    setTimeout(() => setRefreshing(false), 500);
  };

  const providerIcons: Record<string, React.ReactNode> = {
    deepseek: <Zap size={14} />,
    zhipu: <Sparkles size={14} />,
    openrouter: <Shield size={14} />,
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '12px 20px', borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <h2 style={{ fontSize: 15, fontWeight: 500, color: 'var(--text-primary)' }}>
          {t('models.title')}
        </h2>
        <button
          onClick={handleRefresh}
          disabled={refreshing}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            background: 'var(--bg-tertiary)', border: '1px solid var(--border-color)',
            color: 'var(--text-secondary)', padding: '6px 12px',
            borderRadius: 6, cursor: 'pointer', fontSize: 12,
          }}
        >
          <RefreshCw size={14} style={{ animation: refreshing ? 'spin 1s linear infinite' : 'none' }} />
          {t('models.refresh')}
        </button>
      </div>

      {/* Search */}
      <div style={{ padding: '12px 20px' }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          background: 'var(--bg-primary)', borderRadius: 8,
          border: '1px solid var(--border-color)', padding: '6px 12px',
        }}>
          <Search size={14} color="var(--text-muted)" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('models.search')}
            style={{
              flex: 1, background: 'transparent', border: 'none',
              color: 'var(--text-primary)', outline: 'none', fontSize: 13,
            }}
          />
        </div>
      </div>

      {/* Model list grouped by provider */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '0 20px 20px' }}>
        {providers.map((provider) => {
          const providerModels = filtered.filter((m) => m.provider === provider);
          if (providerModels.length === 0) return null;

          return (
            <div key={provider} style={{ marginBottom: 20 }}>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 6,
                marginBottom: 8, color: 'var(--text-secondary)',
                fontSize: 12, fontWeight: 500,
              }}>
                {providerIcons[provider]}
                <span>{provider}</span>
                <span style={{ color: 'var(--text-muted)' }}>
                  ({providerModels.length})
                </span>
              </div>

              {providerModels.map((model) => (
                <button
                  key={model.id}
                  onClick={() => setSelectedModel(model.id)}
                  style={{
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                    width: '100%', padding: '10px 14px', marginBottom: 4,
                    background: model.id === selectedModel ? 'var(--bg-tertiary)' : 'var(--bg-secondary)',
                    border: model.id === selectedModel
                      ? '1px solid var(--accent)'
                      : '1px solid var(--border-color)',
                    borderRadius: 8, cursor: 'pointer', color: 'var(--text-primary)',
                    textAlign: 'left', transition: 'border-color 0.15s',
                  }}
                >
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 500 }}>{model.name}</div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{model.id}</div>
                  </div>
                  <div style={{
                    fontSize: 11, padding: '2px 8px', borderRadius: 4,
                    background: model.plan.includes('free')
                      ? 'rgba(129,199,132,0.15)'
                      : 'rgba(79,195,247,0.1)',
                    color: model.plan.includes('free') ? 'var(--success)' : 'var(--accent)',
                  }}>
                    {model.plan.includes('free') ? t('models.free') : model.plan}
                  </div>
                </button>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default ModelsPage;
