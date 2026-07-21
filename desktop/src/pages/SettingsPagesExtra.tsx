import React, { useState, useEffect } from 'react';
import { Trash2, Shield, Wrench, FileText, Terminal, Search, GitBranch, HardDrive } from 'lucide-react';
import { useAppStore } from '../stores/appStore';

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

export function PageTools({ store }: { store: any }) {
  const [bashTimeout, setBashTimeout] = useState(30);
  const [rules, setRules] = useState<Record<string,string>>({});
  const backendUrl = useAppStore((s) => s.backendUrl);

  const saveConfig = async (patch: any) => {
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
    { name:'read_file',   icon:FileText,    label:'读文件',  desc:'读取文件内容', risk:'safe' },
    { name:'write_file',  icon:FileText,    label:'写文件',  desc:'创建/覆盖文件', risk:'mutate' },
    { name:'edit',        icon:Wrench,      label:'编辑',    desc:'就地修改文件', risk:'mutate' },
    { name:'bash',        icon:Terminal,    label:'终端命令',desc:'执行Shell命令', risk:'danger' },
    { name:'grep',        icon:Search,      label:'代码搜索',desc:'搜索代码内容', risk:'safe' },
    { name:'glob',        icon:Search,      label:'文件匹配',desc:'文件名匹配', risk:'safe' },
    { name:'ls',          icon:Search,      label:'列目录',  desc:'列出目录内容', risk:'safe' },
    { name:'fetch',       icon:Search,      label:'网络请求',desc:'HTTP请求', risk:'safe' },
    { name:'disk_usage',  icon:HardDrive,   label:'磁盘查看',desc:'查看磁盘空间', risk:'safe' },
    { name:'disk_cleanup',icon:HardDrive,   label:'磁盘清理',desc:'清理临时文件', risk:'danger' },
    { name:'git_diff',    icon:GitBranch,   label:'Git差异', desc:'查看代码差异', risk:'safe' },
    { name:'git_commit',  icon:GitBranch,   label:'Git提交', desc:'创建提交', risk:'mutate' },
    { name:'git_status',  icon:GitBranch,   label:'Git状态', desc:'仓库状态查看', risk:'safe' },
    { name:'task',        icon:Wrench,      label:'子代理',  desc:'委派子任务', risk:'mutate' },
    { name:'web_search',  icon:Search,      label:'网页搜索',desc:'网络搜索', risk:'safe' },
    { name:'search_replace',icon:Wrench,    label:'搜索替换',desc:'批量搜索替换', risk:'mutate' },
  ];

  const getRule = (toolName: string): string => {
    return rules[toolName] || '';
  };

  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title="工具权限规则">
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 12, lineHeight:1.6 }}>
          为每个工具设置权限规则。规则覆盖所有会话：
          <b style={{color:'var(--success)'}}>始终允许</b> / 
          <b style={{color:'var(--warning)'}}>每次询问</b> / 
          <b style={{color:'var(--error)'}}>始终禁止</b>
        </div>
        <div style={{ display:'flex', flexWrap:'wrap', gap:6 }}>
          {TOOLS.map(t => {
            const Icon = t.icon;
            const rule = getRule(t.name);
            const riskColor = t.risk === 'safe' ? 'var(--success)' : t.risk === 'danger' ? 'var(--error)' : 'var(--warning)';
            const borderColor = rule === 'allow' ? 'var(--success)' : rule === 'deny' ? 'var(--error)' : 'var(--border-color)';
            const bgColor = rule === 'allow' ? 'rgba(63,185,80,0.06)' : rule === 'deny' ? 'rgba(248,81,73,0.06)' : 'var(--bg-primary)';
            return (
              <div key={t.name} style={{
                padding: '10px 12px', borderRadius: 8, border: `1px solid ${borderColor}`,
                background: bgColor, minWidth: 180, flex: 1,
                transition: 'all 0.12s',
              }}>
                <div style={{ display:'flex', alignItems:'center', gap:6, marginBottom:6 }}>
                  <Icon size={14} color={riskColor} />
                  <span style={{ fontSize:12, fontWeight:500, color:'var(--text-primary)' }}>{t.label}</span>
                  <span style={{ fontSize:9, color:'var(--text-muted)', marginLeft:'auto' }}>{t.name}</span>
                </div>
                <div style={{ fontSize:10, color:'var(--text-muted)', marginBottom:8 }}>{t.desc}</div>
                <div style={{ display:'flex', gap:3 }}>
                  {[
                    { v:'',    label:'默认' },
                    { v:'allow', label:'始终允许' },
                    { v:'deny',  label:'始终禁止' },
                  ].map(opt => (
                    <button key={opt.v} onClick={() => setRule(t.name, opt.v)} style={{
                      flex:1, padding:'3px 6px', borderRadius:4, cursor:'pointer', fontSize:9,
                      background: rule === opt.v ? 'var(--accent-soft)' : 'transparent',
                      border: rule === opt.v ? '1px solid var(--accent)' : '1px solid transparent',
                      color: rule === opt.v ? 'var(--accent)' : 'var(--text-muted)',
                      fontWeight: rule === opt.v ? 600 : 400,
                      transition: 'all 0.1s',
                    }}>{opt.label}</button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </Section>
      <Section title="Bash 执行">
        <label style={lbl}>Bash 超时（秒）</label>
        <input type="number" min={5} max={300} value={bashTimeout}
          onChange={e => { setBashTimeout(Number(e.target.value)); saveConfig({ tools: { bash_timeout: Number(e.target.value) } }); }}
          style={{ ...selectStyle, width: 120 }} />
      </Section>
      <Section title="安全沙箱">
        <div style={{ fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.6 }}>
          权限模式控制 AI 可执行的操作范围。
          <div style={{ marginTop: 8, padding: 12, background: 'var(--bg-primary)', borderRadius: 8, fontSize: 11 }}>
            • 禁止命令：rm -rf, sudo, chmod, curl|sh<br/>
            • 可编辑范围：仅限工作区目录
          </div>
        </div>
      </Section>
    </div>
  );
}

export function PageUpdates({ store }: { store: any }) {
  const [autoUpdate, setAutoUpdate] = useState(true);
  const [channel, setChannel] = useState('stable');
  const saveConfig = async (patch: any) => {
    if (!store.backendUrl) return;
    try { await fetch(`${store.backendUrl}/api/config`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(patch) }); } catch {}
  };
  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title="自动更新">
        <label style={{ display:'flex', alignItems:'center', gap:8, cursor:'pointer' }}>
          <input type="checkbox" checked={autoUpdate}
            onChange={e => { setAutoUpdate(e.target.checked); saveConfig({ update: { auto_update: e.target.checked } }); }} />
          <span style={{ fontSize:12 }}>启动时自动检查更新</span>
        </label>
      </Section>
      <Section title="更新频道">
        <div style={{ display:'flex', gap:6 }}>
          {[{ v:'stable', label:'稳定版' }, { v:'beta', label:'Beta' }, { v:'nightly', label:'每夜构建' }].map(o => (
            <button key={o.v} onClick={() => { setChannel(o.v); saveConfig({ update: { channel: o.v } }); }}
              style={{ padding:'6px 14px', borderRadius:6, fontSize:11, cursor:'pointer', border: channel === o.v ? '1.5px solid var(--accent)' : '1px solid var(--border-color)', background: channel === o.v ? 'var(--accent-soft)' : 'transparent', color: channel === o.v ? 'var(--accent)' : 'var(--text-secondary)', fontWeight: channel === o.v ? 600 : 400 }}>
              {o.label}
            </button>
          ))}
        </div>
      </Section>
    </div>
  );
}

export function PageAbout({ store }: { store: any }) {
  const clearData = async () => {
    if (!window.confirm('确定清除所有会话数据？')) return;
    if (!store.backendUrl) return;
    try {
      const resp = await fetch(`${store.backendUrl}/api/sessions`);
      const sessions = await resp.json();
      for (const s of (sessions as any[])) {
        await fetch(`${store.backendUrl}/api/sessions/${s.id}`, { method: 'DELETE' });
      }
    } catch {}
  };
  return (
    <div style={{ display:'flex', flexDirection:'column', gap:20 }}>
      <Section title="iCode">
        <div style={{ fontSize:12, lineHeight:1.6 }}>
          <div><span style={{ color:'var(--text-muted)' }}>版本：</span>v0.4.0</div>
          <div><span style={{ color:'var(--text-muted)' }}>引擎：</span>iCode Go + React</div>
          <div><span style={{ color:'var(--text-muted)' }}>数据路径：</span>~/.icode/</div>
        </div>
      </Section>
      <Section title="数据管理">
        <button onClick={clearData} style={{ ...btnGhost, color: '#EF4444', borderColor: '#EF444433' }}>
          <Trash2 size={12} /> 清除所有会话
        </button>
      </Section>
    </div>
  );
}
