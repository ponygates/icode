import React, { useState, useEffect } from 'react';
import { Routes, Route, useNavigate } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import ChatPage from './pages/ChatPage';
import ModelsPage from './pages/ModelsPage';
import SettingsPage from './pages/SettingsPage';

const App: React.FC = () => {
  const [sidebarOpen, setSidebarOpen] = useState(true);

  // Apply the saved theme on startup so the whole app matches the user's
  // preference even before the Settings page is opened.
  useEffect(() => {
    if (window.icode && window.icode.getConfig) {
      window.icode
        .getConfig()
        .then((cfg: any) => {
          if (!cfg) return;
          const theme = cfg.tui?.theme || 'dark';
          const root = document.documentElement;
          if (theme === 'auto') {
            const mq = window.matchMedia('(prefers-color-scheme: light)');
            root.setAttribute('data-theme', mq.matches ? 'light' : 'dark');
          } else {
            root.setAttribute('data-theme', theme);
          }
        })
        .catch(() => {});
    }
  }, []);

  return (
    <div style={{ display: 'flex', height: '100vh', background: 'var(--bg-primary)', color: 'var(--text-primary)' }}>
      {sidebarOpen && <Sidebar onToggle={() => setSidebarOpen(false)} />}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        {!sidebarOpen && (
          <button
            onClick={() => setSidebarOpen(true)}
            style={{
              position: 'absolute', top: 8, left: 8, zIndex: 10,
              background: 'var(--bg-secondary)', border: '1px solid var(--border-color)',
              color: 'var(--text-secondary)', padding: '4px 8px', borderRadius: 6,
              cursor: 'pointer', fontSize: 12,
            }}
          >
            Menu
          </button>
        )}
        <Routes>
          <Route path="/" element={<ChatPage />} />
          <Route path="/models" element={<ModelsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </div>
    </div>
  );
};

export default App;
