import React from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';

const SECURITY_LABELS: Record<string, string> = {
  local:        '🔒 本地处理',
  desensitize:  '🛡 脱敏处理',
  'local-llm':  '💻 本地大模型',
  'foreign-llm':'🌐 国外大模型',
  unrestricted: '⚠ 无限制',
};

const TokenBar: React.FC = () => {
  const { t } = useTranslation();
  const { tokenUsage, securityLevel } = useAppStore();

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 16,
      padding: '4px 16px', fontSize: 11, color: 'var(--text-muted)',
      background: 'var(--bg-primary)', borderTop: '1px solid var(--border-color)',
    }}>
      <span style={{ fontWeight: 500, color: 'var(--text-secondary)' }}>
        {t('token.usage')}
      </span>
      <span>{t('token.input')}: {tokenUsage.input.toLocaleString()}</span>
      <span>{t('token.output')}: {tokenUsage.output.toLocaleString()}</span>
      {tokenUsage.cacheHit > 0 && (
        <span style={{ color: 'var(--success)' }}>
          {t('token.cacheHit')}: {((tokenUsage.cacheHit / (tokenUsage.input + tokenUsage.output + 1)) * 100).toFixed(1)}%
        </span>
      )}
      <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
        {securityLevel !== 'local' && (
          <span style={{
            padding: '1px 6px', borderRadius: 4,
            background: 'var(--bg-tertiary)', border: '1px solid var(--border-color)',
            fontWeight: 500, color: 'var(--text-secondary)',
          }}>
            {SECURITY_LABELS[securityLevel] || securityLevel}
          </span>
        )}
        <span style={{ fontWeight: 500 }}>
          {t('token.cost')}: {tokenUsage.cost}
        </span>
      </span>
    </div>
  );
};

export default TokenBar;
