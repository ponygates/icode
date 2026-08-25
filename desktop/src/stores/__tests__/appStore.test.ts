import { describe, it, expect, beforeEach } from 'vitest';
import { useAppStore, type Model, type Session } from '../appStore';

function resetStore() {
  useAppStore.setState({
    language: 'zh-CN',
    securityLevel: 'local',
    mode: 'auto',
    backendUrl: null,
    backendConnected: false,
    backendChecking: false,
    autostart: false,
    serverPort: 0,
    models: [],
    selectedModel: 'openrouter/free',
    sessions: [],
    activeSessionId: null,
    openTabIds: [],
    workspaces: [],
    activeWorkspaceId: null,
    tokenUsage: { input: 0, output: 0, cacheHit: 0, cost: '', saved: 0 },
    customModels: [],
    refreshSummary: null,
  } as Partial<ReturnType<typeof useAppStore.getState>>);
}

describe('appStore', () => {
  beforeEach(() => {
    resetStore();
  });

  it('has sane defaults', () => {
    const s = useAppStore.getState();
    expect(s.language).toBe('zh-CN');
    expect(s.securityLevel).toBe('local');
    expect(s.mode).toBe('auto');
    expect(s.selectedModel).toBe('openrouter/free');
    expect(s.sessions).toEqual([]);
    expect(s.models).toEqual([]);
  });

  it('setModels / setSelectedModel update state', () => {
    const model: Model = { id: 'm1', name: 'M1', provider: 'p1', plan: 'free' };
    useAppStore.getState().setModels([model]);
    useAppStore.getState().setSelectedModel('m1');
    const s = useAppStore.getState();
    expect(s.models).toEqual([model]);
    expect(s.selectedModel).toBe('m1');
  });

  it('setSelectedModel with a new model id propagates', () => {
    useAppStore.getState().setSelectedModel('deepseek-v4-flash');
    expect(useAppStore.getState().selectedModel).toBe('deepseek-v4-flash');
  });

  it('setLanguage updates state and persists to localStorage', () => {
    useAppStore.getState().setLanguage('en');
    expect(useAppStore.getState().language).toBe('en');
    expect(localStorage.getItem('icode.language')).toBe('en');
  });

  it('setSecurityLevel updates state and persists to localStorage', () => {
    useAppStore.getState().setSecurityLevel('local');
    expect(useAppStore.getState().securityLevel).toBe('local');
    expect(localStorage.getItem('icode.securityLevel')).toBe('local');
  });

  it('setBackendUrl updates connection target', () => {
    useAppStore.getState().setBackendUrl('http://127.0.0.1:57356');
    expect(useAppStore.getState().backendUrl).toBe('http://127.0.0.1:57356');
  });

  it('setAutostart / setServerPort update desktop settings without a backend', () => {
    // backendUrl is null in tests, so these must not throw (fetch is skipped).
    useAppStore.getState().setAutostart(true);
    useAppStore.getState().setServerPort(57356);
    const s = useAppStore.getState();
    expect(s.autostart).toBe(true);
    expect(s.serverPort).toBe(57356);
  });

  it('createSession appends a session, activates it and opens a tab', () => {
    useAppStore.getState().createSession('deepseek-v4-flash', 'deepseek');
    const s = useAppStore.getState();
    expect(s.sessions).toHaveLength(1);
    expect(s.activeSessionId).toBe(s.sessions[0].id);
    expect(s.openTabIds).toEqual([s.sessions[0].id]);
    expect(s.sessions[0].modelId).toBe('deepseek-v4-flash');
    expect(s.sessions[0].provider).toBe('deepseek');
  });

  it('createSession generates unique ids and increments titles', () => {
    useAppStore.getState().createSession('a', 'p');
    useAppStore.getState().createSession('b', 'q');
    const s = useAppStore.getState();
    expect(s.sessions).toHaveLength(2);
    expect(s.sessions[0].id).not.toBe(s.sessions[1].id);
    expect(s.sessions[1].title).toBe('Session 2');
  });

  it('deleteSession removes the session and closes its tab', () => {
    useAppStore.getState().createSession('a', 'p');
    useAppStore.getState().createSession('b', 'q');
    const s0 = useAppStore.getState();
    const firstId = s0.sessions[0].id;
    const secondId = s0.sessions[1].id;
    useAppStore.getState().deleteSession(firstId);
    const s = useAppStore.getState();
    expect(s.sessions.map((x) => x.id)).toEqual([secondId]);
    expect(s.openTabIds).not.toContain(firstId);
  });

  it('renameSession updates the title in place', () => {
    useAppStore.getState().createSession('a', 'p');
    const id = useAppStore.getState().sessions[0].id;
    useAppStore.getState().renameSession(id, 'My renamed chat');
    expect(useAppStore.getState().sessions[0].title).toBe('My renamed chat');
  });

  it('addMessage / clearMessages manage the transcript', () => {
    useAppStore.getState().createSession('a', 'p');
    const id = useAppStore.getState().sessions[0].id;
    const msg = { id: 'm1', role: 'user', content: 'hi' } as Session['messages'][number];
    useAppStore.getState().addMessage(id, msg);
    expect(useAppStore.getState().sessions[0].messages).toEqual([msg]);
    useAppStore.getState().clearMessages(id);
    expect(useAppStore.getState().sessions[0].messages).toEqual([]);
  });

  it('updateTokenUsage merges partial usage', () => {
    useAppStore.getState().updateTokenUsage({ input: 100, output: 50 });
    const s = useAppStore.getState();
    expect(s.tokenUsage.input).toBe(100);
    expect(s.tokenUsage.output).toBe(50);
    useAppStore.getState().updateTokenUsage({ input: 150 });
    expect(useAppStore.getState().tokenUsage.input).toBe(150);
    expect(useAppStore.getState().tokenUsage.output).toBe(50);
  });

  it('addCustomModel / removeCustomModel call the backend', async () => {
    const calls: Array<{ method: string; url: string }> = [];
    (globalThis as any).fetch = async (url: string, opts?: RequestInit) => {
      calls.push({ method: opts?.method || 'GET', url: String(url) });
      const okBody = JSON.stringify({ ok: true });
      const modelsBody = JSON.stringify({ models: [] });
      return {
        ok: true,
        status: 200,
        json: async () => (String(url).includes('/api/config/model') && opts?.method === 'GET' ? modelsBody : okBody),
      } as Response;
    };
    try {
      useAppStore.setState({ backendUrl: 'http://localhost:PORT' });
      const model: Model = { id: 'custom-x', name: 'Custom X', provider: 'self', plan: 'own' };
      const addErr = await useAppStore.getState().addCustomModel(model);
      expect(addErr).toBeNull();
      expect(calls).toContainEqual({ method: 'PUT', url: 'http://localhost:PORT/api/config/model' });
      const rmErr = await useAppStore.getState().removeCustomModel('custom-x', 'self');
      expect(rmErr).toBeNull();
      expect(calls).toContainEqual({ method: 'DELETE', url: `http://localhost:PORT/api/config/model?id=${encodeURIComponent('self/custom-x')}` });
    } finally {
      delete (globalThis as any).fetch;
      useAppStore.setState({ backendUrl: null });
    }
  });
});
