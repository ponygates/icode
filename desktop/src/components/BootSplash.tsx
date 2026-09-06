import React, { useState, useEffect } from 'react';
import PlumBlossom from './PlumBlossom';

/**
 * BootSplash is a lightweight startup prompt shown the moment the desktop app
 * mounts. It stays up for a fixed ~3 seconds then fades out and unmounts, so
 * the user always gets clear "the app is starting" feedback during the
 * (backend boot + WebView2 JS load) window instead of staring at a blank frame.
 *
 * It is intentionally independent of the backend: it never waits on
 * /api/health, so even if the backend is slow or unreachable the splash still
 * auto-closes and the (offline-capable) UI is revealed.
 *
 * Brand: switched to opencode-style circular logo with "iCode" wordmark,
 * paired with opencode-style indeterminate progress bar below.
 */
const BootSplash: React.FC = () => {
  const [show, setShow] = useState(true);
  const [fade, setFade] = useState(false);

  useEffect(() => {
    // Visible for 3s, then fade (~450ms) and unmount.
    const tHide = setTimeout(() => setFade(true), 3000);
    const tRemove = setTimeout(() => setShow(false), 3000 + 450);
    return () => {
      clearTimeout(tHide);
      clearTimeout(tRemove);
    };
  }, []);

  if (!show) return null;

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 5000,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 18,
        background: 'var(--bg-app)',
        color: 'var(--text-primary)',
        opacity: fade ? 0 : 1,
        transition: 'opacity 0.45s ease',
        pointerEvents: fade ? 'none' : 'auto',
      }}
    >
      {/* Brand: the plum blossom mark (restored per user preference) */}
      <PlumBlossom
        size={72}
        style={{ filter: 'drop-shadow(0 4px 14px rgba(230,111,168,0.35))' }}
      />
      
      <div style={{ fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em', color: 'var(--text-primary)' }}>
        iCode
      </div>
      <div className="boot-bar" />
      <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>正在启动…</div>
    </div>
  );
};

export default BootSplash;