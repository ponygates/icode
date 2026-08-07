import React from 'react';
import { useTranslation } from 'react-i18next';
import { Folder, Plus, Check } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

// WorkspaceSidebar is the desktop "project container" panel — it lists the
// user's workspaces (each grouping related sessions) and lets them switch or
// create one. Creating a workspace can bind a real local directory: the native
// WebView2 bridge exposes window.pickDirectory() (a Win32 folder dialog), so a
// user can add an existing folder instead of hand-typing a path.
const WorkspaceSidebar: React.FC = () => {
  const { t } = useTranslation();
  const workspaces = useAppStore((s) => s.workspaces);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace);
  const createWorkspace = useAppStore((s) => s.createWorkspace);
  const [creating, setCreating] = React.useState(false);

  // basename returns the last path segment (or the whole path as a fallback),
  // used to derive a sensible default workspace name from a chosen folder.
  const basename = (p: string) => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p;

  const handleNew = async () => {
    if (creating) return;
    setCreating(true);
    try {
      // Prefer the native folder picker (desktop WebView2 bridge). When it is
      // unavailable (dev-mode browser / plain webview) fall back to prompts.
      const picker = (window as unknown as Record<string, unknown>).pickDirectory;
      if (typeof picker === 'function') {
        const picked = await (picker as () => Promise<string>)();
        if (!picked) return; // user cancelled the dialog
        const name = t('workspace.autoName', { dir: basename(picked) });
        await createWorkspace(name, picked);
        return;
      }
      // Fallback: legacy manual flow.
      const name = window.prompt(t('workspace.newPrompt') || '工作区名称', '');
      if (!name) return;
      const path = window.prompt(t('workspace.pathPrompt') || '路径（可选，留空为“未关联目录”）', '') || '';
      await createWorkspace(name.trim(), path.trim());
    } finally {
      setCreating(false);
    }
  };

  // Invokes the native folder picker to set/change a workspace's directory.
  const handlePickDir = async (id: string) => {
    const picker = (window as unknown as Record<string, unknown>).pickDirectory;
    if (typeof picker !== 'function') return;
    const path = await (picker as () => Promise<string>)();
    if (!path) return;
    await useAppStore.getState().updateWorkspace(id, { path });
  };

  return (
    <div style={{
      borderBottom: '1px solid var(--border-color)',
      padding: '8px 8px 10px', flexShrink: 0,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <div style={{
          fontSize: 10, textTransform: 'uppercase', letterSpacing: 0.5,
          color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: 4,
        }}>
          <Folder size={11} /> {t('workspace.title')}
        </div>
        <button
          onClick={handleNew}
          disabled={creating}
          title={t('workspace.new')}
          style={{
            background: 'transparent', border: 'none', cursor: creating ? 'default' : 'pointer',
            color: 'var(--text-muted)', display: 'flex', padding: 2, borderRadius: 4, opacity: creating ? 0.4 : 0.7,
          }}
          onMouseEnter={(e) => { if (!creating) e.currentTarget.style.opacity = '1'; }}
          onMouseLeave={(e) => { if (!creating) e.currentTarget.style.opacity = '0.7'; }}
        >
          <Plus size={13} />
        </button>
      </div>

      {workspaces.length === 0 ? (
        <div style={{ fontSize: 11, color: 'var(--text-muted)', padding: '2px 4px', lineHeight: 1.5 }}>
          {t('workspace.empty')}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2, maxHeight: 160, overflowY: 'auto' }}>
          {workspaces.map((ws) => {
            const active = ws.id === activeWorkspaceId;
            const hasPath = !!ws.path;
            return (
              <div
                key={ws.id}
                onClick={() => setActiveWorkspace(ws.id)}
                title={hasPath ? ws.path : undefined}
                style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '5px 8px', borderRadius: 6, cursor: 'pointer',
                  fontSize: 12,
                  background: active ? 'var(--bg-primary)' : 'transparent',
                  color: active ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}
              >
                {active ? <Check size={11} style={{ color: 'var(--accent)' }} /> : <Folder size={11} style={{ color: 'var(--text-muted)' }} />}
                <span style={{ overflow: 'hidden', flex: 1, lineHeight: 1.3 }}>
                  <span style={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ws.name}
                  </span>
                  {hasPath && (
                    <span style={{ display: 'block', fontSize: 9, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {ws.path}
                    </span>
                  )}
                </span>
                {ws.session_ids.length > 0 && (
                  <span style={{ fontSize: 10, color: 'var(--text-muted)', flexShrink: 0 }}>
                    {ws.session_ids.length}
                  </span>
                )}
                <button
                  title={hasPath ? t('workspace.changeDir') : t('workspace.bindDir')}
                  onClick={(e) => { e.stopPropagation(); handlePickDir(ws.id); }}
                  style={{ flexShrink: 0, cursor: 'pointer', background: 'transparent', border: 'none', color: 'var(--text-muted)', opacity: 0.7, display: 'flex', alignItems: 'center', padding: 1 }}
                >
                  <Folder size={11} />
                </button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default WorkspaceSidebar;
