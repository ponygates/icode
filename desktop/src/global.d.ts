// Global type declaration for the iCode Electron preload bridge (window.icode).
// Exposes the native API injected by preload.js into the renderer.

interface ICodeAPI {
  getVersion: () => Promise<string>;
  getPlatform: () => Promise<string>;
  getBackendPort: () => Promise<number | null>;
  getBackendURL: () => Promise<string | null>;
  openExternal: (url: string) => Promise<void>;

  listModels: () => Promise<any>;
  refreshModels: () => Promise<any>;

  listSessions: () => Promise<any>;
  getSession: (id: string) => Promise<any>;
  createSession: (session: any) => Promise<any>;
  deleteSession: (id: string) => Promise<any>;

  sendMessage: (sessionId: string, content: string) => Promise<any>;
  stopChat: (sessionId: string) => Promise<any>;
  onChatStream: (callback: (event: any) => void) => () => void;

  getConfig: () => Promise<any>;
  setConfig: (cfg: any) => Promise<any>;
  setLanguage: (lang: string) => Promise<any>;
  setApiKey: (provider: string, apiKey: string, apiBase?: string) => Promise<any>;
  listKeys: () => Promise<any>;
  setPermissionMode: (mode: string) => Promise<any>;
}

declare global {
  interface Window {
    icode: ICodeAPI;
  }
}

export {};
