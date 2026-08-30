import React from 'react';
import { useTranslation } from 'react-i18next';
import { X, Plus } from 'lucide-react';

interface Tab {
  id: string;
  title: string;
}

interface TabBarProps {
  tabs: Tab[];
  activeId: string | null;
  /** Sessions currently streaming (any tab, incl. background — D1 parallel streams). */
  streamingIds?: string[];
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onNew: () => void;
  /** Drag-to-reorder (dragId dropped before dropId). */
  onReorder?: (dragId: string, dropId: string) => void;
  onContextMenu?: (e: React.MouseEvent, id: string) => void;
}

const TabBar: React.FC<TabBarProps> = ({ tabs, activeId, streamingIds, onSelect, onClose, onNew, onReorder, onContextMenu }) => {
  const { t } = useTranslation();
  const [dragId, setDragId] = React.useState<string | null>(null);
  const [overId, setOverId] = React.useState<string | null>(null);
  if (tabs.length === 0) return null;

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 0,
      background: 'var(--bg-secondary)', borderBottom: '1px solid var(--border-color)',
      overflowX: 'auto', flexShrink: 0, height: 34,
    }}>
      {tabs.map(tab => {
        const active = tab.id === activeId;
        const streaming = streamingIds?.includes(tab.id) ?? false;
        const dragging = dragId === tab.id;
        const dropTarget = overId === tab.id && dragId !== null && dragId !== tab.id;
        return (
          <div
            key={tab.id}
            onClick={() => onSelect(tab.id)}
            onContextMenu={(e) => onContextMenu?.(e, tab.id)}
            title={active ? t('tab.ctxHint') : undefined}
            draggable={!!onReorder}
            onDragStart={() => setDragId(tab.id)}
            onDragOver={(e) => { if (onReorder) { e.preventDefault(); setOverId(tab.id); } }}
            onDragLeave={() => setOverId(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (onReorder && dragId && dragId !== tab.id) onReorder(dragId, tab.id);
              setDragId(null);
              setOverId(null);
            }}
            onDragEnd={() => { setDragId(null); setOverId(null); }}
            style={{
              display: 'flex', alignItems: 'center', gap: 5,
              padding: '0 10px', height: '100%', cursor: 'pointer',
              fontSize: 11, whiteSpace: 'nowrap',
              borderBottom: active ? '2px solid var(--accent)' : dropTarget ? '2px solid var(--warning, #f59e0b)' : '2px solid transparent',
              background: active ? 'var(--bg-primary)' : 'transparent',
              color: active ? 'var(--text-primary)' : 'var(--text-muted)',
              opacity: dragging ? 0.4 : 1,
              transition: 'all 0.12s',
              userSelect: 'none',
            }}
          >
            {streaming && (
              <span
                title={t('tab.streaming', '生成中…')}
                style={{
                  width: 6, height: 6, borderRadius: '50%',
                  background: 'var(--accent)', flexShrink: 0,
                  animation: 'tabStreamPulse 1.2s ease-in-out infinite',
                }}
              />
            )}
            <span style={{ maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {tab.title || t('tab.newTab')}
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
        title={t('tab.newTabTitle')}
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
      <style>{`
        @keyframes tabStreamPulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.3; }
        }
      `}</style>
    </div>
  );
};

export default TabBar;
