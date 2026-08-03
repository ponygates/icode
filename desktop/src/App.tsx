import React, { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Routes, Route, Navigate } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import ChatPage from './pages/ChatPage';
import ModelsPage from './pages/ModelsPage';
import SettingsModal from './pages/SettingsPage';
import AnalyticsPage from './pages/AnalyticsPage';
import ModelCompare from './pages/ModelCompare';
import SetupWizard from './components/SetupWizard';
import BootSplash from './components/BootSplash';
import ShortcutPanel from './components/ShortcutPanel';
import { useAppStore } from './stores/appStore';
import ErrorBoundary from './components/ErrorBoundary';

function hasAnyKey(): boolean {
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && k.startsWith('icode.key.')) return true;
    }
  } catch {}
  return false;
}

const App: React.FC = () => {
  const { t } = useTranslation();
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [showWizard, setShowWizard] = useState(false);
  const loadSecurityLevel = useAppStore((s) => s.loadSecurityLevel);
  const loadSessions = useAppStore((s) => s.loadSessions);
  const loadWorkspaces = useAppStore((s) => s.loadWorkspaces);
  const refreshModels = useAppStore((s) => s.refreshModels);
  const checkBackend = useAppStore((s) => s.checkBackend);
  const fetchMode = useAppStore((s) => s.fetchMode);
  const loadDesktopSettings = useAppStore((s) => s.loadDesktopSettings);
  const backendConnected = useAppStore((s) => s.backendConnected);
  const backendChecking = useAppStore((s) => s.backendChecking);

  // Startup: connect to the backend FIRST, then load config/sessions/models.
  // Connecting first lets session/model loads use the HTTP path (the backend
  // already holds everything in SQLite) instead of the heavy localStorage
  // fallback — this avoids a large synchronous parse/serialize at launch.
  useEffect(() => {
    let cancelled = false;
    const init = async () => {
      window.__icodePhase = 'local-ui';
      // 1) Local UI setup first — never touches the network, so it can't hang
      //    and the welcome screen is always fully styled and interactive.
      const savedFontSize = localStorage.getItem('icode.fontSize');
      if (savedFontSize) {
        document.documentElement.style.fontSize = savedFontSize + 'px';
      }
      const theme = localStorage.getItem('icode.theme') || 'dark';
      const root = document.documentElement;
      if (theme === 'auto') {
        root.setAttribute('data-theme', window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
      } else {
        root.setAttribute('data-theme', theme);
      }
      const seen = localStorage.getItem('icode.wizard.seen');
      if (!seen && !hasAnyKey()) {
        setShowWizard(true);
      }

      // 2) Backend-dependent loads, wrapped in a hard timeout so a slow or
      //    unreachable backend can NEVER leave the app stuck on the welcome
      //    screen. The UI stays responsive and simply shows "backend offline".
      try {
        await Promise.race([
          (async () => {
            window.__icodePhase = 'check-backend';
            await checkBackend();
            if (cancelled) return;
            window.__icodePhase = 'fetch-mode';
            await fetchMode();
            loadSecurityLevel();
            window.__icodePhase = 'sessions';
            await loadSessions();
            window.__icodePhase = 'workspaces';
            await loadWorkspaces();
            window.__icodePhase = 'done';
            // Don't block the welcome screen on model/settings refresh — they
            // populate when ready. This guarantees the UI is interactive the
            // moment sessions load, so a slow /api/models can never stall
            // startup.
            refreshModels().catch(() => {});
            loadDesktopSettings().catch(() => {});
          })(),
          new Promise<void>((resolve) => setTimeout(resolve, 12000)),
        ]);
      } catch (e) {
        // eslint-disable-next-line no-console
        console.warn('[iCode] startup backend load failed:', e);
      }
    };
    init();
    // Listen for settings shortcut
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === ',') {
        e.preventDefault();
        setSettingsOpen(v => !v);
        return;
      }
      // Ctrl+N — new chat session (mirrors the sidebar "+" button).
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'n') {
        e.preventDefault();
        window.dispatchEvent(new CustomEvent('icode:new-session'));
        return;
      }
      // Ctrl+L — focus the chat input bar.
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'l') {
        e.preventDefault();
        window.dispatchEvent(new CustomEvent('icode:focus-input'));
        return;
      }
      // Esc — interrupt streaming generation (no-op when idle).
      if (e.key === 'Escape') {
        window.dispatchEvent(new CustomEvent('icode:stop-chat'));
        return;
      }
      // "?" toggles the shortcut panel — but never while the user is typing in
      // an input / textarea / contentEditable (where "?" is legitimate text).
      if (e.key === '?' || e.key === '？') {
        const el = e.target as HTMLElement | null;
        const tag = el?.tagName;
        const editable = tag === 'INPUT' || tag === 'TEXTAREA' || el?.isContentEditable;
        if (!editable) {
          e.preventDefault();
          setShortcutsOpen(v => !v);
        }
      }
    };
    window.addEventListener('keydown', handler);
    // Settings modal via sidebar event
    const openSettings = () => setSettingsOpen(true);
    window.addEventListener('icode:open-settings', openSettings);
    return () => {
      cancelled = true;
      window.removeEventListener('keydown', handler);
      window.removeEventListener('icode:open-settings', openSettings);
    };
  }, [loadSecurityLevel]);

  // Periodic backend health check
  useEffect(() => {
    const interval = setInterval(() => checkBackend(), 30000);
    return () => clearInterval(interval);
  }, [checkBackend]);

  return (
    <div style={{ display: 'flex', height: '100vh', background: 'var(--bg-primary)', color: 'var(--text-primary)' }}>
      <ErrorBoundary>
      {sidebarOpen && <Sidebar onToggle={() => setSidebarOpen(false)} />}

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        {/* Backend connection banner */}
        {!backendChecking && !backendConnected && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '6px 16px', fontSize: 12,
            background: 'var(--yellow)', color: '#000',
            borderBottom: '1px solid rgba(0,0,0,0.1)', flexShrink: 0,
          }}>
            <span>⚡ {t('chat.noResponse')}</span>
            <button onClick={() => checkBackend()} style={{
              background: 'rgba(0,0,0,0.1)', border: 'none', color: '#000',
              padding: '3px 10px', borderRadius: 4, cursor: 'pointer', fontSize: 11,
            }}>{t('settings.refreshModels')}</button>
          </div>
        )}

        {!sidebarOpen && (
          <button onClick={() => setSidebarOpen(true)} style={{
            position: 'absolute', top: 8, left: 8, zIndex: 10,
            background: 'var(--bg-secondary)', border: '1px solid var(--border-color)',
            color: 'var(--text-secondary)', padding: '4px 8px', borderRadius: 6,
            cursor: 'pointer', fontSize: 12,
          }}>Menu</button>
        )}

        <Routes>
          <Route path="/" element={<ChatPage />} />
          <Route path="/models" element={<ModelsPage />} />
          <Route path="/analytics" element={<AnalyticsPage />} />
          <Route path="/compare" element={<ModelCompare />} />
          <Route path="/settings" element={<Navigate to="/" replace />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </div>

      {/* Reasonix-style settings modal overlay (Ctrl+,) */}
      <SettingsModal visible={settingsOpen} onClose={() => setSettingsOpen(false)} />
      {/* Keyboard shortcut reference overlay (?) */}
      <ShortcutPanel visible={shortcutsOpen} onClose={() => setShortcutsOpen(false)} />
      {/* Startup prompt — shows for ~3s then auto-closes (see BootSplash). */}
      <BootSplash />
      {/* First-run setup wizard */}
      {showWizard && (
        <SetupWizard onDone={() => {
          setShowWizard(false);
          localStorage.setItem('icode.wizard.seen', '1');
          // Model refresh is already fire-and-forget in the init useEffect;
          // defer by 100ms to avoid a fetch-state update colliding with the
          // React render cycle that unmounts the wizard and mounts the main UI
          // for the first time (that collision could freeze on slow machines).
          setTimeout(() => refreshModels().catch(() => {}), 100);
        }} />
      )}
      </ErrorBoundary>
    </div>
  );
};

export default App;
