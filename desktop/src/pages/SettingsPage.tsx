import React from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Globe, Moon, Sun, Key, Info } from 'lucide-react';

const SettingsPage: React.FC = () => {
  const { t, i18n } = useTranslation();
  const { language, setLanguage } = useAppStore();

  const handleLanguageChange = (lang: string) => {
    setLanguage(lang);
    i18n.changeLanguage(lang);
  };

  const languages = [
    { code: 'zh-CN', label: t('lang.zhCN') },
    { code: 'zh-TW', label: t('lang.zhTW') },
    { code: 'en', label: t('lang.en') },
  ];

  const providerConfigs = [
    { name: 'DeepSeek', env: 'DEEPSEEK_API_KEY', base: 'https://api.deepseek.com/v1' },
    { name: 'Zhipu (智谱)', env: 'ZHIPU_API_KEY', base: 'https://open.bigmodel.cn/api/paas/v4' },
    { name: 'Kimi (月之暗面)', env: 'KIMI_API_KEY', base: 'https://api.moonshot.cn/v1' },
    { name: 'OpenRouter', env: 'OPENROUTER_API_KEY', base: 'https://openrouter.ai/api/v1' },
    { name: 'OpenAI Compatible', env: 'OPENAI_API_KEY', base: 'Custom endpoint' },
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

        {/* Provider Config */}
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 10,
          border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12,
            color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
          }}>
            <Key size={16} />
            <span>{t('settings.provider')}</span>
          </div>

          {providerConfigs.map((provider) => (
            <div
              key={provider.name}
              style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '10px 12px', marginBottom: 6,
                background: 'var(--bg-primary)', borderRadius: 8,
                border: '1px solid var(--border-color)',
              }}
            >
              <div>
                <div style={{ fontSize: 13, color: 'var(--text-primary)' }}>{provider.name}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
                  {provider.base}
                </div>
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                Env: {provider.env}
              </div>
            </div>
          ))}

          <div style={{
            marginTop: 8, padding: '10px 12px', fontSize: 11,
            color: 'var(--text-muted)', background: 'var(--bg-primary)',
            borderRadius: 8, border: '1px dashed var(--border-color)',
          }}>
            在终端中运行 <code style={{ color: 'var(--accent)' }}>icode auth set --provider &lt;name&gt; --key &lt;api-key&gt;</code> 来配置 API 密钥。
          </div>
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
