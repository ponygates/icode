import React from 'react';
import { X, Plus } from 'lucide-react';

interface Tab {
  id: string;
  title: string;
}

interface TabBarProps {
  tabs: Tab[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onNew: () => void;
}

const TabBar: React.FC<TabBarProps> = ({ tabs, activeId, onSelect, onClose, onNew }) => {
  if (tabs.length === 0) return null;

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 0,
      background: 'var(--bg-secondary)', borderBottom: '1px solid var(--border-color)',
      overflowX: 'auto', flexShrink: 0, height: 34,
    }}>
      {tabs.map(tab => {
        const active = tab.id === activeId;
        return (
          <div
            key={tab.id}
            onClick={() => onSelect(tab.id)}
            style={{
              display: 'flex', alignItems: 'center', gap: 5,
              padding: '0 10px', height: '100%', cursor: 'pointer',
              fontSize: 11, whiteSpace: 'nowrap',
              borderBottom: active ? '2px solid var(--accent)' : '2px solid transparent',
              background: active ? 'var(--bg-primary)' : 'transparent',
              color: active ? 'var(--text-primary)' : 'var(--text-muted)',
              transition: 'all 0.12s',
              userSelect: 'none',
            }}
          >
            <span style={{ maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {tab.title || '新会话'}
            </span>
            <button
              onClick={(e) => { e.stopPropagation(); onClose(tab.id); }}
              style={{
                background: 'transparent', border: 'none', cursor: 'pointer',
                color: 'var(--text-muted)', padding: 0, display: 'flex',
                borderRadius: 3, width: 14, height: 14,
                alignItems: 'center', justifyContent: 'center',
                opacity: 0.5, fontSize: 10,
              }}
              onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
              onMouseLeave={e => (e.currentTarget.style.opacity = '0.5')}
            >×</button>
          </div>
        );
      })}
      <button
        onClick={onNew}
        title="新建标签"
        style={{
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          width: 30, height: 30, background: 'transparent', border: 'none',
          cursor: 'pointer', color: 'var(--text-muted)', borderRadius: 4,
          flexShrink: 0, marginLeft: 2, opacity: 0.6,
        }}
        onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
        onMouseLeave={e => (e.currentTarget.style.opacity = '0.6')}
      >
        <Plus size={14} />
      </button>
    </div>
  );
};

export default TabBar;
