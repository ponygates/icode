import React from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { MessageSquare, Cpu, Settings, PanelLeftClose, Github } from 'lucide-react';

interface Props {
  onToggle: () => void;
}

const Sidebar: React.FC<Props> = ({ onToggle }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();

  const navItems = [
    { path: '/', icon: MessageSquare, label: t('sidebar.chat') },
    { path: '/models', icon: Cpu, label: t('sidebar.models') },
    { path: '/settings', icon: Settings, label: t('sidebar.settings') },
  ];

  const isActive = (path: string) => {
    if (path === '/') return location.pathname === '/';
    return location.pathname.startsWith(path);
  };

  return (
    <div style={{
      width: 220, minWidth: 220, background: 'var(--bg-secondary)',
      borderRight: '1px solid var(--border-color)', display: 'flex',
      flexDirection: 'column', height: '100vh',
    }}>
      {/* Header */}
      <div style={{
        padding: '16px 16px 12px', borderBottom: '1px solid var(--border-color)',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 20, fontWeight: 600, color: 'var(--accent)' }}>iCode</span>
        </div>
        <button
          onClick={onToggle}
          style={{
            background: 'none', border: 'none', color: 'var(--text-muted)',
            cursor: 'pointer', padding: 4, display: 'flex',
          }}
        >
          <PanelLeftClose size={16} />
        </button>
      </div>

      {/* Navigation */}
      <nav style={{ flex: 1, padding: '8px 8px' }}>
        {navItems.map((item) => (
          <button
            key={item.path}
            onClick={() => navigate(item.path)}
            style={{
              display: 'flex', alignItems: 'center', gap: 10, width: '100%',
              padding: '8px 12px', marginBottom: 2, borderRadius: 6,
              background: isActive(item.path) ? 'var(--bg-tertiary)' : 'transparent',
              color: isActive(item.path) ? 'var(--accent)' : 'var(--text-secondary)',
              border: 'none', cursor: 'pointer', fontSize: 13,
              transition: 'background 0.15s',
            }}
          >
            <item.icon size={16} />
            <span>{item.label}</span>
          </button>
        ))}
      </nav>

      {/* Footer */}
      <div style={{
        padding: '12px 16px', borderTop: '1px solid var(--border-color)',
        fontSize: 11, color: 'var(--text-muted)',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      }}>
        <span>v0.1.0-dev</span>
        <button
          onClick={() => window.icode?.openExternal?.('https://github.com/ponygates/icode')}
          style={{
            background: 'none', border: 'none', color: 'var(--text-muted)',
            cursor: 'pointer', padding: 2, display: 'flex',
          }}
          title="GitHub"
        >
          <Github size={14} />
        </button>
      </div>
    </div>
  );
};

export default Sidebar;
