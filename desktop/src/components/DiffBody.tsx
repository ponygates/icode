import React from 'react';

export interface DiffLine {
  text: string;
  kind: 'ctx' | 'add' | 'del' | 'hunk' | 'meta';
}
export interface DiffFile {
  header: string;
  lines: DiffLine[];
}

// parseUnifiedDiff turns standard `git diff` output into per-file hunks.
export function parseUnifiedDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let cur: DiffFile | null = null;
  for (const line of diff.split('\n')) {
    if (line.startsWith('diff --git ') || line.startsWith('--- ')) {
      if (cur) files.push(cur);
      if (line.startsWith('diff --git ')) {
        cur = { header: line.replace(/^diff --git /, ''), lines: [] };
      }
      continue;
    }
    if (!cur) cur = { header: '', lines: [] };
    let kind: DiffLine['kind'] = 'ctx';
    if (line.startsWith('+++')) kind = 'meta';
    else if (line.startsWith('@@')) kind = 'hunk';
    else if (line.startsWith('+')) kind = 'add';
    else if (line.startsWith('-')) kind = 'del';
    cur.lines.push({ text: line, kind });
  }
  if (cur) files.push(cur);
  return files;
}

export const diffColorFor = (kind: DiffLine['kind']): { bg: string; fg: string } => {
  switch (kind) {
    case 'add': return { bg: 'rgba(46,160,67,0.12)', fg: 'var(--success)' };
    case 'del': return { bg: 'rgba(248,81,73,0.12)', fg: 'var(--error)' };
    case 'hunk': return { bg: 'rgba(88,166,255,0.10)', fg: 'var(--accent)' };
    case 'meta': return { bg: 'transparent', fg: 'var(--accent)' };
    default: return { bg: 'transparent', fg: 'var(--text-secondary)' };
  }
};

// DiffBody renders parsed unified-diff files — shared by the checkpoint
// DiffViewer and the git workbench panel.
export const DiffBody: React.FC<{ files: DiffFile[] }> = ({ files }) => (
  <>
    {files.map((f, fi) => (
      <div key={fi} style={{ marginBottom: 14 }}>
        {f.header && (
          <div style={{
            color: 'var(--accent)', fontWeight: 600, fontSize: 11,
            marginBottom: 6, padding: '4px 8px',
            background: 'rgba(88,166,255,0.07)', borderRadius: 6,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          }}>
            {f.header}
          </div>
        )}
        {f.lines.map((l, li) => {
          const c = diffColorFor(l.kind);
          return (
            <div key={li} style={{
              background: c.bg, color: c.fg, padding: '0 8px',
              whiteSpace: 'pre-wrap', wordBreak: 'break-all',
              borderRadius: 2,
            }}>
              {l.text || ' '}
            </div>
          );
        })}
      </div>
    ))}
  </>
);
