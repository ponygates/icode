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
  // Respond to an interactive permission request raised during streaming.
  respondPermission: (requestId, decision) =>
    ipcRenderer.invoke('chat:permissionRespond', { requestId, decision }),
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

  // MCP servers (Reasonix-style tool integration)
  listMCP: () => ipcRenderer.invoke('mcp:list'),
  addMCP: (cfg) => ipcRenderer.invoke('mcp:add', cfg),
  removeMCP: (name) => ipcRenderer.invoke('mcp:remove', name),
  testMCP: (cfg) => ipcRenderer.invoke('mcp:test', cfg),
  listMCPTools: () => ipcRenderer.invoke('mcp:tools'),

  // Custom models / presets
  listCustomModels: () => ipcRenderer.invoke('model:listCustom'),
  addCustomModel: (m) => ipcRenderer.invoke('model:add', m),
  deleteCustomModel: (id) => ipcRenderer.invoke('model:delete', id),
});
