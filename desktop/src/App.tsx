import React, { useState } from 'react';
import { Routes, Route, useNavigate } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import ChatPage from './pages/ChatPage';
import ModelsPage from './pages/ModelsPage';
import SettingsPage from './pages/SettingsPage';

const App: React.FC = () => {
  const [sidebarOpen, setSidebarOpen] = useState(true);

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
