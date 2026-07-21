import React, { useState } from 'react';
import { useAppStore } from '../stores/appStore';
import { Key, ArrowRight, Cpu } from 'lucide-react';

const providers = [
  { id: 'openrouter', name: 'OpenRouter', desc: '支持 Claude/GPT/Gemini 等 200+ 模型', baseUrl: 'https://openrouter.ai/api/v1', keyUrl: 'https://openrouter.ai/keys' },
  { id: 'deepseek', name: 'DeepSeek', desc: '国产最强，V4 Flash 性价比之王', baseUrl: 'https://api.deepseek.com', keyUrl: 'https://platform.deepseek.com/api_keys' },
  { id: 'zhipu', name: '智谱 GLM', desc: 'GLM-5 系列，中文理解优秀', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', keyUrl: 'https://open.bigmodel.cn/usercenter/apikeys' },
  { id: 'kimi', name: 'Kimi (月之暗面)', desc: 'K2.7 Code 专为编程优化', baseUrl: 'https://api.moonshot.cn/v1', keyUrl: 'https://platform.moonshot.cn/console/api-keys' },
];

const SetupWizard: React.FC<{ onDone: () => void }> = ({ onDone }) => {
  const { backendUrl } = useAppStore();
  const [step, setStep] = useState(0);
  const [provider, setProvider] = useState('openrouter');
  const [apiKey, setApiKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const current = providers.find(p => p.id === provider)!;

  const handleSave = async () => {
    if (!apiKey.trim()) return;
    setSaving(true);
    setError('');
    try {
      if (backendUrl) {
        const res = await fetch(`${backendUrl}/api/config/key`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ provider, api_key: apiKey.trim(), api_base: current.baseUrl }),
        });
        if (!res.ok) throw new Error('保存失败');
      }
      // Also save in localStorage
      localStorage.setItem(`icode.key.${provider}`, apiKey.trim());
      onDone();
    } catch (e: any) {
      setError(e?.message || '保存失败，请检查后端是否在运行');
    } finally {
      setSaving(false);
    }
  };

  const handleSkip = () => onDone();

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 10000,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(0,0,0,0.7)', backdropFilter: 'blur(8px)',
    }}>
      <div style={{
        background: 'var(--bg-secondary)', borderRadius: 16,
        border: '1px solid var(--border-color)',
        width: 540, boxShadow: '0 20px 60px rgba(0,0,0,0.4)',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          padding: '24px 28px 0',
          textAlign: 'center',
        }}>
          <div style={{ fontSize: 24, marginBottom: 4 }}>👋</div>
          <div style={{ fontSize: 16, fontWeight: 600, color: 'var(--text-primary)' }}>
            {step === 0 ? '欢迎使用 iCode' : '配置 API Key'}
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 4 }}>
            {step === 0 ? '选择一个 AI 提供商开始使用' : '填入你的 API Key 即可开始'}
          </div>
        </div>

        {/* Step indicator */}
        <div style={{
          display: 'flex', justifyContent: 'center', gap: 8, margin: '16px 0',
        }}>
          {[0, 1].map(s => (
            <div key={s} style={{
              width: s <= step ? 32 : 8, height: 4, borderRadius: 2,
              background: s <= step ? 'var(--accent)' : 'var(--border-color)',
              transition: 'all 0.2s',
            }} />
          ))}
        </div>

        {/* Content */}
        <div style={{ padding: '0 28px 20px' }}>
          {step === 0 ? (
            <>
              {/* Provider selection */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {providers.map(p => (
                  <button
                    key={p.id}
                    onClick={() => { setProvider(p.id); setStep(1); }}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 12,
                      padding: '12px 14px', borderRadius: 10,
                      background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                      cursor: 'pointer', textAlign: 'left',
                      transition: 'border-color 0.15s',
                    }}
                    onMouseEnter={e => (e.currentTarget.style.borderColor = 'var(--accent)')}
                    onMouseLeave={e => (e.currentTarget.style.borderColor = 'var(--border-color)')}
                  >
                    <div style={{
                      width: 36, height: 36, borderRadius: 8,
                      background: 'var(--accent-soft)', display: 'flex',
                      alignItems: 'center', justifyContent: 'center',
                    }}>
                      <Cpu size={18} color="var(--accent)" />
                    </div>
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>
                        {p.name}
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                        {p.desc}
                      </div>
                    </div>
                    <ArrowRight size={14} color="var(--text-muted)" />
                  </button>
                ))}
              </div>
            </>
          ) : (
            <>
              {/* API Key input */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  padding: '10px 12px', borderRadius: 8,
                  background: 'var(--accent-soft)', fontSize: 12,
                }}>
                  <Key size={14} color="var(--accent)" />
                  <span style={{ color: 'var(--text-primary)', flex: 1 }}>
                    {current.name} API Key
                  </span>
                  <a href={current.keyUrl} target="_blank" rel="noopener noreferrer"
                    style={{ color: 'var(--accent)', fontSize: 11, textDecoration: 'underline' }}>
                    获取 Key →
                  </a>
                </div>
                <input
                  type="password"
                  placeholder={`粘贴你的 ${current.name} API Key...`}
                  value={apiKey}
                  onChange={e => setApiKey(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && handleSave()}
                  autoFocus
                  style={{
                    width: '100%', padding: '12px 14px', borderRadius: 8,
                    border: '1px solid var(--border-color)',
                    background: 'var(--bg-primary)', color: 'var(--text-primary)',
                    fontSize: 13, outline: 'none',
                    fontFamily: 'var(--font-mono)',
                  }}
                />
                {error && (
                  <div style={{ fontSize: 12, color: 'var(--error)', padding: '4px 0' }}>
                    {error}
                  </div>
                )}
                <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <button onClick={() => setStep(0)} style={{
                    padding: '8px 16px', borderRadius: 8,
                    background: 'transparent', border: '1px solid var(--border-color)',
                    color: 'var(--text-muted)', cursor: 'pointer', fontSize: 12,
                  }}>
                    返回
                  </button>
                  <button onClick={handleSave} disabled={!apiKey.trim() || saving} style={{
                    padding: '8px 20px', borderRadius: 8,
                    background: apiKey.trim() ? 'var(--accent)' : 'var(--bg-tertiary)',
                    border: 'none', color: apiKey.trim() ? '#fff' : 'var(--text-muted)',
                    cursor: apiKey.trim() ? 'pointer' : 'not-allowed',
                    fontSize: 12, fontWeight: 500,
                  }}>
                    {saving ? '保存中...' : '保存并开始'}
                  </button>
                </div>
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        <div style={{
          borderTop: '1px solid var(--border-color)',
          padding: '12px 28px', display: 'flex', justifyContent: 'center',
        }}>
          <button onClick={handleSkip} style={{
            background: 'transparent', border: 'none',
            color: 'var(--text-muted)', cursor: 'pointer', fontSize: 11,
          }}>
            跳过，使用默认配置
          </button>
        </div>
      </div>
    </div>
  );
};

export default SetupWizard;
