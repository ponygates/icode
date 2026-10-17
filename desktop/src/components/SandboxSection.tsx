import { useCallback, useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { ShieldCheck, ShieldAlert, ShieldOff, RefreshCw, FolderLock, Undo2 } from 'lucide-react';
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
    // Whether "!" commands are inside the boundary too. Separate from enabled
    // because a confined sandbox can still leave them unrestricted.
    confine_user_shell?: boolean;
  };
  config: {
    mode: string;
    network: string;
    writable_paths: string[];
    grant_workspace: boolean;
    confine_user_shell: boolean;
  };
  restart_required: boolean;
}

// A directory whose access-control list now carries an entry for the sandbox
// identity. This is a change to the user's disk, so it is listed, not hidden.
export interface SandboxGrant {
  path: string;
  granted_at: string;
}

const MODES = ['auto', 'on', 'off'] as const;

export function SandboxSection() {
  const { t } = useTranslation();
  const backendUrl = useAppStore((s) => s.backendUrl);
  const [state, setState] = useState<SandboxState | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [restartHint, setRestartHint] = useState(false);

  const [grants, setGrants] = useState<SandboxGrant[]>([]);
  const [revocable, setRevocable] = useState(true);
  const [grantError, setGrantError] = useState('');
  const [grantNotice, setGrantNotice] = useState('');
  const [revoking, setRevoking] = useState('');
  const [confirmRevoke, setConfirmRevoke] = useState('');
  // The mode waiting for consent. Confinement writes an inheritable ACE into
  // the project directory, so switching it on is a filesystem change the user
  // agrees to explicitly — not a side effect they discover later.
  const [pendingMode, setPendingMode] = useState<string | null>(null);

  const loadGrants = useCallback(async () => {
    try {
      const r = await fetch(`${backendUrl}/api/sandbox/grants`);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      const data = await r.json();
      setGrants(Array.isArray(data.grants) ? data.grants : []);
      setRevocable(data.revocable !== false);
      setGrantError('');
    } catch (e) {
      setGrantError(e instanceof Error ? e.message : String(e));
    }
  }, [backendUrl]);

  const load = useCallback(async () => {
    try {
      const r = await fetch(`${backendUrl}/api/sandbox`);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      setState(await r.json());
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    await loadGrants();
  }, [backendUrl, loadGrants]);

  useEffect(() => { void load(); }, [load]);

  const runtime = state?.runtime;
  const configuredMode = state?.config.mode || 'auto';

  const save = async (patch: Record<string, unknown>) => {
    setSaving(true);
    setRestartHint(false);
    try {
      const r = await fetch(`${backendUrl}/api/sandbox`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
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

  const choose = (mode: string) => {
    // Turning confinement back on is the moment new ACEs start appearing on
    // disk, so that transition asks first. Turning it off never does.
    if (mode !== 'off' && configuredMode === 'off') {
      setPendingMode(mode);
      return;
    }
    void save({ mode });
  };

  const confirmConsent = () => {
    const mode = pendingMode;
    setPendingMode(null);
    if (mode) void save({ mode });
  };

  const revoke = async (path: string) => {
    setRevoking(path);
    setGrantNotice('');
    try {
      const r = await fetch(`${backendUrl}/api/sandbox/grants`, {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
      });
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      const data = await r.json();
      setGrants(Array.isArray(data.grants) ? data.grants : []);
      // revoked=false means the ACL never carried the entry. Saying "revoked"
      // there would take credit for a change that did not happen.
      setGrantNotice(data.revoked
        ? t('tools.sandboxGrantsRevoked', { path })
        : t('tools.sandboxGrantsAlreadyClean', { path }));
      setGrantError('');
      setConfirmRevoke('');
    } catch (e) {
      setGrantError(e instanceof Error ? e.message : String(e));
    } finally {
      setRevoking('');
    }
  };

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
              onClick={() => choose(m)}
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

      {/* The third shell entry point. It stays unrestricted by default —
          these commands are typed by the machine's owner, and confinement
          would break git push and editor launches. Saying so out loud beats
          letting "sandbox is on" imply more than it does. */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <label style={{
          display: 'flex', alignItems: 'center', gap: 8,
          cursor: saving ? 'default' : 'pointer',
        }}>
          <input
            type="checkbox"
            checked={!!state?.config.confine_user_shell}
            disabled={saving || !state}
            onChange={(e) => void save({ confine_user_shell: e.target.checked })}
            style={{ margin: 0, cursor: 'inherit' }}
          />
          <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-secondary)' }}>
            {t('tools.sandboxUserShellTitle')}
          </span>
        </label>
        <div style={{ fontSize: 10, color: 'var(--text-muted)', lineHeight: 1.6, paddingLeft: 22 }}>
          {t('tools.sandboxUserShellDesc')}
        </div>
        {runtime?.enabled && !runtime.confine_user_shell && (
          <div style={{ fontSize: 10, color: 'var(--warning)', paddingLeft: 22, lineHeight: 1.6 }}>
            {t('tools.sandboxUserShellGap')}
          </div>
        )}
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

      <GrantList
        grants={grants}
        revocable={revocable}
        revoking={revoking}
        confirmRevoke={confirmRevoke}
        error={grantError}
        notice={grantNotice}
        onAskRevoke={setConfirmRevoke}
        onCancelRevoke={() => setConfirmRevoke('')}
        onRevoke={(p) => void revoke(p)}
      />

      {pendingMode && (
        <ConsentDialog
          mode={pendingMode}
          onCancel={() => setPendingMode(null)}
          onConfirm={confirmConsent}
        />
      )}
    </div>
  );
}

interface GrantListProps {
  grants: SandboxGrant[];
  revocable: boolean;
  revoking: string;
  confirmRevoke: string;
  error: string;
  notice: string;
  onAskRevoke: (path: string) => void;
  onCancelRevoke: () => void;
  onRevoke: (path: string) => void;
}

// GrantList is split out so the revoke flow can be exercised without a live
// backend, and so the section above stays readable.
function GrantList(props: GrantListProps) {
  const { t } = useTranslation();
  const { grants, revocable, revoking, confirmRevoke, error, notice } = props;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <FolderLock size={12} color="var(--text-muted)" />
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-secondary)' }}>
          {t('tools.sandboxGrantsTitle')}
        </span>
      </div>
      <div style={{ fontSize: 10, color: 'var(--text-muted)', lineHeight: 1.6 }}>
        {revocable ? t('tools.sandboxGrantsDesc') : t('tools.sandboxGrantsUnsupported')}
      </div>

      {grants.length === 0 ? (
        <div style={{ fontSize: 10, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          {t('tools.sandboxGrantsEmpty')}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {grants.map((g) => {
            const confirming = confirmRevoke === g.path;
            return (
              <div
                key={g.path}
                style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  padding: '6px 8px', borderRadius: 6,
                  background: 'var(--bg-primary)', border: '1px solid var(--border-color)',
                }}
              >
                <span
                  title={g.path}
                  style={{
                    flex: 1, fontSize: 10, color: 'var(--text-secondary)',
                    fontFamily: 'ui-monospace, monospace',
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    direction: 'rtl', textAlign: 'left',
                  }}
                >
                  {g.path}
                </span>
                {confirming ? (
                  <>
                    <button
                      onClick={props.onRevoke.bind(null, g.path)}
                      disabled={revoking === g.path}
                      style={dangerBtn}
                    >
                      {t('tools.sandboxGrantsRevokeConfirm')}
                    </button>
                    <button onClick={props.onCancelRevoke} style={quietBtn}>
                      {t('tools.sandboxGrantsCancel')}
                    </button>
                  </>
                ) : (
                  <button
                    onClick={() => props.onAskRevoke(g.path)}
                    disabled={!revocable || revoking === g.path}
                    title={revocable ? t('tools.sandboxGrantsRevoke') : t('tools.sandboxGrantsUnsupported')}
                    style={{
                      ...quietBtn,
                      display: 'flex', alignItems: 'center', gap: 3,
                      opacity: revocable ? 1 : 0.4,
                      cursor: revocable ? 'pointer' : 'default',
                    }}
                  >
                    <Undo2 size={10} />
                    {t('tools.sandboxGrantsRevoke')}
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {notice && (
        <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{notice}</div>
      )}
      {error && (
        <div style={{ fontSize: 10, color: 'var(--error)' }}>
          {t('tools.sandboxGrantsFailed')}: {error}
        </div>
      )}
    </div>
  );
}

// ConsentDialog states what will change on disk before it changes. Dismissing
// it leaves the mode untouched, so cancelling is a real no-op.
function ConsentDialog({ mode, onCancel, onConfirm }: {
  mode: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="dialog"
      aria-modal="true"
      style={{
        position: 'fixed', inset: 0, zIndex: 1000,
        background: 'rgba(0,0,0,0.45)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: 24,
      }}
      onClick={onCancel}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          maxWidth: 460, width: '100%', borderRadius: 12, padding: 20,
          background: 'var(--bg-secondary)', border: '1px solid var(--border-color)',
          boxShadow: '0 12px 40px rgba(0,0,0,0.28)',
          display: 'flex', flexDirection: 'column', gap: 12,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ShieldAlert size={16} color="var(--warning)" />
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
            {t('tools.sandboxConsentTitle')}
          </span>
        </div>
        <div style={{ fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.75 }}>
          {t('tools.sandboxConsentBody')}
        </div>
        <ul style={{
          margin: 0, paddingLeft: 16, fontSize: 11,
          color: 'var(--text-muted)', lineHeight: 1.8,
        }}>
          <li>{t('tools.sandboxConsentPoint1')}</li>
          <li>{t('tools.sandboxConsentPoint2')}</li>
        </ul>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
          <button onClick={onCancel} style={quietBtn}>
            {t('tools.sandboxConsentCancel')}
          </button>
          <button onClick={onConfirm} style={primaryBtn}>
            {t('tools.sandboxConsentConfirm')}
          </button>
        </div>
        <div style={{ fontSize: 9, color: 'var(--text-muted)' }}>
          {t('tools.sandboxConsentMode', { mode: t(`tools.sandboxMode_${mode}`) })}
        </div>
      </div>
    </div>
  );
}

const quietBtn: CSSProperties = {
  padding: '4px 10px', borderRadius: 6, fontSize: 10,
  background: 'transparent', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)', cursor: 'pointer',
};

const dangerBtn: CSSProperties = {
  ...quietBtn,
  border: '1px solid var(--error)',
  color: 'var(--error)',
};

const primaryBtn: CSSProperties = {
  padding: '6px 14px', borderRadius: 6, fontSize: 11, fontWeight: 600,
  background: 'var(--accent)', border: '1px solid var(--accent)',
  color: '#fff', cursor: 'pointer',
};
