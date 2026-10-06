import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { useAppStore } from '../stores/appStore';
import { ArrowLeft, Bot, RefreshCw, Terminal, X } from 'lucide-react';

// Desktop Agents view (D3): a live window into the process-wide background
// tasks — shell commands (bg-N) and sub-agents (agt-N) started from any
// session. Mirrors the TUI /tasks panel: status badges, elapsed time, an
// output tail preview and one-click cancel for running tasks. Polling is
// cheap (GET /api/bgtasks returns compact snapshots) so 5s feels live
// without spamming the server.

interface BgTaskInfo {
  id: string;
  kind: 'shell' | 'agent';
  status: 'running' | 'finished' | 'failed' | 'cancelled';
  elapsed: number; // seconds
  label: string;
  brief?: string;
  tokens?: number;
  tail?: string;
}

const statusColor: Record<BgTaskInfo['status'], string> = {
  running: 'var(--accent, #4f6ef7)',
  finished: '#4caf50',
  failed: '#e06c6c',
  cancelled: 'var(--text-muted)',
};

const fmtElapsed = (sec: number): string => {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m${sec % 60}s`;
  return `${Math.floor(sec / 3600)}h${Math.floor((sec % 3600) / 60)}m`;
};

const AgentsPage: React.FC = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const backendUrl = useAppStore((s) => s.backendUrl);
  const [tasks, setTasks] = useState<BgTaskInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<string | null>(null);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (!backendUrl || inFlight.current) return;
    inFlight.current = true;
    try {
      const r = await fetch(`${backendUrl}/api/bgtasks`);
      if (r.ok) {
        const d = await r.json();
        setTasks((d.tasks || []) as BgTaskInfo[]);
      }
    } catch {
      // Server briefly unreachable — keep the previous snapshot.
    }
    inFlight.current = false;
    setLoaded(true);
  }, [backendUrl]);

  useEffect(() => {
    load();
    const timer = window.setInterval(load, 5000);
    return () => window.clearInterval(timer);
  }, [load]);

  const cancel = async (id: string) => {
    if (!backendUrl || busy) return;
    setBusy(id);
    try {
      await fetch(`${backendUrl}/api/bgtasks/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
      await load(); // immediately reflect the new state
    } catch {}
    setBusy(null);
  };

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const running = tasks.filter((tk) => tk.status === 'running').length;

  const cardStyle: React.CSSProperties = {
    background: 'var(--bg-primary)',
    borderRadius: 8,
    border: '1px solid var(--border-color)',
    padding: '12px 14px',
    marginBottom: 10,
  };

  return (
    <div style={{ maxWidth: 860, margin: '0 auto', padding: '32px 24px' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
        <button
          onClick={() => navigate('/')}
          title={t('shortcuts.close')}
          style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: 4, display: 'flex', borderRadius: 6 }}
        ><ArrowLeft size={18} /></button>
        <div style={{ flex: 1 }}>
          <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--text-primary)', letterSpacing: '-0.02em' }}>
            {t('agents.title')}
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 2 }}>
            {t('agents.desc')}
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {running > 0 && (
            <span style={{ fontSize: 12, color: 'var(--accent, #4f6ef7)', background: 'var(--bg-primary)', border: '1px solid var(--border-color)', borderRadius: 999, padding: '3px 10px' }}>
              {t('agents.runningCount', { count: running })}
            </span>
          )}
          <button
            onClick={load}
            title={t('agents.refresh')}
            style={{ background: 'none', border: '1px solid var(--border-color)', color: 'var(--text-muted)', cursor: 'pointer', padding: 6, display: 'flex', borderRadius: 6 }}
          ><RefreshCw size={14} /></button>
        </div>
      </div>

      {/* Task list */}
      {loaded && tasks.length === 0 && (
        <div style={{ marginTop: 48, textAlign: 'center', color: 'var(--text-muted)', fontSize: 13 }}>
          <Bot size={36} style={{ opacity: 0.4, marginBottom: 10 }} />
          <div>{t('agents.empty')}</div>
        </div>
      )}
      {tasks.map((tk) => (
        <div key={tk.id} style={cardStyle}>
          {/* Row 1: id + status + kind + elapsed + cancel */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {tk.kind === 'agent' ? <Bot size={14} color="var(--text-muted)" /> : <Terminal size={14} color="var(--text-muted)" />}
            <span style={{ fontFamily: 'monospace', fontSize: 13, color: 'var(--text-primary)', fontWeight: 600 }}>{tk.id}</span>
            <span style={{ fontSize: 11, color: statusColor[tk.status], border: `1px solid ${statusColor[tk.status]}44`, borderRadius: 999, padding: '1px 8px' }}>
              {t(`agents.status.${tk.status}`)}
            </span>
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{fmtElapsed(tk.elapsed)}</span>
            {tk.kind === 'agent' && !!tk.tokens && tk.status !== 'running' && (
              <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('agents.tokens', { count: tk.tokens })}</span>
            )}
            <span style={{ flex: 1 }} />
            {tk.status === 'running' && (
              <button
                onClick={() => cancel(tk.id)}
                disabled={busy === tk.id}
                title={t('agents.cancel')}
                style={{ background: 'none', border: '1px solid var(--border-color)', color: '#e06c6c', cursor: busy === tk.id ? 'wait' : 'pointer', padding: '3px 8px', display: 'flex', alignItems: 'center', gap: 4, borderRadius: 6, fontSize: 11 }}
              >
                <X size={12} />{busy === tk.id ? t('agents.cancelling') : t('agents.cancel')}
              </button>
            )}
          </div>
          {/* Row 2: label / brief */}
          {(tk.label || tk.brief) && (
            <div style={{ marginTop: 6, fontSize: 12, color: 'var(--text-primary)', wordBreak: 'break-all' }}>
              {tk.label}
              {tk.brief && <span style={{ color: 'var(--text-muted)' }}> — {tk.brief}</span>}
            </div>
          )}
          {/* Row 3: output tail (collapsed to 2 lines by default) */}
          {tk.tail && (
            <pre
              onClick={() => toggle(tk.id)}
              style={{
                marginTop: 8, padding: '8px 10px', background: 'var(--bg-secondary, #171a21)', borderRadius: 6,
                border: '1px solid var(--border-color)', fontSize: 11, lineHeight: 1.5, color: 'var(--text-secondary, #aab)',
                whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, cursor: 'pointer',
                maxHeight: expanded.has(tk.id) ? undefined : '3em', overflow: 'hidden',
              }}
            >{tk.tail}</pre>
          )}
        </div>
      ))}
    </div>
  );
};

export default AgentsPage;
