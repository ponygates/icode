import React from 'react';
import { useAppStore } from '../stores/appStore';

/**
 * Inline todo section — meant to be embedded inside a parent sidebar,
 * not positioned fixed. This avoids overlapping with the chat stats panel.
 */
const TodoPanel: React.FC = () => {
  const { activeSessionId } = useAppStore();
  const [todos, setTodos] = React.useState<any[]>([]);

  // Use the store's backendUrl instead of window.icode
  const backendUrl = useAppStore(s => s.backendUrl);

  React.useEffect(() => {
    if (!backendUrl || !activeSessionId) return;
    let alive = true;
    const poll = async () => {
      try {
        const res = await fetch(`${backendUrl}/api/todos/${activeSessionId}`);
        if (res.ok && alive) {
          const data = await res.json();
          setTodos(data.items || []);
        }
      } catch { /* ignore */ }
    };
    poll(); // immediate first fetch
    const interval = setInterval(poll, 3000);
    return () => { alive = false; clearInterval(interval); };
  }, [backendUrl, activeSessionId]);

  if (todos.length === 0) return null;

  const statusSymbol = (s: string) => {
    switch (s) {
      case 'in_progress': return '▶';
      case 'completed': return '✓';
      default: return '☐';
    }
  };

  return (
    <div style={{ marginTop: 8 }}>
      <div style={{
        fontSize: 11, color: 'var(--text-muted)', marginBottom: 6,
        letterSpacing: 0.5, fontWeight: 500,
      }}>
        待办 ({todos.filter(t => t.status !== 'completed').length}/{todos.length})
      </div>
      {todos.map((t: any, i: number) => (
        <div key={t.id || i} style={{
          display: 'flex', gap: 6, padding: '3px 0',
          color: t.status === 'completed' ? 'var(--text-muted)' : 'var(--text-secondary)',
          fontSize: 11,
        }}>
          <span style={{ color: t.status === 'in_progress' ? 'var(--accent)' : t.status === 'completed' ? 'var(--success)' : 'var(--text-muted)' }}>
            {statusSymbol(t.status)}
          </span>
          <span style={{
            textDecoration: t.status === 'completed' ? 'line-through' : 'none',
            flex: 1, lineHeight: 1.4,
          }}>
            {t.activeForm || t.content}
          </span>
        </div>
      ))}
    </div>
  );
};

export default TodoPanel;
