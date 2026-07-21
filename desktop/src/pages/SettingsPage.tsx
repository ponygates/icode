import React, { useState, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import { PageTools, PageUpdates, PageAbout } from './SettingsPagesExtra';
import {
  Settings, X, Key, Globe, Shield, Cpu, Moon, Sun, Monitor,
  Zap, Wrench, Boxes, DollarSign, ChevronDown, Check, Plus, Trash2,
  Thermometer, Hash, Layers, RefreshCw, Info, ExternalLink, Star, AlertCircle,
} from 'lucide-react';

// ═══════════════════════════════════════════════════════════════
// Reasonix-style Settings Modal
// ═══════════════════════════════════════════════════════════════

type PageId = 'general' | 'models' | 'mcp' | 'skills' | 'billing' | 'shortcuts' | 'tools' | 'updates' | 'about';

interface PageMeta { id: PageId; icon: React.ElementType; labelKey: string; }

const PAGES: PageMeta[] = [
  { id: 'general',  icon: Settings,    labelKey: 'pageGeneral' },
  { id: 'models',   icon: Cpu,         labelKey: 'pageModels' },
  { id: 'mcp',      icon: Wrench,      labelKey: 'pageMCP' },
  { id: 'tools',    icon: Shield,      labelKey: 'pageTools' },
  { id: 'skills',   icon: Zap,         labelKey: 'pageSkills' },
  { id: 'billing',  icon: DollarSign,  labelKey: 'pageBilling' },
  { id: 'updates',  icon: RefreshCw,   labelKey: 'pageUpdates' },
  { id: 'shortcuts',icon: Boxes,       labelKey: 'pageShortcuts' },
  { id: 'about',    icon: Info,        labelKey: 'pageAbout' },
];

// Provider colours
const pc: Record<string,string> = {
  deepseek:'#4F46E5', zhipu:'#7C3AED', kimi:'#0891B2',
  openrouter:'#F59E0B', volcengine:'#3B82F6', tencent:'#06B6D4',
  huawei:'#EF4444', scnet:'#10B981', nvidia:'#84CC16',
  anthropic:'#D97706',
};

// ═══════════════════════════════════════════════════════════════
// Main Settings Component
// ═══════════════════════════════════════════════════════════════

const SettingsPage: React.FC<{ visible: boolean; onClose: () => void }> = ({ visible, onClose }) => {
  const [page, setPage] = useState<PageId>('general');
  const store = useAppStore();
  const { t } = useTranslation();

  if (!visible) return null;

  return (
    <div style={maskStyle} onClick={onClose}>
      <div style={modalStyle} onClick={(e) => e.stopPropagation()}>
        {/* Left nav — Reasonix settings-side pattern */}
        <nav style={sideStyle}>
          <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.08em', padding: '16px 20px 12px' }}>
            设置
          </div>
          {PAGES.map((p) => {
            const Icon = p.icon;
            const active = page === p.id;
            return (
              <div key={p.id}
                onClick={() => setPage(p.id)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 10,
                  padding: '8px 20px', cursor: 'pointer', fontSize: 13,
                  color: active ? 'var(--text-primary)' : 'var(--text-secondary)',
                  background: active ? 'var(--accent-soft)' : 'transparent',
                  borderRight: active ? `2px solid var(--accent)` : '2px solid transparent',
                  fontWeight: active ? 500 : 400,
                  transition: 'all 0.12s',
                }}
              >
                <Icon size={14} />
                {t(`settings.${p.labelKey}`)}
              </div>
            );
          })}
        </nav>

        {/* Right content — Reasonix settings-main pattern */}
        <div style={mainStyle}>
          <div style={headStyle}>
            <div>
              <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>
                {t(`settings.${PAGES.find(p => p.id === page)?.labelKey || 'pageGeneral'}`)}
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
                {t(page === 'models' ? 'settings.subtitleModels' : 'settings.subtitleGeneral')}
              </div>
            </div>
            <button onClick={onClose} style={closeBtn}><X size={17} /></button>
          </div>

          <div style={bodyStyle}>
            {page === 'general' && <PageGeneral store={store} />}
            {page === 'models' && <PageModels store={store} />}
            {page === 'mcp' && <PageMCP store={store} />}
            {page === 'tools' && <PageTools store={store} />}
            {page === 'skills' && <PageSkills />}
            {page === 'billing' && <PageBilling store={store} />}
            {page === 'updates' && <PageUpdates store={store} />}
            {page === 'shortcuts' && <PageShortcuts />}
            {page === 'about' && <PageAbout store={store} />}
          </div>
        </div>
      </div>
    </div>
  );
};

// ═══════════════════════════════════════════════════════════════
// Page: General
// ═══════════════════════════════════════════════════════════════

function PageGeneral({ store }: { store: ReturnType<typeof useAppStore.getState> }) {
  const [theme, setTheme] = useState(() =>
    document.documentElement.getAttribute('data-theme') || 'dark');

  const applyTheme = (t: string) => {
    setTheme(t);
    const root = document.documentElement;
    if (t === 'auto') {
      root.setAttribute('data-theme', window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    } else {
      root.setAttribute('data-theme', t);
    }
    localStorage.setItem('icode.theme', t);
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      {/* Theme */}
      <Section title="外观">
        <div style={{ display:'flex', gap:6 }}>
          {[
            { v:'dark',  icon: Moon, label:'深色' },
            { v:'light', icon: Sun, label:'浅色' },
            { v:'auto',  icon: Monitor, label:'自动' },
          ].map(o => {
            const sel = theme === o.v;
            return (
              <button key={o.v} onClick={() => applyTheme(o.v)} style={{
                display:'flex', alignItems:'center', gap:6, padding:'8px 16px',
                borderRadius:8, border: sel ? '1.5px solid var(--accent)' : '1px solid var(--border-color)',
                background: sel ? 'var(--accent-soft)' : 'var(--bg-primary)',
                color: sel ? 'var(--accent)' : 'var(--text-secondary)',
                fontSize:12, fontWeight: sel?600:400, cursor:'pointer', flex:1, justifyContent:'center',
              }}>
                <o.icon size={13} />{o.label}
              </button>
            );
          })}
        </div>
      </Section>

      {/* Font Size */}
      <Section title="界面字号">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ fontSize: 11, color: 'var(--text-muted)', minWidth: 30 }}>12</span>
          <input type="range" min={10} max={20} step={0.5} defaultValue={13}
            style={{ flex: 1, accentColor: 'var(--accent)' }}
            onChange={(e) => {
              const size = e.target.value;
              document.documentElement.style.fontSize = size + 'px';
              localStorage.setItem('icode.fontSize', size);
            }} />
          <span style={{ fontSize: 11, color: 'var(--text-muted)', minWidth: 30, textAlign: 'right' }}>20</span>
        </div>
      </Section>

      {/* Language */}
      <Section title="语言">
        <select style={selectStyle} defaultValue={store.language}
          onChange={(e) => store.setLanguage(e.target.value)}>
          <option value="zh-CN">简体中文</option>
          <option value="zh-TW">繁體中文</option>
          <option value="en">English</option>
        </select>
      </Section>

      {/* Security Level */}
      <Section title="安全等级" desc="控制数据隐私边界。等级越高，对外发送的限制越严格。">
        <select style={selectStyle} value={store.securityLevel}
          onChange={(e) => store.setSecurityLevel(e.target.value)}>
          <option value="local">🔒 本地 — 所有数据仅本地处理</option>
          <option value="desensitize">🛡 脱敏 — 隐藏敏感信息后发送</option>
          <option value="local-llm">💻 本地大模型 — 仅调用本地模型</option>
          <option value="foreign-llm">🌐 国外大模型 — 允许国外 API</option>
          <option value="unrestricted">⚠ 无限制</option>
        </select>
        <div style={{ fontSize:11, color:'var(--text-muted)', marginTop:6 }}>
          <Shield size={10} style={{display:'inline',marginRight:4}} />
          当前: <b>{store.securityLevel === 'local' ? '所有数据仅本地处理，不发送到外部' : store.securityLevel}</b>
        </div>
      </Section>

      {/* Permission Mode */}
      <Section title="权限模式" desc="控制工具调用的确认方式。">
        <div style={{ display:'flex', gap:6 }}>
          {[
            { v:'plan',  label:'plan — 只读，修改被阻止' },
            { v:'agent', label:'agent — 每次操作确认' },
            { v:'auto',  label:'auto — 只读自动，修改询问' },
            { v:'yolo',  label:'yolo — 全自动，无需确认' },
          ].map(({v, label}) => {
            const sel = store.mode === v || (store.mode === 'plan' && v === 'agent');
            return (
              <button key={v} onClick={() => store.setMode(v)} style={{
                padding:'7px 14px', borderRadius:6, fontSize:11, fontWeight: sel ? 600 : 400,
                background: sel ? 'var(--accent-soft)' : 'var(--bg-primary)',
                border: sel ? '1.5px solid var(--accent)' : '1px solid var(--border-color)',
                color: sel ? 'var(--accent)' : 'var(--text-secondary)', cursor:'pointer',
                transition: 'all 0.12s',
              }}>{label}</button>
            );
          })}
        </div>
      </Section>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Page: Models — Provider groups, collapsible, one-at-a-time
// ═══════════════════════════════════════════════════════════════

function PageModels({ store }: { store: ReturnType<typeof useAppStore.getState> }) {
  const models = store.models;
  const providers = Array.from(new Set(models.map(m => m.provider)));
  const [expanded, setExpanded] = useState<string | null>(null);
  const [editingModel, setEditingModel] = useState<string | null>(null);

  // Per-model edit state
  const [editData, setEditData] = useState<Record<string,{ apiKey:string; apiBase:string; temp:number; maxToks:number }>>({});
  const [saved, setSaved] = useState<Record<string,boolean>>({});

  const toggleProvider = (p: string) => {
    setExpanded(prev => prev === p ? null : p);
    setEditingModel(null);
  };

  const startEdit = (modelId: string) => {
    setEditingModel(prev => prev === modelId ? null : modelId);
    if (!editData[modelId]) {
      setEditData(prev => ({
        ...prev,
        [modelId]: { apiKey:'', apiBase:'', temp:0.7, maxToks:4096 },
      }));
    }
  };

  const updateField = (modelId: string, field: string, value: string | number) => {
    setEditData(prev => ({
      ...prev,
      [modelId]: { ...(prev[modelId] || { apiKey:'', apiBase:'', temp:0.7, maxToks:4096 }), [field]:value },
    }));
  };

  const doSave = async (modelId: string) => {
    const data = editData[modelId];
    const prov = models.find(m => m.id === modelId)?.provider || '';
    if (!data || !store.backendUrl || !prov) return;
    try {
      await fetch(`${store.backendUrl}/api/config/key`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider: prov, api_key: data.apiKey, api_base: data.apiBase }),
      });
    } catch {}
    setSaved(prev => ({ ...prev, [modelId]: true }));
    setTimeout(() => setSaved(prev => ({ ...prev, [modelId]: false })), 1500);
    setEditingModel(null);
    await store.refreshModels();
  };

  const doDeleteKey = async (modelId: string) => {
    const prov = models.find(m => m.id === modelId)?.provider || '';
    if (store.backendUrl && prov) {
      try {
        await fetch(`${store.backendUrl}/api/config/key`, {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ provider: prov }),
        });
      } catch {}
    }
    updateField(modelId, 'apiKey', '');
    updateField(modelId, 'apiBase', '');
    setSaved(prev => ({ ...prev, [modelId]: true }));
    setTimeout(() => setSaved(prev => ({ ...prev, [modelId]: false })), 1000);
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:8 }}>
      {providers.map(provider => {
        const pModels = models.filter(m => m.provider === provider);
        const isOpen = expanded === provider;
        const color = pc[provider] || '#6366F1';
        const hasKeyConfigured = editData[pModels[0]?.id]?.apiKey;

        return (
          <div key={provider} style={{
            background: 'var(--bg-primary)', borderRadius: 10,
            border: isOpen ? `1px solid ${color}40` : '1px solid var(--border-color)',
            overflow: 'hidden',
            transition: 'border-color 0.15s',
          }}>
            {/* Provider header — Reasonix card-head pattern */}
            <div onClick={() => toggleProvider(provider)} style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '12px 16px', cursor: 'pointer', userSelect: 'none',
            }}>
              <div style={{
                width: 30, height: 30, borderRadius: 7,
                background: `${color}16`, border: `1px solid ${color}30`,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <Cpu size={15} color={color} />
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                  {provider}
                </div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                  {pModels.length} 个模型 · {hasKeyConfigured ? '已配置 API Key' : '点击配置'}
                </div>
              </div>
              {hasKeyConfigured && <span style={{
                fontSize: 9, padding: '2px 6px', borderRadius: 4, background: 'rgba(63,185,80,0.12)', color: 'var(--success)',
              }}>
                <Check size={10} style={{display:'inline',marginRight:2}} />已关联
              </span>}
              <ChevronDown size={14} color="var(--text-muted)" style={{
                transform: isOpen ? 'rotate(180deg)' : 'rotate(0deg)',
                transition: 'transform 0.2s ease',
              }} />
            </div>

            {/* Expanded model list — Reasonix mcard pattern */}
            {isOpen && (
              <div style={{ padding: '0 16px 16px', display: 'flex', flexDirection: 'column', gap: 6, animation: 'slideUp 0.15s ease' }}>
                {pModels.map(model => {
                  const ed = editData[model.id];
                  const isEditing = editingModel === model.id;
                  const isSaved = saved[model.id];
                  const isSelected = store.selectedModel === model.id;

                  return (
                    <div key={model.id} style={{
                      padding: '10px 14px', borderRadius: 8,
                      background: isSelected ? `${color}0C` : 'var(--bg-secondary)',
                      border: isSelected ? `1px solid ${color}30` : '1px solid transparent',
                      borderLeft: isSelected ? `3px solid ${color}` : '3px solid transparent',
                      transition: 'all 0.12s',
                    }}>
                      {/* Model row */}
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <div onClick={() => store.setSelectedModel(model.id)}
                          style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1, cursor: 'pointer' }}>
                          <div style={{
                            width: 14, height: 14, borderRadius: '50%',
                            border: isSelected ? `2px solid ${color}` : '2px solid var(--border-color)',
                            background: isSelected ? color : 'transparent',
                            flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            {isSelected && <Check size={8} color="#fff" />}
                          </div>
                          <div>
                            <div style={{ fontSize: 12.5, fontWeight: isSelected ? 600 : 400, color: 'var(--text-primary)' }}>
                              {model.name}
                            </div>
                            <div style={{ fontSize: 9.5, color: 'var(--text-muted)' }}>{model.id}</div>
                          </div>
                        </div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          {model.plan && (
                            <span style={{
                              fontSize: 9, padding: '2px 6px', borderRadius: 4,
                              background: `${color}14`, color, fontWeight: 500,
                            }}>{model.plan}</span>
                          )}
                          <button onClick={() => startEdit(model.id)} style={{
                            width: 26, height: 26, borderRadius: 6, border: '1px solid var(--border-color)',
                            background: isEditing ? `${color}18` : 'transparent',
                            color: isEditing ? color : 'var(--text-muted)',
                            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            <Settings size={12} />
                          </button>
                        </div>
                      </div>

                      {/* Edit form — Reasonix expand pattern (conditional render) */}
                      {isEditing && (
                        <div style={{ marginTop: 12, display:'flex', flexDirection:'column', gap: 10, animation: 'fadeIn 0.15s ease' }}>
                          <div style={{ display:'grid', gridTemplateColumns:'1fr 1fr', gap:10 }}>
                            <Field label="API Key" type="password" placeholder="sk-..."
                              value={ed?.apiKey || ''}
                              onChange={v => updateField(model.id, 'apiKey', v)} />
                            <Field label="Base URL" placeholder="默认端点"
                              value={ed?.apiBase || ''}
                              onChange={v => updateField(model.id, 'apiBase', v)} />
                          </div>
                          <div style={{ display:'grid', gridTemplateColumns:'1fr 1fr', gap:10 }}>
                            <div>
                              <label style={lbl}>温度 {ed?.temp?.toFixed(1) ?? '0.7'}</label>
                              <input type="range" min={0} max={2} step={0.1}
                                value={ed?.temp ?? 0.7}
                                onChange={e => updateField(model.id, 'temp', parseFloat(e.target.value))}
                                style={{ width: '100%', accentColor: color }} />
                            </div>
                            <div>
                              <label style={lbl}>最大 Token {(ed?.maxToks && ed.maxToks >= 1000) ? `${(ed.maxToks/1000).toFixed(1)}K` : ed?.maxToks ?? '4K'}</label>
                              <input type="range" min={256} max={128000} step={256}
                                value={ed?.maxToks ?? 4096}
                                onChange={e => updateField(model.id, 'maxToks', parseInt(e.target.value))}
                                style={{ width: '100%', accentColor: color }} />
                            </div>
                          </div>

                          {/* Actions */}
                          <div style={{ display:'flex', gap:6, justifyContent:'flex-end' }}>
                            <button onClick={() => doDeleteKey(model.id)} style={btnGhost}>
                              <Trash2 size={11} /> 清除
                            </button>
                            <button onClick={() => setEditingModel(null)} style={btnGhost}>取消</button>
                            <button onClick={() => doSave(model.id)} style={{
                              ...btnPrimary, background: isSaved ? 'var(--success)' : color,
                            }}>
                              {isSaved ? <><Check size={12} /> 已保存</> : '保存'}
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}

      {/* Refresh button */}
      <div style={{ display:'flex', justifyContent:'center', marginTop: 4 }}>
        <button onClick={() => store.refreshModels()} style={{
          ...btnGhost, fontSize: 11, padding: '6px 14px',
        }}>
          <RefreshCw size={12} /> 刷新模型列表
        </button>
      </div>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Page: MCP — full CRUD + test management
// ═══════════════════════════════════════════════════════════════

interface MCPServerView {
  name: string; type: string; command: string; args?: string[];
  url?: string; enabled: boolean; connected: boolean; tools: number;
  trustMode?: string;
}

function PageMCP({ store }: { store: ReturnType<typeof useAppStore.getState> }) {
  const [servers, setServers] = useState<MCPServerView[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<string | null>(null);    // server name being edited
  const [adding, setAdding] = useState(false);
  const [testResult, setTestResult] = useState<{name:string; ok:boolean; tools?:string[]; error?:string} | null>(null);
  const [allTools, setAllTools] = useState<string[]>([]);
  const [showTools, setShowTools] = useState(false);

  // Form state for add/edit
  const [form, setForm] = useState({ name:'', type:'stdio', command:'', args:'', url:'', enabled:true });

  const api = (path: string, opts?: RequestInit) =>
    fetch(`${store.backendUrl}/api/mcp${path}`, { headers:{'Content-Type':'application/json'}, ...opts });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api('');
      if (res.ok) setServers(await res.json());
    } catch {}
    setLoading(false);
  }, [store.backendUrl]);

  const loadTools = useCallback(async () => {
    try {
      const res = await api('/tools');
      if (res.ok) {
        const tools = await res.json();
        setAllTools((tools as any[]).map(t => t.name));
      }
    } catch {}
  }, [store.backendUrl]);

  useEffect(() => { load(); loadTools(); }, [load, loadTools]);

  const resetForm = () => setForm({ name:'', type:'stdio', command:'', args:'', url:'', enabled:true });

  const startAdd = () => { resetForm(); setAdding(true); setEditing(null); };

  const startEdit = (s: MCPServerView) => {
    setForm({ name:s.name, type:s.type, command:s.command, args:(s.args||[]).join(' '), url:s.url||'', enabled:s.enabled });
    setEditing(s.name); setAdding(false);
  };

  const doSave = async () => {
    const body: any = { name: form.name, type: form.type, command: form.command, enabled: form.enabled };
    if (form.args.trim()) body.args = form.args.trim().split(/\s+/);
    if (form.url.trim()) body.url = form.url.trim();
    await api('', { method:'PUT', body: JSON.stringify(body) });
    setAdding(false); setEditing(null); load(); loadTools();
  };

  const doDelete = async (name: string) => {
    if (!window.confirm(`确定删除 MCP 服务器「${name}」？`)) return;
    await api('', { method:'DELETE', body: JSON.stringify({ name }) });
    load(); loadTools();
  };

  const doTest = async (s: MCPServerView) => {
    const body: any = { name: s.name, type: s.type, command: s.command, enabled: true };
    if ((s.args||[]).length > 0) body.args = s.args;
    if (s.url) body.url = s.url;
    setTestResult(null);
    try {
      const res = await api('/test', { method:'POST', body: JSON.stringify(body) });
      const data = await res.json();
      setTestResult({ name: s.name, ok: data.ok, tools: data.tools, error: data.error });
    } catch (e: any) {
      setTestResult({ name: s.name, ok: false, error: e.message });
    }
  };

  if (loading && servers.length === 0) {
    return <Section title="MCP 服务器"><div style={{padding:16,textAlign:'center',fontSize:12,color:'var(--text-muted)'}}>加载中...</div></Section>;
  }

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:12 }}>
      {/* Toolbar */}
      <div style={{ display:'flex', justifyContent:'space-between', alignItems:'center' }}>
        <Section title="MCP 服务器" desc="管理 Model Context Protocol 服务器连接，添加后自动发现工具并注入引擎。">
          <div style={{ fontSize: 10.5, color: 'var(--text-muted)', marginTop: -4 }}>
            {servers.length} 台服务器 · {allTools.length} 个工具
          </div>
        </Section>
        <div style={{ display:'flex', gap:6 }}>
          <button onClick={() => { setShowTools(!showTools); if(!showTools) loadTools(); }} style={btnGhost}>
            <Layers size={12} /> 工具列表
          </button>
          <button onClick={startAdd} style={{ ...btnPrimary, background: 'var(--accent)' }}>
            <Plus size={12} /> 添加服务器
          </button>
        </div>
      </div>

      {/* Tools panel */}
      {showTools && (
        <div style={{ padding:12, background:'var(--bg-primary)', borderRadius:8, border:'1px solid var(--border-color)', fontSize:11, maxHeight:160, overflowY:'auto' }}>
          <div style={{ fontWeight:600, color:'var(--text-primary)', marginBottom:6 }}>已发现 MCP 工具</div>
          {allTools.length === 0
            ? <div style={{ color:'var(--text-muted)' }}>暂无工具，添加 MCP 服务器后自动发现</div>
            : allTools.map(t => <div key={t} style={{ padding:'3px 0', color:'var(--text-secondary)', fontFamily:'var(--font-mono)', fontSize:10 }}>{t}</div>)
          }
        </div>
      )}

      {/* Add form */}
      {adding && (
        <div style={{ padding:14, background:'var(--bg-primary)', borderRadius:10, border:'1px solid var(--accent)', animation:'fadeIn 0.12s' }}>
          <div style={{ fontSize:12, fontWeight:600, color:'var(--accent)', marginBottom:10 }}>+ 新建 MCP 服务器</div>
          <MCPServerForm form={form} setForm={setForm} />
          <div style={{ display:'flex', gap:6, justifyContent:'flex-end', marginTop:10 }}>
            <button onClick={() => setAdding(false)} style={btnGhost}>取消</button>
            <button onClick={doSave} disabled={!form.name || !form.command} style={{...btnPrimary, background:'var(--accent)', opacity:(!form.name||!form.command)?0.5:1}}>
              <Check size={12} /> 保存
            </button>
          </div>
        </div>
      )}

      {/* Server list */}
      {servers.length === 0 && !adding && (
        <div style={{ padding:24, textAlign:'center', color:'var(--text-muted)', fontSize:12 }}>
          <Wrench size={24} style={{ marginBottom:8, opacity:0.4 }} />
          <div>暂无 MCP 服务器</div>
          <button onClick={startAdd} style={{ ...btnGhost, margin:'10px auto 0' }}><Plus size={12} /> 添加第一个</button>
        </div>
      )}

      {servers.map(s => {
        const isEditing = editing === s.name;
        const isTesting = testResult?.name === s.name;

        return (
          <div key={s.name} style={{
            background:'var(--bg-primary)', borderRadius:10,
            border: isEditing ? '1px solid var(--accent)' : '1px solid var(--border-color)',
            overflow:'hidden', transition:'border-color 0.15s',
          }}>
            {/* Header row */}
            <div style={{ display:'flex', alignItems:'center', gap:10, padding:'10px 14px' }}>
              <div style={{
                width:28, height:28, borderRadius:7,
                background: s.connected ? 'rgba(63,185,80,0.12)' : 'rgba(239,68,68,0.1)',
                border: `1px solid ${s.connected ? 'rgba(63,185,80,0.25)' : 'rgba(239,68,68,0.2)'}`,
                display:'flex', alignItems:'center', justifyContent:'center',
              }}>
                <Wrench size={13} color={s.connected ? 'var(--success)' : '#EF4444'} />
              </div>
              <div style={{ flex:1 }}>
                <div style={{ display:'flex', alignItems:'center', gap:6 }}>
                  <span style={{ fontSize:13, fontWeight:600, color:'var(--text-primary)' }}>{s.name}</span>
                  {s.connected
                    ? <span style={{ fontSize:9, padding:'1px 6px', borderRadius:4, background:'rgba(63,185,80,0.1)', color:'var(--success)' }}>已连接</span>
                    : <span style={{ fontSize:9, padding:'1px 6px', borderRadius:4, background:'rgba(239,68,68,0.1)', color:'#EF4444' }}>未连接</span>
                  }
                  {!s.enabled && <span style={{ fontSize:9, padding:'1px 6px', borderRadius:4, background:'rgba(255,159,67,0.1)', color:'#FF9F43' }}>已禁用</span>}
                </div>
                <div style={{ fontSize:10, color:'var(--text-muted)', marginTop:2, fontFamily:'var(--font-mono)', whiteSpace:'nowrap', overflow:'hidden', textOverflow:'ellipsis' }}>
                  {s.command}{s.args?.length ? ' ' + s.args.join(' ') : ''}
                </div>
                <div style={{ fontSize:9.5, color:'var(--text-muted)', marginTop:1 }}>
                  {s.tools} 个工具 · {s.type}
                </div>
              </div>
              {/* Actions */}
              <div style={{ display:'flex', gap:4, alignItems:'center' }}>
                {/* Pre-trust toggle */}
                <select
                  value={s.trustMode || 'ask'}
                  onChange={async (e) => {
                    const mode = e.target.value;
                    try {
                      await api('/trust', { method:'PUT', body: JSON.stringify({ name: s.name, trust_mode: mode }) });
                      load();
                    } catch {}
                  }}
                  title="预信任模式"
                  style={{
                    fontSize: 10, padding: '1px 4px', borderRadius: 4,
                    border: '1px solid var(--border-color)',
                    background: 'var(--bg-secondary)', color: 'var(--text-muted)',
                    cursor: 'pointer', maxWidth: 80,
                  }}>
                  <option value="ask">🔒 全部询问</option>
                  <option value="readonly">📖 只读预信任</option>
                  <option value="all">🔓 全部预信任</option>
                </select>

                <button onClick={() => doTest(s)} title="测试连接" style={{
                  width:26, height:26, borderRadius:6, border:'1px solid var(--border-color)',
                  background:'transparent', color:'var(--text-muted)', cursor:'pointer',
                  display:'flex', alignItems:'center', justifyContent:'center',
                }}>
                  <RefreshCw size={11} />
                </button>
                <button onClick={() => startEdit(s)} title="编辑" style={{
                  width:26, height:26, borderRadius:6, border:'1px solid var(--border-color)',
                  background: isEditing ? 'var(--accent-soft)' : 'transparent',
                  color: isEditing ? 'var(--accent)' : 'var(--text-muted)', cursor:'pointer',
                  display:'flex', alignItems:'center', justifyContent:'center',
                }}>
                  <Settings size={11} />
                </button>
                <button onClick={() => doDelete(s.name)} title="删除" style={{
                  width:26, height:26, borderRadius:6, border:'1px solid rgba(239,68,68,0.25)',
                  background:'transparent', color:'#EF4444', cursor:'pointer',
                  display:'flex', alignItems:'center', justifyContent:'center',
                }}>
                  <Trash2 size={11} />
                </button>
              </div>
            </div>

            {/* Test result */}
            {isTesting && testResult && (
              <div style={{ margin:'0 14px 10px', padding:'8px 12px', borderRadius:6, fontSize:11,
                background: testResult.ok ? 'rgba(63,185,80,0.08)' : 'rgba(239,68,68,0.08)',
                border: `1px solid ${testResult.ok ? 'rgba(63,185,80,0.2)' : 'rgba(239,68,68,0.2)'}`,
                color: testResult.ok ? 'var(--success)' : '#EF4444',
              }}>
                {testResult.ok
                  ? <>✅ 连接成功 · 发现 {testResult.tools?.length || 0} 个工具：{testResult.tools?.join(', ') || '无'}</>
                  : <>❌ 连接失败：{testResult.error}</>
                }
                <span style={{ cursor:'pointer', marginLeft:8, opacity:0.5 }} onClick={() => setTestResult(null)}>✕</span>
              </div>
            )}

            {/* Edit form */}
            {isEditing && (
              <div style={{ padding:'0 14px 14px', animation:'fadeIn 0.12s' }}>
                <div style={{ height:1, background:'var(--border-color)', marginBottom:10 }} />
                <MCPServerForm form={form} setForm={setForm} />
                <div style={{ display:'flex', gap:6, justifyContent:'flex-end', marginTop:10 }}>
                  <button onClick={() => setEditing(null)} style={btnGhost}>取消</button>
                  <button onClick={doSave} style={{...btnPrimary, background:'var(--accent)'}}>
                    <Check size={12} /> 更新
                  </button>
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// MCP server form fields (shared by add & edit)
function MCPServerForm({ form, setForm }: { form: any; setForm: (f: any) => void }) {
  const upd = (k: string, v: any) => setForm({ ...form, [k]: v });
  return (
    <div style={{ display:'flex', flexDirection:'column', gap:8 }}>
      <div style={{ display:'grid', gridTemplateColumns:'1fr 1fr', gap:8 }}>
        <Field label="名称" placeholder="my-server" value={form.name}
          onChange={v => upd('name', v)} />
        <div>
          <label style={lbl}>类型</label>
          <select style={selectStyle} value={form.type} onChange={e => upd('type', e.target.value)}>
            <option value="stdio">stdio</option>
            <option value="sse">SSE</option>
          </select>
        </div>
      </div>
      <Field label="命令" placeholder="npx" value={form.command}
        onChange={v => upd('command', v)} mono />
      <Field label="参数（空格分隔）" placeholder="-y @modelcontextprotocol/server-filesystem E:\" value={form.args}
        onChange={v => upd('args', v)} mono />
      {form.type === 'sse' && (
        <Field label="URL" placeholder="http://localhost:3000/mcp" value={form.url}
          onChange={v => upd('url', v)} />
      )}
      <label style={{ display:'flex', alignItems:'center', gap:8, cursor:'pointer', fontSize:12 }}>
        <input type="checkbox" checked={form.enabled}
          onChange={e => upd('enabled', e.target.checked)} />
        启用（启动时自动连接）
      </label>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Page: Skills
// ═══════════════════════════════════════════════════════════════

function PageSkills() {
  const store = useAppStore();
  const [content, setContent] = useState('加载中...');
  const [saved, setSaved] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      try {
        if (store.backendUrl) {
          const res = await fetch(`${store.backendUrl}/api/memory/icode`);
          if (res.ok) {
            const data = await res.json();
            setContent(data.content || data || '');
          } else {
            setContent('# iCode 记忆\n\n_暂无内容，用 # 命令在聊天中追加记忆。_');
          }
        }
      } catch {
        setContent('# iCode 记忆\n\n_无法连接到后端。_');
      }
      setLoading(false);
    };
    load();
  }, [store.backendUrl]);

  const handleSave = async () => {
    if (!store.backendUrl) return;
    try {
      await fetch(`${store.backendUrl}/api/memory/icode`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 1500);
    } catch {}
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:12 }}>
      <Section title="结构化记忆" desc="编辑 ICODE.md（项目记忆）。用 # 命令在对话中快速追加，也可在这里手动编辑。">
        <div style={{ fontSize:10, color:'var(--text-muted)', marginTop:-4 }}>
          支持 Markdown 格式。内容会作为系统提示的一部分发送给 AI。
        </div>
      </Section>
      {loading ? (
        <div style={{padding:16,textAlign:'center',color:'var(--text-muted)',fontSize:12}}>加载中...</div>
      ) : (
        <>
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            style={{
              width:'100%', minHeight:240, padding:12, borderRadius:8,
              border:'1px solid var(--border-color)',
              background:'var(--bg-primary)', color:'var(--text-primary)',
              fontSize:12, fontFamily:'var(--font-mono)',
              resize:'vertical', outline:'none', lineHeight:1.6,
            }}
          />
          <div style={{ display:'flex', justifyContent:'flex-end' }}>
            <button onClick={handleSave} style={{
              padding:'6px 16px', borderRadius:6,
              background: saved ? 'var(--success)' : 'var(--accent)',
              border:'none', color:'#fff', cursor:'pointer', fontSize:11, fontWeight:500,
            }}>
              {saved ? '✓ 已保存' : '保存记忆'}
            </button>
          </div>
        </>
      )}
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Page: Billing
// ═══════════════════════════════════════════════════════════════

function PageBilling({ store }: { store: ReturnType<typeof useAppStore.getState> }) {
  return (
    <div style={{display:'flex',flexDirection:'column',gap:16}}>
      <Section title="当前用量">
        <div style={{display:'grid', gridTemplateColumns:'1fr 1fr', gap:10}}>
          <StatCard label="输入 Token" value={store.tokenUsage.input} />
          <StatCard label="输出 Token" value={store.tokenUsage.output} />
          <StatCard label="缓存命中" value={store.tokenUsage.cacheHit} />
          <StatCard label="预估费用" value={store.tokenUsage.cost} unit="" />
        </div>
      </Section>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Page: Shortcuts
// ═══════════════════════════════════════════════════════════════

function PageShortcuts() {
  const keys = [
    ['Ctrl+K','命令面板'],
    ['Ctrl+N','新建会话'],
    ['Ctrl+L','聚焦输入'],
    ['Esc','中断生成'],
    ['↑↓','浏览历史'],
    ['Tab','补全命令'],
    ['Ctrl+C','退出'],
    ['/, @','命令/文件补全'],
  ];
  return (
    <Section title="快捷键">
      <div style={{display:'flex',flexDirection:'column',gap:2}}>
        {keys.map(([key, desc]) => (
          <div key={key} style={{display:'flex',justifyContent:'space-between',padding:'6px 10px',borderRadius:6,fontSize:12,color:'var(--text-secondary)'}}>
            <span>{desc}</span>
            <kbd style={{fontSize:10,background:'var(--bg-primary)',padding:'2px 7px',borderRadius:4,border:'1px solid var(--border-color)',color:'var(--text-muted)',fontFamily:'var(--font-mono)',}}>{key}</kbd>
          </div>
        ))}
      </div>
    </Section>
  );
}

// ═══════════════════════════════════════════════════════════════
// Reusable Components
// ═══════════════════════════════════════════════════════════════

function Section({ title, desc, children }: { title: string; desc?: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: desc ? 2 : 8 }}>
        {title}
      </div>
      {desc && <div style={{ fontSize: 10.5, color: 'var(--text-muted)', marginBottom: 8, lineHeight: 1.5 }}>{desc}</div>}
      {children}
    </div>
  );
}

function Field({ label, type, placeholder, value, onChange, mono }: { label: string; type?: string; placeholder?: string; value: string; onChange: (v:string) => void; mono?: boolean }) {
  return (
    <div>
      <label style={lbl}>{label}</label>
      <input type={type || 'text'} placeholder={placeholder} value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          width: '100%', boxSizing: 'border-box', padding: '7px 10px', fontSize: 12,
          borderRadius: 6, border: '1px solid var(--border-color)',
          background: 'var(--bg-primary)', color: 'var(--text-primary)', outline: 'none',
          fontFamily: mono ? 'var(--font-mono)' : undefined,
        }} />
    </div>
  );
}

function StatCard({ label, value, unit }: { label: string; value: string | number; unit?: string }) {
  return (
    <div style={{ padding: '10px 14px', background: 'var(--bg-primary)', borderRadius: 8, border: '1px solid var(--border-color)' }}>
      <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>{label}</div>
      <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--text-primary)', marginTop: 2 }}>
        {typeof value === 'number' ? value.toLocaleString() : value}
        {unit !== '' && <span style={{ fontSize: 11, fontWeight: 400, color: 'var(--text-muted)', marginLeft: 3 }}>{unit || ' tok'}</span>}
      </div>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
// Styles
// ═══════════════════════════════════════════════════════════════

const maskStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, zIndex: 2000,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  background: 'rgba(0,0,0,0.6)', backdropFilter: 'blur(6px)',
  animation: 'fadeIn 0.12s ease',
};

const modalStyle: React.CSSProperties = {
  width: 720, height: 520, maxWidth: '92vw', maxHeight: '88vh',
  background: 'var(--bg-secondary)', borderRadius: 16,
  border: '1px solid var(--border-color)',
  boxShadow: '0 24px 64px rgba(0,0,0,0.5)',
  display: 'flex', overflow: 'hidden',
};

const sideStyle: React.CSSProperties = {
  width: 170, flexShrink: 0,
  background: 'var(--bg-primary)', borderRight: '1px solid var(--border-color)',
  overflowY: 'auto', paddingBottom: 16,
};

const mainStyle: React.CSSProperties = {
  flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden',
};

const headStyle: React.CSSProperties = {
  display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between',
  padding: '20px 24px 16px', borderBottom: '1px solid var(--border-color)',
};

const bodyStyle: React.CSSProperties = {
  flex: 1, overflowY: 'auto', padding: '20px 24px 24px',
};

const closeBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--text-muted)',
  cursor: 'pointer', padding: 4, borderRadius: 6, flexShrink: 0,
};

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

const btnPrimary: React.CSSProperties = {
  padding: '6px 16px', borderRadius: 6, cursor: 'pointer', fontSize: 11,
  border: 'none', color: '#fff', fontWeight: 600,
  display: 'flex', alignItems: 'center', gap: 5,
};

export default SettingsPage;
