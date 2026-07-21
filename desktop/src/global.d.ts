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
  getActiveSession: () => Promise<string | null>;

  sendMessage: (sessionId: string, content: string, model?: string, provider?: string) => Promise<any>;
  stopChat: (sessionId: string) => Promise<any>;
  onChatStream: (callback: (event: any) => void) => () => void;
  respondPermission: (requestId: string, decision: string) => Promise<any>;

  getConfig: () => Promise<any>;
  setConfig: (cfg: any) => Promise<any>;
  setLanguage: (lang: string) => Promise<any>;
  setApiKey: (provider: string, apiKey: string, apiBase?: string) => Promise<any>;
  listKeys: () => Promise<any>;
  setPermissionMode: (mode: string) => Promise<any>;

  listMCP: () => Promise<any>;
  addMCP: (cfg: any) => Promise<any>;
  removeMCP: (name: string) => Promise<any>;
  testMCP: (cfg: any) => Promise<any>;
  listMCPTools: () => Promise<any>;

  listCustomModels: () => Promise<any>;
  addCustomModel: (m: any) => Promise<any>;
  deleteCustomModel: (id: string) => Promise<any>;

  getSecurityLevel: () => Promise<{ level: string }>;
  setSecurityLevel: (level: string) => Promise<{ ok: boolean; error?: string }>;
}

declare global {
  interface Window {
    icode?: Partial<ICodeAPI>;
  }
}

export {};
