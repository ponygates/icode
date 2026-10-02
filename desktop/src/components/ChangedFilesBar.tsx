import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { FileText, GitBranch, RefreshCw } from 'lucide-react';
import { useAppStore } from '../stores/appStore';
import DiffViewer from './DiffViewer';

// ── Inline turn review bar (Claude Code parity) ──
// When an assistant turn used file-modifying tools, the SSE handler tags the
// settled message with `changedFiles`. This bar renders under that message:
// file chips + "view diff" (checkpoint diff of the last turn) + "rewind this
// turn" (one-step checkpoint restore) — review without leaving the chat flow.

// File-modifying tool → the argument field that carries the target path.
const FILE_TOOL_ARGS: Record<string, string> = {
  write_file: 'path',
  edit: 'file_path',
  search_replace: 'file_path',
};

// Extract the target path from a tool_use payload, or '' when the tool is
// not file-modifying / the arguments aren't parseable JSON.
export function toolTargetFile(name: string, argsRaw?: string): string {
  const key = FILE_TOOL_ARGS[name];
  if (!key || !argsRaw) return '';
  try {
    const a = JSON.parse(argsRaw);
    const p = a?.[key];
    return typeof p === 'string' ? p : '';
  } catch {
    return '';
  }
}

interface ChangedFilesBarProps {
  sessionId: string;
  msgId: string;
  files: string[];
}

const ChangedFilesBar: React.FC<ChangedFilesBarProps> = ({ sessionId, msgId, files }) => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const updateMessage = useAppStore(s => s.updateMessage);
  const [viewing, setViewing] = useState(false);
  const [rewinding, setRewinding] = useState(false);
  const [rolled, setRolled] = useState(false);

  if (rolled || files.length === 0) return null;

  const rewind = async () => {
    if (!backendUrl || rewinding) return;
    if (!window.confirm(t('chat.rewindTurnConfirm'))) return;
    setRewinding(true);
    try {
      const res = await fetch(`${backendUrl}/api/checkpoints/rewind`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId, steps: 1 }),
      });
      if (res.ok) {
        // Strip the tag so the bar (and a possible second rewind) disappears.
        const st = useAppStore.getState();
        const msg = st.sessions.find(s => s.id === sessionId)
          ?.messages.find(m => m.id === msgId);
        if (msg) updateMessage(sessionId, { ...msg, changedFiles: undefined });
        setRolled(true);
      }
    } catch { /* network error — leave the bar in place for a retry */ }
    setRewinding(false);
  };

  return (
    <div style={{ marginTop: 8 }}>
      {viewing && backendUrl && (
        <DiffViewer
          backendUrl={backendUrl}
          sessionId={sessionId}
          steps={1}
          message={t('chat.turnDiffMessage')}
          onClose={() => setViewing(false)}
        />
      )}
      <div style={{
        border: '1px solid var(--border-color)', borderRadius: 8,
        padding: '6px 10px', background: 'var(--bg-secondary)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11 }}>
          <FileText size={11} style={{ color: 'var(--accent)', flexShrink: 0 }} />
          <span style={{ color: 'var(--text-muted)', fontWeight: 500 }}>
            {t('chat.changedFiles', { count: files.length })}
          </span>
          <span style={{ flex: 1 }} />
          <button
            onClick={() => setViewing(true)}
            title={t('chat.viewDiffTitle')}
            style={{
              background: 'transparent', border: '1px solid var(--border-color)',
              borderRadius: 4, cursor: 'pointer', padding: '1px 6px',
              fontSize: 10, color: 'var(--text-muted)',
              display: 'flex', alignItems: 'center', gap: 3,
            }}
          >
            <GitBranch size={9} /> {t('chat.viewDiff')}
          </button>
          <button
            onClick={rewind}
            disabled={rewinding}
            title={t('chat.rewindTurnTitle')}
            style={{
              background: 'transparent', border: '1px solid var(--border-color)',
              borderRadius: 4, cursor: rewinding ? 'wait' : 'pointer',
              padding: '1px 6px', fontSize: 10,
              color: rewinding ? 'var(--text-muted)' : 'var(--accent)',
              display: 'flex', alignItems: 'center', gap: 3,
              opacity: rewinding ? 0.5 : 1,
            }}
          >
            <RefreshCw size={9} style={rewinding ? { animation: 'spin 1s linear infinite' } : undefined} /> {t('chat.rewindTurn')}
          </button>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 5 }}>
          {files.slice(0, 6).map(f => (
            <span key={f} title={f} style={{
              fontFamily: 'var(--font-mono)', fontSize: 10,
              padding: '1px 6px', borderRadius: 4,
              border: '1px solid var(--border-color)',
              color: 'var(--text-muted)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              maxWidth: 200,
            }}>
              {f.replace(/\\/g, '/').split('/').pop()}
            </span>
          ))}
          {files.length > 6 && (
            <span style={{ fontSize: 10, color: 'var(--text-muted)', alignSelf: 'center' }}>
              +{files.length - 6}
            </span>
          )}
        </div>
      </div>
    </div>
  );
};

export default ChangedFilesBar;
