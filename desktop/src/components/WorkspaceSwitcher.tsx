import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Folder, Check, ChevronDown, FolderOpen, Plus, ExternalLink } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

// WorkspaceSwitcher is the clickable "工作文件夹" pill on the chat toolbar.
// It shows the active workspace (or its bound directory basename) and opens a
// popover to: switch workspace, re-bind the directory via the native folder
// dialog (window.pickDirectory, WebView2 bridge), create a new workspace, or
// reveal the folder in the OS file manager. This makes the folder display an
// actual control instead of a static label (对标 WorkBuddy/VS Code 的目录切换).
const WorkspaceSwitcher: React.FC<{ compact?: boolean }> = ({ compact }) => {
  const { t } = useTranslation();
  const workspaces = useAppStore((s) => s.workspaces);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace);
  const updateWorkspace = useAppStore((s) => s.updateWorkspace);
  const createWorkspace = useAppStore((s) => s.createWorkspace);

  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // Close on outside click / Escape (popover is a lightweight custom dropdown).
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

  const active = workspaces.find((w) => w.id === activeWorkspaceId);
  const basename = (p: string) => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p;

  // Picks a directory via the native WebView2 dialog; falls back to a text
  // prompt in a plain browser (dev mode) so the feature still works there.
  const pick = async (): Promise<string | null> => {
    const picker = (window as unknown as Record<string, unknown>).pickDirectory;
    if (typeof picker === 'function') {
      try {
        return (await (picker as () => Promise<string>)()) || null;
      } catch { return null; }
    }
    const manual = window.prompt(t('workspace.pathPrompt') || '关联目录路径（可选）：', '');
    return (manual || '').trim() || null;
  };

  // 浏览目录：把选中的文件夹绑定到当前工作区；没有工作区就先建一个。
  const handleBrowse = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const path = await pick();
      if (!path) return;
      if (active) {
        await updateWorkspace(active.id, { path });
      } else {
        await createWorkspace(t('workspace.autoName', { dir: basename(path) }), path);
      }
    } finally {
      setBusy(false);
      setOpen(false);
    }
  };

  // 新建工作区（选择目录后自动命名）。
  const handleNew = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const path = await pick();
      if (!path) return;
      await createWorkspace(t('workspace.autoName', { dir: basename(path) }), path);
    } finally {
      setBusy(false);
      setOpen(false);
    }
  };

  const handleOpenInExplorer = () => {
    const p = active?.path;
    if (window.icode?.openFolder) {
      window.icode.openFolder(p || '.').catch(() => {});
    }
    setOpen(false);
  };

  const label = active
    ? (active.path ? basename(active.path) : active.name)
    : (t('chat.openDir') || '工作文件夹');

  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button
        className="interactive"
        title={active?.path || label}
        onClick={() => setOpen((v) => !v)}
        style={compact ? {
          display: 'flex', alignItems: 'center', gap: 5,
          background: 'transparent', border: 'none',
          color: 'var(--text-secondary)', padding: '2px 6px',
          borderRadius: 6, fontSize: 11, maxWidth: 220,
        } : {
          display: 'flex', alignItems: 'center', gap: 6,
          background: active ? 'var(--accent-soft)' : 'none',
          border: active ? '0.5px solid var(--accent)' : '0.5px solid var(--border-color)',
          color: active ? 'var(--accent)' : 'var(--text-secondary)',
          padding: '4px 10px', borderRadius: 6, fontSize: 12,
          maxWidth: 220,
        }}
      >
        <FolderOpen size={compact ? 12 : 13} style={{ flexShrink: 0 }} />
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {label}
        </span>
        <ChevronDown size={10} style={{ flexShrink: 0, opacity: 0.7 }} />
      </button>

      {open && (
        <div style={{
          position: 'absolute', top: 'calc(100% + 6px)', left: 0, zIndex: 80,
          width: 280, maxHeight: 320, overflowY: 'auto',
          background: 'var(--bg-elev)', border: '0.5px solid var(--border-color)',
          borderRadius: 10, padding: 6,
          boxShadow: '0 12px 32px rgba(0,0,0,0.3)',
        }}>
          <div style={{
            padding: '5px 8px 4px', fontSize: 9, letterSpacing: 1,
            textTransform: 'uppercase', color: 'var(--text-muted)', fontWeight: 600,
          }}>
            {t('workspace.title')}
          </div>
          {workspaces.length === 0 && (
            <div style={{ padding: '8px', fontSize: 11, color: 'var(--text-muted)' }}>
              {t('workspace.empty')}
            </div>
          )}
          {workspaces.map((ws) => {
            const isActive = ws.id === activeWorkspaceId;
            return (
              <div
                key={ws.id}
                onClick={() => { setActiveWorkspace(ws.id); setOpen(false); }}
                title={ws.path || undefined}
                style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  padding: '6px 8px', borderRadius: 7, cursor: 'pointer',
                  fontSize: 12, background: isActive ? 'var(--accent-soft)' : 'transparent',
                  color: isActive ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}
                onMouseEnter={(e) => { if (!isActive) e.currentTarget.style.background = 'var(--bg-hover)'; }}
                onMouseLeave={(e) => { if (!isActive) e.currentTarget.style.background = 'transparent'; }}
              >
                {isActive
                  ? <Check size={12} style={{ color: 'var(--accent)', flexShrink: 0 }} />
                  : <Folder size={12} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />}
                <span style={{ flex: 1, minWidth: 0 }}>
                  <span style={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ws.name}
                  </span>
                  {ws.path && (
                    <span style={{ display: 'block', fontSize: 9, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {ws.path}
                    </span>
                  )}
                </span>
              </div>
            );
          })}

          <div style={{ height: 1, background: 'var(--border-color)', margin: '5px 0' }} />

          <button
            onClick={handleBrowse}
            disabled={busy}
            style={actionRow}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--bg-hover)'; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; }}
          >
            <Folder size={12} /> {active ? t('workspace.changeDir') : t('workspace.bindDir')}
          </button>
          <button
            onClick={handleNew}
            disabled={busy}
            style={actionRow}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--bg-hover)'; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; }}
          >
            <Plus size={12} /> {t('workspace.new')}
          </button>
          <button
            onClick={handleOpenInExplorer}
            style={actionRow}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--bg-hover)'; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; }}
          >
            <ExternalLink size={12} /> {t('workspace.openInExplorer')}
          </button>
        </div>
      )}
    </div>
  );
};

const actionRow: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, width: '100%',
  padding: '6px 8px', borderRadius: 7, cursor: 'pointer',
  fontSize: 12, color: 'var(--text-secondary)',
  background: 'transparent', border: 'none', textAlign: 'left',
};

export default WorkspaceSwitcher;
