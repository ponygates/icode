const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('icode', {
  // App info
  getVersion: () => ipcRenderer.invoke('app:getVersion'),
  getPlatform: () => ipcRenderer.invoke('app:getPlatform'),

  // External links
  openExternal: (url) => ipcRenderer.invoke('app:openExternal', url),

  // Model management
  listModels: () => ipcRenderer.invoke('model:list'),
  refreshModels: () => ipcRenderer.invoke('model:refresh'),

  // Clipboard (for copy functionality)
  copyToClipboard: (text) => ipcRenderer.invoke('clipboard:write', text),
});
