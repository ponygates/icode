import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';

const getSecurityLabel = (t: (key: string) => string, level: string): string => {
  const labels: Record<string, string> = {
    local: t('tokenBar.secLocal'),
    desensitize: t('tokenBar.secDesensitize'),
    'local-llm': t('tokenBar.secLocalLLM'),
    'foreign-llm': t('tokenBar.secForeignLLM'),
    unrestricted: t('tokenBar.secUnrestricted'),
  };
  return labels[level] || level;
};

const TokenBar: React.FC = () => {
  const { t } = useTranslation();
  const { tokenUsage, securityLevel, activeSessionId, backendUrl, updateTokenUsage } = useAppStore();

  // Poll the backend's analytics endpoint so the user can SEE iCode's
  // token-saving mechanism at work (tokens_saved from the 5-layer pipeline:
  // Snip → dedup → fold → summarize → budget). Mirrors TodoPanel's polling.
  useEffect(() => {
    if (!backendUrl || !activeSessionId) return;
    let cancelled = false;
    const load = async () => {
      try {
        const res = await fetch(`${backendUrl}/api/analytics/${activeSessionId}`);
        if (!res.ok || cancelled) return;
        const data = await res.json();
        if (cancelled) return;
        updateTokenUsage({ saved: data.tokens_saved || 0 });
      } catch {
        /* backend offline — keep last known value */
      }
    };
    load();
    const id = setInterval(load, 4000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [backendUrl, activeSessionId, updateTokenUsage]);

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
      {tokenUsage.saved > 0 && (
        <span style={{ color: 'var(--success)', fontWeight: 500 }} title={t('token.savedTitle')}>
          🪙 {t('token.saved')}: {tokenUsage.saved.toLocaleString()}
        </span>
      )}
      <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
        {securityLevel !== 'local' && (
          <span style={{
            padding: '1px 6px', borderRadius: 4,
            background: 'var(--bg-tertiary)', border: '1px solid var(--border-color)',
            fontWeight: 500, color: 'var(--text-secondary)',
          }}>
            {getSecurityLabel(t, securityLevel)}
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
