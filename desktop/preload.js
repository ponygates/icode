const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('icode', {
  // App info
  getVersion: () => ipcRenderer.invoke('app:getVersion'),
  getPlatform: () => ipcRenderer.invoke('app:getPlatform'),
  getBackendPort: () => ipcRenderer.invoke('app:getBackendPort'),
  getBackendURL: () => ipcRenderer.invoke('app:getBackendURL'),
  openExternal: (url) => ipcRenderer.invoke('app:openExternal', url),

  // Models
  listModels: () => ipcRenderer.invoke('model:list'),
  refreshModels: () => ipcRenderer.invoke('model:refresh'),

  // Sessions
  listSessions: () => ipcRenderer.invoke('session:list'),
  getSession: (id) => ipcRenderer.invoke('session:get', id),
  createSession: (session) => ipcRenderer.invoke('session:create', session),
  deleteSession: (id) => ipcRenderer.invoke('session:delete', id),

  // Chat
  sendMessage: (sessionId, content) =>
    ipcRenderer.invoke('chat:send', { sessionId, content }),
  stopChat: (sessionId) =>
    ipcRenderer.invoke('chat:stop', sessionId),
  onChatStream: (callback) => {
    const handler = (_, event) => callback(event);
    ipcRenderer.on('chat:stream', handler);
    return () => ipcRenderer.removeListener('chat:stream', handler);
  },

  // Config
  getConfig: () => ipcRenderer.invoke('config:get'),
  setConfig: (cfg) => ipcRenderer.invoke('config:set', cfg),
  setLanguage: (lang) => ipcRenderer.invoke('config:setLang', lang),
  setApiKey: (provider, apiKey, apiBase) =>
    ipcRenderer.invoke('config:setApiKey', { provider, apiKey, apiBase }),
  listKeys: () => ipcRenderer.invoke('config:listKeys'),

  // Permission
  setPermissionMode: (mode) => ipcRenderer.invoke('permission:setMode', mode),
});
