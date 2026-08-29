import React, { useState, useEffect } from 'react';
import { Trash2, Shield, Wrench, FileText, Terminal, Search, GitBranch, HardDrive } from 'lucide-react';
import { useAppStore } from '../stores/appStore';
import PlumBlossom from '../components/PlumBlossom';
import { useTranslation } from 'react-i18next';

const lbl: React.CSSProperties = {
  fontSize: 10.5, color: 'var(--text-muted)', display: 'block', marginBottom: 3, fontWeight: 500,
};

const selectStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', fontSize: 12, borderRadius: 7,
  border: '1px solid var(--border-color)', background: 'var(--bg-primary)',
  color: 'var(--text-primary)', outline: 'none',
};

const btnGhost: React.CSSProperties = {
  padding: '6px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 11,
  background: 'transparent', border: '1px solid var(--border-color)',
  color: 'var(--text-secondary)', fontWeight: 500,
  display: 'flex', alignItems: 'center', gap: 5,
};

const Section: React.FC<{ title: string; children: React.ReactNode }> = ({ title, children }) => (
  <div>
    <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 8, letterSpacing: '0.02em' }}>{title}</div>
    {children}
  </div>
);

type StoreState = ReturnType<typeof useAppStore.getState>;
type ConfigPatch = Record<string, unknown>;

export function PageTools({ store }: { store: StoreState }) {
  const { t } = useTranslation();
  const [bashTimeout, setBashTimeout] = useState(30);
  const [rules, setRules] = useState<Record<string,string>>({});
  const backendUrl = useAppStore((s) => s.backendUrl);

  // Knowledge base (local RAG) import state — D5 parity with WorkBuddy's
  // 资料库: report index status and accept a file path or pasted text.
  const [kbStatus, setKbStatus] = useState<'loading' | 'ok' | 'error'>('loading');
  const [kbChunks, setKbChunks] = useState(0);
  const [kbPath, setKbPath] = useState('');
  const [kbName, setKbName] = useState('');
  const [kbContent, setKbContent] = useState('');
  const [kbMsg, setKbMsg] = useState('');
  const [kbMsgOk, setKbMsgOk] = useState(true);
  const [kbBusy, setKbBusy] = useState(false);

  useEffect(() => {
    if (!backendUrl) return;
    fetch(`${backendUrl}/api/knowledge`)
      .then(r => r.json())
      .then(d => {
        setKbStatus(d.configured ? 'ok' : 'error');
        setKbChunks(d.chunks || 0);
      })
      .catch(() => setKbStatus('error'));
  }, [backendUrl]);

  const kbImportPath = async () => {
    if (!backendUrl || !kbPath.trim()) return;
    setKbBusy(true); setKbMsg('');
    try {
      const r = await fetch(`${backendUrl}/api/knowledge/import`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: kbPath.trim() }),
      });
      const d = await r.json().catch(() => ({} as Record<string, unknown>));
      setKbMsgOk(r.ok);
      setKbMsg(r.ok ? t('tools.knowledgeImported', { chunks: Number(d.chunks || 0) }) : String(d.error || 'failed'));
      if (r.ok) setKbPath('');
    } catch { setKbMsgOk(false); setKbMsg('failed'); }
    setKbBusy(false);
  };

  const kbImportPaste = async () => {
    if (!backendUrl || !kbContent.trim()) return;
    setKbBusy(true); setKbMsg('');
    try {
      const r = await fetch(`${backendUrl}/api/knowledge/import`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: kbName.trim() || 'pasted.md', content: kbContent }),
      });
      const d = await r.json().catch(() => ({} as Record<string, unknown>));
      setKbMsgOk(r.ok);
      setKbMsg(r.ok ? t('tools.knowledgeImported', { chunks: Number(d.chunks || 0) }) : String(d.error || 'failed'));
      if (r.ok) { setKbContent(''); setKbName(''); }
    } catch { setKbMsgOk(false); setKbMsg('failed'); }
    setKbBusy(false);
  };

  const saveConfig = async (patch: ConfigPatch) => {
    if (!store.backendUrl) return;
    try { await fetch(`${store.backendUrl}/api/config`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(patch) }); } catch {}
  };

  // Load existing tool rules from backend
  useEffect(() => {
    if (!backendUrl) return;
    fetch(`${backendUrl}/api/permission/rules`)
      .then(r => r.json())
      .then(d => setRules(d.rules || {}))
      .catch(() => {});
  }, [backendUrl]);

  const setRule = (tool: string, rule: string) => {
    setRules(prev => ({ ...prev, [tool]: rule }));
    if (backendUrl) {
      fetch(`${backendUrl}/api/permission/rules`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tool, rule }),
      }).catch(() => {});
    }
  };

  // Static tool catalog for display - sorted by danger
  const TOOLS = [
    { name:'read_file',   icon:FileText,    labelKey:'tools.readFile',  descKey:'tools.readFileDesc', risk:'safe' },
    { name:'write_file',  icon:FileText,    labelKey:'tools.writeFile',  descKey:'tools.writeFileDesc', risk:'mutate' },
    { name:'edit',        icon:Wrench,      labelKey:'tools.editFile',    descKey:'tools.editFileDesc', risk:'mutate' },
    { name:'bash',        icon:Terminal,    labelKey:'tools.bash',descKey:'tools.bashDesc', risk:'danger' },
    { name:'grep',        icon:Search,      labelKey:'tools.grep',descKey:'tools.grepDesc', risk:'safe' },
    { name:'glob',        icon:Search,      labelKey:'tools.glob',descKey:'tools.globDesc', risk:'safe' },
    { name:'ls',          icon:Search,      labelKey:'tools.ls',  descKey:'tools.lsDesc', risk:'safe' },
    { name:'fetch',       icon:Search,      labelKey:'tools.fetch',descKey:'tools.fetchDesc', risk:'safe' },
    { name:'disk_usage',  icon:HardDrive,   labelKey:'tools.diskUsage',descKey:'tools.diskUsageDesc', risk:'safe' },
    { name:'disk_cleanup',icon:HardDrive,   labelKey:'tools.diskCleanup',descKey:'tools.diskCleanupDesc', risk:'danger' },
    { name:'git_diff',    icon:GitBranch,   labelKey:'tools.gitDiff', descKey:'tools.gitDiffDesc', risk:'safe' },
    { name:'git_commit',  icon:GitBranch,   labelKey:'tools.gitCommit', descKey:'tools.gitCommitDesc', risk:'mutate' },
    { name:'git_status',  icon:GitBranch,   labelKey:'tools.gitStatus', descKey:'tools.gitStatusDesc', risk:'safe' },
    { name:'task',        icon:Wrench,      labelKey:'tools.subAgent',  descKey:'tools.subAgentDesc', risk:'mutate' },
    { name:'web_search',  icon:Search,      labelKey:'tools.webSearch',descKey:'tools.webSearchDesc', risk:'safe' },
    { name:'search_replace',icon:Wrench,    labelKey:'tools.searchReplace',descKey:'tools.searchReplaceDesc', risk:'mutate' },
  ];

  const getRule = (toolName: string): string => {
    return rules[toolName] || '';
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title={t('tools.toolRules')}>
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 12, lineHeight:1.6 }}>
          {t('tools.toolRulesDesc')}
          <b style={{color:'var(--success)'}}>{t('tools.alwaysAllow')}</b> / 
          <b style={{color:'var(--warning)'}}>{t('tools.askEach')}</b> / 
          <b style={{color:'var(--error)'}}>{t('tools.alwaysDeny')}</b>
        </div>
        <div style={{ display:'flex', flexWrap:'wrap', gap:6 }}>
          {TOOLS.map(tool => {
            const Icon = tool.icon;
            const rule = getRule(tool.name);
            const riskColor = tool.risk === 'safe' ? 'var(--success)' : tool.risk === 'danger' ? 'var(--error)' : 'var(--warning)';
            const borderColor = rule === 'allow' ? 'var(--success)' : rule === 'deny' ? 'var(--error)' : 'var(--border-color)';
            const bgColor = rule === 'allow' ? 'rgba(63,185,80,0.06)' : rule === 'deny' ? 'rgba(248,81,73,0.06)' : 'var(--bg-primary)';
            return (
              <div key={tool.name} style={{
                padding: '10px 12px', borderRadius: 8, border: `1px solid ${borderColor}`,
                background: bgColor, minWidth: 180, flex: 1,
                transition: 'all 0.12s',
              }}>
                <div style={{ display:'flex', alignItems:'center', gap:6, marginBottom:6 }}>
                  <Icon size={14} color={riskColor} />
                  <span style={{ fontSize:12, fontWeight:500, color:'var(--text-primary)' }}>{t(tool.labelKey)}</span>
                  <span style={{ fontSize:9, color:'var(--text-muted)', marginLeft:'auto' }}>{tool.name}</span>
                </div>
                <div style={{ fontSize:10, color:'var(--text-muted)', marginBottom:8 }}>{t(tool.descKey)}</div>
                <div style={{ display:'flex', gap:3 }}>
                  {[
                    { v:'',    labelKey:'tools.default' },
                    { v:'allow', labelKey:'tools.alwaysAllow' },
                    { v:'deny',  labelKey:'tools.alwaysDeny' },
                  ].map(opt => (
                    <button key={opt.v} onClick={() => setRule(tool.name, opt.v)} style={{
                      flex:1, padding:'3px 6px', borderRadius:4, cursor:'pointer', fontSize:9,
                      background: rule === opt.v ? 'var(--accent-soft)' : 'transparent',
                      border: rule === opt.v ? '1px solid var(--accent)' : '1px solid transparent',
                      color: rule === opt.v ? 'var(--accent)' : 'var(--text-muted)',
                      fontWeight: rule === opt.v ? 600 : 400,
                      transition: 'all 0.1s',
                    }}>{t(opt.labelKey)}</button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </Section>
      <Section title={t('tools.bashTimeout')}>
        <label style={lbl}>{t('tools.bashTimeout')}</label>
        <input type="number" min={5} max={300} value={bashTimeout}
          onChange={e => { setBashTimeout(Number(e.target.value)); saveConfig({ tools: { bash_timeout: Number(e.target.value) } }); }}
          style={{ ...selectStyle, width: 120 }} />
      </Section>
      <Section title={t('tools.sandbox')}>
        <div style={{ fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.6 }}>
          {t('tools.sandboxDesc')}
          <div style={{ marginTop: 8, padding: 12, background: 'var(--bg-primary)', borderRadius: 8, fontSize: 11 }}>
            {t('tools.blockedCmds')}<br/>
            {t('tools.editableScope')}
          </div>
        </div>
      </Section>
      <Section title={t('tools.knowledgeTitle')}>
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 10, lineHeight: 1.6 }}>
          {t('tools.knowledgeDesc')}
        </div>
        <div style={{ fontSize: 11, color: 'var(--text-secondary)', marginBottom: 10 }}>
          {kbStatus === 'ok'
            ? t('tools.knowledgeStatus', { chunks: kbChunks })
            : kbStatus === 'error'
              ? t('tools.knowledgeNotConfigured')
              : t('tools.loading')}
        </div>
        {/* Import from a local file path */}
        <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
          <input
            value={kbPath}
            onChange={(e) => setKbPath(e.target.value)}
            placeholder={t('tools.knowledgePathPlaceholder')}
            style={{ ...selectStyle, flex: 1 }}
          />
          <button onClick={kbImportPath} disabled={!!kbBusy} style={btnGhost}>{t('tools.knowledgeImportBtn')}</button>
        </div>
        {/* Import by pasting text */}
        <div style={{ display: 'flex', gap: 8 }}>
          <input
            value={kbName}
            onChange={(e) => setKbName(e.target.value)}
            placeholder={t('tools.knowledgeNamePlaceholder')}
            style={{ ...selectStyle, width: 180 }}
          />
          <textarea
            value={kbContent}
            onChange={(e) => setKbContent(e.target.value)}
            placeholder={t('tools.knowledgePastePlaceholder')}
            rows={3}
            style={{ ...selectStyle, flex: 1, resize: 'vertical', fontFamily: 'var(--font-mono)', fontSize: 11 }}
          />
          <button onClick={kbImportPaste} disabled={!!kbBusy} style={btnGhost}>{t('tools.knowledgePasteBtn')}</button>
        </div>
        {kbMsg && (
          <div style={{ fontSize: 11, color: kbMsgOk ? 'var(--success)' : 'var(--error)', marginTop: 8 }}>{kbMsg}</div>
        )}
      </Section>
    </div>
  );
}

export function PageUpdates({ store }: { store: StoreState }) {
  const { t } = useTranslation();
  const [autoUpdate, setAutoUpdate] = useState(true);
  const [channel, setChannel] = useState('stable');
  const [checking, setChecking] = useState(false);
  const [result, setResult] = useState<{ available: boolean; current: string; latest?: string; html_url?: string; error?: string } | null>(null);
  const saveConfig = async (patch: ConfigPatch) => {
    if (!store.backendUrl) return;
    try { await fetch(`${store.backendUrl}/api/config`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(patch) }); } catch {}
  };

  // Load persisted update settings from the backend.
  useEffect(() => {
    if (!store.backendUrl) return;
    fetch(`${store.backendUrl}/api/config`)
      .then(r => r.json())
      .then(cfg => {
        const u = cfg?.update;
        if (!u) return;
        if (typeof u.auto_update === 'boolean') setAutoUpdate(u.auto_update);
        if (typeof u.channel === 'string' && u.channel) setChannel(u.channel);
      })
      .catch(() => {});
  }, [store.backendUrl]);

  const checkUpdate = async () => {
    if (!store.backendUrl) return;
    setChecking(true);
    setResult(null);
    try {
      const res = await fetch(`${store.backendUrl}/api/update/check`, { cache: 'no-cache' });
      if (res.ok) setResult(await res.json());
      else setResult({ available: false, current: '', error: `HTTP ${res.status}` });
    } catch (e) {
      setResult({ available: false, current: '', error: e instanceof Error ? e.message : String(e) });
    } finally {
      setChecking(false);
    }
  };
  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title={t('updates.title')}>
        <label style={{ display:'flex', alignItems:'center', gap:8, cursor:'pointer' }}>
          <input type="checkbox" checked={autoUpdate}
            onChange={e => { setAutoUpdate(e.target.checked); saveConfig({ update: { auto_update: e.target.checked } }); }} />
          <span style={{ fontSize:12 }}>{t('updates.autoCheck')}</span>
        </label>
      </Section>
      <Section title={t('updates.channel')}>
        <div style={{ display:'flex', gap:6 }}>
          {[
            { v:'stable', text: t('updates.stable') },
            { v:'beta', text: 'Beta' },
            { v:'nightly', text: t('updates.nightly') },
          ].map(o => (
            <button key={o.v} onClick={() => { setChannel(o.v); saveConfig({ update: { channel: o.v } }); }}
              style={{ padding:'6px 14px', borderRadius:6, fontSize:11, cursor:'pointer', border: channel === o.v ? '1.5px solid var(--accent)' : '1px solid var(--border-color)', background: channel === o.v ? 'var(--accent-soft)' : 'transparent', color: channel === o.v ? 'var(--accent)' : 'var(--text-secondary)', fontWeight: channel === o.v ? 600 : 400 }}>
              {o.text}
            </button>
          ))}
        </div>
      </Section>
      <Section title={t('updates.checkNow')}>
        <div style={{ display:'flex', alignItems:'center', gap:10 }}>
          <button onClick={checkUpdate} disabled={checking}
            style={{ padding:'6px 14px', borderRadius:6, fontSize:11, cursor:'pointer', border:'1px solid var(--accent)', background:'var(--accent-soft)', color:'var(--accent)' }}>
            {checking ? t('updates.checking') : t('updates.checkNow')}
          </button>
          {result && (
            <div style={{ fontSize:12, color:'var(--text-secondary)', display:'flex', alignItems:'center', gap:8 }}>
              <span>当前 v{result.current || '?'}</span>
              {result.error ? (
                <span style={{ color:'var(--danger,#e05)' }}>{result.error}</span>
              ) : result.available ? (
                <>
                  <span style={{ color:'var(--accent)' }}>→ v{result.latest} 可用</span>
                  <a href={result.html_url} target="_blank" rel="noreferrer"
                    style={{ color:'var(--accent)', textDecoration:'underline', fontSize:12 }}>
                    {t('updates.openDownload')}
                  </a>
                </>
              ) : (
                <span>{t('updates.upToDate')}</span>
              )}
            </div>
          )}
        </div>
      </Section>
    </div>
  );
}

export function PageAbout({ store }: { store: StoreState }) {
  const { t } = useTranslation();
  const clearData = async () => {
    if (!window.confirm(t('settings.confirmClearAll'))) return;
    if (!store.backendUrl) return;
    try {
      // Wipe active sessions AND the trash bin (soft-deleted sessions are not
      // part of the default /api/sessions listing, so clear both).
      for (const suffix of ['', '?trash=1']) {
        const resp = await fetch(`${store.backendUrl}/api/sessions${suffix}`);
        const sessions = (await resp.json()) as Array<{ id: string }>;
        for (const s of sessions) {
          await fetch(`${store.backendUrl}/api/sessions/${s.id}`, { method: 'DELETE' });
        }
      }
    } catch {}
  };
  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title="iCODE">
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 14 }}>
          <PlumBlossom size={48} style={{ flexShrink: 0 }} />
          <div style={{ fontSize: 18, fontWeight: 700, letterSpacing: '-0.02em', color: 'var(--text-primary)' }}>
            iCODE
            <div style={{ fontSize: 12, fontWeight: 400, color: 'var(--text-muted)', marginTop: 2 }}>
              {t('app.subtitle')}
            </div>
          </div>
        </div>
        <div style={{ fontSize:12, lineHeight:1.6 }}>
          <div><span style={{ color:'var(--text-muted)' }}>{t('about.version')}</span>{store.backendVersion || 'v0.1.0'}</div>
          <div><span style={{ color:'var(--text-muted)' }}>{t('about.engine')}</span>iCode Go + React</div>
          <div><span style={{ color:'var(--text-muted)' }}>{t('about.dataPath')}</span>~/.icode/</div>
        </div>
      </Section>
      <Section title={t('about.dataManagement')}>
        <button onClick={clearData} style={{ ...btnGhost, color: '#EF4444', borderColor: '#EF444433' }}>
          <Trash2 size={12} /> {t('about.clearAllSessions')}
        </button>
      </Section>
    </div>
  );
}

export function PageNetwork({ store }: { store: StoreState }) {
  const { t } = useTranslation();
  const [proxy, setProxy] = useState('');
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!store.backendUrl) return;
    fetch(`${store.backendUrl}/api/config`)
      .then(r => r.json())
      .then(cfg => setProxy(cfg?.proxy || ''))
      .catch(() => {});
  }, [store.backendUrl]);

  const save = async () => {
    if (!store.backendUrl) return;
    try {
      await fetch(`${store.backendUrl}/api/config`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ proxy }),
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 1800);
    } catch {}
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title={t('network.proxy')}>
        <label style={lbl}>{t('network.proxy')}</label>
        <input
          type="text"
          value={proxy}
          placeholder="http://127.0.0.1:7890"
          onChange={e => setProxy(e.target.value)}
          style={selectStyle}
        />
        <div style={{ fontSize: 11, color: 'var(--text-muted)', lineHeight: 1.6, marginTop: 6 }}>
          {t('network.proxyHint')}
        </div>
        <button onClick={save} style={{ ...btnGhost, marginTop: 10, color: 'var(--accent)', borderColor: 'var(--accent)' }}>
          {saved ? t('network.saved') : t('network.save')}
        </button>
      </Section>
    </div>
  );
}

// ── Automations (WorkBuddy-style scheduled tasks) ──────────────────────────

interface AutomationTask {
  id: string;
  name: string;
  prompt: string;
  schedule: string;
  enabled: boolean;
  last_run?: string;
  next_run?: string;
  created_at?: string;
}
interface AutomationRun {
  id: string;
  task_id: string;
  started_at?: string;
  finished_at?: string;
  status: string;
  output?: string;
  error?: string;
}

export function PageAutomations({ store }: { store: StoreState }) {
  const { t } = useTranslation();
  const [tasks, setTasks] = useState<AutomationTask[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [schedule, setSchedule] = useState('every:24h');
  const [history, setHistory] = useState<Record<string, AutomationRun[]>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState<string | null>(null);

  const api = (path: string, opts?: RequestInit) =>
    fetch(`${store.backendUrl}/api/automations${path}`, {
      headers: { 'Content-Type': 'application/json' },
      ...opts,
    });

  const load = () => {
    if (!store.backendUrl) return;
    api('').then(r => r.json()).then((d) => {
      if (d && Array.isArray(d.tasks)) {
        setTasks(d.tasks);
        setEnabled(!!d.enabled);
      }
    }).catch(() => {});
  };
  useEffect(load, [store.backendUrl]);

  const toggleHistory = async (id: string) => {
    const next = { ...expanded, [id]: !expanded[id] };
    setExpanded(next);
    if (next[id] && !history[id]) {
      try {
        const r = await api(`/${id}/history?limit=5`);
        const d = await r.json();
        setHistory(h => ({ ...h, [id]: d.runs || [] }));
      } catch { /* ignore */ }
    }
  };

  const create = async () => {
    if (!name.trim() || !prompt.trim()) return;
    try {
      const r = await api('', { method: 'POST', body: JSON.stringify({ name, prompt, schedule }) });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(t('settings.automationCreateFailed', { error: d?.error || r.status }));
        return;
      }
      setName(''); setPrompt('');
      load();
    } catch (e) {
      alert(t('settings.automationCreateFailed', { error: String(e) }));
    }
  };

  const run = async (id: string) => {
    setBusy(id);
    try {
      await api(`/${id}/run`, { method: 'POST' });
      load();
    } catch (e) {
      alert(t('settings.automationRunFailed', { error: String(e) }));
    } finally {
      setBusy(null);
    }
  };

  const toggle = async (task: AutomationTask) => {
    try {
      await api(`/${task.id}`, { method: 'PUT', body: JSON.stringify({ enabled: !task.enabled }) });
      load();
    } catch (e) { alert(String(e)); }
  };

  const del = async (task: AutomationTask) => {
    if (!window.confirm(`${t('settings.automationDelete')}「${task.name}」？`)) return;
    try {
      await api(`/${task.id}`, { method: 'DELETE' });
      load();
    } catch (e) { alert(t('settings.automationDeleteFailed', { error: String(e) })); }
  };

  const fmt = (s?: string) => {
    if (!s) return '—';
    const d = new Date(s);
    return Number.isNaN(d.getTime()) ? s : d.toLocaleString();
  };

  if (!enabled) {
    return <Section title={t('settings.automationsTitle')}>
      <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>{t('settings.automationDisabled')}</div>
    </Section>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <Section title={t('settings.automationsTitle')}>
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 10 }}>{t('settings.automationsDesc')}</div>
        {tasks.length === 0 && (
          <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '8px 0' }}>{t('settings.automationsEmpty')}</div>
        )}
        {tasks.map(task => (
          <div key={task.id} style={{
            border: '1px solid var(--border-color)', borderRadius: 8, padding: '10px 12px', marginBottom: 8,
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>{task.name}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2, wordBreak: 'break-all' }}>
                  {task.schedule} · {t('settings.automationLastRun')}: {fmt(task.last_run)} · {t('settings.automationNextRun')}: {fmt(task.next_run)}
                </div>
              </div>
              <button onClick={() => toggle(task)} style={{
                ...btnGhost, padding: '4px 10px', fontSize: 10,
                color: task.enabled ? 'var(--accent)' : 'var(--text-muted)',
                borderColor: task.enabled ? 'var(--accent)' : 'var(--border-color)',
              }}>
                {task.enabled ? '✓' : '○'} {t('settings.automationEnabled')}
              </button>
              <button onClick={() => run(task.id)} disabled={busy === task.id} style={{ ...btnGhost, padding: '4px 10px', fontSize: 10 }}>
                {busy === task.id ? '…' : '▶'} {t('settings.automationRun')}
              </button>
              <button onClick={() => toggleHistory(task.id)} style={{ ...btnGhost, padding: '4px 10px', fontSize: 10 }}>
                {expanded[task.id] ? '▾' : '▸'} {t('settings.automationHistory')}
              </button>
              <button onClick={() => del(task)} style={{ ...btnGhost, padding: '4px 10px', fontSize: 10, color: '#e5484d' }}>
                <Trash2 size={11} /> {t('settings.automationDelete')}
              </button>
            </div>
            {expanded[task.id] && (
              <div style={{ marginTop: 8, borderTop: '1px solid var(--border-color)', paddingTop: 8 }}>
                <div style={{ fontSize: 10.5, color: 'var(--text-muted)', marginBottom: 4 }}>提示词:</div>
                <div style={{ fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{task.prompt}</div>
                {(history[task.id] || []).map(run => (
                  <div key={run.id} style={{
                    marginTop: 6, fontSize: 11, borderRadius: 6,
                    border: '1px solid var(--border-color)', padding: '6px 8px',
                    background: run.status === 'ok' ? 'rgba(46,160,67,0.08)' : run.status === 'error' ? 'rgba(229,72,77,0.08)' : 'transparent',
                  }}>
                    <div style={{ display: 'flex', gap: 8, color: 'var(--text-muted)' }}>
                      <span style={{
                        fontWeight: 600,
                        color: run.status === 'running' ? '#e5484d' : run.status === 'ok' ? '#2ea043' : 'var(--accent)',
                      }}>
                        {run.status === 'running' ? t('settings.automationRunning') : run.status === 'ok' ? t('settings.automationOk') : t('settings.automationError')}
                      </span>
                      <span>{fmt(run.started_at)}</span>
                      {run.error && <span style={{ color: '#e5484d' }}>{run.error}</span>}
                    </div>
                    {run.output && (
                      <div style={{ marginTop: 4, whiteSpace: 'pre-wrap', wordBreak: 'break-all', color: 'var(--text-secondary)' }}>
                        {run.output.length > 300 ? run.output.slice(0, 300) + '…' : run.output}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </Section>

      <Section title={t('settings.automationAdd')}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div>
            <span style={lbl}>{t('settings.automationName')}</span>
            <input value={name} onChange={e => setName(e.target.value)} placeholder="每日代码检查"
              style={selectStyle} />
          </div>
          <div>
            <span style={lbl}>{t('settings.automationPrompt')}</span>
            <textarea value={prompt} onChange={e => setPrompt(e.target.value)}
              placeholder="运行 git status，报告未提交的改动"
              rows={2}
              style={{ ...selectStyle, resize: 'vertical', fontFamily: 'inherit' }} />
          </div>
          <div>
            <span style={lbl}>{t('settings.automationSchedule')}</span>
            <input value={schedule} onChange={e => setSchedule(e.target.value)} placeholder="every:24h"
              style={selectStyle} />
          </div>
          <button onClick={create} style={{
            ...btnGhost, justifyContent: 'center', background: 'var(--accent)', color: '#fff', borderColor: 'transparent',
          }}>
            {t('settings.automationAdd')}
          </button>
        </div>
      </Section>
    </div>
  );
}
