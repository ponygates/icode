import React from 'react';
import { useTranslation } from 'react-i18next';
import { GitBranch, RefreshCw, X, Sparkles, ArrowUpRight, ArrowDownRight, ChevronDown, ChevronRight } from 'lucide-react';
import { useAppStore } from '../stores/appStore';
import { parseUnifiedDiff, DiffBody } from './DiffBody';

/**
 * Git workbench panel — the desktop counterpart of Claude Code / Reasonix
 * desktop's "review diffs → stage → commit" loop. Sits in the chat right
 * sidebar next to Todo/Checkpoint/LSP panels; polls /api/git/status while
 * expanded and offers per-file stage/unstage/discard, diff review, branch
 * switching and AI-drafted commit messages (cheap model).
 */

interface GitFile {
  path: string;
  index: string;
  worktree: string;
  untracked: boolean;
  renamed_from?: string;
}
interface GitStatus {
  repo: boolean;
  branch?: string;
  ahead: number;
  behind: number;
  files: GitFile[];
}

const statusColor = (f: GitFile): string => {
  if (f.untracked) return 'var(--text-muted)';
  if (f.index === 'D' || f.worktree === 'D') return 'var(--error)';
  if (f.index === 'A') return 'var(--success)';
  if (f.index === 'R' || f.index === 'C') return '#7048e8';
  return '#e0a745'; // M
};

const statusLetter = (f: GitFile): string => {
  if (f.untracked) return 'U';
  if (f.index) return f.index;
  return f.worktree || 'M';
};

const GitPanel: React.FC = () => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const [open, setOpen] = React.useState(false);
  const [status, setStatus] = React.useState<GitStatus | null>(null);
  const [checked, setChecked] = React.useState<Record<string, boolean>>({});
  const [diffFile, setDiffFile] = React.useState<GitFile | null>(null);
  const [diffText, setDiffText] = React.useState('');
  const [diffLoading, setDiffLoading] = React.useState(false);
  const [commitMsg, setCommitMsg] = React.useState('');
  const [msgBusy, setMsgBusy] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState('');
  const [branches, setBranches] = React.useState<{ name: string; current: boolean }[]>([]);
  const [branchOpen, setBranchOpen] = React.useState(false);

  const api = (path: string, opts?: RequestInit) =>
    fetch(`${backendUrl}/api/git${path}`, { headers: { 'Content-Type': 'application/json' }, ...opts });

  const load = React.useCallback(async () => {
    if (!backendUrl) return;
    try {
      const r = await api('/status');
      const d: GitStatus = await r.json();
      setStatus(d);
    } catch { /* backend gone — keep last state */ }
  }, [backendUrl]);

  React.useEffect(() => {
    if (!open || !backendUrl) return;
    let alive = true;
    const tick = () => { if (alive) load(); };
    tick();
    const iv = setInterval(tick, 5000);
    return () => { alive = false; clearInterval(iv); };
  }, [open, backendUrl, load]);

  React.useEffect(() => {
    if (!open || !backendUrl || !status?.repo) return;
    api('/branches').then(r => r.json()).then(d => setBranches(d.branches || [])).catch(() => {});
  }, [open, backendUrl, status?.repo, status?.branch]);

  const openDiff = async (f: GitFile, staged: boolean) => {
    setDiffFile(f);
    setDiffText('');
    setDiffLoading(true);
    try {
      const r = await api(`/diff?file=${encodeURIComponent(f.path)}${staged ? '&staged=1' : ''}`);
      const d = await r.json();
      setDiffText(d.diff || '');
    } catch { setDiffText(''); }
    setDiffLoading(false);
  };

  const checkedPaths = (status?.files || []).filter(f => checked[f.path]).map(f => f.path);
  const stagedCount = (status?.files || []).filter(f => f.index).length;

  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setErr('');
    try { await fn(); await load(); }
    catch (e) { setErr(String(e)); }
    setBusy(false);
  };

  const stage = (files: string[]) => run(async () => {
    const r = await api('/stage', { method: 'POST', body: JSON.stringify({ files }) });
    if (!r.ok) throw new Error((await r.json().catch(() => ({} as any))).error || r.status);
  });

  const unstage = (files: string[]) => run(async () => {
    const r = await api('/unstage', { method: 'POST', body: JSON.stringify({ files }) });
    if (!r.ok) throw new Error((await r.json().catch(() => ({} as any))).error || r.status);
  });

  const discard = (files: string[]) => run(async () => {
    if (!window.confirm(t('git.discardConfirm', { n: files.length }))) throw new Error('cancelled');
    const r = await api('/discard', { method: 'POST', body: JSON.stringify({ files }) });
    if (!r.ok) throw new Error((await r.json().catch(() => ({} as any))).error || r.status);
    setChecked({});
  });

  const commit = () => run(async () => {
    const r = await api('/commit', { method: 'POST', body: JSON.stringify({ message: commitMsg, files: checkedPaths }) });
    const d = await r.json().catch(() => ({} as any));
    if (!r.ok) throw new Error(d.error || r.status);
    setCommitMsg(''); setChecked({});
  });

  const aiMessage = async () => {
    setMsgBusy(true); setErr('');
    try {
      // Draft from the diff of what will be committed: checked files, else staged, else everything.
      const targets = checkedPaths.length ? checkedPaths : (status?.files || []).filter(f => f.index).map(f => f.path);
      const q = targets.length
        ? targets.map(p => `file=${encodeURIComponent(p)}`).join('&')
        : '';
      const dr = await api(`/diff${q ? `?${q}` : ''}`);
      const dd = await dr.json();
      const r = await api('/commit-message', { method: 'POST', body: JSON.stringify({ diff: dd.diff || '' }) });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error || r.status);
      setCommitMsg(d.message || '');
    } catch (e) {
      setErr(t('git.aiUnavailable', { error: String((e as Error).message || e) }));
    }
    setMsgBusy(false);
  };

  const checkout = (name: string) => run(async () => {
    const r = await api('/checkout', { method: 'POST', body: JSON.stringify({ branch: name }) });
    if (!r.ok) throw new Error((await r.json().catch(() => ({} as any))).error || r.status);
    setBranchOpen(false);
  });

  const newBranch = () => {
    const name = window.prompt(t('git.newBranchPrompt'));
    if (!name?.trim()) return;
    run(async () => {
      const r = await api('/checkout', { method: 'POST', body: JSON.stringify({ new: name.trim() }) });
      if (!r.ok) throw new Error((await r.json().catch(() => ({} as any))).error || r.status);
      setBranchOpen(false);
    });
  };

  const diffFiles = parseUnifiedDiff(diffText);

  return (
    <div style={{ borderTop: '0.5px solid var(--border-color)', padding: '12px 16px' }}>
      {/* Header row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', userSelect: 'none' }}
        onClick={() => setOpen(o => !o)}>
        {open ? <ChevronDown size={13} style={{ color: 'var(--text-muted)' }} /> : <ChevronRight size={13} style={{ color: 'var(--text-muted)' }} />}
        <GitBranch size={13} style={{ color: 'var(--accent)' }} />
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)' }}>{t('git.title')}</span>
        {status?.repo && (
          <span style={{ fontSize: 10.5, color: 'var(--text-muted)' }}>
            {status.branch}
            {status.ahead > 0 && <span title={t('git.ahead')}> <ArrowUpRight size={10} style={{ verticalAlign: -1, color: 'var(--success)' }} />{status.ahead}</span>}
            {status.behind > 0 && <span title={t('git.behind')}> <ArrowDownRight size={10} style={{ verticalAlign: -1, color: 'var(--warning)' }} />{status.behind}</span>}
          </span>
        )}
        <span style={{ flex: 1 }} />
        {(status?.files?.length || 0) > 0 && (
          <span style={{
            fontSize: 9.5, padding: '1px 7px', borderRadius: 99,
            background: 'rgba(224,167,69,0.15)', color: '#e0a745', fontWeight: 600,
          }}>{status!.files.length}</span>
        )}
      </div>

      {open && (
        <div style={{ marginTop: 10 }}>
          {!status?.repo ? (
            <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('git.noRepo')}</div>
          ) : (
            <>
              {/* File list */}
              {status.files.length === 0 ? (
                <div style={{ fontSize: 11, color: 'var(--text-muted)', padding: '2px 0 4px 19px' }}>
                  ✓ {t('git.clean')}
                </div>
              ) : (
                <div style={{ maxHeight: 190, overflowY: 'auto' }}>
                  {status.files.map(f => (
                    <div key={f.path} style={{
                      display: 'flex', alignItems: 'center', gap: 6, padding: '3px 4px', borderRadius: 5,
                    }}
                      onMouseEnter={e => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
                      onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}>
                      <input type="checkbox" checked={!!checked[f.path]}
                        onChange={e => setChecked(c => ({ ...c, [f.path]: e.target.checked }))}
                        style={{ accentColor: 'var(--accent)', cursor: 'pointer' }} />
                      <span style={{
                        fontSize: 10, fontWeight: 700, width: 14, textAlign: 'center', flexShrink: 0,
                        color: statusColor(f),
                      }}>{statusLetter(f)}</span>
                      <span onClick={() => openDiff(f, !!f.index && !f.worktree)} title={f.path}
                        style={{
                          fontSize: 11, color: 'var(--text-secondary)', cursor: 'pointer',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1,
                          textDecoration: 'underline dotted transparent',
                        }}
                        onMouseEnter={e => (e.currentTarget.style.textDecoration = 'underline dotted var(--text-muted)')}
                        onMouseLeave={e => (e.currentTarget.style.textDecoration = 'underline dotted transparent')}>
                        {f.path}
                      </span>
                      {f.index ? (
                        <button onClick={() => unstage([f.path])} title={t('git.unstageOne')}
                          style={{ border: 'none', background: 'transparent', cursor: 'pointer', padding: 2, color: 'var(--text-muted)', display: 'flex' }}>
                          <X size={11} />
                        </button>
                      ) : (
                        <button onClick={() => stage([f.path])} title={t('git.stageOne')}
                          style={{ border: 'none', background: 'transparent', cursor: 'pointer', padding: 2, color: 'var(--text-muted)', display: 'flex', fontSize: 11 }}>
                          +
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {/* Bulk actions */}
              {status.files.length > 0 && (
                <div style={{ display: 'flex', gap: 6, marginTop: 8, marginLeft: 19, flexWrap: 'wrap' }}>
                  <button disabled={busy} onClick={() => stage([])} style={miniBtn}>{t('git.stageAll')}</button>
                  <button disabled={busy || stagedCount === 0} onClick={() => unstage([])} style={miniBtn}>{t('git.unstageAll')}</button>
                  {checkedPaths.length > 0 && (
                    <button disabled={busy} onClick={() => discard(checkedPaths)}
                      style={{ ...miniBtn, color: 'var(--error)' }}>{t('git.discard', { n: checkedPaths.length })}</button>
                  )}
                </div>
              )}

              {/* Commit box */}
              <div style={{ display: 'flex', gap: 6, marginTop: 8, marginLeft: 19 }}>
                <input value={commitMsg} onChange={e => setCommitMsg(e.target.value)}
                  placeholder={t('git.commitPlaceholder')}
                  onKeyDown={e => { if (e.key === 'Enter' && commitMsg.trim()) commit(); }}
                  style={{
                    flex: 1, fontSize: 11, padding: '5px 9px', borderRadius: 6,
                    border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
                    color: 'var(--text-primary)', outline: 'none',
                  }} />
                <button onClick={aiMessage} disabled={msgBusy} title={t('git.aiTitle')} style={{
                  ...miniBtn, borderColor: 'var(--accent)', color: 'var(--accent)', flexShrink: 0,
                }}>{msgBusy ? '…' : <Sparkles size={11} />}</button>
                <button onClick={commit} disabled={busy || !commitMsg.trim() || (checkedPaths.length === 0 && stagedCount === 0)}
                  title={t('git.commitTitle')} style={{
                    ...miniBtn, background: 'var(--accent)', color: '#fff', borderColor: 'transparent', flexShrink: 0,
                  }}>{t('git.commit')}</button>
              </div>

              {/* Branch switcher */}
              <div style={{ marginTop: 8, marginLeft: 19, display: 'flex', alignItems: 'center', gap: 6 }}>
                <button onClick={() => setBranchOpen(o => !o)} style={miniBtn} disabled={branches.length === 0}>
                  {t('git.switchBranch')}
                </button>
                {branchOpen && (
                  <>
                    <select onChange={e => { if (e.target.value) checkout(e.target.value); }} value=""
                      style={{
                        fontSize: 10.5, padding: '3px 6px', borderRadius: 5,
                        border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
                        color: 'var(--text-primary)', outline: 'none', maxWidth: 130,
                      }}>
                      <option value="">{t('git.pickBranch')}</option>
                      {branches.filter(b => !b.current).map(b => <option key={b.name} value={b.name}>{b.name}</option>)}
                    </select>
                    <button onClick={newBranch} style={miniBtn}>+ {t('git.newBranch')}</button>
                  </>
                )}
                <span style={{ flex: 1 }} />
                <button onClick={load} title={t('git.refresh')} style={{ ...miniBtn, padding: '3px 6px' }}>
                  <RefreshCw size={10} />
                </button>
              </div>

              {err && <div style={{ marginTop: 6, marginLeft: 19, fontSize: 10.5, color: 'var(--error)', wordBreak: 'break-all' }}>{err}</div>}
            </>
          )}
        </div>
      )}

      {/* Diff modal */}
      {diffFile && (
        <div onClick={() => setDiffFile(null)} style={{
          position: 'fixed', inset: 0, zIndex: 1000,
          background: 'rgba(0,0,0,0.55)', backdropFilter: 'blur(4px)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <div onClick={e => e.stopPropagation()} style={{
            background: 'var(--bg-secondary)', borderRadius: 14,
            border: '1px solid var(--border-color)',
            width: 'min(880px, 92vw)', height: 'min(640px, 85vh)',
            display: 'flex', flexDirection: 'column', overflow: 'hidden',
            boxShadow: '0 20px 60px rgba(0,0,0,0.35)',
          }}>
            <div style={{
              padding: '14px 18px', borderBottom: '1px solid var(--border-color)',
              display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8,
            }}>
              <span style={{
                fontSize: 13, fontWeight: 600, color: 'var(--text-primary)',
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              }}>{t('git.diffTitle')} — {diffFile.path}</span>
              <button onClick={() => setDiffFile(null)} style={closeBtn}><X size={16} /></button>
            </div>
            <div style={{
              flex: 1, overflowY: 'auto', padding: '12px 16px',
              fontFamily: 'var(--font-mono)', fontSize: 11.5, lineHeight: 1.55,
            }}>
              {diffLoading
                ? <div style={{ color: 'var(--text-muted)', textAlign: 'center', paddingTop: 40 }}>{t('diff.loading')}</div>
                : diffFiles.length === 0
                  ? <div style={{ color: 'var(--text-muted)', textAlign: 'center', paddingTop: 40 }}>{t('diff.empty')}</div>
                  : <DiffBody files={diffFiles} />}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

const miniBtn: React.CSSProperties = {
  fontSize: 10.5, padding: '4px 10px', borderRadius: 6, cursor: 'pointer',
  background: 'transparent', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)', fontWeight: 500,
  display: 'flex', alignItems: 'center', gap: 4,
};

const closeBtn: React.CSSProperties = {
  background: 'transparent', border: 'none', color: 'var(--text-muted)',
  cursor: 'pointer', padding: 4, display: 'flex', borderRadius: 6,
};

export default GitPanel;
