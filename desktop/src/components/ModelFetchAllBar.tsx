import React from 'react';
import { useTranslation } from 'react-i18next';
import { Download, RefreshCw, X, Check } from 'lucide-react';
import type { UseModelFetch } from '../lib/useModelFetch';

/**
 * "获取全部厂商" sweep control: one button, live progress, and a result
 * breakdown once it finishes.
 *
 * The sweep is deliberately not silent about its three outcomes. A vendor can
 * legitimately come back as *unsupported* (it has no live-discovery endpoint —
 * the backend answers 501), which is a very different thing from *failed* (no
 * key, bad key, network). Collapsing them into "3 errors" would send the user
 * hunting for a problem that does not exist.
 */
export const ModelFetchAllBar: React.FC<{
  mf: UseModelFetch;
  /** Vendors to sweep — normally every registered provider. */
  providers: string[];
  accent?: string;
}> = ({ mf, providers, accent = 'var(--text-secondary)' }) => {
  const { t } = useTranslation();
  const prog = mf.allProgress;
  const sum = mf.allSummary;
  const running = !!prog;
  const pct = prog && prog.total > 0 ? Math.round((prog.done / prog.total) * 100) : 0;

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
      <button
        onClick={() => mf.fetchAll(providers)}
        disabled={running || providers.length === 0}
        title={t('models.fetchAllHint')}
        style={{
          display: 'flex', alignItems: 'center', gap: 6,
          background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
          color: accent, padding: '7px 14px', borderRadius: 8,
          cursor: running || providers.length === 0 ? 'not-allowed' : 'pointer',
          fontSize: 12, fontWeight: 500,
          opacity: running || providers.length === 0 ? 0.6 : 1,
          transition: 'all 0.15s',
        }}
      >
        {running
          ? <RefreshCw size={13} style={{ animation: 'spin 1s linear infinite' }} />
          : <Download size={13} />}
        {running
          ? t('models.fetchingAll', { done: prog?.done ?? 0, total: prog?.total ?? 0 })
          : t('models.fetchAll')}
      </button>

      {/* Thin progress rail — a 13-vendor sweep takes long enough that the
          user needs to see it moving, not just spinning. */}
      {running && (
        <div style={{
          width: 90, height: 4, borderRadius: 2, overflow: 'hidden',
          background: 'var(--border-color)',
        }}>
          <div style={{
            width: `${pct}%`, height: '100%', background: 'var(--accent, #6366F1)',
            transition: 'width 0.25s ease',
          }} />
        </div>
      )}

      {!running && sum && (
        <span style={{
          display: 'flex', alignItems: 'center', gap: 8,
          fontSize: 11, color: 'var(--text-muted)',
          padding: '4px 10px', borderRadius: 6,
          background: 'var(--bg-secondary)', border: '1px solid var(--border-color)',
        }}>
          {sum.ok > 0 && (
            <span style={{ color: 'var(--success, #3FB950)', display: 'flex', alignItems: 'center', gap: 3 }}>
              <Check size={11} />{t('models.fetchAllOk', { count: sum.ok })}
            </span>
          )}
          {sum.unsupported > 0 && (
            <span>{t('models.fetchAllUnsupported', { count: sum.unsupported })}</span>
          )}
          {sum.failed > 0 && (
            <span style={{ color: 'var(--error, #f85149)' }}>
              {t('models.fetchAllFailed', { count: sum.failed })}
            </span>
          )}
          {sum.ok > 0 && <span>· {t('models.fetchAllNext')}</span>}
          <span
            onClick={mf.clearAllSummary}
            title={t('models.dismiss')}
            style={{ cursor: 'pointer', display: 'flex', color: 'var(--text-muted)' }}
          >
            <X size={11} />
          </span>
        </span>
      )}
    </div>
  );
};

export default ModelFetchAllBar;
