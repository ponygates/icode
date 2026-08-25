import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { ChevronUp, Gauge, Copy, Zap } from 'lucide-react';

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
  // Precise selectors: this component only renders the token bar, so it
  // should only re-render when these slices change — not on every message
  // append or session switch.
  const tokenUsage = useAppStore(s => s.tokenUsage);
  const securityLevel = useAppStore(s => s.securityLevel);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const backendUrl = useAppStore(s => s.backendUrl);
  const updateTokenUsage = useAppStore(s => s.updateTokenUsage);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // Close the popover on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

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
    <div ref={ref} style={{ position: 'relative' }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 16,
        padding: '4px 16px', fontSize: 11, color: 'var(--text-muted)',
        background: 'var(--bg-primary)', borderTop: '1px solid var(--border-color)',
        cursor: 'pointer', userSelect: 'none',
      }} onClick={() => setOpen(v => !v)} title={t('token.barClickHint')}>
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
          <ChevronUp size={11} style={{ opacity: 0.6, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s' }} />
        </span>
      </div>

      {open && (
        <div style={{
          position: 'absolute', bottom: '100%', right: 16, zIndex: 80,
          width: 320, padding: 12,
          background: 'var(--bg-elev)', border: '0.5px solid var(--border-color)',
          borderRadius: 10, boxShadow: '0 -12px 32px rgba(0,0,0,0.3)',
          marginBottom: 6,
        }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 8 }}>
            {t('token.detailTitle')}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px 12px', fontSize: 11 }}>
            <span style={{ color: 'var(--text-muted)' }}>{t('token.input')}</span>
            <span style={{ textAlign: 'right', color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}>
              {tokenUsage.input.toLocaleString()}
            </span>
            <span style={{ color: 'var(--text-muted)' }}>{t('token.output')}</span>
            <span style={{ textAlign: 'right', color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}>
              {tokenUsage.output.toLocaleString()}
            </span>
            <span style={{ color: 'var(--text-muted)' }}>{t('token.cacheHit')}</span>
            <span style={{ textAlign: 'right', color: 'var(--success)', fontVariantNumeric: 'tabular-nums' }}>
              {tokenUsage.cacheHit.toLocaleString()}
            </span>
            <span style={{ color: 'var(--text-muted)' }}>🪙 {t('token.saved')}</span>
            <span style={{ textAlign: 'right', color: 'var(--success)', fontVariantNumeric: 'tabular-nums' }}>
              {tokenUsage.saved.toLocaleString()}
            </span>
            <span style={{ color: 'var(--text-muted)' }}>{t('token.cost')}</span>
            <span style={{ textAlign: 'right', color: 'var(--accent)', fontWeight: 600 }}>
              {tokenUsage.cost}
            </span>
          </div>

          <div style={{ height: 1, background: 'var(--border-color)', margin: '10px 0' }} />

          <div style={{ display: 'flex', gap: 6 }}>
            <button
              onClick={() => {
                if (activeSessionId) window.dispatchEvent(new CustomEvent('icode:compact-session'));
                setOpen(false);
              }}
              style={btnStyle}
              onMouseEnter={e => { e.currentTarget.style.background = 'var(--accent-soft)'; }}
              onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; }}
            >
              <Zap size={12} style={{ color: 'var(--accent)' }} /> {t('token.compactNow')}
            </button>
            <button
              onClick={() => {
                const text = [
                  `${t('token.input')}: ${tokenUsage.input}`,
                  `${t('token.output')}: ${tokenUsage.output}`,
                  `${t('token.cacheHit')}: ${tokenUsage.cacheHit}`,
                  `🪙 ${t('token.saved')}: ${tokenUsage.saved}`,
                  `${t('token.cost')}: ${tokenUsage.cost}`,
                ].join('\n');
                navigator.clipboard?.writeText(text).catch(() => {});
                setOpen(false);
              }}
              style={btnStyle}
              onMouseEnter={e => { e.currentTarget.style.background = 'var(--bg-hover)'; }}
              onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; }}
            >
              <Copy size={12} /> {t('token.copyStats')}
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

const btnStyle: React.CSSProperties = {
  flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
  padding: '6px 8px', borderRadius: 7, cursor: 'pointer',
  fontSize: 11, color: 'var(--text-secondary)',
  background: 'transparent', border: '0.5px solid var(--border-color)',
};

export default TokenBar;
