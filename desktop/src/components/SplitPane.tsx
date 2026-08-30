import React, { useCallback, useEffect, useRef, useState } from 'react';

/**
 * SplitPane renders two panes side-by-side with a draggable divider. The
 * ratio persists to localStorage so it survives restarts (D1 split-pane
 * groundwork — used by the future two-session side-by-side view).
 *
 * Props:
 *  - left / right: React nodes rendered in each pane
 *  - storageKey:  localStorage key for the persisted ratio (default 50/50)
 *  - minRatio / maxRatio: clamp bounds for the left pane width (0..1)
 */
interface SplitPaneProps {
  left: React.ReactNode;
  right: React.ReactNode;
  storageKey?: string;
  minRatio?: number;
  maxRatio?: number;
}

const SplitPane: React.FC<SplitPaneProps> = ({
  left,
  right,
  storageKey = 'icode.splitRatio',
  minRatio = 0.25,
  maxRatio = 0.75,
}) => {
  const [ratio, setRatio] = useState<number>(() => {
    try {
      const v = parseFloat(localStorage.getItem(storageKey) || '');
      if (!Number.isNaN(v) && v >= minRatio && v <= maxRatio) return v;
    } catch {
      /* ignore */
    }
    return 0.5;
  });
  const containerRef = useRef<HTMLDivElement | null>(null);
  const draggingRef = useRef(false);

  const onMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    draggingRef.current = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, []);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current || !containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      let next = (e.clientX - rect.left) / rect.width;
      next = Math.min(maxRatio, Math.max(minRatio, next));
      setRatio(next);
    };
    const onUp = () => {
      if (!draggingRef.current) return;
      draggingRef.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      try {
        setRatio((r) => {
          localStorage.setItem(storageKey, String(r));
          return r;
        });
      } catch {
        /* ignore */
      }
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, [storageKey, minRatio, maxRatio]);

  const resetToHalf = useCallback(() => {
    setRatio(0.5);
    try {
      localStorage.setItem(storageKey, '0.5');
    } catch {
      /* ignore */
    }
  }, [storageKey]);

  return (
    <div
      ref={containerRef}
      style={{ display: 'flex', width: '100%', height: '100%', overflow: 'hidden' }}
    >
      <div style={{ width: `${ratio * 100}%`, minWidth: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
        {left}
      </div>
      <div
        onMouseDown={onMouseDown}
        onDoubleClick={resetToHalf}
        title="拖动调整宽度 · 双击恢复 50/50"
        style={{
          width: 5,
          cursor: 'col-resize',
          background: 'var(--border-color)',
          flexShrink: 0,
          transition: 'background 0.15s',
        }}
        onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--accent)')}
        onMouseLeave={(e) => (e.currentTarget.style.background = 'var(--border-color)')}
      />
      <div style={{ width: `${(1 - ratio) * 100}%`, minWidth: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
        {right}
      </div>
    </div>
  );
};

export default SplitPane;
