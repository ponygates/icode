import React, { useState } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { MessageSquare, Cpu, Settings, BarChart, ArrowLeftRight, PanelLeftClose, Github, Plus, Server, Pencil } from 'lucide-react';

interface Props { onToggle: () => void; }

const Sidebar: React.FC<Props> = ({ onToggle }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');

  const {
    sessions, activeSessionId, selectedModel,
    setActiveSession, createSession, deleteSession, renameSession,
  } = useAppStore();
  const currentModel = useAppStore((s) => s.models.find((m) => m.id === s.selectedModel));
  const backendConnected = useAppStore((s) => s.backendConnected);
  const backendChecking = useAppStore((s) => s.backendChecking);

  const navItems = [
    { path: '/', icon: MessageSquare, label: t('sidebar.chat') },
    { path: '/models', icon: Cpu, label: t('sidebar.models') },
    { path: '/analytics', icon: BarChart, label: '分析' },
    { path: '/compare', icon: ArrowLeftRight, label: '对比' },
    { action: 'settings', icon: Settings, label: t('sidebar.settings') },
  ];

  const handleNav = (item: typeof navItems[0]) => {
    if ('action' in item) {
      window.dispatchEvent(new CustomEvent('icode:open-settings'));
    } else { navigate(item.path); }
  };

  const isActive = (item: typeof navItems[0]) => {
    if ('action' in item) return false;
    if (item.path === '/') return location.pathname === '/';
    return location.pathname.startsWith(item.path);
  };

  const toggleCollapse = () => { setCollapsed(!collapsed); };

  const sidebarWidth = collapsed ? 52 : 220;

  return (
    <div style={{
      width: sidebarWidth, minWidth: sidebarWidth, background: 'var(--bg-secondary)',
      borderRight: '0.5px solid var(--border-color)', display: 'flex',
      flexDirection: 'column', height: '100vh', transition: 'width var(--t-normal)',
    }}>
      {/* Header */}
      <div style={{
        padding: collapsed ? '14px 10px' : '18px 16px 12px',
        borderBottom: '0.5px solid var(--border-color)',
        display: 'flex', alignItems: 'center', justifyContent: collapsed ? 'center' : 'space-between',
      }}>
        {!collapsed && (
          <span style={{ fontSize: 18, fontWeight: 700, color: 'var(--accent)', letterSpacing: '-0.02em' }}>
            iCode
          </span>
        )}
        <button
          onClick={collapsed ? toggleCollapse : onToggle}
          className="interactive"
          style={{
            background: 'none', border: 'none', color: 'var(--text-muted)',
            padding: 4, display: 'flex', borderRadius: 4,
          }}
        >
          <PanelLeftClose size={16} style={collapsed ? { transform: 'rotate(180deg)' } : undefined} />
        </button>
      </div>

      {/* Navigation */}
      <nav style={{ padding: '8px 6px', borderBottom: '0.5px solid var(--border-color)' }}>
        {navItems.map((item) => {
          const active = isActive(item);
          return (
            <button
              key={item.path || item.action}
              onClick={() => handleNav(item)}
              title={item.label}
              className={active ? 'nav-item active' : 'nav-item'}
              style={{ width: '100%', justifyContent: collapsed ? 'center' : 'flex-start', marginBottom: 2 }}
            >
              <item.icon size={16} style={active ? { color: 'var(--accent)' } : undefined} />
              {!collapsed && <span>{item.label}</span>}
            </button>
          );
        })}
      </nav>

      {/* Session history */}
      <div style={{ flex: 1, overflowY: 'auto', padding: collapsed ? '4px 4px' : '6px 6px' }}>
        {!collapsed && (
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '8px 8px 4px', color: 'var(--text-muted)',
            fontSize: 9, letterSpacing: 1, textTransform: 'uppercase', fontWeight: 600,
          }}>
            <span>会话</span>
            <button
              onClick={() => createSession(selectedModel, currentModel?.provider || 'openrouter')}
              className="interactive"
              style={{
                background: 'none', border: 'none', color: 'var(--text-muted)',
                padding: 3, display: 'flex', borderRadius: 4,
              }}
            >
              <Plus size={12} />
            </button>
          </div>
        )}
        {sessions.length === 0 && !collapsed && (
          <div style={{ padding: '16px 8px', color: 'var(--text-muted)', fontSize: 11, textAlign: 'center' }}>
            暂无会话
          </div>
        )}
        {sessions.slice(-20).reverse().map((s) => {
          const active = s.id === activeSessionId;
          const isEditing = editingId === s.id;
          return (
            <div
              key={s.id}
              onClick={() => { if (!isEditing) { setActiveSession(s.id); navigate('/'); } }}
              className={`sidebar-item${active ? ' active' : ''}`}
              title={!collapsed ? undefined : (s.title || s.id.slice(0, 6))}
              style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: collapsed ? '6px 6px' : '6px 8px', cursor: 'pointer',
                justifyContent: collapsed ? 'center' : 'flex-start',
              }}
              onMouseEnter={(e) => {
                if (collapsed) return;
                e.currentTarget.querySelectorAll('.action-hidden').forEach((el) => {
                  (el as HTMLElement).style.opacity = '0.6';
                });
              }}
              onMouseLeave={(e) => {
                if (collapsed) return;
                e.currentTarget.querySelectorAll('.action-hidden').forEach((el) => {
                  (el as HTMLElement).style.opacity = '0';
                });
              }}
            >
              <div style={{
                width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
                background: active ? 'var(--accent)' : 'transparent',
                transition: 'background var(--t-fast)',
              }} />
              {!collapsed && (
                <>
                  {isEditing ? (
                    <input
                      autoFocus
                      value={editValue}
                      onChange={(e) => setEditValue(e.target.value)}
                      onClick={(e) => e.stopPropagation()}
                      onBlur={() => { renameSession(s.id, editValue); setEditingId(null); }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') { e.preventDefault(); renameSession(s.id, editValue); setEditingId(null); }
                        else if (e.key === 'Escape') { e.preventDefault(); setEditingId(null); }
                      }}
                      style={{
                        flex: 1, fontSize: 11.5, lineHeight: 1.3, minWidth: 0,
                        background: 'var(--bg-tertiary)', color: 'var(--text-primary)',
                        border: '0.5px solid var(--accent)', borderRadius: 4,
                        padding: '2px 4px', outline: 'none',
                      }}
                    />
                  ) : (
                    <div style={{
                      flex: 1, overflow: 'hidden', textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap', fontSize: 11.5, lineHeight: 1.3,
                    }}>
                      {s.title || `会话 ${s.id.slice(0, 6)}`}
                    </div>
                  )}
                  <button className="action-hidden"
                    onClick={(e) => { e.stopPropagation(); setEditingId(s.id); setEditValue(s.title || ''); }}
                    title="重命名"
                    style={{
                      background: 'none', border: 'none', color: 'var(--text-muted)',
                      cursor: 'pointer', padding: 2, display: 'flex',
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--accent)'; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
                  ><Pencil size={11} /></button>
                  <button className="action-hidden"
                    onClick={(e) => { e.stopPropagation(); deleteSession(s.id); }}
                    title="删除"
                    style={{
                      background: 'none', border: 'none', color: 'var(--text-muted)',
                      cursor: 'pointer', padding: 2, display: 'flex', fontSize: 10,
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--error)'; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
                  >×</button>
                </>
              )}
            </div>
          );
        })}
      </div>

      {/* Footer */}
      <div style={{
        padding: '10px 12px', borderTop: '0.5px solid var(--border-color)',
        fontSize: 10, color: 'var(--text-muted)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, justifyContent: collapsed ? 'center' : 'flex-start' }}>
          <div style={{
            width: 6, height: 6, borderRadius: '50%',
            background: backendChecking ? 'var(--text-muted)' : backendConnected ? 'var(--success)' : 'var(--warning)',
            flexShrink: 0,
          }} />
          {!collapsed && (
            <span>{backendChecking ? '连接中…' : backendConnected ? '已连接' : '未连接'}</span>
          )}
        </div>
        {!collapsed && (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span>v0.4.0</span>
            <button
              onClick={() => window.icode?.openExternal?.('https://github.com/ponygates/icode')}
              className="interactive"
              style={{ background: 'none', border: 'none', color: 'var(--text-muted)', padding: 2, display: 'flex', borderRadius: 4 }}
            ><Github size={13} /></button>
          </div>
        )}
      </div>
    </div>
  );
};

export default Sidebar;
