import React, { useState, useEffect } from 'react';
import { useAppStore } from '../stores/appStore';
import { X, Folder, File, ChevronRight, ChevronDown } from 'lucide-react';

interface FileNode {
  name: string;
  path: string;
  isDir: boolean;
  children?: FileNode[];
}

const IGNORE = new Set(['node_modules', '.git', 'dist', '.next', 'target', '__pycache__', '.cache', '.idea', '.vscode']);

async function scanDir(dir: string, depth: number): Promise<FileNode[]> {
  if (depth > 3) return [];
  // Use Electron IPC if available, otherwise return empty
  if ((window as any).icode?.readDir) {
    try {
      const result: FileNode[] = [];
      const entries = await (window.icode as any).readDir(dir) || [];
      for (const e of (Array.isArray(entries) ? entries : [])) {
        const name = typeof e === 'string' ? e : e.name || e;
        if (IGNORE.has(name) || name.startsWith('.')) continue;
        const fullPath = dir + '/' + name;
        const isDir = (typeof e === 'object' && (e as any).is_dir) || !name.includes('.');
        result.push({ name, path: fullPath, isDir });
      }
      result.sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
      return result;
    } catch {}
  }
  return [];
}

const FilePicker: React.FC<{ visible: boolean; onClose: () => void; onSelect: (path: string) => void }> = ({ visible, onClose, onSelect }) => {
  const [cwd, setCwd] = useState('.');
  const [files, setFiles] = useState<FileNode[]>([]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!visible) return;
    (async () => {
      let dir = '.';
      if ((window.icode as any)?.getCwd) {
        try { dir = await (window.icode as any).getCwd(); } catch {}
      }
      setCwd(dir);
      const f = await scanDir(dir, 0);
      setFiles(f);
    })();
  }, [visible]);

  const toggleDir = async (node: FileNode) => {
    if (expanded.has(node.path)) {
      setExpanded(prev => { const n = new Set(prev); n.delete(node.path); return n; });
      return;
    }
    if (!node.children) {
      const children = await scanDir(node.path, 1);
      node.children = children;
    }
    setExpanded(prev => { const n = new Set(prev); n.add(node.path); return n; });
    setFiles([...files]);
  };

  const select = (path: string) => {
    onSelect(path);
    onClose();
  };

  if (!visible) return null;

  const renderNode = (node: FileNode, depth: number) => (
    <React.Fragment key={node.path}>
      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 4,
          padding: '3px 8px', paddingLeft: 8 + depth * 14,
          cursor: 'pointer', fontSize: 11,
          color: 'var(--text-secondary)',
          background: 'transparent',
        }}
        onMouseEnter={e => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
        onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
        onClick={() => node.isDir ? toggleDir(node) : select(node.path)}
      >
        {node.isDir ? (
          expanded.has(node.path) ? <ChevronDown size={10} /> : <ChevronRight size={10} />
        ) : <span style={{ width: 10 }} />}
        {node.isDir ? <Folder size={12} color="var(--yellow)" /> : <File size={12} color="var(--text-muted)" />}
        <span style={{ overflow:'hidden', textOverflow:'ellipsis', whiteSpace:'nowrap' }}>
          {node.name}
        </span>
      </div>
      {node.isDir && expanded.has(node.path) && node.children?.map(c => renderNode(c, depth + 1))}
    </React.Fragment>
  );

  return (
    <div onClick={onClose} style={{
      position: 'fixed', inset: 0, zIndex: 2000,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'rgba(0,0,0,0.4)',
    }}>
      <div onClick={e => e.stopPropagation()} style={{
        background: 'var(--bg-secondary)', borderRadius: 12,
        border: '1px solid var(--border-color)',
        width: 420, maxHeight: 400, overflow: 'hidden',
        boxShadow: '0 12px 40px rgba(0,0,0,0.3)',
        display: 'flex', flexDirection: 'column',
      }}>
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '8px 12px', borderBottom: '1px solid var(--border-color)',
          fontSize: 11, color: 'var(--text-muted)',
        }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10 }}>{cwd}</span>
          <button onClick={onClose} style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-muted)', padding: 0,
          }}><X size={14} /></button>
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {files.map(f => renderNode(f, 0))}
          {files.length === 0 && (
            <div style={{ padding: 20, textAlign: 'center', color: 'var(--text-muted)', fontSize: 11 }}>
              无法读取目录
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default FilePicker;
