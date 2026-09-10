import React, { useState, useCallback, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore, RefreshSummary, type Model } from '../stores/appStore';
import { ModelFetchPanel } from '../components/ModelFetchPanel';
import { ModelFetchAllBar } from '../components/ModelFetchAllBar';
import { AddModelModal, type AddModelPayload } from '../components/AddModelModal';
import { useModelFetch } from '../lib/useModelFetch';
import { sortModels, type ModelSort } from '../lib/modelSort';
import {
  RefreshCw, Search, Zap, Sparkles, Shield, Cpu, X, Check,
  Key, Globe, Thermometer, Hash, DollarSign, Layers,
  ChevronRight, Settings, Star, Plus, Trash2, AlertTriangle,
  Download, ListChecks, AlertCircle, ArrowUpDown, Sparkle,
} from 'lucide-react';

// ── Per-model settings modal ──────────────────────────────────────
interface ModelSettings {
  apiKey: string;
  apiBase: string;
  temperature: number;
  maxTokens: number;
  topP: number;
}

const ModelSettingsModal: React.FC<{
  model: Model;
  onClose: () => void;
  onSave: (settings: ModelSettings) => Promise<string> | void;
  onSetDefault: (modelId: string) => void;
  isDefault: boolean;
}> = ({ model, onClose, onSave, onSetDefault, isDefault }) => {
  const [settings, setSettings] = useState<ModelSettings>({
    apiKey: '',
    apiBase: '',
    temperature: 0.7,
    maxTokens: 4096,
    topP: 0.9,
  });
  const { t } = useTranslation();
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  const providerColors: Record<string, string> = {
    deepseek: '#4F46E5', zhipu: '#7C3AED', kimi: '#0891B2',
    openrouter: '#F59E0B', volcengine: '#3B82F6', tencent: '#06B6D4',
    huawei: '#EF4444', scnet: '#10B981', nvidia: '#84CC16',
    anthropic: '#D97706',
  };

  const color = providerColors[model.provider] || '#6366F1';

  return (
    <div onClick={onClose} style={{
      position: 'fixed', inset: 0, zIndex: 1000,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
    }}>
      <div style={{
        background: 'var(--bg-secondary)', borderRadius: 16,
        border: '1px solid var(--border-color)',
        width: 520, maxHeight: '85vh', overflow: 'hidden',
        display: 'flex', flexDirection: 'column',
        boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
      }} onClick={(e) => e.stopPropagation()}>

        {/* Modal Header */}
        <div style={{
          padding: '20px 24px 16px',
          borderBottom: '1px solid var(--border-color)',
          background: `linear-gradient(135deg, ${color}18, transparent)`,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{
                width: 40, height: 40, borderRadius: 10,
                background: `${color}22`, border: `1.5px solid ${color}44`,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <Cpu size={20} color={color} />
              </div>
              <div>
                <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {model.name}
                </div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 1 }}>
                  {model.provider} · {model.id}
                </div>
              </div>
            </div>
            <button onClick={onClose} style={{
              background: 'none', border: 'none', color: 'var(--text-muted)',
              cursor: 'pointer', padding: 4, borderRadius: 6,
            }}>
              <X size={18} />
            </button>
          </div>
        </div>

        {/* Modal Body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px' }}>
          {/* Model info chips */}
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 20 }}>
            {model.contextWindow && (
              <span style={chipStyle}>📐 {model.contextWindow >= 1000 ? `${Math.round(model.contextWindow/1000)}K` : model.contextWindow} ctx</span>
            )}
            {model.maxOutputTokens && (
              <span style={chipStyle}>📤 {model.maxOutputTokens >= 1000 ? `${Math.round(model.maxOutputTokens/1000)}K` : model.maxOutputTokens} max</span>
            )}
            {model.capabilities?.tools && <span style={{ ...chipStyle, background: 'rgba(99,102,241,0.12)', color: '#818CF8' }}>🔧 Tools</span>}
            {model.capabilities?.streaming && <span style={{ ...chipStyle, background: 'rgba(16,185,129,0.12)', color: '#34D399' }}>📡 Streaming</span>}
            {model.supportsVision && <span style={{ ...chipStyle, background: 'rgba(245,158,11,0.12)', color: '#FBBF24' }}>👁 Vision</span>}
            {model.capabilities?.jsonMode && <span style={{ ...chipStyle, background: 'rgba(139,92,246,0.12)', color: '#A78BFA' }}>📋 JSON</span>}
          </div>

          {/* Pricing */}
          {model.plans && model.plans.length > 0 && (
            <div style={{ marginBottom: 20 }}>
              <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
                <DollarSign size={12} style={{ display: 'inline', marginRight: 4 }} /> {t('models.pricing')}
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {model.plans.map((plan, i) => (
                  <div key={i} style={{
                    background: 'var(--bg-primary)', borderRadius: 8,
                    border: '1px solid var(--border-color)', padding: '8px 12px', flex: 1, minWidth: 130,
                  }}>
                    <div style={{ fontSize: 12, fontWeight: 500, color: 'var(--text-primary)' }}>{plan.name}</div>
                    {plan.description && <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{plan.description}</div>}
                    <div style={{ fontSize: 11, color: 'var(--text-secondary)', marginTop: 4 }}>
                      {plan.inputPrice != null && <div>{t('models.priceIn')} ${plan.inputPrice}/M tok</div>}
                      {plan.outputPrice != null && <div>{t('models.priceOut')} ${plan.outputPrice}/M tok</div>}
                      {plan.cachePrice != null && <div style={{ color: 'var(--success)' }}>{t('models.priceCache')} ${plan.cachePrice}/M tok</div>}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* API Configuration */}
          <div style={{ marginBottom: 20 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
              <Key size={12} style={{ display: 'inline', marginRight: 4 }} /> {t('models.apiConfig')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
                  API Key
                </label>
                <input
                  type="password"
                  placeholder={t('models.apiKeyPlaceholder', { provider: model.provider })}
                  value={settings.apiKey}
                  onChange={(e) => setSettings((s) => ({ ...s, apiKey: e.target.value }))}
                  style={inputField}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
                  <Globe size={10} style={{ display: 'inline', marginRight: 4 }} />
                  Base URL（{t('models.keepDefault')}）
                </label>
                <input
                  placeholder="https://api.example.com/v1"
                  value={settings.apiBase}
                  onChange={(e) => setSettings((s) => ({ ...s, apiBase: e.target.value }))}
                  style={inputField}
                />
              </div>
            </div>
          </div>

          {/* Generation Parameters */}
          <div style={{ marginBottom: 20 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
              <Settings size={12} style={{ display: 'inline', marginRight: 4 }} /> {t('models.genParams')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              {/* Temperature */}
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    <Thermometer size={10} style={{ display: 'inline', marginRight: 4 }} />
                    {t('models.temperature')}
                  </label>
                  <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--accent)' }}>{settings.temperature.toFixed(1)}</span>
                </div>
                <input
                  type="range" min={0} max={2} step={0.1}
                  value={settings.temperature}
                  onChange={(e) => setSettings((s) => ({ ...s, temperature: parseFloat(e.target.value) }))}
                  style={{ width: '100%', accentColor: color }}
                />
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 9, color: 'var(--text-muted)' }}>
                  <span>{t('models.tempPrecise')}</span><span>{t('models.tempBalanced')}</span><span>{t('models.tempCreative')}</span>
                </div>
              </div>

              {/* Max Tokens */}
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    <Hash size={10} style={{ display: 'inline', marginRight: 4 }} />
                    {t('models.maxOutputTokens')}
                  </label>
                  <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--accent)' }}>{settings.maxTokens >= 1000 ? `${(settings.maxTokens/1000).toFixed(1)}K` : settings.maxTokens}</span>
                </div>
                <input
                  type="range" min={256} max={model.maxOutputTokens || 128000} step={256}
                  value={settings.maxTokens}
                  onChange={(e) => setSettings((s) => ({ ...s, maxTokens: parseInt(e.target.value) }))}
                  style={{ width: '100%', accentColor: color }}
                />
              </div>

              {/* Top P */}
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    <Layers size={10} style={{ display: 'inline', marginRight: 4 }} />
                    Top P
                  </label>
                  <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--accent)' }}>{settings.topP.toFixed(2)}</span>
                </div>
                <input
                  type="range" min={0} max={1} step={0.05}
                  value={settings.topP}
                  onChange={(e) => setSettings((s) => ({ ...s, topP: parseFloat(e.target.value) }))}
                  style={{ width: '100%', accentColor: color }}
                />
              </div>
            </div>
          </div>
        </div>

        {/* Modal Footer */}
        <div style={{
          padding: '16px 24px', borderTop: '1px solid var(--border-color)',
          display: 'flex', gap: 8, justifyContent: 'flex-end', alignItems: 'center',
        }}>
          {error && (
            <span style={{
              fontSize: 11, color: '#F87171', marginRight: 'auto', maxWidth: 280,
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            }}>
              {error}
            </span>
          )}
          {!isDefault && (
            <button onClick={() => onSetDefault(model.id)} style={{
              padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
              background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
              color: 'var(--text-secondary)', fontWeight: 500,
              display: 'flex', alignItems: 'center', gap: 6,
            }}>
              <Star size={13} /> {t('models.setAsDefault')}
            </button>
          )}
          <button onClick={onClose} style={{
            padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
            background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
            color: 'var(--text-secondary)',
          }}>
            {t('settings.cancel')}
          </button>
          <button onClick={async () => {
            setError('');
            setSaved(false);
            const err = await onSave(settings);
            if (err) { setError(err); return; }
            setSaved(true);
            setTimeout(() => setSaved(false), 2000);
          }} style={{
            padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
            background: saved ? 'var(--success)' : color,
            border: 'none', color: '#fff', fontWeight: 600,
            display: 'flex', alignItems: 'center', gap: 6,
            transition: 'background 0.2s',
          }}>
            {saved ? <><Check size={13} /> {t('models.saved')}</> : t('models.saveSettings')}
          </button>
        </div>
      </div>
    </div>
  );
};

const chipStyle: React.CSSProperties = {
  fontSize: 10, fontWeight: 500, padding: '3px 8px', borderRadius: 6,
  background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)',
};

const inputField: React.CSSProperties = {
  width: '100%', boxSizing: 'border-box', padding: '8px 12px', fontSize: 12,
  borderRadius: 8, border: '1px solid var(--border-color)',
  background: 'var(--bg-primary)', color: 'var(--text-primary)', outline: 'none',
};

// ── Models Page ───────────────────────────────────────────────────

const providerColors: Record<string, string> = {
  deepseek: '#4F46E5', zhipu: '#7C3AED', kimi: '#0891B2',
  openrouter: '#F59E0B', volcengine: '#3B82F6', tencent: '#06B6D4',
  huawei: '#EF4444', scnet: '#10B981', nvidia: '#84CC16',
  anthropic: '#D97706',
};

const providerIcons: Record<string, React.ReactNode> = {
  deepseek: <Zap size={13} />,
  zhipu: <Sparkles size={13} />,
  openrouter: <Shield size={13} />,
};

const ModelsPage: React.FC = () => {
  const { t } = useTranslation();
  const models = useAppStore(s => s.models);
  const selectedModel = useAppStore(s => s.selectedModel);
  const setSelectedModel = useAppStore(s => s.setSelectedModel);
  const refreshModels = useAppStore(s => s.refreshModels);
  const refreshSummary = useAppStore(s => s.refreshSummary);
  const clearRefreshSummary = useAppStore(s => s.clearRefreshSummary);
  const backendUrl = useAppStore(s => s.backendUrl);
  const customModels = useAppStore(s => s.customModels);
  const addCustomModel = useAppStore(s => s.addCustomModel);
  const removeCustomModel = useAppStore(s => s.removeCustomModel);
  const saveProvider = useAppStore(s => s.saveProvider);
  const deleteProvider = useAppStore(s => s.deleteProvider);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<ModelSort>('default');
  const [refreshing, setRefreshing] = useState(false);
  const [selectedSettingsModel, setSelectedSettingsModel] = useState<Model | null>(null);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(new Set());
  // Add-model dialog. `provider` locks the vendor when opened from a card;
  // absent means the free-form global entry (any vendor name).
  const [addTarget, setAddTarget] = useState<{ open: boolean; provider?: string }>({ open: false });
  // Deleting a hand-added model is destructive and has no undo, so the first
  // click arms the button and the second one commits.
  const [armedDelete, setArmedDelete] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [showAddProvider, setShowAddProvider] = useState(false);
  const [newProvider, setNewProvider] = useState({ name: '', apiBase: '', apiKey: '', timeout: '' });
  const [newProviderError, setNewProviderError] = useState('');
  const [deletingProvider, setDeletingProvider] = useState<string | null>(null);
  const [showToast, setShowToast] = useState(false);

  // Live model discovery ("获取模型") — shared with the settings modal.
  const mf = useModelFetch(backendUrl);
  const doFetchModels = (provider: string) =>
    mf.fetchModels(provider, () =>
      setExpandedProviders(prev => new Set(prev).add(provider)));

  useEffect(() => {
    if (refreshSummary && (refreshSummary.totalAdded > 0 || refreshSummary.totalRemoved > 0)) {
      setShowToast(true);
      const timer = setTimeout(() => { setShowToast(false); clearRefreshSummary(); }, 8000);
      return () => clearTimeout(timer);
    }
  }, [refreshSummary]);

  const filtered = models.filter((m) =>
    m.name.toLowerCase().includes(search.toLowerCase()) ||
    m.provider.toLowerCase().includes(search.toLowerCase()) ||
    m.id.toLowerCase().includes(search.toLowerCase())
  );

  // Sort applies after filtering so the two compose predictably.
  const ordered = sortModels(filtered, sort);

  // Include vendors that have no catalogue entry at all (a freshly added
  // custom provider). Without this they would be invisible on the only page
  // that can populate them — and "获取模型" is exactly how you populate one.
  const providers = Array.from(new Set([
    ...ordered.map((m) => m.provider),
    ...(search ? [] : Object.keys(mf.meta)),
  ]));

  const handleRefresh = async () => {
    setRefreshing(true);
    await refreshModels();
    setTimeout(() => setRefreshing(false), 500);
  };

  const toggleProvider = (p: string) => {
    setExpandedProviders((prev) => {
      const next = new Set(prev);
      if (next.has(p)) next.delete(p); else next.add(p);
      return next;
    });
  };

  const handleSaveModelSettings = useCallback(async (modelId: string, settings: ModelSettings): Promise<string> => {
    if (!backendUrl) return 'Backend not connected';
    try {
      const res = await fetch(`${backendUrl}/api/config/key`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          provider: selectedSettingsModel?.provider,
          api_key: settings.apiKey,
          api_base: settings.apiBase,
          model_id: modelId,
          temperature: settings.temperature,
          max_tokens: settings.maxTokens,
          top_p: settings.topP,
        }),
      });
      if (!res.ok) return `HTTP ${res.status}`;
      if (settings.apiKey) await refreshModels();
      return '';
    } catch (e) {
      return String(e);
    }
  }, [backendUrl, selectedSettingsModel, refreshModels]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: 'var(--bg-primary)' }}>
      {/* Modal overlay */}
      {selectedSettingsModel && (
        <ModelSettingsModal
          model={selectedSettingsModel}
          onClose={() => setSelectedSettingsModel(null)}
          onSave={(s) => { handleSaveModelSettings(selectedSettingsModel.id, s); }}
          onSetDefault={(id) => { setSelectedModel(id); }}
          isDefault={selectedSettingsModel.id === selectedModel}
        />
      )}

      {/* Refresh summary toast */}
      {showToast && refreshSummary && (
        <div style={{
          position: 'fixed', top: 20, right: 20, zIndex: 1100,
          background: 'var(--bg-secondary)', borderRadius: 12,
          border: '1px solid var(--border-color)',
          boxShadow: '0 8px 32px rgba(0,0,0,0.3)',
          padding: '14px 20px', maxWidth: 380,
          animation: 'slideIn 0.3s ease',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
              {t('models.refreshResult', '模型刷新结果')}
            </span>
            <button onClick={() => { setShowToast(false); clearRefreshSummary(); }} style={{
              background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: 2,
            }}>
              <X size={14} />
            </button>
          </div>
          <div style={{ display: 'flex', gap: 16, marginBottom: refreshSummary.providers.length > 0 ? 8 : 0 }}>
            {refreshSummary.totalAdded > 0 && (
              <span style={{ fontSize: 12, color: '#34D399', fontWeight: 500 }}>
                +{refreshSummary.totalAdded} {t('models.newModels', '新模型')}
              </span>
            )}
            {refreshSummary.totalRemoved > 0 && (
              <span style={{ fontSize: 12, color: '#FBBF24', fontWeight: 500 }}>
                ⚠ {refreshSummary.totalRemoved} {t('models.deprecatedModels', '已下架')}
              </span>
            )}
          </div>
          {refreshSummary.providers.slice(0, 5).map(p => (
            <div key={p.name} style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
              <strong>{p.name}</strong>:
              {p.added > 0 && ` +${p.added}`}
              {p.removed > 0 && ` ⚠${p.removed}`}
              {p.addedModels.length > 0 && ` (${p.addedModels.slice(0, 3).map(m => m.name || m.id).join(', ')}${p.addedModels.length > 3 ? '...' : ''})`}
            </div>
          ))}
        </div>
      )}

      {/* Header — Reasonix glass-morphism style */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '16px 24px',
        borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 600, color: 'var(--text-primary)', margin: 0 }}>
            {t('models.title')}
          </h2>
          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
            {t('models.modelsCount', { count: filtered.length })} · {t('models.providerCount', { count: providers.length })}
          </div>
        </div>
        <button onClick={handleRefresh} disabled={refreshing}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
            color: 'var(--text-secondary)', padding: '7px 14px',
            borderRadius: 8, cursor: 'pointer', fontSize: 12, fontWeight: 500,
            transition: 'all 0.15s',
          }}
        >
          <RefreshCw size={13} style={{ animation: refreshing ? 'spin 1s linear infinite' : 'none' }} />
          {t('models.refresh')}
        </button>
        <button onClick={() => setAddTarget({ open: true })}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            background: 'linear-gradient(135deg, #6366F1, #8B5CF6)',
            border: 'none', color: '#fff', padding: '7px 14px',
            borderRadius: 8, cursor: 'pointer', fontSize: 12, fontWeight: 500,
            transition: 'all 0.15s',
          }}
        >
          <Plus size={13} />
          {t('models.addCustom')}
        </button>
        <button onClick={() => setShowAddProvider(true)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            background: 'transparent', color: 'var(--text-primary)',
            border: '1px solid var(--border-color)', padding: '7px 14px',
            borderRadius: 8, cursor: 'pointer', fontSize: 12, fontWeight: 500,
            transition: 'all 0.15s',
          }}
        >
          <Plus size={13} />
          {t('models.addProvider')}
        </button>
      </div>

      {/* Search + sort + sweep — list-wide controls live together */}
      <div style={{
        padding: '12px 24px', background: 'var(--bg-primary)',
        display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
      }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10, flex: 1, minWidth: 220,
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: '8px 14px',
          transition: 'border-color 0.15s',
        }}>
          <Search size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
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
          {search && (
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>
              {filtered.length} / {models.length}
            </span>
          )}
        </div>

        {/* Sort — "newly discovered first" surfaces models the vendor ships
            that this build's catalogue has never heard of. */}
        <label style={{
          display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0,
          background: 'var(--bg-secondary)', border: '1px solid var(--border-color)',
          borderRadius: 10, padding: '8px 12px',
        }}>
          <ArrowUpDown size={13} style={{ color: 'var(--text-muted)' }} />
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as ModelSort)}
            title={t('models.sortBy')}
            style={{
              background: 'transparent', border: 'none', outline: 'none',
              color: 'var(--text-primary)', fontSize: 12, cursor: 'pointer',
            }}
          >
            <option value="default">{t('models.sortDefault')}</option>
            <option value="context-desc">{t('models.sortContextDesc')}</option>
            <option value="context-asc">{t('models.sortContextAsc')}</option>
            <option value="new-first">{t('models.sortNewFirst')}</option>
          </select>
        </label>

        <ModelFetchAllBar mf={mf} providers={Object.keys(mf.meta)} />
      </div>

      {/* A failed add/delete must be visible — the previous inline form held its
          error in a modal that no longer exists, so failures vanished. */}
      {deleteError && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          margin: '0 24px 12px', padding: '8px 12px', borderRadius: 8,
          background: 'var(--error-soft, rgba(248,81,73,0.1))',
          border: '1px solid var(--error, #f85149)',
          color: 'var(--error, #f85149)', fontSize: 11.5,
        }}>
          <AlertCircle size={13} style={{ flexShrink: 0 }} />
          <span style={{ flex: 1 }}>{deleteError}</span>
          <span onClick={() => setDeleteError(null)} style={{ cursor: 'pointer', display: 'flex' }}>
            <X size={13} />
          </span>
        </div>
      )}

      {/* Add-model dialog — shared with the settings modal. Opened from a
          vendor card it locks that vendor; opened from the toolbar it accepts
          any vendor name, which is how a model is added to a vendor that does
          not render a card yet. */}
      <AddModelModal
        open={addTarget.open}
        presetProvider={addTarget.provider}
        onClose={() => setAddTarget({ open: false })}
        onSubmit={async (payload) => {
          const err = await addCustomModel({
            id: payload.id,
            name: payload.name,
            provider: payload.provider,
            plan: 'Custom',
            contextWindow: payload.contextWindow,
            maxOutputTokens: payload.maxOutputTokens,
          });
          if (!err) await mf.loadMeta();
          return err;
        }}
      />

      {/* Add Provider Modal (custom OpenAI-compatible vendor) */}
      {showAddProvider && (
        <div onClick={() => setShowAddProvider(false)} style={{
          position: 'fixed', inset: 0, zIndex: 1000,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
        }}>
          <div onClick={(e) => e.stopPropagation()} style={{
            background: 'var(--bg-secondary)', borderRadius: 16,
            border: '1px solid var(--border-color)', width: 460,
            boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
          }}>
            <div style={{ padding: '20px 24px 16px', borderBottom: '1px solid var(--border-color)' }}>
              <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
                <Plus size={16} style={{ display: 'inline', marginRight: 8 }} />
                {t('models.addProvider')}
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>
                {t('models.addProviderHint')}
              </div>
            </div>
            <div style={{ padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 12 }}>
              {newProviderError && (
                <div style={{
                  fontSize: 11, color: '#F87171', background: 'rgba(248,113,113,0.08)',
                  border: '1px solid rgba(248,113,113,0.2)', borderRadius: 8, padding: '8px 12px',
                }}>
                  {newProviderError}
                </div>
              )}
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('models.providerName')} *</label>
                <input value={newProvider.name} onChange={(e) => setNewProvider({...newProvider, name: e.target.value})}
                  placeholder={t('models.providerNameExample')} style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>API Base URL</label>
                <input value={newProvider.apiBase} onChange={(e) => setNewProvider({...newProvider, apiBase: e.target.value})}
                  placeholder="https://api.example.com/v1（{t('models.keepDefault')}）" style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>API Key</label>
                <input type="password" value={newProvider.apiKey} onChange={(e) => setNewProvider({...newProvider, apiKey: e.target.value})}
                  placeholder="sk-..." style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('models.providerTimeout')}</label>
                <input value={newProvider.timeout} onChange={(e) => setNewProvider({...newProvider, timeout: e.target.value})}
                  placeholder="120" style={inputField} />
              </div>
            </div>
            <div style={{ padding: '16px 24px', borderTop: '1px solid var(--border-color)', display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button onClick={() => setShowAddProvider(false)} style={{
                padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                color: 'var(--text-secondary)',
              }}>{t('settings.cancel')}</button>
              <button onClick={async () => {
                const name = newProvider.name.trim();
                if (!name) return;
                const timeoutSec = parseInt(newProvider.timeout, 10);
                const err = await saveProvider(
                  name,
                  newProvider.apiBase.trim(),
                  newProvider.apiKey.trim(),
                  Number.isFinite(timeoutSec) && timeoutSec > 0 ? timeoutSec : 0,
                );
                if (err) {
                  setNewProviderError(err);
                  return;
                }
                setNewProvider({ name: '', apiBase: '', apiKey: '', timeout: '' });
                setNewProviderError('');
                setShowAddProvider(false);
              }} style={{
                padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: '#6366F1', border: 'none', color: '#fff', fontWeight: 600,
                display: 'flex', alignItems: 'center', gap: 6,
              }}>
                <Check size={13} /> {t('models.addProvider')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Provider confirm */}
      {deletingProvider && (
        <div onClick={() => setDeletingProvider(null)} style={{
          position: 'fixed', inset: 0, zIndex: 1001,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
        }}>
          <div onClick={(e) => e.stopPropagation()} style={{
            background: 'var(--bg-secondary)', borderRadius: 16,
            border: '1px solid var(--border-color)', width: 380,
            boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
          }}>
            <div style={{ padding: '20px 24px 8px' }}>
              <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
                <AlertTriangle size={16} style={{ display: 'inline', marginRight: 8, color: '#F87171' }} />
                {t('models.deleteProvider')}
              </div>
            </div>
            <div style={{ padding: '8px 24px 20px', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.6 }}>
              {t('models.deleteProviderConfirm', { name: deletingProvider })}
            </div>
            <div style={{ padding: '16px 24px', borderTop: '1px solid var(--border-color)', display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button onClick={() => setDeletingProvider(null)} style={{
                padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                color: 'var(--text-secondary)',
              }}>{t('settings.cancel')}</button>
              <button onClick={async () => {
                const name = deletingProvider;
                setDeletingProvider(null);
                const err = await deleteProvider(name);
                if (err) setNewProviderError(err);
              }} style={{
                padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: '#EF4444', border: 'none', color: '#fff', fontWeight: 600,
              }}>
                <Trash2 size={13} style={{ display: 'inline', marginRight: 6 }} />
                {t('models.deleteProvider')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Model list — Reasonix-style cards grouped by provider */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '0 24px 24px' }}>
        {providers.map((provider) => {
          const providerModels = ordered.filter((m) => m.provider === provider);
          // An empty vendor is still worth rendering (it needs "获取模型"),
          // but not while searching — there it is just noise.
          if (providerModels.length === 0 && !(mf.meta[provider] && !search)) return null;
          const isExpanded = expandedProviders.has(provider) || search.length > 0;
          const color = providerColors[provider] || '#6366F1';
          const hasActiveInProvider = providerModels.some((m) => m.id === selectedModel);

          return (
            <div key={provider} style={{ marginBottom: 12 }}>
              {/* Provider header */}
              <div
                onClick={() => toggleProvider(provider)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer',
                  padding: '10px 14px', marginBottom: 2,
                  borderRadius: 10, userSelect: 'none',
                  background: hasActiveInProvider ? `${color}10` : 'transparent',
                  border: hasActiveInProvider ? `1px solid ${color}30` : '1px solid transparent',
                  transition: 'all 0.15s',
                }}
              >
                <div style={{
                  width: 28, height: 28, borderRadius: 7,
                  background: `${color}18`, border: `1px solid ${color}30`,
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  flexShrink: 0,
                }}>
                  {providerIcons[provider] || <Cpu size={14} color={color} />}
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                    {provider}
                  </div>
                  <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                    {t('models.modelsCount', { count: providerModels.length })}{hasActiveInProvider ? ' · ' + t('models.active') : ''}
                  </div>
                </div>
                {mf.meta[provider]?.filtered && (
                  <span title={t('models.filteredHint')} style={{
                    fontSize: 9, padding: '2px 6px', borderRadius: 4,
                    background: `${color}14`, color, fontWeight: 500,
                    display: 'flex', alignItems: 'center', gap: 3, flexShrink: 0,
                  }}>
                    <ListChecks size={9} />
                    {t('models.enabledOf', {
                      enabled: mf.meta[provider].enabled,
                      total: mf.meta[provider].models,
                    })}
                  </span>
                )}
                {/* Live catalogue pull — the vendor knows what the key can use,
                    the built-in list is only a snapshot from build time. */}
                <button
                  onClick={(e) => { e.stopPropagation(); doFetchModels(provider); }}
                  disabled={mf.fetching === provider}
                  title={t('models.fetchHint')}
                  style={{
                    fontSize: 10.5, padding: '4px 9px', borderRadius: 6, flexShrink: 0,
                    display: 'flex', alignItems: 'center', gap: 4, fontWeight: 500,
                    background: mf.panel === provider ? `${color}14` : 'transparent',
                    border: `1px solid ${(mf.fetching === provider || mf.panel === provider) ? `${color}60` : 'var(--border-color)'}`,
                    color: (mf.fetching === provider || mf.panel === provider) ? color : 'var(--text-secondary)',
                    cursor: mf.fetching === provider ? 'wait' : 'pointer',
                  }}>
                  {mf.fetching === provider
                    ? <RefreshCw size={11} style={{ animation: 'spin 1s linear infinite' }} />
                    : <Download size={11} />}
                  {mf.fetching === provider ? t('models.fetching') : t('models.fetchModels')}
                </button>
                {providerModels.every((m) => m.custom) && (
                  <span
                    onClick={(e) => { e.stopPropagation(); setDeletingProvider(provider); }}
                    title={t('models.deleteProvider')}
                    style={{ color: 'var(--text-muted)', cursor: 'pointer', display: 'flex', padding: 4, borderRadius: 6 }}
                  >
                    <Trash2 size={13} />
                  </span>
                )}
                <ChevronRight
                  size={14} color="var(--text-muted)"
                  style={{
                    transform: isExpanded ? 'rotate(90deg)' : 'rotate(0deg)',
                    transition: 'transform 0.2s ease',
                  }}
                />
              </div>

              {/* Models under this provider */}
              {isExpanded && (
                <div style={{ paddingLeft: 4, display: 'flex', flexDirection: 'column', gap: 3 }}>
                  {/* Errors that have no panel to live in (fetch failed, etc.). */}
                  {mf.error[provider] && !mf.panel && (
                    <div style={{
                      display: 'flex', alignItems: 'center', gap: 6,
                      padding: '6px 10px', borderRadius: 6, marginBottom: 4,
                      background: 'var(--error-soft)', border: '1px solid var(--error)',
                      color: 'var(--error)', fontSize: 10.5,
                    }}>
                      <AlertCircle size={12} style={{ flexShrink: 0 }} />
                      <span style={{ flex: 1 }}>{mf.error[provider]}</span>
                    </div>
                  )}

                  {/* Live-fetch checklist */}
                  <ModelFetchPanel provider={provider} color={color} mf={mf}
                    onApplied={() => refreshModels()} />

                  {providerModels.length === 0 && mf.panel !== provider && (
                    <div style={{ fontSize: 11, color: 'var(--text-muted)', padding: '8px 12px' }}>
                      {t('models.noModelsYet')}
                    </div>
                  )}

                  {/* Add a model under *this* vendor — the common case is a
                      model the vendor just shipped, so the provider is implied
                      by where the user clicked. */}
                  <button
                    onClick={(e) => { e.stopPropagation(); setAddTarget({ open: true, provider }); }}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 5, marginBottom: 4,
                      alignSelf: 'flex-start',
                      padding: '5px 11px', borderRadius: 7, fontSize: 11,
                      background: 'transparent', border: `1px dashed ${color}50`,
                      color, cursor: 'pointer', fontWeight: 500,
                    }}
                  >
                    <Plus size={11} /> {t('models.addModelTo', { provider })}
                  </button>

                  {providerModels.map((model) => {
                    const isSelected = model.id === selectedModel;
                    const isDeprecated = model.deprecated;
                    return (
                      <div
                        key={model.id}
                        style={{
                          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                          padding: '9px 14px 9px 16px',
                          borderRadius: 8, cursor: 'pointer',
                          background: isDeprecated ? 'rgba(251,191,36,0.06)' : isSelected ? `${color}14` : 'var(--bg-secondary)',
                          border: isDeprecated
                            ? '1px solid rgba(251,191,36,0.2)'
                            : isSelected
                              ? `1px solid ${color}40`
                              : '1px solid transparent',
                          transition: 'all 0.15s',
                          borderLeft: isSelected ? `3px solid ${color}` : isDeprecated ? '3px solid rgba(251,191,36,0.4)' : '3px solid transparent',
                          opacity: isDeprecated ? 0.7 : 1,
                        }}
                      >
                        {/* Left: select model */}
                        <div
                          onClick={() => setSelectedModel(model.id)}
                          style={{ display: 'flex', alignItems: 'center', gap: 10, flex: 1 }}
                        >
                          <div style={{
                            width: 16, height: 16, borderRadius: '50%',
                            border: isSelected ? `2px solid ${color}` : '2px solid var(--border-color)',
                            background: isSelected ? color : 'transparent',
                            flexShrink: 0, transition: 'all 0.15s',
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            {isSelected && <Check size={9} color="#fff" />}
                          </div>
                          <div>
                            <div style={{ fontSize: 12.5, fontWeight: isSelected ? 600 : 400, color: isDeprecated ? 'var(--text-muted)' : 'var(--text-primary)', display: 'flex', alignItems: 'center', gap: 6 }}>
                              {model.name}
                              {isDeprecated && <AlertTriangle size={11} color="#FBBF24" />}
                            </div>
                            <div style={{ fontSize: 10, color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: 5 }}>
                              {model.id}
                              {/* Not in the built-in catalogue — either the vendor
                                  shipped it after this build, or it was added by
                                  hand. Either way it is the flag the
                                  "newly discovered first" sort keys on. */}
                              {model.custom && (
                                <span title={t('models.newModelHint')} style={{
                                  fontSize: 8.5, padding: '1px 5px', borderRadius: 3,
                                  fontWeight: 600, letterSpacing: '0.02em',
                                  background: `${color}18`, color,
                                  display: 'inline-flex', alignItems: 'center', gap: 2,
                                }}>
                                  <Sparkle size={8} />{t('models.newModel')}
                                </span>
                              )}
                            </div>
                          </div>
                        </div>

                        {/* Right: plan badge + deprecated badge + settings button */}
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          {isDeprecated && (
                            <span style={{
                              fontSize: 9, padding: '2px 6px', borderRadius: 4, fontWeight: 500,
                              background: 'rgba(251,191,36,0.12)', color: '#FBBF24',
                            }}>
                              {t('models.deprecated', '已下架')}
                            </span>
                          )}
                          {model.plan && !isDeprecated && (
                            <span style={{
                              fontSize: 10, padding: '2px 7px', borderRadius: 4, fontWeight: 500,
                              background: model.plan.includes('free')
                                ? 'rgba(52,211,153,0.12)'
                                : `${color}14`,
                              color: model.plan.includes('free') ? '#34D399' : color,
                            }}>
                              {model.plan.includes('free') ? t('models.free') : model.plan}
                            </span>
                          )}
                          <button
                            onClick={(e) => { e.stopPropagation(); setSelectedSettingsModel(model); }}
                            title={t('models.customSettings')}
                            style={{
                              width: 28, height: 28, borderRadius: 6,
                              background: 'transparent', border: '1px solid var(--border-color)',
                              color: 'var(--text-muted)', cursor: 'pointer',
                              display: 'flex', alignItems: 'center', justifyContent: 'center',
                              transition: 'all 0.15s',
                            }}
                          >
                            <Settings size={13} />
                          </button>
                          {/* Only hand-added models can be removed — a built-in
                              entry is owned by the provider catalogue. */}
                          {model.custom && (() => {
                            const armed = armedDelete === model.id;
                            return (
                              <button
                                onClick={async (e) => {
                                  e.stopPropagation();
                                  if (!armed) { setArmedDelete(model.id); return; }
                                  setArmedDelete(null);
                                  const err = await removeCustomModel(model.id, model.provider);
                                  if (err) setDeleteError(err);
                                }}
                                title={armed ? t('models.deleteConfirm') : t('models.deleteCustom')}
                                style={{
                                  height: 28, padding: armed ? '0 8px' : 0,
                                  width: armed ? 'auto' : 28, borderRadius: 6,
                                  background: armed ? 'var(--error-soft, rgba(248,81,73,0.12))' : 'transparent',
                                  border: `1px solid ${armed ? 'var(--error, #f85149)' : 'var(--border-color)'}`,
                                  color: armed ? 'var(--error, #f85149)' : 'var(--text-muted)',
                                  cursor: 'pointer', fontSize: 10.5, fontWeight: armed ? 600 : 400,
                                  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4,
                                  transition: 'all 0.15s',
                                }}
                              >
                                <Trash2 size={13} />
                                {armed && t('models.deleteConfirm')}
                              </button>
                            );
                          })()}
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}

        {/* Custom Models Section */}
        {customModels.length > 0 && !search && (
          <div style={{ marginTop: 16, marginBottom: 12 }}>
            <div style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '10px 14px', borderRadius: 10,
              background: 'rgba(99,102,241,0.08)',
              border: '1px solid rgba(99,102,241,0.2)',
            }}>
              <div style={{
                width: 28, height: 28, borderRadius: 7,
                background: 'rgba(99,102,241,0.18)', border: '1px solid rgba(99,102,241,0.3)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <Plus size={14} color="#6366F1" />
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {t('models.customModels')}
                </div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                  {t('models.customModelCount', { count: customModels.length })}
                </div>
              </div>
            </div>
            <div style={{ paddingLeft: 4, display: 'flex', flexDirection: 'column', gap: 3, marginTop: 4 }}>
              {customModels.map((model) => {
                const isSelected = model.id === selectedModel;
                return (
                  <div key={model.id} style={{
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                    padding: '9px 14px 9px 16px', borderRadius: 8,
                    background: isSelected ? 'rgba(99,102,241,0.14)' : 'var(--bg-secondary)',
                    border: isSelected ? '1px solid rgba(99,102,241,0.4)' : '1px solid transparent',
                    borderLeft: isSelected ? '3px solid #6366F1' : '3px solid transparent',
                  }}>
                    <div onClick={() => setSelectedModel(model.id)}
                      style={{ display: 'flex', alignItems: 'center', gap: 10, flex: 1, cursor: 'pointer' }}>
                      <div style={{
                        width: 16, height: 16, borderRadius: '50%',
                        border: isSelected ? '2px solid #6366F1' : '2px solid var(--border-color)',
                        background: isSelected ? '#6366F1' : 'transparent',
                        display: 'flex', alignItems: 'center', justifyContent: 'center',
                      }}>
                        {isSelected && <Check size={9} color="#fff" />}
                      </div>
                      <div>
                        <div style={{ fontSize: 12.5, fontWeight: isSelected ? 600 : 400, color: 'var(--text-primary)' }}>
                          {model.name}
                        </div>
                        <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                          {model.id} · {model.provider}
                        </div>
                      </div>
                    </div>
                    <button onClick={async () => {
                      const err = await removeCustomModel(model.id, model.provider);
                      if (err) setDeleteError(err);
                    }}
                      title={t('models.deleteCustom')}
                      style={{
                        width: 28, height: 28, borderRadius: 6,
                        background: 'transparent', border: '1px solid var(--border-color)',
                        color: 'var(--text-muted)', cursor: 'pointer',
                        display: 'flex', alignItems: 'center', justifyContent: 'center',
                      }}
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {filtered.length === 0 && (
          <div style={{
            textAlign: 'center', padding: 40, color: 'var(--text-muted)', fontSize: 13,
          }}>
            {t('models.noResults')}
          </div>
        )}
      </div>
    </div>
  );
};

export default ModelsPage;
