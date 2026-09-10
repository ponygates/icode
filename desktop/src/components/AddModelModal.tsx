import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Check, AlertCircle, Cpu } from 'lucide-react';

export interface AddModelPayload {
  /** The vendor's own model id — what the API expects, not the composite key. */
  id: string;
  name: string;
  provider: string;
  contextWindow?: number;
  maxOutputTokens?: number;
}

/**
 * Add a model to a vendor by hand.
 *
 * Needed because the built-in catalogue is a build-time snapshot and live
 * discovery needs a working key: a model the vendor ships can be usable long
 * before either of those catches up, and an OpenAI-compatible relay often
 * exposes ids no catalogue will ever list.
 *
 * Shared by the standalone model page and the settings modal. When opened from
 * a vendor card the provider is fixed and pre-filled, which is the whole point
 * — the user is already looking at the vendor they mean.
 */
export const AddModelModal: React.FC<{
  open: boolean;
  /** Locked provider when opened from a vendor card; free text otherwise. */
  presetProvider?: string;
  onClose: () => void;
  /** Resolves to an error message, or null on success. */
  onSubmit: (payload: AddModelPayload) => Promise<string | null>;
}> = ({ open, presetProvider, onClose, onSubmit }) => {
  const { t } = useTranslation();
  const [id, setId] = useState('');
  const [name, setName] = useState('');
  const [provider, setProvider] = useState('');
  const [contextWindow, setContextWindow] = useState('');
  const [maxOutputTokens, setMaxOutputTokens] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // Reset on each open so a previous attempt's error or half-typed id never
  // leaks into the next one.
  useEffect(() => {
    if (!open) return;
    setId('');
    setName('');
    setProvider(presetProvider || '');
    setContextWindow('');
    setMaxOutputTokens('');
    setError('');
    setBusy(false);
  }, [open, presetProvider]);

  if (!open) return null;

  const locked = !!presetProvider;
  const canSubmit = !!id.trim() && !!provider.trim() && !busy;

  const submit = async () => {
    const trimmedId = id.trim();
    const trimmedProvider = provider.trim();
    if (!trimmedId || !trimmedProvider) return;
    if (/\s/.test(trimmedId)) {
      setError(t('models.modelIdNoSpace'));
      return;
    }
    setBusy(true);
    setError('');
    const err = await onSubmit({
      id: trimmedId,
      // Falling back to the id keeps a one-field add possible — most of the
      // time the id is already readable enough.
      name: name.trim() || trimmedId,
      provider: trimmedProvider,
      contextWindow: contextWindow ? parseInt(contextWindow, 10) || undefined : undefined,
      maxOutputTokens: maxOutputTokens ? parseInt(maxOutputTokens, 10) || undefined : undefined,
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    onClose();
  };

  return (
    <div onClick={onClose} style={overlayStyle}>
      <div onClick={(e) => e.stopPropagation()} style={panelStyle}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '18px 22px 14px', borderBottom: '1px solid var(--border-color)' }}>
          <Cpu size={16} color="var(--accent, #6366F1)" />
          <div style={{ flex: 1, fontSize: 14.5, fontWeight: 600, color: 'var(--text-primary)' }}>
            {locked
              ? t('models.addModelTo', { provider: presetProvider })
              : t('models.addCustom')}
          </div>
          <span onClick={onClose} style={{ cursor: 'pointer', display: 'flex', color: 'var(--text-muted)' }}>
            <X size={15} />
          </span>
        </div>

        <div style={{ padding: '16px 22px', display: 'flex', flexDirection: 'column', gap: 12 }}>
          {error && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 6,
              fontSize: 11, color: 'var(--error, #f85149)',
              background: 'var(--error-soft, rgba(248,81,73,0.1))',
              border: '1px solid var(--error, #f85149)',
              borderRadius: 8, padding: '8px 12px',
            }}>
              <AlertCircle size={12} style={{ flexShrink: 0 }} />
              <span>{error}</span>
            </div>
          )}

          <Field label={`${t('models.modelId')} *`} hint={t('models.modelIdHint')}>
            <input
              autoFocus
              value={id}
              onChange={(e) => setId(e.target.value)}
              placeholder={t('models.modelIdExample')}
              style={inputStyle}
            />
          </Field>

          <Field label={t('models.modelName')} hint={t('models.modelNameOptional')}>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('models.modelNameExample')}
              style={inputStyle}
            />
          </Field>

          {!locked && (
            <Field label={`${t('models.providerName')} *`} hint={t('models.providerNameHint')}>
              <input
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
                placeholder={t('models.providerNameExample')}
                style={inputStyle}
              />
            </Field>
          )}

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            <Field label={t('models.contextWindow')} hint={t('models.optional')}>
              <input
                value={contextWindow}
                onChange={(e) => setContextWindow(e.target.value.replace(/[^0-9]/g, ''))}
                placeholder="128000"
                style={inputStyle}
              />
            </Field>
            <Field label={t('models.maxOutputTokens')} hint={t('models.optional')}>
              <input
                value={maxOutputTokens}
                onChange={(e) => setMaxOutputTokens(e.target.value.replace(/[^0-9]/g, ''))}
                placeholder="8192"
                style={inputStyle}
              />
            </Field>
          </div>

          {/* Honest about a limit rather than offering a field that does
              nothing: per-model base URLs are not routed at chat time, so a
              model on a different endpoint needs its own vendor. */}
          <div style={{ fontSize: 10.5, color: 'var(--text-muted)', lineHeight: 1.5 }}>
            {t('models.addModelEndpointHint')}
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, padding: '14px 22px', borderTop: '1px solid var(--border-color)' }}>
          <button onClick={onClose} style={btnGhostStyle}>{t('settings.cancel')}</button>
          <button onClick={submit} disabled={!canSubmit} style={{
            ...btnPrimaryStyle,
            opacity: canSubmit ? 1 : 0.5,
            cursor: canSubmit ? 'pointer' : 'not-allowed',
          }}>
            <Check size={13} /> {busy ? t('settings.saving') : t('models.add')}
          </button>
        </div>
      </div>
    </div>
  );
};

const Field: React.FC<{ label: string; hint?: string; children: React.ReactNode }> = ({ label, hint, children }) => (
  <div>
    <label style={{ fontSize: 11, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
      {label}
      {hint && <span style={{ color: 'var(--text-muted)', marginLeft: 6, fontSize: 10 }}>{hint}</span>}
    </label>
    {children}
  </div>
);

const overlayStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, zIndex: 1000,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
};

const panelStyle: React.CSSProperties = {
  background: 'var(--bg-secondary)', borderRadius: 16,
  border: '1px solid var(--border-color)', width: 440, maxWidth: '92vw',
  maxHeight: '88vh', overflowY: 'auto',
  boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
};

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: 8, fontSize: 12.5,
  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
  color: 'var(--text-primary)', outline: 'none', boxSizing: 'border-box',
};

const btnGhostStyle: React.CSSProperties = {
  padding: '8px 16px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
  background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)',
};

const btnPrimaryStyle: React.CSSProperties = {
  padding: '8px 20px', borderRadius: 8, cursor: 'pointer', fontSize: 12,
  background: '#6366F1', border: 'none', color: '#fff', fontWeight: 600,
  display: 'flex', alignItems: 'center', gap: 6,
};

export default AddModelModal;
