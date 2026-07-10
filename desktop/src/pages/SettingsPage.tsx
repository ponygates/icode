import React, { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Globe, Moon, Sun, Key, Info, Check, X } from 'lucide-react';

const PROVIDER_LABELS: Record<string, string> = {
  deepseek: 'DeepSeek',
  zhipu: 'Zhipu (智谱)',
  kimi: 'Kimi (月之暗面)',
  openrouter: 'OpenRouter',
  volcengine: 'Volcengine (豆包)',
  tencent: 'Tencent (混元)',
  huawei: 'Huawei (盘古)',
  scnet: 'SCNET',
  nvidia: 'NVIDIA',
  anthropic: 'Anthropic',
};

const SettingsPage: React.FC = () => {
  const { t, i18n } = useTranslation();
  const { language, setLanguage } = useAppStore();

  const [providers, setProviders] = useState<any[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [bases, setBases] = useState<Record<string, string>>({});
  const [status, setStatus] = useState<Record<string, string>>({});

  const handleLanguageChange = (lang: string) => {
    setLanguage(lang);
    i18n.changeLanguage(lang);
  };

  useEffect(() => {
    if (window.icode && window.icode.listKeys) {
      window.icode
        .listKeys()
        .then((res: any) => {
          const list = res?.providers || [];
          setProviders(list);
          const b: Record<string, string> = {};
          list.forEach((p: any) => {
            b[p.name] = p.api_base || '';
          });
          setBases(b);
          setLoaded(true);
        })
        .catch(() => {
          setLoaded(true);
        });
    } else {
      setLoaded(true);
    }
  }, []);

  const handleSaveKey = async (name: string) => {
    setStatus((s) => ({ ...s, [name]: 'saving' }));
    try {
      const res = await window.icode.setApiKey(name, keys[name] || '', bases[name] || '');
      if (res?.ok) {
        setStatus((s) => ({ ...s, [name]: 'saved' }));
        // Refresh key status so the badge updates immediately.
        const r2 = await window.icode.listKeys();
        if (r2?.providers) {
          setProviders(r2.providers);
          const b: Record<string, string> = {};
          r2.providers.forEach((p: any) => {
            b[p.name] = p.api_base || '';
          });
          setBases(b);
        }
        setTimeout(() => setStatus((s) => ({ ...s, [name]: '' })), 2500);
      } else {
        setStatus((s) => ({ ...s, [name]: 'error: ' + (res?.error || '保存失败') }));
      }
    } catch (e: any) {
      setStatus((s) => ({ ...s, [name]: 'error: ' + (e?.message || String(e)) }));
    }
  };

  const languages = [
    { code: 'zh-CN', label: t('lang.zhCN') },
    { code: 'zh-TW', label: t('lang.zhTW') },
    { code: 'en', label: t('lang.en') },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      {/* Header */}
      <div style={{
        padding: '12px 20px', borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)',
      }}>
        <h2 style={{ fontSize: 15, fontWeight: 500, color: 'var(--text-primary)' }}>
          {t('settings.title')}
        </h2>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
        {/* Language Settings */}
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12,
            color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
          }}>
            <Globe size={16} />
            <span>{t('settings.language')}</span>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            {languages.map((l) => (
              <button
                key={l.code}
                onClick={() => handleLanguageChange(l.code)}
                style={{
                  padding: '8px 16px', borderRadius: 8,
                  background: language === l.code ? 'var(--bg-tertiary)' : 'var(--bg-primary)',
                  border: language === l.code
                    ? '1px solid var(--accent)'
                    : '1px solid var(--border-color)',
                  color: language === l.code ? 'var(--accent)' : 'var(--text-secondary)',
                  cursor: 'pointer', fontSize: 13, fontWeight: language === l.code ? 500 : 400,
                  transition: 'all 0.15s',
                }}
              >
                {l.label}
              </button>
            ))}
          </div>
        </div>

        {/* Theme */}
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12,
            color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
          }}>
            <Moon size={16} />
            <span>{t('settings.appearance')}</span>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            {[
              { key: 'dark', icon: Moon, label: 'Dark' },
              { key: 'light', icon: Sun, label: 'Light' },
              { key: 'auto', icon: Sun, label: 'Auto' },
            ].map((th) => (
              <button
                key={th.key}
                style={{
                  padding: '8px 16px', borderRadius: 8,
                  background: 'var(--bg-primary)',
                  border: '1px solid var(--border-color)',
                  color: 'var(--text-secondary)', cursor: 'pointer',
                  fontSize: 13, display: 'flex', alignItems: 'center', gap: 6,
                }}
              >
                <th.icon size={14} />
                {th.label}
              </button>
            ))}
          </div>
        </div>

        {/* Provider Config — API Key */}
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4,
            color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
          }}>
            <Key size={16} />
            <span>{t('settings.provider')} · API Key</span>
          </div>
          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 12 }}>
            在此填写各服务商的 API Key，保存后立即生效（无需重启）。Key 仅保存在本地配置文件中。
          </div>

          {!loaded && (
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>加载中…</div>
          )}

          {loaded && providers.length === 0 && (
            <div style={{
              marginTop: 8, padding: '10px 12px', fontSize: 11,
              color: 'var(--text-muted)', background: 'var(--bg-primary)',
              borderRadius: 8, border: '1px dashed var(--border-color)',
            }}>
              无法连接后端，请确认 iCode 后端服务已启动。也可在终端运行{' '}
              <code style={{ color: 'var(--accent)' }}>
                icode auth set --provider &lt;name&gt; --key &lt;api-key&gt;
              </code>{' '}
              配置。
            </div>
          )}

          {providers.map((p) => (
            <div
              key={p.name}
              style={{
                padding: '10px 12px', marginBottom: 8,
                background: 'var(--bg-primary)', borderRadius: 8,
                border: '1px solid var(--border-color)',
              }}
            >
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                marginBottom: 8,
              }}>
                <div style={{ fontSize: 13, color: 'var(--text-primary)', fontWeight: 500 }}>
                  {PROVIDER_LABELS[p.name] || p.name}
                </div>
                <span style={{
                  fontSize: 11,
                  color: p.key_set ? 'var(--accent)' : 'var(--text-muted)',
                  display: 'flex', alignItems: 'center', gap: 4,
                }}>
                  {p.key_set ? <><Check size={12} /> 已配置</> : <><X size={12} /> 未配置</>}
                </span>
              </div>

              <input
                placeholder="API Base（可选，留空用默认值）"
                value={bases[p.name] || ''}
                onChange={(e) => setBases((s) => ({ ...s, [p.name]: e.target.value }))}
                style={{
                  width: '100%', boxSizing: 'border-box', marginBottom: 6,
                  padding: '6px 8px', fontSize: 12, borderRadius: 6,
                  border: '1px solid var(--border-color)', background: 'var(--bg-secondary)',
                  color: 'var(--text-primary)', outline: 'none',
                }}
              />

              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  type="password"
                  placeholder={p.key_set ? '更新 API Key（留空保留）' : '输入 API Key'}
                  value={keys[p.name] || ''}
                  onChange={(e) => setKeys((s) => ({ ...s, [p.name]: e.target.value }))}
                  style={{
                    flex: 1, padding: '6px 8px', fontSize: 12, borderRadius: 6,
                    border: '1px solid var(--border-color)', background: 'var(--bg-secondary)',
                    color: 'var(--text-primary)', outline: 'none',
                  }}
                />
                <button
                  onClick={() => handleSaveKey(p.name)}
                  disabled={status[p.name] === 'saving'}
                  style={{
                    padding: '6px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
                    border: 'none', background: 'var(--accent)', color: '#000',
                    opacity: status[p.name] === 'saving' ? 0.6 : 1,
                  }}
                >
                  {status[p.name] === 'saving' ? '保存中…' : '保存'}
                </button>
              </div>

              {status[p.name] === 'saved' && (
                <div style={{ marginTop: 6, fontSize: 11, color: 'var(--accent)' }}>
                  ✓ 已保存并立即生效
                </div>
              )}
              {status[p.name]?.startsWith('error') && (
                <div style={{ marginTop: 6, fontSize: 11, color: '#e5484d' }}>
                  {status[p.name]}
                </div>
              )}
            </div>
          ))}
        </div>

        {/* About */}
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: 16,
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12,
            color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
          }}>
            <Info size={16} />
            <span>{t('settings.about')}</span>
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', lineHeight: 1.8 }}>
            <div>iCode v0.1.0-dev</div>
            <div>Multi-Model AI Coding Agent</div>
            <div>Apache-2.0 License</div>
            <div>
              <a
                href="https://github.com/ponygates/icode"
                target="_blank"
                rel="noopener noreferrer"
                style={{ color: 'var(--accent)' }}
              >
                github.com/ponygates/icode
              </a>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default SettingsPage;
