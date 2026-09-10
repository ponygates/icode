import React from 'react';
import { useTranslation } from 'react-i18next';
import { ListChecks, AlertCircle } from 'lucide-react';
import type { UseModelFetch } from '../lib/useModelFetch';

/**
 * Checklist shown after "获取模型" pulls a vendor's live catalogue.
 *
 * Shared by the settings modal (SettingsPage → PageModels) and the standalone
 * model page (ModelsPage) so both surfaces behave identically. Renders nothing
 * unless this provider's panel is open and a list was returned.
 */
export const ModelFetchPanel: React.FC<{
  provider: string;
  color: string;
  mf: UseModelFetch;
  /** Runs after a successful save — refresh the model list / provider meta. */
  onApplied?: () => Promise<void> | void;
}> = ({ provider, color, mf, onApplied }) => {
  const { t } = useTranslation();

  const list = mf.lists[provider] || [];
  const sel = mf.ticked[provider] || [];
  if (mf.panel !== provider || list.length === 0) return null;

  const selSet = new Set(sel);
  const allOn = sel.length === list.length;
  const blocked = mf.saving || sel.length === 0;

  return (
    <div style={{
      padding: 12, borderRadius: 8, marginBottom: 6,
      background: 'var(--bg-secondary)', border: `1px solid ${color}30`,
      animation: 'fadeIn 0.15s ease',
    }}>
      {/* Header: count + select all/none */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <ListChecks size={13} color={color} />
        <div style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--text-primary)' }}>
          {t('models.fetchTitle', { count: list.length })}
        </div>
        <div style={{ flex: 1 }} />
        <button
          onClick={() => mf.selectAll(provider, !allOn)}
          style={{ ...btnGhost, fontSize: 10.5, padding: '3px 9px' }}>
          {allOn ? t('models.selectNone') : t('models.selectAll')}
        </button>
      </div>

      {/* Model checklist */}
      <div style={{
        maxHeight: 240, overflowY: 'auto', marginBottom: 10,
        display: 'flex', flexDirection: 'column', gap: 1,
      }}>
        {list.map(m => {
          const on = selSet.has(m.id);
          return (
            <label key={m.id} style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '5px 8px', borderRadius: 6, cursor: 'pointer',
              background: on ? `${color}0C` : 'transparent',
            }}>
              <input
                type="checkbox"
                checked={on}
                onChange={() => mf.toggle(provider, m.id)}
                style={{ accentColor: color, width: 13, height: 13, flexShrink: 0, cursor: 'pointer' }}
              />
              <span style={{ fontSize: 11.5, color: 'var(--text-primary)', flex: 1 }}>
                {m.name || m.id}
              </span>
              {!!m.context_window && (
                <span style={{ fontSize: 9, color: 'var(--text-muted)' }}>
                  {m.context_window >= 1000 ? `${Math.round(m.context_window / 1000)}K` : m.context_window}
                </span>
              )}
              <span style={{
                fontSize: 9.5, color: 'var(--text-muted)',
                fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
              }}>{m.id}</span>
            </label>
          );
        })}
      </div>

      {/* Footer: selection count + actions */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ fontSize: 10.5, color: 'var(--text-muted)' }}>
          {t('models.selectedCount', { count: sel.length, total: list.length })}
        </span>
        <div style={{ flex: 1 }} />
        <button onClick={mf.close} style={btnGhost}>{t('settings.cancel')}</button>
        <button
          onClick={() => mf.save(provider, onApplied)}
          disabled={blocked}
          style={{
            ...btnPrimary, background: color,
            opacity: blocked ? 0.5 : 1,
            cursor: blocked ? 'not-allowed' : 'pointer',
          }}>
          {mf.saving ? t('settings.saving') : t('models.applySelection')}
        </button>
      </div>

      {mf.error[provider] && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6, marginTop: 8,
          fontSize: 10.5, color: 'var(--error)',
        }}>
          <AlertCircle size={12} style={{ flexShrink: 0 }} />
          <span>{mf.error[provider]}</span>
        </div>
      )}
    </div>
  );
};

const btnGhost: React.CSSProperties = {
  padding: '6px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 11,
  background: 'transparent', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)', fontWeight: 500,
  display: 'flex', alignItems: 'center', gap: 5,
};

const btnPrimary: React.CSSProperties = {
  padding: '6px 16px', borderRadius: 6, cursor: 'pointer', fontSize: 11,
  border: 'none', color: '#fff', fontWeight: 600,
  display: 'flex', alignItems: 'center', gap: 5,
};

export default ModelFetchPanel;
