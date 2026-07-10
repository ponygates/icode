import { create } from 'zustand';

export interface Model {
  id: string;
  name: string;
  provider: string;
  plan: string;
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
  { id: 'deepseek-v4-flash', name: 'DeepSeek V4 Flash', provider: 'scnet', plan: 'tokenplan' },
  { id: 'deepseek-v4-pro', name: 'DeepSeek V4 Pro', provider: 'scnet', plan: 'tokenplan' },
  { id: 'auto', name: 'OpenRouter Auto', provider: 'openrouter', plan: 'Auto Router' },
  { id: 'free', name: 'OpenRouter Free', provider: 'openrouter', plan: 'Free Tier' },
  { id: 'anthropic/claude-sonnet-4', name: 'Claude Sonnet 4', provider: 'openrouter', plan: 'Coding Plan' },
  { id: 'openai/gpt-4o', name: 'GPT-4o', provider: 'openrouter', plan: 'Token Plan' },
  { id: 'google/gemini-2.0-flash-exp:free', name: 'Gemini 2.0 Flash', provider: 'openrouter', plan: 'Free Tier' },
];

export const useAppStore = create<AppStore>((set, get) => ({
  language: 'zh-CN',
  setLanguage: (lang) => set({ language: lang }),

  models: defaultModels,
  selectedModel: 'deepseek-v4-flash',
  setModels: (models) => set({ models }),
  setSelectedModel: (id) => set({ selectedModel: id }),

  async refreshModels() {
    // In Electron mode, query backend
    if (window.icode && window.icode.listModels) {
      const models = await window.icode.listModels();
      set({ models });
    }
  },

  sessions: [],
  activeSessionId: null,

  createSession: async (modelId, provider) => {
    const session: Session = {
      id: Date.now().toString(36) + Math.random().toString(36).slice(2, 7),
      title: `Session ${get().sessions.length + 1}`,
      messages: [],
      modelId,
      provider,
      createdAt: Date.now(),
    };

    // Persist to backend
    if (window.icode && window.icode.createSession) {
      await window.icode.createSession({
        id: session.id,
        title: session.title,
        model_id: modelId,
        provider_name: provider,
        created_at: new Date(session.createdAt).toISOString(),
      });
    }
    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: session.id,
    }));
  },

  setActiveSession: (id) => set({ activeSessionId: id }),
  deleteSession: (id) => {
    set((state) => ({
      sessions: state.sessions.filter((s) => s.id !== id),
      activeSessionId: state.activeSessionId === id ? null : state.activeSessionId,
    }));
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
