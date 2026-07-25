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
import { useAppStore } from './stores/appStore';

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
            await checkBackend();
            if (cancelled) return;
            await fetchMode();
            loadSecurityLevel();
            await loadSessions();
            await loadWorkspaces();
            await refreshModels();
            await loadDesktopSettings();
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
      {/* Startup prompt — shows for ~3s then auto-closes (see BootSplash). */}
      <BootSplash />
      {/* First-run setup wizard */}
      {showWizard && (
        <SetupWizard onDone={() => {
          setShowWizard(false);
          localStorage.setItem('icode.wizard.seen', '1');
          refreshModels();
        }} />
      )}
    </div>
  );
};

export default App;
