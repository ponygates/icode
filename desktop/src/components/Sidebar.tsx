import React, { useState, useEffect } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAppStore, type Session } from '../stores/appStore';
import { MessageSquare, Cpu, Settings, BarChart, ArrowLeftRight, PanelLeftClose, Github, Plus, Server, Pencil, RotateCcw, Trash2, Search, Store } from 'lucide-react';
import PlumBlossom from './PlumBlossom';
import WorkspaceSidebar from './WorkspaceSidebar';

interface Props { onToggle: () => void; }

interface SearchHit {
  session_id?: string;
  session_title?: string;
  content?: string;
  timestamp?: string | number;
}

// groupSessions buckets a session list by recency — 今天/昨天/7 天内/更早 —
// Claude Code-style timeline grouping so the sidebar reads like a modern chat
// app instead of a flat list. Uses updatedAt when available, createdAt as the
// fallback.
function groupSessions(sessions: Session[], t: (k: string) => string): { label: string; items: Session[] }[] {
  const now = Date.now();
  const day = 86400000;
  const buckets = [
    { label: t('sidebar.today'), items: [] as Session[] },
    { label: t('sidebar.yesterday'), items: [] as Session[] },
    { label: t('sidebar.last7'), items: [] as Session[] },
    { label: t('sidebar.earlier'), items: [] as Session[] },
  ];
  for (const s of sessions) {
    const ts = s.updatedAt || s.createdAt || now;
    const days = Math.floor((now - ts) / day);
    if (days <= 0) buckets[0].items.push(s);
    else if (days === 1) buckets[1].items.push(s);
    else if (days < 7) buckets[2].items.push(s);
    else buckets[3].items.push(s);
  }
  return buckets.filter((b) => b.items.length > 0);
}

const Sidebar: React.FC<Props> = ({ onToggle }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [searchHits, setSearchHits] = useState<SearchHit[] | null>(null);
  const [searching, setSearching] = useState(false);

  // Precise selectors: the sidebar renders the session list + model name.
  // It must re-render when sessions/activeSession/selectedModel change, but
  // not on every tokenUsage tick or message append in the active session.
  const sessions = useAppStore(s => s.sessions);
  const activeSessionId = useAppStore(s => s.activeSessionId);
  const selectedModel = useAppStore(s => s.selectedModel);
  const setActiveSession = useAppStore(s => s.setActiveSession);
  const createSession = useAppStore(s => s.createSession);
  const deleteSession = useAppStore(s => s.deleteSession);
  const renameSession = useAppStore(s => s.renameSession);
  const trash = useAppStore(s => s.trash);
  const loadTrash = useAppStore(s => s.loadTrash);
  const restoreSession = useAppStore(s => s.restoreSession);
  const deleteForever = useAppStore(s => s.deleteForever);
  const purgeTrash = useAppStore(s => s.purgeTrash);
  const currentModel = useAppStore((s) => s.models.find((m) => m.id === s.selectedModel));
  const backendConnected = useAppStore((s) => s.backendConnected);
  const backendChecking = useAppStore((s) => s.backendChecking);
  const backendVersion = useAppStore((s) => s.backendVersion);
  const backendUrl = useAppStore((s) => s.backendUrl);

  // Debounced session-content search against /api/search (the backend has
  // full-text search over all sessions; the sidebar surfaces it).
  useEffect(() => {
    if (!searchQuery.trim() || !backendConnected) {
      setSearchHits(null);
      setSearching(false);
      return;
    }
    setSearching(true);
    const timer = setTimeout(async () => {
      try {
        const url = backendUrl || '';
        const res = await fetch(`${url}/api/search?q=${encodeURIComponent(searchQuery.trim())}&limit=8`);
        if (res.ok) {
          const data = await res.json();
          setSearchHits(data.results || []);
        } else {
          setSearchHits([]);
        }
      } catch { setSearchHits([]); }
      setSearching(false);
    }, 350);
    return () => clearTimeout(timer);
  }, [searchQuery, backendConnected, backendUrl]);

  const openHit = (hit: SearchHit) => {
    if (!hit?.session_id) return;
    setActiveSession(hit.session_id);
    setSearchQuery('');
    setSearchHits(null);
    navigate('/');
  };

  // Load the soft-deleted ("recently removed") sessions whenever the backend
  // connects, so the trash section stays in sync with /clear on other surfaces.
  useEffect(() => {
    if (backendConnected) loadTrash();
  }, [backendConnected, loadTrash]);

  const navItems = [
    { path: '/', icon: MessageSquare, label: t('sidebar.chat') },
    { path: '/models', icon: Cpu, label: t('sidebar.models') },
    { path: '/market', icon: Store, label: t('sidebar.market') },
    { path: '/analytics', icon: BarChart, label: t('sidebar.analytics') },
    { path: '/compare', icon: ArrowLeftRight, label: t('sidebar.compare') },
    { action: 'settings', icon: Settings, label: t('sidebar.settings') },
  ];

  const handleNav = (item: typeof navItems[0]) => {
    if ('action' in item) {
      window.dispatchEvent(new CustomEvent('icode:open-settings'));
    } else { navigate(item.path); }
  };

  const isActive = (item: typeof navItems[0]) => {
    if ('action' in item) return false;
    if (item.path === '/') return location.pathname === '/';
    return location.pathname.startsWith(item.path);
  };

  const toggleCollapse = () => { setCollapsed(!collapsed); };

  const sidebarWidth = collapsed ? 52 : 220;

  return (
    <div style={{
      width: sidebarWidth, minWidth: sidebarWidth, background: 'var(--bg-secondary)',
      borderRight: '0.5px solid var(--border-color)', display: 'flex',
      flexDirection: 'column', height: '100vh', transition: 'width var(--t-normal)',
    }}>
      {/* Header */}
      <div style={{
        padding: collapsed ? '14px 10px' : '18px 16px 12px',
        borderBottom: '0.5px solid var(--border-color)',
        display: 'flex', alignItems: 'center', justifyContent: collapsed ? 'center' : 'space-between',
      }}>
        {!collapsed && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <PlumBlossom size={22} style={{ flexShrink: 0 }} />
            <span style={{ fontSize: 18, fontWeight: 700, color: 'var(--accent)', letterSpacing: '-0.02em' }}>
              iCODE
            </span>
          </span>
        )}
        {collapsed && <PlumBlossom size={22} style={{ flexShrink: 0 }} />}
        <button
          onClick={collapsed ? toggleCollapse : onToggle}
          className="interactive"
          style={{
            background: 'none', border: 'none', color: 'var(--text-muted)',
            padding: 4, display: 'flex', borderRadius: 4,
          }}
        >
          <PanelLeftClose size={16} style={collapsed ? { transform: 'rotate(180deg)' } : undefined} />
        </button>
      </div>

      {/* Navigation */}
      <nav style={{ padding: '8px 6px', borderBottom: '0.5px solid var(--border-color)' }}>
        {navItems.map((item) => {
          const active = isActive(item);
          return (
            <button
              key={item.path || item.action}
              onClick={() => handleNav(item)}
              title={item.label}
              className={active ? 'nav-item active' : 'nav-item'}
              style={{ width: '100%', justifyContent: collapsed ? 'center' : 'flex-start', marginBottom: 2 }}
            >
              <item.icon size={16} style={active ? { color: 'var(--accent)' } : undefined} />
              {!collapsed && <span>{item.label}</span>}
            </button>
          );
        })}
      </nav>

      {/* Workspaces (v0.12) — project containers grouping sessions */}
      {!collapsed && <WorkspaceSidebar />}

      {/* Session history */}
      <div style={{ flex: 1, overflowY: 'auto', padding: collapsed ? '4px 4px' : '6px 6px' }}>
        {!collapsed && (
          <div style={{ padding: '0 6px 6px', position: 'relative' }}>
            <div style={{
              display: 'flex', alignItems: 'center', gap: 6,
              background: 'var(--bg-tertiary)', border: '0.5px solid var(--border-color)',
              borderRadius: 6, padding: '4px 8px',
            }}>
              <Search size={11} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
              <input
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                onFocus={(e) => { if (e.target.value) { setSearchHits(null); } }}
                placeholder={t('sidebar.searchSessions')}
                style={{
                  flex: 1, background: 'transparent', border: 'none', outline: 'none',
                  color: 'var(--text-primary)', fontSize: 11, minWidth: 0,
                }}
              />
              {searching && <span style={{ color: 'var(--text-muted)', fontSize: 9 }}>…</span>}
            </div>
            {searchQuery.trim() && searchHits !== null && (
              <div style={{
                position: 'absolute', left: 6, right: 6, top: '100%', zIndex: 60,
                background: 'var(--bg-secondary)', border: '0.5px solid var(--border-color)',
                borderRadius: 8, boxShadow: '0 8px 24px rgba(0,0,0,0.35)',
                maxHeight: 240, overflowY: 'auto', padding: 4,
              }}>
                {searchHits.length === 0 ? (
                  <div style={{ padding: '8px', color: 'var(--text-muted)', fontSize: 11 }}>
                    {t('sidebar.noSearchResults')}
                  </div>
                ) : searchHits.map((hit, i) => (
                  <div
                    key={i}
                    onClick={() => openHit(hit)}
                    style={{
                      padding: '5px 8px', borderRadius: 6, cursor: 'pointer',
                      fontSize: 11,
                    }}
                    onMouseEnter={(e) => { (e.currentTarget as HTMLElement).style.background = 'var(--bg-tertiary)'; }}
                    onMouseLeave={(e) => { (e.currentTarget as HTMLElement).style.background = 'transparent'; }}
                  >
                    <div style={{
                      color: 'var(--text-primary)', fontWeight: 500,
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>
                      {hit.session_title || hit.session_id || '?'}
                    </div>
                    <div style={{
                      color: 'var(--text-muted)', fontSize: 10,
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                      marginTop: 1,
                    }}>
                      {(hit.content || '').slice(0, 80)}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
        {!collapsed && (
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '8px 8px 4px', color: 'var(--text-muted)',
            fontSize: 9, letterSpacing: 1, textTransform: 'uppercase', fontWeight: 600,
          }}>
            <span>{t('sidebar.sessions')}</span>
            <button
              onClick={() => createSession(selectedModel, currentModel?.provider || 'openrouter')}
              className="interactive"
              style={{
                background: 'none', border: 'none', color: 'var(--text-muted)',
                padding: 3, display: 'flex', borderRadius: 4,
              }}
            >
              <Plus size={12} />
            </button>
          </div>
        )}
        {sessions.length === 0 && !collapsed && (
          <div style={{ padding: '16px 8px', color: 'var(--text-muted)', fontSize: 11, textAlign: 'center' }}>
            {t('sidebar.noSessions')}
          </div>
        )}
        {(() => {
          const recent = sessions.slice(-20).reverse();
          const groups = collapsed
            ? [{ label: '', items: recent }]
            : groupSessions(recent, t);
          return groups.map((g) => (
            <div key={g.label || '__flat'}>
              {!collapsed && (
                <div style={{
                  padding: '6px 8px 2px', color: 'var(--text-muted)',
                  fontSize: 9, letterSpacing: 1, textTransform: 'uppercase', fontWeight: 600,
                }}>{g.label}</div>
              )}
              {g.items.map((s) => {
                const active = s.id === activeSessionId;
                const isEditing = editingId === s.id;
                return (
            <div
              key={s.id}
              onClick={() => { if (!isEditing) { setActiveSession(s.id); navigate('/'); } }}
              className={`sidebar-item${active ? ' active' : ''}`}
              title={!collapsed ? undefined : (s.title || s.id.slice(0, 6))}
              style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: collapsed ? '6px 6px' : '6px 8px', cursor: 'pointer',
                justifyContent: collapsed ? 'center' : 'flex-start',
              }}
              onMouseEnter={(e) => {
                if (collapsed) return;
                e.currentTarget.querySelectorAll('.action-hidden').forEach((el) => {
                  (el as HTMLElement).style.opacity = '0.6';
                });
              }}
              onMouseLeave={(e) => {
                if (collapsed) return;
                e.currentTarget.querySelectorAll('.action-hidden').forEach((el) => {
                  (el as HTMLElement).style.opacity = '0';
                });
              }}
            >
              <div style={{
                width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
                background: active ? 'var(--accent)' : 'transparent',
                transition: 'background var(--t-fast)',
              }} />
              {!collapsed && (
                <>
                  {isEditing ? (
                    <input
                      autoFocus
                      value={editValue}
                      onChange={(e) => setEditValue(e.target.value)}
                      onClick={(e) => e.stopPropagation()}
                      onBlur={() => { renameSession(s.id, editValue); setEditingId(null); }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') { e.preventDefault(); renameSession(s.id, editValue); setEditingId(null); }
                        else if (e.key === 'Escape') { e.preventDefault(); setEditingId(null); }
                      }}
                      style={{
                        flex: 1, fontSize: 11.5, lineHeight: 1.3, minWidth: 0,
                        background: 'var(--bg-tertiary)', color: 'var(--text-primary)',
                        border: '0.5px solid var(--accent)', borderRadius: 4,
                        padding: '2px 4px', outline: 'none',
                      }}
                    />
                  ) : (
                    <div style={{
                      flex: 1, overflow: 'hidden', textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap', fontSize: 11.5, lineHeight: 1.3,
                    }}>
                      {s.title || t('chat.sessionN', { n: s.id.slice(0, 6) })}
                    </div>
                  )}
                  <button className="action-hidden"
                    onClick={(e) => { e.stopPropagation(); setEditingId(s.id); setEditValue(s.title || ''); }}
                    title={t('sidebar.rename')}
                    style={{
                      background: 'none', border: 'none', color: 'var(--text-muted)',
                      cursor: 'pointer', padding: 2, display: 'flex',
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--accent)'; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
                  ><Pencil size={11} /></button>
                  <button className="action-hidden"
                    onClick={(e) => { e.stopPropagation(); deleteSession(s.id); }}
                    title={t('sidebar.delete')}
                    style={{
                      background: 'none', border: 'none', color: 'var(--text-muted)',
                      cursor: 'pointer', padding: 2, display: 'flex', fontSize: 10,
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--error)'; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
                  >×</button>
                </>
              )}
              </div>
                );
              })}
            </div>
          ));
        })()}
      </div>

      {/* Trash — soft-deleted sessions, restorable */}
      {!collapsed && trash.length > 0 && (
        <div style={{ borderTop: '0.5px solid var(--border-color)', padding: '6px 6px' }}>
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '4px 8px', color: 'var(--text-muted)',
            fontSize: 9, letterSpacing: 1, textTransform: 'uppercase', fontWeight: 600,
          }}>
            <span>{t('sidebar.trash')} ({trash.length})</span>
            <button
              onClick={() => { if (window.confirm(t('sidebar.purgeTrashConfirm'))) purgeTrash(); }}
              title={t('sidebar.purgeTrash')}
              className="interactive"
              style={{ background: 'none', border: 'none', color: 'var(--text-muted)', padding: 2, display: 'flex', borderRadius: 4, fontSize: 9 }}
            ><Trash2 size={11} /></button>
          </div>
          {trash.map((s) => (
            <div
              key={s.id}
              className="sidebar-item"
              title={s.title || s.id.slice(0, 6)}
              style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: '6px 8px', opacity: 0.75,
              }}
            >
              <div style={{
                width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
                background: 'var(--text-muted)',
              }} />
              <div style={{
                flex: 1, overflow: 'hidden', textOverflow: 'ellipsis',
                whiteSpace: 'nowrap', fontSize: 11.5, lineHeight: 1.3,
                textDecoration: 'line-through', color: 'var(--text-muted)',
              }}>
                {s.title || t('chat.sessionN', { n: s.id.slice(0, 6) })}
              </div>
              <button className="action-hidden"
                onClick={() => restoreSession(s.id)}
                title={t('sidebar.restore')}
                style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: 2, display: 'flex', fontSize: 10 }}
                onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--success)'; }}
                onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
              ><RotateCcw size={11} /></button>
              <button className="action-hidden"
                onClick={() => deleteForever(s.id)}
                title={t('sidebar.deleteForever')}
                style={{ background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', padding: 2, display: 'flex', fontSize: 10 }}
                onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--error)'; }}
                onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-muted)'; }}
              ><Trash2 size={11} /></button>
            </div>
          ))}
        </div>
      )}

      {/* Footer */}
      <div style={{
        padding: '10px 12px', borderTop: '0.5px solid var(--border-color)',
        fontSize: 10, color: 'var(--text-muted)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, justifyContent: collapsed ? 'center' : 'flex-start' }}>
          <div style={{
            width: 6, height: 6, borderRadius: '50%',
            background: backendChecking ? 'var(--text-muted)' : backendConnected ? 'var(--success)' : 'var(--warning)',
            flexShrink: 0,
          }} />
          {!collapsed && (
            <span>{backendChecking ? t('sidebar.connecting') : backendConnected ? t('settings.connected') : t('settings.disconnected')}</span>
          )}
        </div>
        {!collapsed && (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span>{backendVersion || 'v0.1.0'}</span>
            <button
              onClick={() => window.icode?.openExternal?.('https://github.com/ponygates/icode')}
              className="interactive"
              style={{ background: 'none', border: 'none', color: 'var(--text-muted)', padding: 2, display: 'flex', borderRadius: 4 }}
            ><Github size={13} /></button>
          </div>
        )}
      </div>
    </div>
  );
};

export default Sidebar;
