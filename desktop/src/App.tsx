import React, { useState, useEffect } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import ChatPage from './pages/ChatPage';
import ModelsPage from './pages/ModelsPage';
import SettingsModal from './pages/SettingsPage';
import AnalyticsPage from './pages/AnalyticsPage';
import ModelCompare from './pages/ModelCompare';
import SetupWizard from './components/SetupWizard';
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
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [showWizard, setShowWizard] = useState(false);
  const loadSecurityLevel = useAppStore((s) => s.loadSecurityLevel);
  const loadSessions = useAppStore((s) => s.loadSessions);
  const refreshModels = useAppStore((s) => s.refreshModels);
  const checkBackend = useAppStore((s) => s.checkBackend);
  const fetchMode = useAppStore((s) => s.fetchMode);
  const backendConnected = useAppStore((s) => s.backendConnected);
  const backendChecking = useAppStore((s) => s.backendChecking);

  // Startup: load config, apply theme, show setup wizard if first run
  useEffect(() => {
    loadSecurityLevel();
    loadSessions();
    refreshModels();
    checkBackend();
    fetchMode();
    // Font size from localStorage
    const savedFontSize = localStorage.getItem('icode.fontSize');
    if (savedFontSize) {
      document.documentElement.style.fontSize = savedFontSize + 'px';
    }
    // Theme from local config
    const theme = localStorage.getItem('icode.theme') || 'dark';
    const root = document.documentElement;
    if (theme === 'auto') {
      root.setAttribute('data-theme', window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    } else {
      root.setAttribute('data-theme', theme);
    }
    // Show setup wizard on first run (no API key configured)
    const seen = localStorage.getItem('icode.wizard.seen');
    if (!seen && !hasAnyKey()) {
      setShowWizard(true);
    }
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
            <span>⚡ 后端未连接 — 请启动 iCode 服务</span>
            <button onClick={() => checkBackend()} style={{
              background: 'rgba(0,0,0,0.1)', border: 'none', color: '#000',
              padding: '3px 10px', borderRadius: 4, cursor: 'pointer', fontSize: 11,
            }}>重试</button>
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
