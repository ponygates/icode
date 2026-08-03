import React, { useState, useCallback, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore, RefreshSummary, type Model } from '../stores/appStore';
import {
  RefreshCw, Search, Zap, Sparkles, Shield, Cpu, X, Check,
  Key, Globe, Thermometer, Hash, DollarSign, Layers,
  ChevronRight, Settings, Star, Plus, Trash2, AlertTriangle,
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
  onSave: (settings: ModelSettings) => void;
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
          display: 'flex', gap: 8, justifyContent: 'flex-end',
        }}>
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
          <button onClick={() => { onSave(settings); setSaved(true); setTimeout(() => setSaved(false), 2000); }} style={{
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
  const [search, setSearch] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [selectedSettingsModel, setSelectedSettingsModel] = useState<Model | null>(null);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(new Set());
  const [showAddCustom, setShowAddCustom] = useState(false);
  const [newCustom, setNewCustom] = useState({ name: '', id: '', provider: '', apiBase: '' });
  const [showToast, setShowToast] = useState(false);

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

  const providers = Array.from(new Set(filtered.map((m) => m.provider)));

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

  const handleSaveModelSettings = useCallback(async (modelId: string, settings: ModelSettings) => {
    if (backendUrl) {
      try {
        await fetch(`${backendUrl}/api/config/key`, {
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
        if (settings.apiKey) await refreshModels();
      } catch {}
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
        <button onClick={() => setShowAddCustom(true)}
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
      </div>

      {/* Search bar */}
      <div style={{ padding: '12px 24px', background: 'var(--bg-primary)' }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
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
      </div>

      {/* Custom Model Modal */}
      {showAddCustom && (
        <div onClick={() => setShowAddCustom(false)} style={{
          position: 'fixed', inset: 0, zIndex: 1000,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
        }}>
          <div onClick={(e) => e.stopPropagation()} style={{
            background: 'var(--bg-secondary)', borderRadius: 16,
            border: '1px solid var(--border-color)', width: 440,
            boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
          }}>
            <div style={{ padding: '20px 24px 16px', borderBottom: '1px solid var(--border-color)' }}>
              <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
                <Plus size={16} style={{ display: 'inline', marginRight: 8 }} />
                {t('models.addCustom')}
              </div>
            </div>
            <div style={{ padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('models.modelName')} *</label>
                <input value={newCustom.name} onChange={(e) => setNewCustom({...newCustom, name: e.target.value})}
                  placeholder={t('models.modelNameExample')} style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('models.modelId')} *</label>
                <input value={newCustom.id} onChange={(e) => setNewCustom({...newCustom, id: e.target.value})}
                  placeholder={t('models.modelIdExample')} style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('models.providerName')} *</label>
                <input value={newCustom.provider} onChange={(e) => setNewCustom({...newCustom, provider: e.target.value})}
                  placeholder={t('models.providerNameExample')} style={inputField} />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>API Base URL</label>
                <input value={newCustom.apiBase} onChange={(e) => setNewCustom({...newCustom, apiBase: e.target.value})}
                  placeholder="https://api.example.com/v1（{t('models.keepDefault')}）" style={inputField} />
              </div>
            </div>
            <div style={{ padding: '16px 24px', borderTop: '1px solid var(--border-color)', display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button onClick={() => setShowAddCustom(false)} style={{
                padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                color: 'var(--text-secondary)',
              }}>{t('settings.cancel')}</button>
              <button onClick={() => {
                if (!newCustom.name || !newCustom.id || !newCustom.provider) return;
                const model: Model = {
                  id: newCustom.id,
                  name: newCustom.name,
                  provider: newCustom.provider,
                  plan: 'Custom',
                  apiBase: newCustom.apiBase || undefined,
                  capabilities: { tools: true, streaming: true },
                };
                addCustomModel(model);
                setNewCustom({ name: '', id: '', provider: '', apiBase: '' });
                setShowAddCustom(false);
              }} style={{
                padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
                background: '#6366F1', border: 'none', color: '#fff', fontWeight: 600,
                display: 'flex', alignItems: 'center', gap: 6,
              }}>
                <Check size={13} /> {t('models.add')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Model list — Reasonix-style cards grouped by provider */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '0 24px 24px' }}>
        {providers.map((provider) => {
          const providerModels = filtered.filter((m) => m.provider === provider);
          if (providerModels.length === 0) return null;
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
                            <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                              {model.id}
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
                    <button onClick={() => removeCustomModel(model.id)}
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
