import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ListChecks, AlertCircle, Sparkle, Search, X } from 'lucide-react';
import type { UseModelFetch } from '../lib/useModelFetch';

/**
 * Checklist shown after "获取模型" pulls a vendor's live catalogue.
 *
 * The list is the union of three sources — what the vendor reported, the
 * built-in catalogue, and models the user added by hand — so it is the only
 * surface that can honestly claim to be "every model of this vendor". That is
 * also why it needs a search box: a big vendor plus its built-ins easily runs
 * to dozens of rows.
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
  // Declared above the early return: hooks must run on every render, and the
  // panel unmounts its body whenever another vendor's panel is opened.
  const [query, setQuery] = useState('');

  const list = mf.lists[provider] || [];
  const sel = mf.ticked[provider] || [];
  if (mf.panel !== provider || list.length === 0) return null;

  const selSet = new Set(sel);
  const q = query.trim().toLowerCase();
  // Match id *and* display name: the id is what the vendor accepts, the name
  // is what the user recognises, and either may be what they type.
  const shown = q
    ? list.filter(m => m.id.toLowerCase().includes(q) || (m.name || '').toLowerCase().includes(q))
    : list;
  const builtinOnly = list.filter(m => m.builtin_only).length;
  const shownIds = shown.map(m => m.id);
  // "Select all" reflects what is on screen, so after a search it means
  // "everything that matched" rather than "everything, including what I
  // cannot see".
  const allOn = shownIds.length > 0 && shownIds.every(id => selSet.has(id));
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
          onClick={() => mf.selectAll(provider, !allOn, shownIds)}
          style={{ ...btnGhost, fontSize: 10.5, padding: '3px 9px' }}>
          {allOn
            ? (q ? t('models.selectNoneShown') : t('models.selectNone'))
            : (q ? t('models.selectAllShown') : t('models.selectAll'))}
        </button>
      </div>

      {/* Why the list can be longer than the vendor's own /models response.
          Without this the extra rows look like a bug. */}
      {builtinOnly > 0 && (
        <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 8, lineHeight: 1.5 }}>
          {t('models.builtinNote', { count: builtinOnly })}
        </div>
      )}

      {/* Search */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
        <Search size={12} color="var(--text-muted)" style={{ flexShrink: 0 }} />
        <input
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder={t('models.searchPlaceholder')}
          style={{ ...searchInput, borderColor: q ? `${color}80` : 'var(--border-color)' }}
        />
        {!!query && (
          <span
            onClick={() => setQuery('')}
            style={{ cursor: 'pointer', display: 'flex', color: 'var(--text-muted)', flexShrink: 0 }}>
            <X size={13} />
          </span>
        )}
        {!!q && (
          <span style={{ fontSize: 10, color: 'var(--text-muted)', flexShrink: 0, whiteSpace: 'nowrap' }}>
            {t('models.shownCount', { shown: shown.length, total: list.length })}
          </span>
        )}
      </div>

      {/* Model checklist */}
      <div style={{
        maxHeight: 240, overflowY: 'auto', marginBottom: 10,
        display: 'flex', flexDirection: 'column', gap: 1,
      }}>
        {shown.length === 0 && (
          <div style={{ fontSize: 11, color: 'var(--text-muted)', padding: '14px 8px', textAlign: 'center' }}>
            {t('models.searchNoMatch')}
          </div>
        )}
        {shown.map(m => {
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
              {/* Absent from the built-in catalogue: the vendor knows a model
                  this build does not. Worth flagging — it is the whole reason
                  live discovery exists. */}
              {!m.known && (
                <span title={t('models.newModelHint')} style={{
                  fontSize: 8.5, padding: '1px 5px', borderRadius: 3,
                  fontWeight: 600, background: `${color}18`, color,
                  display: 'inline-flex', alignItems: 'center', gap: 2, flexShrink: 0,
                }}>
                  <Sparkle size={8} />{t('models.newModel')}
                </span>
              )}
              {/* The mirror image: a built-in the vendor did not report. Shown
                  so the user can still tick it instead of hitting a dead end
                  in the manual-add dialog. */}
              {m.builtin_only && (
                <span style={{
                  fontSize: 8.5, padding: '1px 5px', borderRadius: 3,
                  fontWeight: 600,
                  background: 'var(--bg-primary)', color: 'var(--text-muted)',
                  border: '1px solid var(--border-color)', flexShrink: 0,
                }}>{t('models.builtinBadge')}</span>
              )}
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

const searchInput: React.CSSProperties = {
  flex: 1, minWidth: 0, padding: '5px 9px', borderRadius: 6, fontSize: 11.5,
  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
  color: 'var(--text-primary)', outline: 'none', boxSizing: 'border-box',
};

export default ModelFetchPanel;
