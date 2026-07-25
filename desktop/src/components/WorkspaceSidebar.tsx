import React from 'react';
import { useTranslation } from 'react-i18next';
import { Folder, Plus, Check } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

// WorkspaceSidebar is the desktop "project container" panel — it lists the
// user's workspaces (each grouping related sessions) and lets them switch or
// create one. This is the v0.12 workspace foundation; deeper per-workspace
// session binding UI lands in a later release.
const WorkspaceSidebar: React.FC = () => {
  const { t } = useTranslation();
  const workspaces = useAppStore((s) => s.workspaces);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace);
  const createWorkspace = useAppStore((s) => s.createWorkspace);

  const handleNew = () => {
    const name = window.prompt(t('workspace.newPrompt') || '工作区名称', '');
    if (!name) return;
    const path = window.prompt(t('workspace.pathPrompt') || '路径（可选，留空为“未关联目录”）', '') || '';
    createWorkspace(name.trim(), path.trim());
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
          title={t('workspace.new')}
          style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-muted)', display: 'flex', padding: 2, borderRadius: 4, opacity: 0.7,
          }}
          onMouseEnter={(e) => (e.currentTarget.style.opacity = '1')}
          onMouseLeave={(e) => (e.currentTarget.style.opacity = '0.7')}
        >
          <Plus size={13} />
        </button>
      </div>

      {workspaces.length === 0 ? (
        <div style={{ fontSize: 11, color: 'var(--text-muted)', padding: '2px 4px', lineHeight: 1.5 }}>
          {t('workspace.empty')}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2, maxHeight: 140, overflowY: 'auto' }}>
          {workspaces.map((ws) => {
            const active = ws.id === activeWorkspaceId;
            return (
              <div
                key={ws.id}
                onClick={() => setActiveWorkspace(ws.id)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '5px 8px', borderRadius: 6, cursor: 'pointer',
                  fontSize: 12,
                  background: active ? 'var(--bg-primary)' : 'transparent',
                  color: active ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}
              >
                {active ? <Check size={11} style={{ color: 'var(--accent)' }} /> : <Folder size={11} style={{ color: 'var(--text-muted)' }} />}
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
                  {ws.name}
                </span>
                {ws.session_ids.length > 0 && (
                  <span style={{ fontSize: 10, color: 'var(--text-muted)', flexShrink: 0 }}>
                    {ws.session_ids.length}
                  </span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default WorkspaceSidebar;
