import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Folder, FileText, ChevronRight, ChevronDown, Search } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

type FileType = 'go' | 'ts' | 'py' | 'md' | 'json' | 'yaml' | 'toml' | 'html' | 'css' | 'sh' | 'sql' | 'proto' | 'svg' | 'image' | 'file' | 'other' | 'dir';
type FileItem = { path: string; type: FileType; isDir: boolean; depth: number };

const DIRS = new Set(['node_modules', '.git', '.idea', '.vscode', 'dist', 'build', '.next', 'vendor', '__pycache__', '.cache', 'target', '.icode', '.mimocode']);

interface Props {
  /** Root directory to list (passed as ?path= to /api/files). Defaults to the backend cwd. */
  path?: string;
  /** injected into the input box when a file is double-clicked or Enter is pressed */
  onInsertPath?: (relPath: string) => void;
  /** context-menu action fired by right-click on a file */
  onAction?: (action: 'ask' | 'explain' | 'optimize', path: string) => void;
  style?: React.CSSProperties;
}

// ── Icon & colour helpers ───────────────────────────────────────────────────

const extIcon: Record<string, { icon: React.ReactNode; color: string }> = {
  '.go':   { icon: '🐹', color: '#00ADD8' },
  '.ts':   { icon: '🔷', color: '#3178C6' },
  '.tsx':  { icon: '🔷', color: '#3178C6' },
  '.jsx':  { icon: '🔶', color: '#F9AC00' },
  '.js':   { icon: '🟨', color: '#F9AC00' },
  '.py':   { icon: '🐍', color: '#3572A5' },
  '.md':   { icon: '📝', color: '#8B5CF6' },
  '.json': { icon: '📋', color: '#CBCB41' },
  '.yaml': { icon: '⚙️', color: '#CBCB41' },
  '.yml':  { icon: '⚙️', color: '#CBCB41' },
  '.toml': { icon: '📦', color: '#9C4221' },
  '.html': { icon: '🌐', color: '#E44D26' },
  '.css':  { icon: '🎨', color: '#563D7C' },
  '.scss': { icon: '🎨', color: '#CF649A' },
  '.sh':   { icon: '⚡', color: '#89E051' },
  '.sql':  { icon: '🗄️', color: '#E44D26' },
  '.proto':{ icon: '🔗', color: '#60a3e8' },
  '.svg':  { icon: '🖼️', color: '#FFB13B' },
  '.png':  { icon: '🖼️', color: '#FFB13B' },
  '.jpg':  { icon: '🖼️', color: '#FFB13B' },
  '.gif':  { icon: '🖼️', color: '#FFB13B' },
};

function fileIcon(type: string) {
  return extIcon[type] || { icon: '📄', color: 'var(--text-muted)' };
}

function pathToType(p: string): FileType {
  const ext = p.slice(p.lastIndexOf('.')).toLowerCase();
  switch (ext) {
    case '.go': return 'go';
    case '.ts': return 'ts';
    case '.tsx': return 'ts';
    case '.jsx': return 'ts';
    case '.js': return 'ts';
    case '.py': return 'py';
    case '.md': return 'md';
    case '.json': return 'json';
    case '.yaml': return 'yaml';
    case '.yml': return 'yaml';
    case '.toml': return 'toml';
    case '.html': return 'html';
    case '.css': return 'css';
    case '.scss': return 'css';
    case '.less': return 'css';
    case '.sh': return 'sh';
    case '.sql': return 'sql';
    case '.proto': return 'proto';
    case '.svg': return 'svg';
    case '.png': return 'image';
    case '.jpg': return 'image';
    case '.jpeg': return 'image';
    case '.gif': return 'image';
    default: return 'file';
  }
}

// ── Flatten + expand/collapse ────────────────────────────────────────────────

interface FlatNode { path: string; type: FileType; isDir: boolean; depth: number; name: string }

function flatten(files: FileItem[]): FlatNode[] {
  return files.map((f) => ({ path: f.path, type: f.type as FileType, isDir: f.isDir, depth: f.depth, name: f.path.split('/').pop() || f.path }));
}

// ── Component ───────────────────────────────────────────────────────────────

const FileTree: React.FC<Props> = ({ path, onInsertPath, onAction, style }) => {
  const { backendUrl } = useAppStore();
  const [items, setItems] = useState<FlatNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [searchVisible, setSearchVisible] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; path: string; type: FileType | 'dir' } | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Fetch tree on mount / backendUrl / path change
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setItems([]);
    (async () => {
      try {
        const url = path
          ? `${backendUrl}/api/files?path=${encodeURIComponent(path)}`
          : `${backendUrl}/api/files`;
        const res = await fetch(url, { cache: 'no-cache' });
        if (!res.ok) return;
        const data = await res.json();
        if (!cancelled) {
          setItems(flatten(data.files || []));
          // Auto-expand top-level dirs so the tree looks useful on first load.
          setExpanded(new Set(data.files?.filter((f: FileItem) => f.isDir).map((f: FileItem) => f.path)));
        }
      } catch {} finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [backendUrl, path]);

  // Keyboard shortcut: Ctrl+F to toggle search
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault();
        setSearchVisible((v) => !v);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Close context menu on click-away
  useEffect(() => {
    const onDown = () => setCtxMenu(null);
    window.addEventListener('mousedown', onDown);
    return () => window.removeEventListener('mousedown', onDown);
  }, []);

  // ── filtered list ───────────────────────────────────────────────────────
  const filtered = search
    ? items.filter((n) => n.path.toLowerCase().includes(search.toLowerCase()))
    : items;

  // ── toggle expand ───────────────────────────────────────────────────────
  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path); else next.add(path);
      return next;
    });
  }, []);

  // ── insert path into the input box ──────────────────────────────────────
  const insert = useCallback((path: string) => {
    onInsertPath?.(`@${path}`);
    // Keep menu open after insert so the user can keep picking.
  }, [onInsertPath]);

  // ── context menu ────────────────────────────────────────────────────────
  const handleRightClick = useCallback((e: React.MouseEvent, path: string, type: string) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, path, type: type as FileType | 'dir' });
  }, []);

  const ctxAction = (action: 'ask' | 'explain' | 'optimize') => {
    if (!ctxMenu) return;
    onAction?.(action, ctxMenu.path);
    setCtxMenu(null);
  };

  // ── drag to insert ──────────────────────────────────────────────────────
  const handleDragStart = useCallback((e: React.DragEvent, path: string) => {
    e.dataTransfer.setData('text/plain', `@${path}`);
    e.dataTransfer.effectAllowed = 'copy';
  }, []);

  // ── render node row ─────────────────────────────────────────────────────
  const renderNode = (node: FlatNode) => {
    const isExpanded = expanded.has(node.path);
    const icon = node.isDir
      ? (isExpanded ? <ChevronDown size={11} /> : <ChevronRight size={11} />)
      : <span style={{ width: 11, display: 'inline-block' }} />;
    const { icon: fi, color } = fileIcon(node.type);
    const isMatch = search && node.path.toLowerCase().includes(search.toLowerCase());

    return (
      <React.Fragment key={node.path}>
        <div
          draggable
          onDragStart={(e) => !node.isDir && handleDragStart(e, node.path)}
          onContextMenu={(e) => handleRightClick(e, node.path, node.type)}
          onDoubleClick={() => insert(node.path)}
          style={{
            display: 'flex', alignItems: 'center', gap: 4,
            paddingLeft: 6 + node.depth * 12,
            paddingRight: 6,
            paddingBlock: 2,
            cursor: node.isDir ? 'pointer' : 'grab',
            fontSize: 11,
            color: isMatch ? 'var(--accent)' : 'var(--text-secondary)',
            background: isMatch ? 'rgba(137,180,250,0.08)' : 'transparent',
            borderRadius: 4,
          }}
          onMouseEnter={(e) => { if (!isMatch) e.currentTarget.style.background = 'var(--bg-tertiary)'; }}
          onMouseLeave={(e) => { if (!isMatch) e.currentTarget.style.background = 'transparent'; }}
        >
          {icon}
          <span style={{ fontSize: 12, flexShrink: 0 }}>{node.isDir ? <Folder size={12} color="var(--yellow)" /> : <span style={{ color }}>{fi}</span>}</span>
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {node.name}
          </span>
        </div>
        {node.isDir && isExpanded &&
          items
            .filter((c) => c.path.startsWith(node.path + '/') && c.depth === node.depth + 1)
            .map(renderNode)}
      </React.Fragment>
    );
  };

  // ── context menu popup ──────────────────────────────────────────────────
  const menu = ctxMenu ? (
    <div
      style={{
        position: 'fixed', zIndex: 1000,
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border-color)',
        borderRadius: 8, padding: '4px 0',
        boxShadow: '0 8px 24px rgba(0,0,0,0.25)',
        minWidth: 160,
      }}
    >
      {([
        { label: '询问此文件', action: 'ask' as const },
        { label: '解释此文件', action: 'explain' as const },
        { label: '优化此文件', action: 'optimize' as const },
      ] as const).map(({ label, action }) => (
        <button
          key={action}
          onClick={() => ctxAction(action)}
          style={{
            display: 'block', width: '100%',
            background: 'none', border: 'none',
            padding: '6px 14px', textAlign: 'left',
            fontSize: 12, color: 'var(--text-primary)', cursor: 'pointer',
          }}
          onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
          onMouseLeave={(e) => (e.currentTarget.style.background = 'none')}
        >
          {label}
        </button>
      ))}
    </div>
  ) : null;

  // ── main render ─────────────────────────────────────────────────────────
  return (
    <>
      {menu}
      <div ref={containerRef} style={{
        display: 'flex', flexDirection: 'column',
        flex: 1, overflow: 'hidden',
        ...style,
      }}>
        {/* Toolbar */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 4,
          padding: '6px 8px 4px',
          borderBottom: '0.5px solid var(--border-color)',
          fontSize: 10, color: 'var(--text-muted)',
        }}>
          <span style={{ fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.8, marginRight: 4 }}>📁 文件</span>
          <button
            onClick={() => setSearchVisible((v) => !v)}
            title="Ctrl+F 搜索"
            style={{
              background: 'none', border: 'none', cursor: 'pointer',
              color: 'var(--text-muted)', padding: '2px 4px', borderRadius: 4,
              display: 'flex', alignItems: 'center',
            }}
            onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
            onMouseLeave={(e) => (e.currentTarget.style.background = 'none')}
          >
            <Search size={11} />
          </button>
          {!searchVisible && (
            <span style={{ marginLeft: 'auto', fontSize: 10 }}>
              {loading ? '加载…' : `${items.length} 项`}
            </span>
          )}
        </div>

        {/* Search bar */}
        {searchVisible && (
          <div style={{ padding: '4px 8px', borderBottom: '0.5px solid var(--border-color)' }}>
            <input
              ref={inputRef}
              autoFocus
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="过滤文件…"
              style={{
                width: '100%', fontSize: 11,
                background: 'var(--bg-tertiary)', color: 'var(--text-primary)',
                border: '0.5px solid var(--border-color)', borderRadius: 4,
                padding: '3px 6px', outline: 'none', boxSizing: 'border-box',
              }}
            />
          </div>
        )}

        {/* File list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '2px 0' }}>
          {loading ? (
            <div style={{ padding: 12, textAlign: 'center', color: 'var(--text-muted)', fontSize: 11 }}>
              加载文件树…
            </div>
          ) : filtered.length === 0 ? (
            <div style={{ padding: 12, textAlign: 'center', color: 'var(--text-muted)', fontSize: 11 }}>
              {search ? '无匹配文件' : '暂无文件'}
            </div>
          ) : (
            items.filter((n) => n.depth === 1).map(renderNode)
          )}
        </div>

        {/* Footer hint */}
        <div style={{
          padding: '4px 10px', borderTop: '0.5px solid var(--border-color)',
          fontSize: 10, color: 'var(--text-muted)', lineHeight: 1.4,
        }}>
          双击 / Enter 插入 @{search || '文件路径'} · 拖拽到输入框
        </div>
      </div>
    </>
  );
};

export default FileTree;
