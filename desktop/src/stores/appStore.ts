import { create } from 'zustand';
import i18n from '../i18n';

export interface Model {
  id: string;
  name: string;
  provider: string;
  plan: string;
  plans?: Array<{
    name: string;
    description?: string;
    inputPrice?: number;
    outputPrice?: number;
    cachePrice?: number;
  }>;
  contextWindow?: number;
  maxOutputTokens?: number;
  capabilities?: {
    tools?: boolean;
    streaming?: boolean;
    jsonMode?: boolean;
  };
  supportsVision?: boolean;
  freeTier?: boolean;
  description?: string;
  updatedAt?: number;
  icon?: string;
  category?: string;
  tags?: string[];
}

export interface Attachment {
  type?: string;       // "image" | "pdf" | ...
  mime?: string;       // image/png, image/jpeg, ...
  data?: string;       // base64-encoded content (no data: prefix)
  url?: string;        // optional external URL
  alt_text?: string;   // optional description
}

export interface Message {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  timestamp: number;
  attachments?: Attachment[];
}

export interface Session {
  id: string;
  title: string;
  messages: Message[];
  modelId: string;
  provider: string;
  createdAt: number;
}

export interface Workspace {
  id: string;
  name: string;
  path: string;
  session_ids: string[];
  created_at: string;
  updated_at: string;
}

interface AppStore {
  // Language
  language: string;
  setLanguage: (lang: string) => void;

  // Security level (privacy boundary)
  securityLevel: string;
  setSecurityLevel: (level: string) => void;
  loadSecurityLevel: () => Promise<void>;

  // Permission mode (plan / agent / auto / yolo) — shared across components
  mode: string;
  setMode: (mode: string) => Promise<void>;
  fetchMode: () => Promise<void>;

  // Backend connection status
  backendUrl: string | null;
  backendConnected: boolean;
  backendChecking: boolean;
  backendVersion: string;
  setBackendUrl: (url: string | null) => void;
  checkBackend: () => Promise<void>;

  // Desktop settings (launch-on-login + fixed backend port)
  autostart: boolean;
  serverPort: number;
  setAutostart: (enabled: boolean) => void;
  setServerPort: (port: number) => void;
  loadDesktopSettings: () => Promise<void>;

  // Models
  models: Model[];
  selectedModel: string;
  setModels: (models: Model[]) => void;
  setSelectedModel: (id: string) => void;
  refreshModels: () => Promise<void>;

  // Sessions
  sessions: Session[];
  activeSessionId: string | null;
  // Open tabs (multi-session) — kept in the store (not ChatPage local state)
  // so they survive route changes and restarts.
  openTabIds: string[];
  createSession: (modelId: string, provider: string) => void;
  setActiveSession: (id: string) => void;
  deleteSession: (id: string) => void;
  closeTab: (id: string) => void;
  renameSession: (id: string, title: string) => void;
  loadSessions: () => Promise<void>;

  // Workspaces (project containers grouping sessions)
  workspaces: Workspace[];
  activeWorkspaceId: string | null;
  loadWorkspaces: () => Promise<void>;
  createWorkspace: (name: string, path: string) => Promise<void>;
  setActiveWorkspace: (id: string) => void;

  // Messages
  addMessage: (sessionId: string, msg: Message) => void;
  updateMessage: (sessionId: string, msg: Message) => void;
  clearMessages: (sessionId: string) => void;

  // Token stats
  tokenUsage: { input: number; output: number; cacheHit: number; cost: string; saved: number };
  updateTokenUsage: (usage: Partial<AppStore['tokenUsage']>) => void;

  // Custom models (user-added providers)
  customModels: Model[];
  addCustomModel: (model: Model) => void;
  removeCustomModel: (modelId: string) => void;
}

const defaultModels: Model[] = [
  { id: 'deepseek-v4-flash', name: 'DeepSeek V4 Flash', provider: 'deepseek', plan: 'Coding Plan' },
  { id: 'deepseek-v4-pro', name: 'DeepSeek V4 Pro', provider: 'deepseek', plan: 'Reasoning Plan' },
  { id: 'glm-5', name: 'GLM-5', provider: 'zhipu', plan: 'Coding Plan' },
  { id: 'glm-4-flash', name: 'GLM-4 Flash', provider: 'zhipu', plan: 'Free Plan' },
  { id: 'kimi-k2.7-code', name: 'Kimi K2.7 Code', provider: 'kimi', plan: 'Coding Plan' },
  { id: 'kimi-k2.6', name: 'Kimi K2.6', provider: 'kimi', plan: 'Token Plan' },
  { id: 'doubao-seed-2.1-pro', name: '豆包 Seed 2.1 Pro', provider: 'volcengine', plan: 'Coding Plan' },
  { id: 'doubao-seed-2.1-turbo', name: '豆包 Seed 2.1 Turbo', provider: 'volcengine', plan: 'Token Plan' },
  { id: 'hunyuan-turbos', name: '混元 TurboS', provider: 'tencent', plan: 'Coding Plan (free)' },
  { id: 'hunyuan-t1', name: '混元 T1', provider: 'tencent', plan: 'Reasoning Plan' },
  { id: 'pangu-5.0-pro', name: '盘古 5.0 Pro', provider: 'huawei', plan: 'Coding Plan' },
  { id: 'pangu-5.0-code', name: '盘古 5.0 Code', provider: 'huawei', plan: 'Code Plan' },
  { id: 'scnet-code', name: 'SCNET Code Pro', provider: 'scnet', plan: 'tokenplan' },
  { id: 'MiniMax-m2.5', name: 'MiniMax M2.5', provider: 'scnet', plan: 'codingplan' },
  { id: 'scnet-deepseek-v4-flash', name: 'DeepSeek V4 Flash', provider: 'scnet', plan: 'tokenplan' },
  { id: 'scnet-deepseek-v4-pro', name: 'DeepSeek V4 Pro', provider: 'scnet', plan: 'tokenplan' },
  { id: 'auto', name: 'OpenRouter Auto', provider: 'openrouter', plan: 'Auto Router' },
  { id: 'openrouter/free', name: 'OpenRouter Free', provider: 'openrouter', plan: 'Free Tier' },
  { id: 'anthropic/claude-sonnet-4', name: 'Claude Sonnet 4', provider: 'openrouter', plan: 'Coding Plan' },
  { id: 'openai/gpt-4o', name: 'GPT-4o', provider: 'openrouter', plan: 'Token Plan' },
  { id: 'google/gemini-2.0-flash-exp:free', name: 'Gemini 2.0 Flash', provider: 'openrouter', plan: 'Free Tier' },
];

export const useAppStore = create<AppStore>((set, get) => ({
  language: 'zh-CN',
  setLanguage: (lang) => {
    set({ language: lang });
    i18n.changeLanguage(lang);
    localStorage.setItem('icode.language', lang);
  },

  // Security level — defaults to "local" (safest). iCode NEVER sends
  // telemetry or usage data anywhere, regardless of level.
  securityLevel: 'local',
  setSecurityLevel: (level) => {
    set({ securityLevel: level });
    localStorage.setItem('icode.securityLevel', level);
  },
  loadSecurityLevel: async () => {
    try {
      const res = await window.icode?.getSecurityLevel?.();
      if (res?.level) set({ securityLevel: res.level });
    } catch { /* ignore — keep default */ }
  },

  // Permission mode — synced bidirectionally with backend
  mode: 'auto',
  setMode: async (mode) => {
    set({ mode });
    const url = get().backendUrl;
    if (url) {
      try {
        await fetch(`${url}/api/config`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ defaults: { mode } }),
        });
        // Also update the backend permission gate directly
        await fetch(`${url}/api/permission/mode`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ mode }),
        });
      } catch { /* retry on next toggle */ }
    }
  },
  fetchMode: async () => {
    const url = get().backendUrl;
    if (url) {
      try {
        const res = await fetch(`${url}/api/config`);
        if (res.ok) {
          const cfg = await res.json();
          if (cfg?.defaults?.mode) set({ mode: cfg.defaults.mode });
        }
      } catch {}
    }
  },

  // Backend connection status
  backendUrl: null,
  backendConnected: false,
  backendChecking: true,
  backendVersion: '',
  setBackendUrl: (url) => set({ backendUrl: url }),

  // Desktop settings — kept in sync with the backend config (/api/config).
  autostart: false,
  serverPort: 0,
  setAutostart: (enabled) => {
    set({ autostart: enabled });
    const url = get().backendUrl;
    if (url) {
      fetch(`${url}/api/config`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ autostart: enabled }),
      }).catch(() => { /* retry on next toggle */ });
    }
  },
  setServerPort: (port) => {
    set({ serverPort: port });
    const url = get().backendUrl;
    if (url) {
      fetch(`${url}/api/config`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ server: { port } }),
      }).catch(() => { /* retry on next change */ });
    }
  },
  loadDesktopSettings: async () => {
    const url = get().backendUrl;
    if (!url) return;
    try {
      const res = await fetch(`${url}/api/config`);
      if (res.ok) {
        const cfg = await res.json();
        if (typeof cfg?.autostart === 'boolean') set({ autostart: cfg.autostart });
        if (typeof cfg?.server?.port === 'number') set({ serverPort: cfg.server.port });
      }
    } catch { /* keep defaults */ }
  },

  checkBackend: async () => {
    set({ backendChecking: true });
    try {
      // Strategy 1: Electron IPC discovery (window.icode injected by preload)
      if (window.icode && window.icode.getBackendURL) {
        const url = await window.icode.getBackendURL();
        if (url) {
          set({ backendUrl: url });
          const res = await fetch(`${url}/api/health`, { method: 'GET', cache: 'no-cache' });
          if (res.ok) {
            const health = await res.json().catch(() => ({}));
            set({ backendConnected: true, backendChecking: false, backendVersion: health.version || '' });
            return;
          }
        }
      }

      // Strategy 2: Relative URL discovery (frontend served by Go backend)
      const origin = window.location.origin;
      if (origin && origin !== 'null' && !origin.startsWith('file://')) {
        try {
          const relRes = await fetch(`${origin}/api/health`, { method: 'GET', cache: 'no-cache' });
          if (relRes.ok) {
            const health = await relRes.json().catch(() => ({}));
            set({ backendUrl: origin, backendConnected: true, backendChecking: false, backendVersion: health.version || '' });
            return;
          }
        } catch { /* not served by same origin */ }
      }

      // Strategy 3: Try localhost discovery (common dev scenario)
      for (const port of [57356, 8080, 3000]) {
        try {
          const lr = await fetch(`http://127.0.0.1:${port}/api/health`, { method: 'GET', cache: 'no-cache' });
          if (lr.ok) {
            const health = await lr.json().catch(() => ({}));
            set({ backendUrl: `http://127.0.0.1:${port}`, backendConnected: true, backendChecking: false, backendVersion: health.version || '' });
            return;
          }
        } catch { /* try next port */ }
      }

      set({ backendConnected: false, backendChecking: false });
    } catch {
      set({ backendConnected: false, backendChecking: false });
    }
  },

  models: defaultModels,
  selectedModel: 'openrouter/free',
  setModels: (models) => set({ models }),
  setSelectedModel: (id) => set({ selectedModel: id }),

  async refreshModels() {
    // Try Electron IPC
    if (window.icode && window.icode.listModels) {
      const models = await window.icode.listModels();
      if (Array.isArray(models) && models.length > 0) {
        set({ models });
        return;
      }
    }
    // HTTP fallback (native WebView2 desktop)
    const { backendUrl } = get();
    if (backendUrl) {
      try {
        const res = await fetch(`${backendUrl}/api/models`, { cache: 'no-cache' });
        if (res.ok) {
          const data = await res.json();
          const list = data.models || data || [];
          if (Array.isArray(list) && list.length > 0) {
            set({ models: list });
          }
        }
      } catch { /* ignore */ }
    }
    // Also check backend health
    get().checkBackend();
  },

  sessions: [],
  activeSessionId: null,
  openTabIds: loadOpenTabs(),

  workspaces: [],
  activeWorkspaceId: loadActiveWorkspace(),

  loadSessions: async () => {
    try {
      // Try Electron IPC first
      if (window.icode && window.icode.listSessions) {
        const list = await window.icode.listSessions();
        if (Array.isArray(list) && list.length > 0) {
          const loaded: Session[] = list.map((s: any) => ({
            id: s.id,
            title: s.title || '会话',
            messages: (s.messages || []).map((m: any) => ({
              id: m.id || Math.random().toString(36).slice(2),
              role: m.role || 'assistant',
              content: m.content || '',
              attachments: m.attachments || [],
              timestamp: m.timestamp ? new Date(m.timestamp).getTime() : Date.now(),
            })),
            modelId: s.model_id || 'openrouter/free',
            provider: s.provider_name || 'openrouter',
            createdAt: s.created_at ? new Date(s.created_at).getTime() : Date.now(),
          }));
          set((state) => ({
            sessions: loaded,
            openTabIds: state.openTabIds.length > 0 ? state.openTabIds : loaded.map((s) => s.id),
          }));
          saveToLocal(loaded);
          if (loaded.length > 0) {
            set({ activeSessionId: loaded[loaded.length - 1].id });
          }
          return;
        }
      }

      // HTTP fallback (native WebView2 desktop)
      const { backendUrl } = get();
      if (backendUrl) {
        const res = await fetch(`${backendUrl}/api/sessions`, { cache: 'no-cache' });
        if (res.ok) {
          const data = await res.json();
          const list = data.sessions || data || [];
          if (Array.isArray(list) && list.length > 0) {
            const loaded: Session[] = list.map((s: any) => ({
              id: s.id,
              title: s.title || '会话',
              messages: (s.messages || []).map((m: any) => ({
                id: m.id || Math.random().toString(36).slice(2),
                role: m.role || 'assistant',
                content: m.content || '',
                timestamp: m.timestamp ? new Date(m.timestamp).getTime() : Date.now(),
              })),
              modelId: s.model_id || 'openrouter/free',
              provider: s.provider_name || 'openrouter',
              createdAt: s.created_at ? new Date(s.created_at).getTime() : Date.now(),
            }));
            set({ sessions: loaded });
            saveToLocal(loaded);
            if (loaded.length > 0) {
              set({ activeSessionId: loaded[loaded.length - 1].id });
            }
            return;
          }
        }
      }

      // Final fallback — always try localStorage, regardless of IPC/HTTP state
      const local = loadFromLocal();
      if (local.length > 0) {
        set((state) => ({
          sessions: local,
          openTabIds: state.openTabIds.length > 0 ? state.openTabIds : local.map((s) => s.id),
          activeSessionId: local[local.length - 1].id,
        }));
      }
    } catch { /* ignore */ }
  },

  createSession: (modelId, provider) => {
    const session: Session = {
      id: Date.now().toString(36) + Math.random().toString(36).slice(2, 7),
      title: `Session ${get().sessions.length + 1}`,
      messages: [],
      modelId,
      provider,
      createdAt: Date.now(),
    };

    const { backendUrl, activeWorkspaceId } = get();

    // Deep binding: if a workspace is active, append the new session to it
    // (fire-and-forget — state update below is synchronous).
    if (activeWorkspaceId && backendUrl) {
      const wsId = activeWorkspaceId;
      fetch(`${backendUrl}/api/workspaces/${wsId}/sessions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: session.id }),
      }).catch(() => {});
    }

    // Persist to backend (fire-and-forget — don't block state update)
    if (window.icode && window.icode.createSession) {
      window.icode.createSession({
        id: session.id,
        title: session.title,
        model_id: modelId,
        provider_name: provider,
        created_at: new Date(session.createdAt).toISOString(),
      }).catch(() => {});
    } else if (backendUrl) {
      fetch(`${backendUrl}/api/sessions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          id: session.id,
          title: session.title,
          model_id: modelId,
          provider_name: provider,
          created_at: new Date(session.createdAt).toISOString(),
        }),
      }).catch(() => {});
    }
    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: session.id,
      openTabIds: state.openTabIds.includes(session.id)
        ? state.openTabIds
        : [...state.openTabIds, session.id],
      // Optimistic workspace membership update so the sidebar count reflects
      // the new session immediately (backend is the source of truth).
      workspaces: activeWorkspaceId
        ? state.workspaces.map((w) =>
            w.id === activeWorkspaceId && !w.session_ids.includes(session.id)
              ? { ...w, session_ids: [...w.session_ids, session.id] }
              : w
          )
        : state.workspaces,
    }));
  },

  setActiveSession: (id) => set((state) => ({
    activeSessionId: id,
    openTabIds: state.openTabIds.includes(id)
      ? state.openTabIds
      : [...state.openTabIds, id],
  })),

  renameSession: (id, title) => {
    const clean = (title || '').trim();
    // Update local state immediately for responsive UI.
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === id ? { ...s, title: clean || s.title } : s
      ),
    }));
    if (!clean) return;
    const { backendUrl } = get();
    if (backendUrl) {
      fetch(`${backendUrl}/api/sessions/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: clean }),
      }).catch(() => {});
    }
  },

  deleteSession: (id) => {
    set((state) => {
      const remaining = state.sessions.filter((s) => s.id !== id);
      // If we just deleted the active session, switch to the most recent one.
      let nextId = state.activeSessionId;
      if (nextId === id) {
        nextId = remaining.length > 0 ? remaining[remaining.length - 1].id : null;
      }
      return {
        sessions: remaining,
        activeSessionId: nextId,
        openTabIds: state.openTabIds.filter((t) => t !== id),
      };
    });
    // Also delete from backend
    const { backendUrl } = get();
    if (backendUrl) {
      fetch(`${backendUrl}/api/sessions/${id}`, { method: 'DELETE' }).catch(() => {});
    }
  },

  // closeTab removes a session from the open-tab strip WITHOUT deleting it.
  // If the last open tab is closed, a fresh session is spawned (which is
  // auto-bound to the active workspace), so the strip always has ≥1 entry.
  closeTab: (id) => {
    const state = get();
    const remaining = state.openTabIds.filter((t) => t !== id);
    if (remaining.length === 0) {
      const active = state.sessions.find((s) => s.id === state.activeSessionId);
      state.createSession(active?.modelId || state.selectedModel, active?.provider || 'openrouter');
      return;
    }
    set({ openTabIds: remaining });
    if (state.activeSessionId === id) {
      set({ activeSessionId: remaining[remaining.length - 1] });
    }
  },

  // ── Workspaces ──
  loadWorkspaces: async () => {
    const { backendUrl } = get();
    if (!backendUrl) return;
    try {
      const res = await fetch(`${backendUrl}/api/workspaces`, { cache: 'no-cache' });
      if (res.ok) {
        const data = await res.json();
        set({ workspaces: data.workspaces || [] });
      }
    } catch { /* ignore */ }
  },

  createWorkspace: async (name, path) => {
    const { backendUrl } = get();
    if (!backendUrl) return;
    try {
      const res = await fetch(`${backendUrl}/api/workspaces`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, path }),
      });
      if (res.ok) {
        await get().loadWorkspaces();
      }
    } catch { /* ignore */ }
  },

  setActiveWorkspace: (id) => {
    set({ activeWorkspaceId: id });
    const ws = get().workspaces.find((w) => w.id === id);
    // When switching into a workspace that already holds sessions, jump to
    // its most recent one (deep session↔workspace binding).
    if (ws && ws.session_ids.length > 0) {
      const last = ws.session_ids[ws.session_ids.length - 1];
      set((state) => ({
        activeSessionId: last,
        openTabIds: state.openTabIds.includes(last)
          ? state.openTabIds
          : [...state.openTabIds, last],
      }));
    }
    // Empty workspace: keep the current session so the user can start one
    // (createSession will auto-bind it to this workspace).
  },

  addMessage: (sessionId, msg) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId
          ? { ...s, messages: [...s.messages, msg] }
          : s
      ),
    }));
  },

  updateMessage: (sessionId, updated) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId
          ? {
              ...s,
              messages: s.messages.map((m) =>
                m.id === updated.id ? updated : m
              ),
            }
          : s
      ),
    }));
  },

  clearMessages: (sessionId) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId ? { ...s, messages: [] } : s
      ),
    }));
  },

  tokenUsage: { input: 0, output: 0, cacheHit: 0, cost: '¥0.00', saved: 0 },
  updateTokenUsage: (usage) => {
    set((state) => ({
      tokenUsage: { ...state.tokenUsage, ...usage },
    }));
  },

  // Custom models (user-added providers)
  customModels: loadCustomModels(),
  addCustomModel: (model) => {
    set((state) => {
      const updated = [...state.customModels, model];
      saveCustomModels(updated);
      return { customModels: updated, models: [...state.models, model] };
    });
  },
  removeCustomModel: (modelId) => {
    set((state) => {
      const updated = state.customModels.filter((m) => m.id !== modelId);
      saveCustomModels(updated);
      return {
        customModels: updated,
        models: state.models.filter((m) => m.id !== modelId),
      };
    });
  },
}));

// Auto-persist sessions + open tabs + active selection to localStorage so
// they survive route changes and restarts.
useAppStore.subscribe((state) => {
  saveToLocal(state.sessions || []);
  try {
    localStorage.setItem(LS_TABS, JSON.stringify(state.openTabIds || []));
    if (state.activeSessionId) localStorage.setItem(LS_ACTIVE, state.activeSessionId);
    if (state.activeWorkspaceId) localStorage.setItem(LS_WORKSPACE, state.activeWorkspaceId);
  } catch {}
});

// ── localStorage persistence (fallback when backend is unavailable) ──

const LS_KEY = 'icode.sessions';
const LS_TABS = 'icode.openTabIds';
const LS_ACTIVE = 'icode.activeSessionId';
const LS_WORKSPACE = 'icode.activeWorkspaceId';

function saveToLocal(sessions: Session[]) {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(sessions.slice(-50)));
  } catch {}
}

function loadFromLocal(): Session[] {
  try {
    const raw = localStorage.getItem(LS_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function loadOpenTabs(): string[] {
  try {
    const raw = localStorage.getItem(LS_TABS);
    const arr = raw ? JSON.parse(raw) : [];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

function loadActiveWorkspace(): string | null {
  try {
    const raw = localStorage.getItem(LS_WORKSPACE);
    return raw ? raw : null;
  } catch {
    return null;
  }
}

// ── Custom models persistence ──

const CUSTOM_MODELS_KEY = 'icode.customModels';

function saveCustomModels(models: Model[]) {
  try {
    localStorage.setItem(CUSTOM_MODELS_KEY, JSON.stringify(models));
  } catch {}
}

function loadCustomModels(): Model[] {
  try {
    const raw = localStorage.getItem(CUSTOM_MODELS_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}
