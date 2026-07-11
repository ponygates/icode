const { app, BrowserWindow, ipcMain, shell, net } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const fs = require('fs');
const http = require('http');

let mainWindow = null;
let backendProcess = null;
let backendPort = null;
let backendReady = false;

// API helper — makes HTTP calls to the Go backend
function apiCall(method, endpoint, body) {
  return new Promise((resolve, reject) => {
    if (!backendPort) return reject(new Error('Backend not available'));

    const options = {
      hostname: '127.0.0.1',
      port: backendPort,
      path: endpoint,
      method,
      headers: { 'Content-Type': 'application/json' },
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', (chunk) => { data += chunk; });
      res.on('end', () => {
        try { resolve(JSON.parse(data)); }
        catch { resolve(data); }
      });
    });
    req.on('error', reject);
    if (body) req.write(JSON.stringify(body));
    req.end();
  });
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1200, height: 800, minWidth: 800, minHeight: 600,
    title: 'iCode',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
    titleBarStyle: 'hiddenInset',
    frame: process.platform !== 'darwin',
  });

  const isDev = process.env.NODE_ENV !== 'production' || process.argv.includes('--dev');
  if (isDev) {
    mainWindow.loadURL('http://localhost:5173');
    mainWindow.webContents.openDevTools();
  } else {
    mainWindow.loadFile(path.join(__dirname, 'dist', 'index.html'));
  }

  mainWindow.on('closed', () => { mainWindow = null; });
}

function startBackend() {
  return new Promise((resolve) => {
    const cwd = process.cwd();
    const binary = process.platform === 'win32' ? 'icode.exe' : 'icode';

    // In dev: look in project directory. In packaged: look in resources.
    const isDev = process.env.NODE_ENV !== 'production' && !app.isPackaged;
    const candidates = [
      path.join(process.resourcesPath || '', binary),
      path.join(__dirname, '..', '..', binary),
      path.join(cwd, binary),
    ];

    let backendPath = null;
    for (const p of candidates) {
      if (fs.existsSync(p)) { backendPath = p; break; }
    }

    if (!backendPath) {
      console.log('[iCode] Backend binary not found, frontend-only mode');
      resolve(false);
      return;
    }

    try {
      backendProcess = spawn(backendPath, ['server', '--port', '0'], {
        cwd: path.dirname(backendPath),
        stdio: ['pipe', 'pipe', 'pipe'],
        windowsHide: true, // suppress the backend's console window on Windows
      });

      backendProcess.stdout.on('data', (data) => {
        console.log(`[backend] ${data}`);
      });
      backendProcess.stderr.on('data', (data) => {
        console.error(`[backend:err] ${data}`);
      });
      backendProcess.on('close', (code) => {
        console.log(`[backend] exited (${code})`);
        backendReady = false;
      });

      // Poll for port file
      const portFile = path.join(require('os').tmpdir(), 'icode', 'port');
      let attempts = 0;
      const check = setInterval(() => {
        try {
          const port = parseInt(fs.readFileSync(portFile, 'utf8').trim());
          if (port > 0) {
            backendPort = port;
            backendReady = true;
            clearInterval(check);
            console.log(`[iCode] Backend ready on port ${port}`);
            resolve(true);
          }
        } catch {}
        attempts++;
        if (attempts > 50) { // 5 seconds
          clearInterval(check);
          console.log('[iCode] Backend startup timeout');
          resolve(false);
        }
      }, 100);
    } catch (err) {
      console.log('[iCode] Backend start failed:', err.message);
      resolve(false);
    }
  });
}

// ============================================================================
// IPC handlers — bridge between renderer and Go backend
// ============================================================================

ipcMain.handle('app:getVersion', () => app.getVersion());
ipcMain.handle('app:getPlatform', () => process.platform);
ipcMain.handle('app:openExternal', (_, url) => shell.openExternal(url));
ipcMain.handle('app:getBackendPort', () => backendPort);
ipcMain.handle('app:getBackendURL', () =>
  backendPort ? `http://127.0.0.1:${backendPort}` : null
);

// Models
ipcMain.handle('model:list', async () => {
  if (!backendReady) return [];
  try { return await apiCall('GET', '/api/models'); }
  catch { return []; }
});

ipcMain.handle('model:refresh', async () => {
  if (!backendReady) return { success: false, error: 'Backend unavailable' };
  try { return await apiCall('POST', '/api/models/refresh'); }
  catch (e) { return { success: false, error: e.message }; }
});

// Sessions
ipcMain.handle('session:list', async () => {
  if (!backendReady) return [];
  try { return await apiCall('GET', '/api/sessions'); }
  catch { return []; }
});

ipcMain.handle('session:get', async (_, id) => {
  if (!backendReady) return null;
  try { return await apiCall('GET', `/api/sessions/${id}`); }
  catch { return null; }
});

ipcMain.handle('session:create', async (_, session) => {
  if (!backendReady) return null;
  try { return await apiCall('POST', '/api/sessions', session); }
  catch { return null; }
});

ipcMain.handle('session:delete', async (_, id) => {
  if (!backendReady) return { ok: false };
  try { return await apiCall('DELETE', `/api/sessions/${id}`); }
  catch { return { ok: false }; }
});

// Chat (via SSE)
ipcMain.handle('chat:send', async (event, { sessionId, content }) => {
  if (!backendPort) {
    const errMsg = 'Backend not available — 后端未启动，无法发送消息';
    if (mainWindow) mainWindow.webContents.send('chat:stream', { type: 'error', content: errMsg });
    return { error: errMsg };
  }

  return new Promise((resolve) => {
    const options = {
      hostname: '127.0.0.1', port: backendPort, path: '/api/chat',
      method: 'POST', headers: { 'Content-Type': 'application/json' },
    };

    const req = http.request(options, (res) => {
      let buffer = '';
      res.on('data', (chunk) => {
        buffer += chunk;
        // Parse SSE events and forward to renderer. Keep the trailing
        // incomplete line (if any) for the next chunk so events that span
        // multiple TCP packets are not lost.
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue;
          const payload = line.slice(6).trim();
          if (!payload) continue;
          try {
            const evt = JSON.parse(payload);
            if (mainWindow) mainWindow.webContents.send('chat:stream', evt);
          } catch {}
        }
      });
      res.on('end', () => resolve({ ok: true }));
    });
    req.on('error', (e) => {
      if (mainWindow) mainWindow.webContents.send('chat:stream', { type: 'error', content: e.message });
      resolve({ error: e.message });
    });
    req.write(JSON.stringify({ session_id: sessionId, content }));
    req.end();
  });
});

// API key management (desktop settings UI)
ipcMain.handle('config:setApiKey', async (_, { provider, apiKey, apiBase }) => {
  if (!backendReady) return { ok: false, error: 'Backend unavailable' };
  try {
    return await apiCall('PUT', '/api/config/key', { provider, api_key: apiKey, api_base: apiBase });
  } catch (e) { return { ok: false, error: e.message }; }
});

ipcMain.handle('config:listKeys', async () => {
  if (!backendReady) return { providers: [] };
  try { return await apiCall('GET', '/api/config/keys'); }
  catch { return { providers: [] }; }
});

ipcMain.handle('chat:stop', async (_, sessionId) => {
  if (!backendReady) return { ok: false };
  try { return await apiCall('POST', '/api/chat/stop', { session_id: sessionId }); }
  catch { return { ok: false }; }
});

// Config
ipcMain.handle('config:get', async () => {
  if (!backendReady) return null;
  try { return await apiCall('GET', '/api/config'); }
  catch { return null; }
});

ipcMain.handle('config:set', async (_, cfg) => {
  if (!backendReady) return { ok: false, error: 'Backend unavailable' };
  try { return await apiCall('PUT', '/api/config', cfg); }
  catch (e) { return { ok: false, error: e.message }; }
});

ipcMain.handle('config:setLang', async (_, lang) => {
  if (!backendReady) return { ok: false };
  try { return await apiCall('POST', '/api/config/lang', { language: lang }); }
  catch { return { ok: false }; }
});

// Permission
ipcMain.handle('permission:setMode', async (_, mode) => {
  if (!backendReady) return { ok: false };
  try { return await apiCall('POST', '/api/permission/mode', { mode }); }
  catch { return { ok: false }; }
});

// ============================================================================
// App lifecycle
// ============================================================================

app.whenReady().then(async () => {
  await startBackend();
  createWindow();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('window-all-closed', () => {
  if (backendProcess) {
    backendProcess.kill();
  }
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('before-quit', () => {
  if (backendProcess) {
    backendProcess.kill('SIGTERM');
  }
});
