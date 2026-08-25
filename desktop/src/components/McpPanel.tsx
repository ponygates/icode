import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { Server, Loader2, PlayCircle, CheckCircle2, XCircle } from 'lucide-react';

interface McpServerInfo {
  name: string;
  type: string;
  command: string;
  args?: string[];
  url?: string;
  enabled: boolean;
  connected: boolean;
  tool_count?: number;
}

interface TestResult {
  ok: boolean;
  tools?: string[];
  error?: string;
}

// McpPanel — visual MCP connection testing (workbuddy parity). Lists the
// configured servers with live status, runs /api/mcp/test on demand without
// persisting anything, and shows the discovered tool names so a broken config
// is obvious at a glance.
const McpPanel: React.FC = () => {
  const { t } = useTranslation();
  const backendUrl = useAppStore(s => s.backendUrl);
  const [servers, setServers] = useState<McpServerInfo[]>([]);
  const [testing, setTesting] = useState('');
  const [results, setResults] = useState<Record<string, TestResult>>({});

  const refresh = async () => {
    if (!backendUrl) return;
    try {
      const res = await fetch(`${backendUrl}/api/mcp`);
      const data = await res.json();
      setServers(Array.isArray(data) ? data : data?.servers || []);
    } catch {
      /* backend not ready */
    }
  };

  useEffect(() => { refresh(); /* eslint-disable-next-line */ }, [backendUrl]);

  const runTest = async (srv: McpServerInfo) => {
    if (!backendUrl || testing) return;
    setTesting(srv.name);
    try {
      const res = await fetch(`${backendUrl}/api/mcp/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: srv.name,
          type: srv.type || 'stdio',
          command: srv.command,
          args: srv.args || [],
          url: srv.url || '',
        }),
      });
      const data = await res.json();
      setResults(prev => ({ ...prev, [srv.name]: data as TestResult }));
    } catch {
      setResults(prev => ({ ...prev, [srv.name]: { ok: false, error: t('mcpTest.networkError') } }));
    } finally {
      setTesting('');
    }
  };

  return (
    <div className="card" style={{ padding: 14 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
        <Server size={13} style={{ color: 'var(--accent)' }} /> {t('mcpTest.title')}
      </div>

      {servers.length === 0 && (
        <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('mcpTest.none')}</div>
      )}

      {servers.map(srv => {
        const r = results[srv.name];
        return (
          <div key={srv.name} style={{ marginBottom: 10, fontSize: 11 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span
                title={srv.connected ? t('mcpTest.connected') : t('mcpTest.disconnected')}
                style={{
                  width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
                  background: srv.connected ? 'var(--success)' : 'var(--text-muted)',
                }}
              />
              <span style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{srv.name}</span>
              <span style={{ color: 'var(--text-muted)', fontSize: 10 }}>
                {srv.type}{srv.connected ? ` · ${srv.tool_count ?? 0} ${t('mcpTest.tools')}` : ''}
              </span>
              <button
                onClick={() => runTest(srv)}
                disabled={testing === srv.name}
                style={btnStyle}
                title={t('mcpTest.test')}
              >
                {testing === srv.name ? <Loader2 size={11} className="spin" /> : <PlayCircle size={11} />}
              </button>
            </div>
            {r && (
              <div style={{ marginTop: 4, paddingLeft: 12, lineHeight: 1.5 }}>
                {r.ok ? (
                  <div style={{ display: 'flex', gap: 5, alignItems: 'flex-start' }}>
                    <CheckCircle2 size={11} style={{ color: 'var(--success)', marginTop: 2, flexShrink: 0 }} />
                    <span style={{ color: 'var(--text-secondary)', wordBreak: 'break-word' }}>
                      {r.tools?.length ? r.tools.join(', ') : t('mcpTest.noTools')}
                    </span>
                  </div>
                ) : (
                  <div style={{ display: 'flex', gap: 5, alignItems: 'flex-start' }}>
                    <XCircle size={11} style={{ color: 'var(--danger)', marginTop: 2, flexShrink: 0 }} />
                    <span style={{ color: 'var(--danger)', wordBreak: 'break-word' }}>{r.error}</span>
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

const btnStyle: React.CSSProperties = {
  marginLeft: 'auto', display: 'flex', alignItems: 'center', padding: '3px 7px',
  borderRadius: 5, cursor: 'pointer', background: 'var(--bg-tertiary)',
  border: '0.5px solid var(--border-color)', color: 'var(--text-secondary)',
};

export default McpPanel;
