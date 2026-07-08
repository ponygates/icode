const { app, BrowserWindow, ipcMain, shell } = require('electron');
const path = require('path');
const { spawn } = require('child_process');

let mainWindow = null;
let backendProcess = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    minWidth: 800,
    minHeight: 600,
    title: 'iCode',
    icon: path.join(__dirname, 'assets', 'icon.png'),
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
    titleBarStyle: 'hiddenInset',
    frame: process.platform !== 'darwin',
  });

  // Development: load from Vite dev server
  const isDev = process.env.NODE_ENV !== 'production' || process.argv.includes('--dev');

  if (isDev) {
    mainWindow.loadURL('http://localhost:5173');
    mainWindow.webContents.openDevTools();
  } else {
    mainWindow.loadFile(path.join(__dirname, 'dist', 'index.html'));
  }

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

// Start backend process
function startBackend() {
  const backendPath = path.join(__dirname, '..', '..');
  const binary = process.platform === 'win32' ? 'icode.exe' : 'icode';

  try {
    backendProcess = spawn(path.join(backendPath, binary), ['server', '--port', '0'], {
      cwd: backendPath,
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    backendProcess.stdout.on('data', (data) => {
      console.log(`[backend] ${data}`);
    });

    backendProcess.stderr.on('data', (data) => {
      console.error(`[backend:err] ${data}`);
    });

    backendProcess.on('close', (code) => {
      console.log(`[backend] exited with code ${code}`);
    });
  } catch (err) {
    console.log('[iCode] Backend not found, running in frontend-only mode');
  }
}

// IPC handlers
ipcMain.handle('app:getVersion', () => app.getVersion());
ipcMain.handle('app:getPlatform', () => process.platform);
ipcMain.handle('app:openExternal', (_, url) => shell.openExternal(url));

// Model management
ipcMain.handle('model:list', async () => {
  // Phase 4: connect to backend API
  return [
    { id: 'deepseek-chat', name: 'DeepSeek-V3', provider: 'deepseek', plan: 'Coding Plan' },
    { id: 'deepseek-reasoner', name: 'DeepSeek-R1', provider: 'deepseek', plan: 'Reasoning Plan' },
    { id: 'glm-4-plus', name: 'GLM-4-Plus', provider: 'zhipu', plan: 'Coding Plan' },
    { id: 'glm-4-flash', name: 'GLM-4-Flash', provider: 'zhipu', plan: 'Token Plan (free)' },
    { id: 'moonshot-v1-8k', name: 'Moonshot v1 8K', provider: 'kimi', plan: 'Coding Plan' },
    { id: 'moonshot-v1-128k', name: 'Moonshot v1 128K', provider: 'kimi', plan: 'Token Plan' },
    { id: 'auto', name: 'OpenRouter Auto', provider: 'openrouter', plan: 'Auto Router' },
    { id: 'free', name: 'OpenRouter Free', provider: 'openrouter', plan: 'Free Tier' },
    { id: 'anthropic/claude-sonnet-4', name: 'Claude Sonnet 4', provider: 'openrouter', plan: 'Coding Plan' },
    { id: 'openai/gpt-4o', name: 'GPT-4o', provider: 'openrouter', plan: 'Token Plan' },
    { id: 'google/gemini-2.0-flash-exp:free', name: 'Gemini 2.0 Flash', provider: 'openrouter', plan: 'Free Tier' },
  ];
});

ipcMain.handle('model:refresh', async () => {
  return { success: true, message: 'Model list refreshed' };
});

app.whenReady().then(() => {
  startBackend();
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
