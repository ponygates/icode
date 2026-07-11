import React, { useState, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useAppStore } from '../stores/appStore';
import {
  Globe, Moon, Sun, Key, Info, Check, X, Cpu, Shield,
  Palette, Wrench, RefreshCw, Plus, Trash2,
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

const SettingsPage: React.FC = () => {
  const { t, i18n } = useTranslation();
  const { language, setLanguage, models } = useAppStore();

  const [cfg, setCfg] = useState<any>(null);
  const [providers, setProviders] = useState<any[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);

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
