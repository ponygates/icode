import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ShieldCheck, ShieldAlert, ShieldOff, RefreshCw } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

// Shape of GET /api/sandbox. runtime describes what is in force right now;
// config describes what will be applied at the next launch. They can disagree,
// and showing only the config would claim a boundary that does not exist.
export interface SandboxState {
  runtime: {
    enabled: boolean;
    backend?: string;
    available: boolean;
    mode: string;
    network: string;
    notices?: string[];
  };
  config: {
    mode: string;
    network: string;
    writable_paths: string[];
    grant_workspace: boolean;
  };
  restart_required: boolean;
}

const MODES = ['auto', 'on', 'off'] as const;

export function SandboxSection() {
  const { t } = useTranslation();
  const backendUrl = useAppStore((s) => s.backendUrl);
  const [state, setState] = useState<SandboxState | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [restartHint, setRestartHint] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await fetch(`${backendUrl}/api/sandbox`);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      setState(await r.json());
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [backendUrl]);

  useEffect(() => { void load(); }, [load]);

  const choose = async (mode: string) => {
    setSaving(true);
    setRestartHint(false);
    try {
      const r = await fetch(`${backendUrl}/api/sandbox`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode }),
      });
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      setState(await r.json());
      // The backend answers with restart_required, but the hint is shown
      // regardless: the process that is running now was built with the old
      // mode and nothing in this response can change that.
      setRestartHint(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const runtime = state?.runtime;
  const configuredMode = state?.config.mode || 'auto';
  // The badge reports the process, not the setting. A sandbox that is
  // configured but not installed gets the warning state, never the green one.
  const badge = !runtime
    ? { color: 'var(--text-muted)', Icon: ShieldAlert, text: t('tools.sandboxLoading') }
    : runtime.enabled
      ? { color: 'var(--success)', Icon: ShieldCheck, text: t('tools.sandboxActive') }
      : { color: 'var(--warning)', Icon: ShieldOff, text: t('tools.sandboxInactive') };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <badge.Icon size={14} color={badge.color} />
        <span style={{ fontSize: 12, fontWeight: 600, color: badge.color }}>{badge.text}</span>
        {runtime?.enabled && runtime.backend && (
          <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>
            {t('tools.sandboxBackend')}: {runtime.backend}
          </span>
        )}
        <button
          onClick={() => void load()}
          title={t('tools.sandboxRefresh')}
          style={{
            marginLeft: 'auto', background: 'transparent', border: 'none',
            cursor: 'pointer', color: 'var(--text-muted)', display: 'flex', padding: 4,
          }}
        >
          <RefreshCw size={12} />
        </button>
      </div>

      {runtime && !runtime.enabled && (
        <div style={{ fontSize: 11, color: 'var(--text-muted)', lineHeight: 1.6 }}>
          {t('tools.sandboxInactiveHint')}
        </div>
      )}

      <div style={{ display: 'flex', gap: 6 }}>
        {MODES.map((m) => {
          const sel = configuredMode === m;
          return (
            <button
              key={m}
              onClick={() => void choose(m)}
              disabled={saving}
              style={{
                padding: '6px 12px', borderRadius: 6, fontSize: 11,
                fontWeight: sel ? 600 : 400,
                background: sel ? 'var(--accent-soft)' : 'var(--bg-primary)',
                border: sel ? '1.5px solid var(--accent)' : '1px solid var(--border-color)',
                color: sel ? 'var(--accent)' : 'var(--text-secondary)',
                cursor: saving ? 'default' : 'pointer',
              }}
            >
              {t(`tools.sandboxMode_${m}`)}
            </button>
          );
        })}
      </div>

      <div style={{ fontSize: 10, color: 'var(--text-muted)', lineHeight: 1.6 }}>
        {t(`tools.sandboxModeHint_${configuredMode}`)}
      </div>

      {restartHint && (
        <div style={{ fontSize: 11, color: 'var(--warning)' }}>
          {t('tools.sandboxRestartHint')}
        </div>
      )}

      {error && (
        <div style={{ fontSize: 11, color: 'var(--error)' }}>
          {t('tools.sandboxError')}: {error}
        </div>
      )}

      {runtime?.notices && runtime.notices.length > 0 && (
        <div style={{
          padding: 10, background: 'var(--bg-primary)', borderRadius: 8,
          fontSize: 10, color: 'var(--text-muted)', lineHeight: 1.7,
        }}>
          {runtime.notices.map((n, i) => (
            <div key={i}>{n}</div>
          ))}
        </div>
      )}
    </div>
  );
}
