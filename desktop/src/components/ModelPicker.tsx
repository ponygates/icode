import React, { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Search, Cpu, Check, X, Zap, Sparkles, Shield, AlertTriangle } from 'lucide-react';

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

const ModelPicker: React.FC<{ open: boolean; onClose: () => void }> = ({ open, onClose }) => {
  const { t } = useTranslation();
  const models = useAppStore((s) => s.models);
  const customModels = useAppStore((s) => s.customModels);
  const selectedModel = useAppStore((s) => s.selectedModel);
  const setSelectedModel = useAppStore((s) => s.setSelectedModel);
  const [search, setSearch] = useState('');

  useEffect(() => {
    if (open) setSearch('');
  }, [open]);

  if (!open) return null;

  const q = search.trim().toLowerCase();
  const filtered = models.filter((m) =>
    !q ||
    m.name.toLowerCase().includes(q) ||
    m.provider.toLowerCase().includes(q) ||
    m.id.toLowerCase().includes(q)
  );
  const filteredCustom = customModels.filter((m) =>
    !q ||
    m.name.toLowerCase().includes(q) ||
    m.provider.toLowerCase().includes(q) ||
    m.id.toLowerCase().includes(q)
  );
  const providers = Array.from(new Set(filtered.map((m) => m.provider)));

  const pick = (id: string) => { setSelectedModel(id); onClose(); };

  return (
    <div
      onClick={onClose}
      onKeyDown={(e) => { if (e.key === 'Escape') onClose(); }}
      style={{
        position: 'fixed', inset: 0, zIndex: 1000,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: 'var(--bg-secondary)', borderRadius: 16,
          border: '1px solid var(--border-color)', width: 460, maxHeight: '80vh',
          display: 'flex', flexDirection: 'column',
          boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
        }}
      >
        {/* Header + search */}
        <div style={{ padding: '16px 20px 12px', borderBottom: '1px solid var(--border-color)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
            <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
              {t('models.title')}
            </span>
            <button onClick={onClose} style={{
              background: 'none', border: 'none', color: 'var(--text-muted)',
              cursor: 'pointer', padding: 4, borderRadius: 6,
            }}>
              <X size={16} />
            </button>
          </div>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10,
            background: 'var(--bg-primary)', borderRadius: 10,
            border: '1px solid var(--border-color)', padding: '8px 12px',
            transition: 'border-color 0.15s',
          }}>
            <Search size={13} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
            <input
              autoFocus
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t('models.search')}
              style={{ flex: 1, background: 'transparent', border: 'none', color: 'var(--text-primary)', outline: 'none', fontSize: 13 }}
            />
            {search && (
              <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                {filtered.length + filteredCustom.length}
              </span>
            )}
          </div>
        </div>

        {/* List */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '10px 12px' }}>
          {providers.map((provider) => {
            const color = providerColors[provider] || '#6366F1';
            return (
              <div key={provider} style={{ marginBottom: 8 }}>
                <div style={{
                  fontSize: 11, fontWeight: 600, color: 'var(--text-muted)',
                  textTransform: 'uppercase', letterSpacing: '0.04em', padding: '6px 8px 4px',
                }}>
                  {provider}
                </div>
                {filtered.filter((m) => m.provider === provider).map((model) => {
                  const isSelected = model.id === selectedModel;
                  const isDeprecated = model.deprecated;
                  return (
                    <div
                      key={model.id}
                      onClick={() => pick(model.id)}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 10,
                        padding: '8px 10px', borderRadius: 8, cursor: 'pointer',
                        background: isSelected ? `${color}14` : 'transparent',
                        border: isSelected ? `1px solid ${color}40` : '1px solid transparent',
                        opacity: isDeprecated ? 0.6 : 1,
                        transition: 'all 0.12s',
                      }}
                    >
                      <div style={{
                        width: 20, height: 20, borderRadius: 6,
                        background: `${color}18`, border: `1px solid ${color}30`,
                        display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                      }}>
                        {providerIcons[provider] || <Cpu size={12} color={color} />}
                      </div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{
                          fontSize: 12.5, fontWeight: isSelected ? 600 : 400,
                          color: 'var(--text-primary)', display: 'flex', alignItems: 'center', gap: 6,
                          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                        }}>
                          {model.name}
                          {isDeprecated && <AlertTriangle size={11} color="#FBBF24" />}
                        </div>
                        <div style={{ fontSize: 10, color: 'var(--text-muted)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                          {model.id}
                        </div>
                      </div>
                      {isSelected && <Check size={14} color={color} />}
                    </div>
                  );
                })}
              </div>
            );
          })}

          {filteredCustom.length > 0 && (
            <div style={{ marginBottom: 8 }}>
              <div style={{
                fontSize: 11, fontWeight: 600, color: 'var(--text-muted)',
                textTransform: 'uppercase', letterSpacing: '0.04em', padding: '6px 8px 4px',
              }}>
                {t('models.customModels')}
              </div>
              {filteredCustom.map((model) => {
                const isSelected = model.id === selectedModel;
                return (
                  <div
                    key={model.id}
                    onClick={() => pick(model.id)}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 10,
                      padding: '8px 10px', borderRadius: 8, cursor: 'pointer',
                      background: isSelected ? 'rgba(99,102,241,0.14)' : 'transparent',
                      border: isSelected ? '1px solid rgba(99,102,241,0.4)' : '1px solid transparent',
                      transition: 'all 0.12s',
                    }}
                  >
                    <div style={{
                      width: 20, height: 20, borderRadius: 6,
                      background: 'rgba(99,102,241,0.18)', border: '1px solid rgba(99,102,241,0.3)',
                      display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                    }}>
                      <Cpu size={12} color="#6366F1" />
                    </div>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 12.5, fontWeight: isSelected ? 600 : 400, color: 'var(--text-primary)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {model.name}
                      </div>
                      <div style={{ fontSize: 10, color: 'var(--text-muted)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {model.id} · {model.provider}
                      </div>
                    </div>
                    {isSelected && <Check size={14} color="#6366F1" />}
                  </div>
                );
              })}
            </div>
          )}

          {filtered.length === 0 && filteredCustom.length === 0 && (
            <div style={{ textAlign: 'center', padding: 30, color: 'var(--text-muted)', fontSize: 13 }}>
              {t('models.noResults')}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default ModelPicker;
