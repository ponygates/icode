import React, { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import {
  RefreshCw, Search, Zap, Sparkles, Shield, Cpu, X, Check,
  Key, Globe, Thermometer, Hash, DollarSign, Layers,
  ChevronRight, Settings, Star,
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
  model: any;
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
                <DollarSign size={12} style={{ display: 'inline', marginRight: 4 }} /> 定价
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {model.plans.map((plan: any, i: number) => (
                  <div key={i} style={{
                    background: 'var(--bg-primary)', borderRadius: 8,
                    border: '1px solid var(--border-color)', padding: '8px 12px', flex: 1, minWidth: 130,
                  }}>
                    <div style={{ fontSize: 12, fontWeight: 500, color: 'var(--text-primary)' }}>{plan.name}</div>
                    {plan.description && <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{plan.description}</div>}
                    <div style={{ fontSize: 11, color: 'var(--text-secondary)', marginTop: 4 }}>
                      {plan.inputPrice != null && <div>入: ${plan.inputPrice}/M tok</div>}
                      {plan.outputPrice != null && <div>出: ${plan.outputPrice}/M tok</div>}
                      {plan.cachePrice != null && <div style={{ color: 'var(--success)' }}>缓存: ${plan.cachePrice}/M tok</div>}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* API Configuration */}
          <div style={{ marginBottom: 20 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
              <Key size={12} style={{ display: 'inline', marginRight: 4 }} /> API 配置
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
                  API Key
                </label>
                <input
                  type="password"
                  placeholder={`输入 ${model.provider} 的 API Key`}
                  value={settings.apiKey}
                  onChange={(e) => setSettings((s) => ({ ...s, apiKey: e.target.value }))}
                  style={inputField}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
                  <Globe size={10} style={{ display: 'inline', marginRight: 4 }} />
                  Base URL（留空使用默认）
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
              <Settings size={12} style={{ display: 'inline', marginRight: 4 }} /> 生成参数
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              {/* Temperature */}
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    <Thermometer size={10} style={{ display: 'inline', marginRight: 4 }} />
                    温度 (Temperature)
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
                  <span>精确</span><span>平衡</span><span>创意</span>
                </div>
              </div>

              {/* Max Tokens */}
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    <Hash size={10} style={{ display: 'inline', marginRight: 4 }} />
                    最大输出 Token
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
              <Star size={13} /> 设为默认
            </button>
          )}
          <button onClick={onClose} style={{
            padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
            background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
            color: 'var(--text-secondary)',
          }}>
            取消
          </button>
          <button onClick={() => { onSave(settings); setSaved(true); setTimeout(() => setSaved(false), 2000); }} style={{
            padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
            background: saved ? 'var(--success)' : color,
            border: 'none', color: '#fff', fontWeight: 600,
            display: 'flex', alignItems: 'center', gap: 6,
            transition: 'background 0.2s',
          }}>
            {saved ? <><Check size={13} /> 已保存</> : '保存设置'}
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
  const { models, selectedModel, setSelectedModel, refreshModels, backendUrl } = useAppStore();
  const [search, setSearch] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [selectedSettingsModel, setSelectedSettingsModel] = useState<any>(null);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(new Set());

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
            {filtered.length} 个可用模型 · {providers.length} 个提供商
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
                    {providerModels.length} 模型{hasActiveInProvider ? ' · 活跃' : ''}
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
                    return (
                      <div
                        key={model.id}
                        style={{
                          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                          padding: '9px 14px 9px 16px',
                          borderRadius: 8, cursor: 'pointer',
                          background: isSelected ? `${color}14` : 'var(--bg-secondary)',
                          border: isSelected
                            ? `1px solid ${color}40`
                            : '1px solid transparent',
                          transition: 'all 0.15s',
                          borderLeft: isSelected ? `3px solid ${color}` : '3px solid transparent',
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
                            <div style={{ fontSize: 12.5, fontWeight: isSelected ? 600 : 400, color: 'var(--text-primary)' }}>
                              {model.name}
                            </div>
                            <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                              {model.id}
                            </div>
                          </div>
                        </div>

                        {/* Right: plan badge + settings button */}
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          {model.plan && (
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
                            title="自定义设置"
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

        {filtered.length === 0 && (
          <div style={{
            textAlign: 'center', padding: 40, color: 'var(--text-muted)', fontSize: 13,
          }}>
            未找到匹配的模型。尝试其他搜索词或点击"刷新"获取最新列表。
          </div>
        )}
      </div>
    </div>
  );
};

export default ModelsPage;
