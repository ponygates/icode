import React, { useState, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import {
  Globe, Moon, Sun, Key, Info, Check, X, Cpu, Shield,
  Palette, Wrench, RefreshCw, Plus, Trash2,
  Server, Boxes, Bell, Keyboard, Download, PlayCircle, Database,
} from 'lucide-react';

const PROVIDER_LABELS: Record<string, string> = {
  deepseek: 'DeepSeek',
  zhipu: 'Zhipu (智谱)',
  kimi: 'Kimi (月之暗面)',
  openrouter: 'OpenRouter',
  volcengine: 'Volcengine (豆包)',
  tencent: 'Tencent (混元)',
  huawei: 'Huawei (盘古)',
  scnet: 'SCNET',
  nvidia: 'NVIDIA',
  anthropic: 'Anthropic',
};

const MODE_LABELS: Record<string, string> = {
  ask: '询问 (ask) — 每次操作都需确认',
  auto: '自动 (auto) — 安全操作自动执行',
  plan: '计划 (plan) — 先出计划再执行',
  yolo: '全速 (yolo) — 全部自动，无需确认',
};

const card: React.CSSProperties = {
  background: 'var(--bg-secondary)', borderRadius: 10,
  border: '1px solid var(--border-color)', padding: 16, marginBottom: 16,
};
const sectionTitle: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4,
  color: 'var(--text-secondary)', fontSize: 13, fontWeight: 500,
};
const hint: React.CSSProperties = {
  fontSize: 11, color: 'var(--text-muted)', marginBottom: 12, lineHeight: 1.6,
};
const inputStyle: React.CSSProperties = {
  width: '100%', boxSizing: 'border-box', padding: '7px 10px', fontSize: 13,
  borderRadius: 6, border: '1px solid var(--border-color)',
  background: 'var(--bg-primary)', color: 'var(--text-primary)', outline: 'none',
};
const saveBtn: React.CSSProperties = {
  padding: '7px 16px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
  border: 'none', background: 'var(--accent)', color: '#000', fontWeight: 500,
};

const kbdStyle: React.CSSProperties = {
  fontSize: 11, padding: '1px 6px', borderRadius: 4,
  border: '1px solid var(--border-color)', background: 'var(--bg-tertiary)',
  color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)',
};

const SettingsPage: React.FC = () => {
  const { t, i18n } = useTranslation();
  const { language, setLanguage, models, sessions, clearMessages, deleteSession, activeSessionId } = useAppStore();

  const [cfg, setCfg] = useState<any>(null);
  const [providers, setProviders] = useState<any[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);

  // MCP servers (Reasonix-style tool integration)
  const [mcpServers, setMcpServers] = useState<any[]>([]);
  const [mcpTools, setMcpTools] = useState<any[]>([]);
  const [mcpTestResult, setMcpTestResult] = useState<string | null>(null);
  const [mcpBusy, setMcpBusy] = useState(false);
  const [newMCP, setNewMCP] = useState({ name: '', type: 'stdio', command: '', args: '', url: '', env: '' });

  // Custom models / presets
  const [customModels, setCustomModels] = useState<any[]>([]);
  const [newModel, setNewModel] = useState({ provider: '', modelId: '', name: '', ctx: '', maxOut: '', free: false });

  // Notifications (client-side preference)
  const [notify, setNotify] = useState<boolean>(() => {
    try { return localStorage.getItem('icode.notify') === '1'; } catch { return false; }
  });

  // Data management

  // Provider key editing
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [bases, setBases] = useState<Record<string, string>>({});
  const [pStatus, setPStatus] = useState<Record<string, string>>({});

  // Custom provider add
  const [newName, setNewName] = useState('');
  const [newKey, setNewKey] = useState('');
  const [newBase, setNewBase] = useState('');

  const loadAll = useCallback(async () => {
    try {
      const c = await window.icode?.getConfig?.();
      if (c) setCfg(c);
      const r = await window.icode?.listKeys?.();
      if (r?.providers) {
        setProviders(r.providers);
        const b: Record<string, string> = {};
        r.providers.forEach((p: any) => { b[p.name] = p.api_base || ''; });
        setBases(b);
      }
      const mcp = await window.icode?.listMCP?.();
      if (Array.isArray(mcp)) setMcpServers(mcp);
      const tools = await window.icode?.listMCPTools?.();
      if (Array.isArray(tools)) setMcpTools(tools);
      const cm = await window.icode?.listCustomModels?.();
      if (cm?.models) setCustomModels(cm.models);
    } catch { /* ignore */ }
    setLoaded(true);
  }, []);

  useEffect(() => { loadAll(); }, [loadAll]);

  // Apply theme globally when it changes.
  useEffect(() => {
    if (!cfg) return;
    const theme = cfg.tui?.theme || 'dark';
    const root = document.documentElement;
    if (theme === 'auto') {
      const mq = window.matchMedia('(prefers-color-scheme: light)');
      root.setAttribute('data-theme', mq.matches ? 'light' : 'dark');
    } else {
      root.setAttribute('data-theme', theme);
    }
  }, [cfg?.tui?.theme]);

  const flash = (type: 'ok' | 'err', text: string) => {
    setMsg({ type, text });
    setTimeout(() => setMsg(null), 2500);
  };

  const patchCfg = (mut: (c: any) => void) => {
    setCfg((prev: any) => {
      const next = prev ? JSON.parse(JSON.stringify(prev)) : {};
      mut(next);
      return next;
    });
  };

  const saveCfg = async (label: string) => {
    try {
      const res = await window.icode?.setConfig?.(cfg);
      if (res?.ok) flash('ok', `${label} 已保存并立即生效`);
      else flash('err', `${label} 保存失败：${res?.error || '未知错误'}`);
    } catch (e: any) {
      flash('err', `${label} 保存失败：${e?.message || String(e)}`);
    }
  };

  const handleLanguageChange = (lang: string) => {
    setLanguage(lang);
    i18n.changeLanguage(lang);
    patchCfg((c) => { c.language = lang; });
    saveCfg('语言');
  };

  const handleSaveKey = async (name: string) => {
    setPStatus((s) => ({ ...s, [name]: 'saving' }));
    try {
      const res = await window.icode.setApiKey(name, keys[name] || '', bases[name] || '');
      if (res?.ok) {
        setPStatus((s) => ({ ...s, [name]: 'saved' }));
        flash('ok', `${PROVIDER_LABELS[name] || name} 密钥已保存`);
        const r2 = await window.icode.listKeys();
        if (r2?.providers) {
          setProviders(r2.providers);
          const b: Record<string, string> = {};
          r2.providers.forEach((p: any) => { b[p.name] = p.api_base || ''; });
          setBases(b);
        }
      } else {
        setPStatus((s) => ({ ...s, [name]: 'error: ' + (res?.error || '失败') }));
      }
    } catch (e: any) {
      setPStatus((s) => ({ ...s, [name]: 'error: ' + (e?.message || String(e)) }));
    }
    setTimeout(() => setPStatus((s) => ({ ...s, [name]: '' })), 2500);
  };

  const handleAddProvider = async () => {
    const name = newName.trim();
    if (!name) return;
    try {
      const res = await window.icode.setApiKey(name, newKey, newBase);
      if (res?.ok) {
        flash('ok', `已添加/更新提供商 ${name}`);
        setNewName(''); setNewKey(''); setNewBase('');
        const r2 = await window.icode.listKeys();
        if (r2?.providers) {
          setProviders(r2.providers);
          const b: Record<string, string> = {};
          r2.providers.forEach((p: any) => { b[p.name] = p.api_base || ''; });
          setBases(b);
        }
      } else {
        flash('err', `添加失败：${res?.error || '未知错误'}`);
      }
    } catch (e: any) {
      flash('err', `添加失败：${e?.message || String(e)}`);
    }
  };

  const handleSetMode = async (mode: string) => {
    patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.mode = mode; });
    try {
      await window.icode?.setPermissionMode?.(mode);
      await window.icode?.setConfig?.(cfg);
      flash('ok', `权限模式已设为 ${mode}`);
    } catch (e: any) {
      flash('err', `权限模式保存失败：${e?.message || String(e)}`);
    }
  };

  // ── MCP server management ──
  const refreshMCP = useCallback(async () => {
    try {
      const mcp = await window.icode?.listMCP?.();
      if (Array.isArray(mcp)) setMcpServers(mcp);
      const tools = await window.icode?.listMCPTools?.();
      if (Array.isArray(tools)) setMcpTools(tools);
    } catch { /* ignore */ }
  }, []);

  const handleAddMCP = async () => {
    if (!newMCP.name.trim() || mcpBusy) return;
    setMcpBusy(true);
    setMcpTestResult(null);
    const cfgObj: any = {
      name: newMCP.name.trim(),
      type: newMCP.type,
      enabled: true,
    };
    if (newMCP.type === 'stdio') {
      cfgObj.command = newMCP.command;
      cfgObj.args = newMCP.args.split(',').map((s: string) => s.trim()).filter(Boolean);
      if (newMCP.env.trim()) {
        const env: Record<string, string> = {};
        newMCP.env.split(',').forEach((pair: string) => {
          const [k, v] = pair.split('=');
          if (k && v != null) env[k.trim()] = v.trim();
        });
        cfgObj.env = env;
      }
    } else {
      cfgObj.url = newMCP.url;
    }
    try {
      const res = await window.icode?.addMCP?.(cfgObj);
      if (res?.ok) {
        flash('ok', `MCP 服务器 ${cfgObj.name} 已添加`);
        setNewMCP({ name: '', type: 'stdio', command: '', args: '', url: '', env: '' });
        await refreshMCP();
      } else {
        flash('err', `添加失败：${res?.error || '未知错误'}`);
      }
    } catch (e: any) {
      flash('err', `添加失败：${e?.message || String(e)}`);
    }
    setMcpBusy(false);
  };

  const handleRemoveMCP = async (name: string) => {
    try {
      const res = await window.icode?.removeMCP?.(name);
      if (res?.ok) { flash('ok', `已移除 ${name}`); await refreshMCP(); }
      else flash('err', `移除失败：${res?.error || '未知错误'}`);
    } catch (e: any) { flash('err', `移除失败：${e?.message || String(e)}`); }
  };

  const handleTestMCP = async () => {
    if (!newMCP.name.trim()) return;
    setMcpBusy(true);
    setMcpTestResult('测试中…');
    const cfgObj: any = { name: newMCP.name.trim(), type: newMCP.type, enabled: true };
    if (newMCP.type === 'stdio') {
      cfgObj.command = newMCP.command;
      cfgObj.args = newMCP.args.split(',').map((s: string) => s.trim()).filter(Boolean);
    } else {
      cfgObj.url = newMCP.url;
    }
    try {
      const res = await window.icode?.testMCP?.(cfgObj);
      if (res?.ok) setMcpTestResult(`✓ 连接成功，发现 ${res.tools?.length || 0} 个工具`);
      else setMcpTestResult(`✗ 连接失败：${res?.error || '未知错误'}`);
    } catch (e: any) { setMcpTestResult(`✗ ${e?.message || String(e)}`); }
    setMcpBusy(false);
  };

  // ── Custom models / presets ──
  const handleAddCustomModel = async () => {
    if (!newModel.provider.trim() || !newModel.modelId.trim()) {
      flash('err', '请填写提供商与模型 ID');
      return;
    }
    try {
      const res = await window.icode?.addCustomModel?.({
        provider: newModel.provider.trim(),
        model_id: newModel.modelId.trim(),
        name: newModel.name.trim() || newModel.modelId.trim(),
        context_window: parseInt(newModel.ctx) || 128000,
        max_output: parseInt(newModel.maxOut) || 4096,
        free_tier: newModel.free,
        custom: true,
      });
      if (res?.ok) {
        flash('ok', '自定义模型已保存');
        setNewModel({ provider: '', modelId: '', name: '', ctx: '', maxOut: '', free: false });
        const cm = await window.icode?.listCustomModels?.();
        if (cm?.models) setCustomModels(cm.models);
      } else flash('err', `保存失败：${res?.error || '未知错误'}`);
    } catch (e: any) { flash('err', `保存失败：${e?.message || String(e)}`); }
  };

  const handleDeleteCustomModel = async (id: string) => {
    try {
      const res = await window.icode?.deleteCustomModel?.(id);
      if (res?.ok) {
        flash('ok', '已删除自定义模型');
        setCustomModels((prev: any[]) => prev.filter((m) => m.id !== id));
      } else flash('err', `删除失败：${res?.error || '未知错误'}`);
    } catch (e: any) { flash('err', `删除失败：${e?.message || String(e)}`); }
  };

  // ── Notifications ──
  const toggleNotify = (v: boolean) => {
    setNotify(v);
    try { localStorage.setItem('icode.notify', v ? '1' : '0'); } catch { /* ignore */ }
    if (v && 'Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission().catch(() => {});
    }
    flash('ok', v ? '已开启桌面通知' : '已关闭桌面通知');
  };

  // ── Data management ──
  const exportSession = (id: string) => {
    const s = sessions.find((x) => x.id === id);
    if (!s) return;
    const md = `# ${s.title}\n\n` + s.messages
      .map((m: any) => `### ${m.role === 'user' ? '用户' : 'iCode'}\n\n${m.content}\n`)
      .join('\n');
    const blob = new Blob([md], { type: 'text/markdown' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `${s.title || 'session'}.md`;
    a.click();
    URL.revokeObjectURL(a.href);
    flash('ok', '已导出会话');
  };

  const clearAllSessions = () => {
    sessions.forEach((s: any) => clearMessages(s.id));
    flash('ok', '已清空所有会话消息');
  };

  const modelOptions = models.length ? models : (cfg?.models || []);
  const d = cfg?.defaults || {};
  const tui = cfg?.tui || {};
  const tools = cfg?.tools || {};
  const upd = cfg?.update || {};

  const languages = [
    { code: 'zh-CN', label: t('lang.zhCN') },
    { code: 'zh-TW', label: t('lang.zhTW') },
    { code: 'en', label: t('lang.en') },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh' }}>
      {/* Header */}
      <div style={{
        padding: '12px 20px', borderBottom: '1px solid var(--border-color)',
        background: 'var(--bg-secondary)', display: 'flex',
        alignItems: 'center', justifyContent: 'space-between',
      }}>
        <h2 style={{ fontSize: 15, fontWeight: 500, color: 'var(--text-primary)' }}>
          {t('settings.title')}
        </h2>
        {msg && (
          <span style={{
            fontSize: 12, color: msg.type === 'ok' ? 'var(--success)' : 'var(--error)',
          }}>
            {msg.text}
          </span>
        )}
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
        {!loaded && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>加载中…</div>}

        {/* ── 模型与预设 ── */}
        <div style={card}>
          <div style={sectionTitle}><Cpu size={16} /><span>模型与预设</span></div>
          <div style={hint}>选择默认模型、调整生成参数。修改后点击保存立即生效。</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              默认模型
              <select
                value={d.model || ''}
                onChange={(e) => patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.model = e.target.value; })}
                style={{ ...inputStyle, marginTop: 4 }}
              >
                <option value="">（未选择）</option>
                {modelOptions.map((m: any) => (
                  <option key={m.id} value={m.id}>{m.name} · {m.provider}</option>
                ))}
              </select>
            </label>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              默认提供商
              <input
                value={d.provider || ''}
                onChange={(e) => patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.provider = e.target.value; })}
                style={{ ...inputStyle, marginTop: 4 }}
                placeholder="如 deepseek"
              />
            </label>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              温度 (Temperature): {d.temperature ?? 0.7}
              <input
                type="range" min={0} max={1} step={0.1}
                value={d.temperature ?? 0.7}
                onChange={(e) => patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.temperature = parseFloat(e.target.value); })}
                style={{ width: '100%', marginTop: 4 }}
              />
            </label>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              最大输出 Token
              <input
                type="number" min={256} max={128000} step={256}
                value={d.max_tokens || 4096}
                onChange={(e) => patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.max_tokens = parseInt(e.target.value) || 4096; })}
                style={{ ...inputStyle, marginTop: 4 }}
              />
            </label>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 10, fontSize: 12, color: 'var(--text-secondary)' }}>
            <input
              type="checkbox" checked={!!d.cache}
              onChange={(e) => patchCfg((c) => { c.defaults = c.defaults || {}; c.defaults.cache = e.target.checked; })}
            />
            启用上下文缓存（Cache）以节省 Token（参考 Reasonix 94% 缓存命中）
          </label>
          <div style={{ marginTop: 12 }}>
            <button style={saveBtn} onClick={() => saveCfg('模型与预设')}>保存</button>
          </div>
        </div>

        {/* ── API 提供商 ── */}
        <div style={card}>
          <div style={sectionTitle}><Key size={16} /><span>API 提供商</span></div>
          <div style={hint}>填写各服务商的 API Key，保存后立即生效（无需重启）。Key 仅保存在本地配置文件中，不会上传。</div>

          {loaded && providers.length === 0 && (
            <div style={{ padding: '10px 12px', fontSize: 11, color: 'var(--text-muted)', background: 'var(--bg-primary)', borderRadius: 8, border: '1px dashed var(--border-color)' }}>
              无法连接后端，请确认 iCode 后端服务已启动。也可在终端运行 <code style={{ color: 'var(--accent)' }}>icode auth set --provider &lt;name&gt; --key &lt;api-key&gt;</code>。
            </div>
          )}

          {providers.map((p) => (
            <div key={p.name} style={{
              padding: '10px 12px', marginBottom: 8, background: 'var(--bg-primary)',
              borderRadius: 8, border: '1px solid var(--border-color)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                <div style={{ fontSize: 13, color: 'var(--text-primary)', fontWeight: 500 }}>
                  {PROVIDER_LABELS[p.name] || p.name}
                </div>
                <span style={{
                  fontSize: 11, color: p.key_set ? 'var(--success)' : 'var(--text-muted)',
                  display: 'flex', alignItems: 'center', gap: 4,
                }}>
                  {p.key_set ? <><Check size={12} /> 已配置</> : <><X size={12} /> 未配置</>}
                </span>
              </div>
              <input
                placeholder="API Base（可选，留空用默认值）"
                value={bases[p.name] || ''}
                onChange={(e) => setBases((s) => ({ ...s, [p.name]: e.target.value }))}
                style={{ ...inputStyle, marginBottom: 6 }}
              />
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  type="password"
                  placeholder={p.key_set ? '更新 API Key（留空保留）' : '输入 API Key'}
                  value={keys[p.name] || ''}
                  onChange={(e) => setKeys((s) => ({ ...s, [p.name]: e.target.value }))}
                  style={{ ...inputStyle, flex: 1 }}
                />
                <button
                  onClick={() => handleSaveKey(p.name)}
                  disabled={pStatus[p.name] === 'saving'}
                  style={{ ...saveBtn, opacity: pStatus[p.name] === 'saving' ? 0.6 : 1 }}
                >
                  {pStatus[p.name] === 'saving' ? '保存中…' : '保存'}
                </button>
              </div>
              {pStatus[p.name] === 'saved' && <div style={{ marginTop: 6, fontSize: 11, color: 'var(--success)' }}>✓ 已保存并立即生效</div>}
              {pStatus[p.name]?.startsWith('error') && <div style={{ marginTop: 6, fontSize: 11, color: 'var(--error)' }}>{pStatus[p.name]}</div>}
            </div>
          ))}

          {/* Add custom provider */}
          <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px dashed var(--border-color)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--text-secondary)', marginBottom: 8 }}>
              <Plus size={14} /> 添加自定义提供商
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 2fr 2fr auto', gap: 8 }}>
              <input placeholder="名称" value={newName} onChange={(e) => setNewName(e.target.value)} style={inputStyle} />
              <input placeholder="Key" type="password" value={newKey} onChange={(e) => setNewKey(e.target.value)} style={inputStyle} />
              <input placeholder="Base URL" value={newBase} onChange={(e) => setNewBase(e.target.value)} style={inputStyle} />
              <button style={saveBtn} onClick={handleAddProvider}><Plus size={14} /></button>
            </div>
          </div>
        </div>

        {/* ── 权限模式 ── */}
        <div style={card}>
          <div style={sectionTitle}><Shield size={16} /><span>权限模式</span></div>
          <div style={hint}>控制 AI 执行文件/命令/工具时的确认策略（对标 Reasonix review/auto/yolo）。</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
            {Object.entries(MODE_LABELS).map(([mode, label]) => (
              <button
                key={mode}
                onClick={() => handleSetMode(mode)}
                style={{
                  textAlign: 'left', padding: '10px 12px', borderRadius: 8, fontSize: 12,
                  background: (d.mode || 'ask') === mode ? 'var(--bg-tertiary)' : 'var(--bg-primary)',
                  border: (d.mode || 'ask') === mode ? '1px solid var(--accent)' : '1px solid var(--border-color)',
                  color: (d.mode || 'ask') === mode ? 'var(--accent)' : 'var(--text-secondary)', cursor: 'pointer',
                }}
              >
                {label}
              </button>
            ))}
          </div>
        </div>

        {/* ── 外观 ── */}
        <div style={card}>
          <div style={sectionTitle}><Palette size={16} /><span>外观</span></div>
          <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
            {[
              { key: 'dark', icon: Moon, label: '暗色' },
              { key: 'light', icon: Sun, label: '亮色' },
              { key: 'auto', icon: Sun, label: '跟随系统' },
            ].map((th) => (
              <button
                key={th.key}
                onClick={() => patchCfg((c) => { c.tui = c.tui || {}; c.tui.theme = th.key; })}
                style={{
                  padding: '8px 16px', borderRadius: 8,
                  background: (tui.theme || 'dark') === th.key ? 'var(--bg-tertiary)' : 'var(--bg-primary)',
                  border: (tui.theme || 'dark') === th.key ? '1px solid var(--accent)' : '1px solid var(--border-color)',
                  color: (tui.theme || 'dark') === th.key ? 'var(--accent)' : 'var(--text-secondary)',
                  cursor: 'pointer', fontSize: 13, display: 'flex', alignItems: 'center', gap: 6,
                }}
              >
                <th.icon size={14} /> {th.label}
              </button>
            ))}
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, color: 'var(--text-secondary)', marginBottom: 8 }}>
            <input
              type="checkbox" checked={!!tui.syntax_highlight}
              onChange={(e) => patchCfg((c) => { c.tui = c.tui || {}; c.tui.syntax_highlight = e.target.checked; })}
            />
            代码语法高亮
          </label>
          <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
            Diff 显示模式
            <select
              value={tui.diff_mode || 'unified'}
              onChange={(e) => patchCfg((c) => { c.tui = c.tui || {}; c.tui.diff_mode = e.target.value; })}
              style={{ ...inputStyle, marginTop: 4 }}
            >
              <option value="unified">Unified（统一）</option>
              <option value="split">Split（并排）</option>
              <option value="side-by-side">Side-by-Side（对照）</option>
            </select>
          </label>
          <div style={{ marginTop: 12 }}>
            <button style={saveBtn} onClick={() => saveCfg('外观')}>保存</button>
          </div>
        </div>

        {/* ── 工具与安全 ── */}
        <div style={card}>
          <div style={sectionTitle}><Wrench size={16} /><span>工具与安全</span></div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              Bash 超时（秒）
              <input
                type="number" min={0} step={10}
                value={tools.bash_timeout_sec ?? 120}
                onChange={(e) => patchCfg((c) => { c.tools = c.tools || {}; c.tools.bash_timeout_sec = parseInt(e.target.value) || 0; })}
                style={{ ...inputStyle, marginTop: 4 }}
              />
            </label>
            <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              禁用命令（逗号分隔）
              <input
                value={(tools.denied_commands || []).join(', ')}
                onChange={(e) => patchCfg((c) => {
                  c.tools = c.tools || {};
                  c.tools.denied_commands = e.target.value.split(',').map((s: string) => s.trim()).filter(Boolean);
                })}
                style={{ ...inputStyle, marginTop: 4 }}
              />
            </label>
          </div>
          <label style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 12, display: 'block' }}>
            允许路径（每行一个）
            <textarea
              value={(tools.allowed_paths || []).join('\n')}
              onChange={(e) => patchCfg((c) => {
                c.tools = c.tools || {};
                c.tools.allowed_paths = e.target.value.split('\n').map((s: string) => s.trim()).filter(Boolean);
              })}
              rows={3}
              style={{ ...inputStyle, marginTop: 4, resize: 'vertical', fontFamily: 'var(--font-mono)' }}
            />
          </label>
          <div style={{ marginTop: 12 }}>
            <button style={saveBtn} onClick={() => saveCfg('工具与安全')}>保存</button>
          </div>
        </div>

        {/* ── 语言 ── */}
        <div style={card}>
          <div style={sectionTitle}><Globe size={16} /><span>{t('settings.language')}</span></div>
          <div style={{ display: 'flex', gap: 8 }}>
            {languages.map((l) => (
              <button
                key={l.code}
                onClick={() => handleLanguageChange(l.code)}
                style={{
                  padding: '8px 16px', borderRadius: 8,
                  background: language === l.code ? 'var(--bg-tertiary)' : 'var(--bg-primary)',
                  border: language === l.code ? '1px solid var(--accent)' : '1px solid var(--border-color)',
                  color: language === l.code ? 'var(--accent)' : 'var(--text-secondary)',
                  cursor: 'pointer', fontSize: 13, fontWeight: language === l.code ? 500 : 400,
                }}
              >
                {l.label}
              </button>
            ))}
          </div>
        </div>

        {/* ── 更新 ── */}
        <div style={card}>
          <div style={sectionTitle}><RefreshCw size={16} /><span>更新</span></div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, color: 'var(--text-secondary)', marginBottom: 8 }}>
            <input
              type="checkbox" checked={!!upd.auto_update}
              onChange={(e) => patchCfg((c) => { c.update = c.update || {}; c.update.auto_update = e.target.checked; })}
            />
            自动检查并更新
          </label>
          <label style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
            更新频道
            <select
              value={upd.channel || 'stable'}
              onChange={(e) => patchCfg((c) => { c.update = c.update || {}; c.update.channel = e.target.value; })}
              style={{ ...inputStyle, marginTop: 4 }}
            >
              <option value="stable">Stable（稳定版）</option>
              <option value="beta">Beta（测试版）</option>
              <option value="nightly">Nightly（每夜版）</option>
            </select>
          </label>
          <div style={{ marginTop: 12 }}>
            <button style={saveBtn} onClick={() => saveCfg('更新')}>保存</button>
          </div>
        </div>

        {/* ── MCP 服务器 ── */}
        <div style={card}>
          <div style={sectionTitle}><Server size={16} /><span>MCP 服务器</span></div>
          <div style={hint}>通过 Model Context Protocol 接入外部工具（文件系统、数据库、API 等）。对标 Reasonix 的 MCP 集成。</div>

          {loaded && mcpServers.length === 0 && (
            <div style={{ padding: '10px 12px', fontSize: 11, color: 'var(--text-muted)', background: 'var(--bg-primary)', borderRadius: 8, border: '1px dashed var(--border-color)' }}>
              暂无 MCP 服务器。在下方添加一个（stdio 命令或 sse/http 地址）。
            </div>
          )}

          {mcpServers.map((s) => (
            <div key={s.name} style={{
              padding: '10px 12px', marginBottom: 8, background: 'var(--bg-primary)',
              borderRadius: 8, border: '1px solid var(--border-color)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
                <div style={{ fontSize: 13, color: 'var(--text-primary)', fontWeight: 500 }}>{s.name}</div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11 }}>
                  <span style={{ color: s.connected ? 'var(--success)' : 'var(--text-muted)' }}>
                    {s.connected ? <><Check size={12} /> 已连接</> : <><X size={12} /> 未连接</>}
                  </span>
                  <span style={{ color: 'var(--text-muted)' }}>{s.tools ?? 0} 工具</span>
                  <button onClick={() => handleRemoveMCP(s.name)} style={{ background: 'none', border: 'none', color: 'var(--error)', cursor: 'pointer', padding: 2 }}>
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                {s.type} · {s.command || s.url || ''}
              </div>
            </div>
          ))}

          {/* Add MCP form */}
          <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px dashed var(--border-color)' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
              <input placeholder="名称" value={newMCP.name} onChange={(e) => setNewMCP({ ...newMCP, name: e.target.value })} style={inputStyle} />
              <select value={newMCP.type} onChange={(e) => setNewMCP({ ...newMCP, type: e.target.value })} style={inputStyle}>
                <option value="stdio">stdio（命令）</option>
                <option value="sse">sse（URL）</option>
                <option value="http">http（URL）</option>
              </select>
            </div>
            {newMCP.type === 'stdio' ? (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
                <input placeholder="命令 (如 npx)" value={newMCP.command} onChange={(e) => setNewMCP({ ...newMCP, command: e.target.value })} style={inputStyle} />
                <input placeholder="参数 (逗号分隔)" value={newMCP.args} onChange={(e) => setNewMCP({ ...newMCP, args: e.target.value })} style={inputStyle} />
              </div>
            ) : (
              <input placeholder="URL" value={newMCP.url} onChange={(e) => setNewMCP({ ...newMCP, url: e.target.value })} style={{ ...inputStyle, marginBottom: 8 }} />
            )}
            <input placeholder="环境变量 (可选, KEY=VALUE 逗号分隔)" value={newMCP.env} onChange={(e) => setNewMCP({ ...newMCP, env: e.target.value })} style={{ ...inputStyle, marginBottom: 8 }} />
            <div style={{ display: 'flex', gap: 8 }}>
              <button style={saveBtn} onClick={handleAddMCP} disabled={mcpBusy}><Plus size={14} /> 添加</button>
              <button style={{ ...saveBtn, background: 'var(--bg-tertiary)', color: 'var(--text-secondary)' }} onClick={handleTestMCP} disabled={mcpBusy}><PlayCircle size={14} /> 测试连接</button>
            </div>
            {mcpTestResult && <div style={{ marginTop: 6, fontSize: 11, color: 'var(--text-secondary)' }}>{mcpTestResult}</div>}
          </div>
        </div>

        {/* ── 自定义模型 / 预设 ── */}
        <div style={card}>
          <div style={sectionTitle}><Boxes size={16} /><span>自定义模型 / 预设</span></div>
          <div style={hint}>添加未在内置列表中提供的模型（如私有部署、兼容 OpenAI 的端点）。</div>

          {customModels.map((m) => (
            <div key={m.id} style={{
              padding: '10px 12px', marginBottom: 8, background: 'var(--bg-primary)',
              borderRadius: 8, border: '1px solid var(--border-color)',
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            }}>
              <div>
                <div style={{ fontSize: 13, color: 'var(--text-primary)', fontWeight: 500 }}>{m.name}</div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{m.provider} / {m.model_id}{m.free_tier ? ' · 免费' : ''}</div>
              </div>
              <button onClick={() => handleDeleteCustomModel(m.id)} style={{ background: 'none', border: 'none', color: 'var(--error)', cursor: 'pointer', padding: 2 }}>
                <Trash2 size={13} />
              </button>
            </div>
          ))}

          <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px dashed var(--border-color)', display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
            <input placeholder="提供商 (如 deepseek)" value={newModel.provider} onChange={(e) => setNewModel({ ...newModel, provider: e.target.value })} style={inputStyle} />
            <input placeholder="模型 ID (如 my-model)" value={newModel.modelId} onChange={(e) => setNewModel({ ...newModel, modelId: e.target.value })} style={inputStyle} />
            <input placeholder="显示名 (可选)" value={newModel.name} onChange={(e) => setNewModel({ ...newModel, name: e.target.value })} style={inputStyle} />
            <input placeholder="上下文窗口 (如 128000)" value={newModel.ctx} onChange={(e) => setNewModel({ ...newModel, ctx: e.target.value })} style={inputStyle} />
            <input placeholder="最大输出 (如 4096)" value={newModel.maxOut} onChange={(e) => setNewModel({ ...newModel, maxOut: e.target.value })} style={inputStyle} />
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--text-secondary)' }}>
              <input type="checkbox" checked={newModel.free} onChange={(e) => setNewModel({ ...newModel, free: e.target.checked })} /> 免费层级
            </label>
            <button style={saveBtn} onClick={handleAddCustomModel}><Plus size={14} /> 添加模型</button>
          </div>
        </div>

        {/* ── 通知 ── */}
        <div style={card}>
          <div style={sectionTitle}><Bell size={16} /><span>通知</span></div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--text-secondary)' }}>
            <input type="checkbox" checked={notify} onChange={(e) => toggleNotify(e.target.checked)} />
            任务完成时显示桌面通知（回复结束 / 需要权限确认时）
          </label>
        </div>

        {/* ── 快捷键 ── */}
        <div style={card}>
          <div style={sectionTitle}><Keyboard size={16} /><span>快捷键</span></div>
          <div style={{ fontSize: 12, color: 'var(--text-secondary)', lineHeight: 2 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>发送消息</span><kbd style={kbdStyle}>Enter</kbd></div>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>换行</span><kbd style={kbdStyle}>Shift + Enter</kbd></div>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>停止生成</span><kbd style={kbdStyle}>Esc</kbd></div>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>新建会话</span><kbd style={kbdStyle}>Ctrl/⌘ + K</kbd></div>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>打开设置</span><kbd style={kbdStyle}>Ctrl/⌘ + ,</kbd></div>
          </div>
        </div>

        {/* ── 数据管理 ── */}
        <div style={card}>
          <div style={sectionTitle}><Database size={16} /><span>数据管理</span></div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <button style={saveBtn} onClick={() => activeSessionId && exportSession(activeSessionId)} disabled={!activeSessionId}>
              <Download size={14} /> 导出当前会话 (Markdown)
            </button>
            <button style={{ ...saveBtn, background: 'var(--bg-tertiary)', color: 'var(--error)' }} onClick={clearAllSessions}>
              <Trash2 size={14} /> 清空所有会话
            </button>
          </div>
        </div>

        {/* ── 关于 ── */}
        <div style={card}>
          <div style={sectionTitle}><Info size={16} /><span>{t('settings.about')}</span></div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', lineHeight: 1.8 }}>
            <div>iCode v0.1.0-dev</div>
            <div>Multi-Model AI Coding Agent</div>
            <div>Apache-2.0 License</div>
            <div>
              <a href="https://github.com/ponygates/icode" target="_blank" rel="noopener noreferrer" style={{ color: 'var(--accent)' }}>
                github.com/ponygates/icode
              </a>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default SettingsPage;
