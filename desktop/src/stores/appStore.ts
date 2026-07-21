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

export interface Message {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  timestamp: number;
}

export interface Session {
  id: string;
  title: string;
  messages: Message[];
  modelId: string;
  provider: string;
  createdAt: number;
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
  setBackendUrl: (url: string | null) => void;
  checkBackend: () => Promise<void>;

  // Models
  models: Model[];
  selectedModel: string;
  setModels: (models: Model[]) => void;
  setSelectedModel: (id: string) => void;
  refreshModels: () => Promise<void>;

  // Sessions
  sessions: Session[];
  activeSessionId: string | null;
  createSession: (modelId: string, provider: string) => void;
  setActiveSession: (id: string) => void;
  deleteSession: (id: string) => void;
  loadSessions: () => Promise<void>;

  // Messages
  addMessage: (sessionId: string, msg: Message) => void;
  updateMessage: (sessionId: string, msg: Message) => void;
  clearMessages: (sessionId: string) => void;

  // Token stats
  tokenUsage: { input: number; output: number; cacheHit: number; cost: string };
  updateTokenUsage: (usage: Partial<AppStore['tokenUsage']>) => void;
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
  setBackendUrl: (url) => set({ backendUrl: url }),
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
            set({ backendConnected: true, backendChecking: false });
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
            set({ backendUrl: origin, backendConnected: true, backendChecking: false });
            return;
          }
        } catch { /* not served by same origin */ }
      }

      // Strategy 3: Try localhost discovery (common dev scenario)
      for (const port of [57356, 8080, 3000]) {
        try {
          const lr = await fetch(`http://127.0.0.1:${port}/api/health`, { method: 'GET', cache: 'no-cache' });
          if (lr.ok) {
            set({ backendUrl: `http://127.0.0.1:${port}`, backendConnected: true, backendChecking: false });
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
        set({ sessions: local, activeSessionId: local[local.length - 1].id });
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

    // Persist to backend (fire-and-forget — don't block state update)
    const { backendUrl } = get();
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
    }));
  },

  setActiveSession: (id) => set({ activeSessionId: id }),
  deleteSession: (id) => {
    set((state) => {
      const remaining = state.sessions.filter((s) => s.id !== id);
      // If we just deleted the active session, switch to the most recent one.
      let nextId = state.activeSessionId;
      if (nextId === id) {
        nextId = remaining.length > 0 ? remaining[remaining.length - 1].id : null;
      }
      return { sessions: remaining, activeSessionId: nextId };
    });
    // Also delete from backend
    const { backendUrl } = get();
    if (backendUrl) {
      fetch(`${backendUrl}/api/sessions/${id}`, { method: 'DELETE' }).catch(() => {});
    }
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

  tokenUsage: { input: 0, output: 0, cacheHit: 0, cost: '¥0.00' },
  updateTokenUsage: (usage) => {
    set((state) => ({
      tokenUsage: { ...state.tokenUsage, ...usage },
    }));
  },
}));

// Auto-persist sessions to localStorage on every change
useAppStore.subscribe((state) => {
  saveToLocal(state.sessions || []);
});

// ── localStorage persistence (fallback when backend is unavailable) ──

const LS_KEY = 'icode.sessions';

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
