// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useModelFetch } from '../useModelFetch';

// Stub i18next entirely so the store's i18n bootstrap does not initialize a
// real instance inside jsdom. Keys are returned verbatim, which is also handy
// for asserting *which* message was produced.
vi.mock('i18next', () => ({
  default: { use: () => ({ init: () => {} }), t: (key: string) => key },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

const BASE = 'http://127.0.0.1:8899';

interface Call { url: string; init?: RequestInit }
let calls: Call[] = [];

function jsonRes(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

/** Default vendor response; individual tests override `plan` to change shapes. */
let plan: (url: string, init?: RequestInit) => Response = () => jsonRes({}, 404);

const liveModels = [
  { id: 'deepseek-v4-flash', name: 'DeepSeek V4 Flash', known: true, enabled: true, context_window: 128000 },
  { id: 'deepseek-v4', name: 'DeepSeek V4', known: true, enabled: true, context_window: 128000 },
  { id: 'deepseek-r1', name: 'DeepSeek R1', known: false, enabled: true, context_window: 64000 },
];

function defaultPlan(url: string, init?: RequestInit): Response {
  if (url.includes('/api/providers')) {
    return jsonRes([{ name: 'deepseek', models: 3, enabled: 3, filtered: false, cache_support: true }]);
  }
  if (url.includes('/api/models/fetch')) {
    return jsonRes({ provider: 'deepseek', count: 3, filtered: false, models: liveModels });
  }
  if (url.includes('/api/models/selection')) {
    return jsonRes({ ok: true, enabled: 3, registered: 1 });
  }
  return jsonRes({}, 404);
}

beforeEach(() => {
  calls = [];
  plan = defaultPlan;
  global.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    return plan(url, init);
  }) as unknown as typeof fetch;
});

afterEach(() => {
  vi.restoreAllMocks();
});

const selCalls = () => calls.filter(c => c.url.includes('/api/models/selection'));

describe('useModelFetch', () => {
  it('loads per-vendor meta on mount so cards can badge "N/M enabled"', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await waitFor(() => expect(result.current.meta.deepseek).toBeTruthy());
    expect(result.current.meta.deepseek).toEqual({ enabled: 3, models: 3, filtered: false });
  });

  it('fetches the live catalogue and pre-ticks everything when no filter is set', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });

    expect(calls.some(c => c.url === `${BASE}/api/models/fetch?provider=deepseek`)).toBe(true);
    expect(result.current.panel).toBe('deepseek');
    expect(result.current.lists.deepseek.map(m => m.id))
      .toEqual(['deepseek-v4-flash', 'deepseek-v4', 'deepseek-r1']);
    // Unset filter restricts nothing, so everything comes back enabled.
    expect(result.current.ticked.deepseek).toHaveLength(3);
    expect(result.current.error.deepseek).toBeFalsy();
  });

  it('pre-ticks only the already-enabled models when a filter exists', async () => {
    plan = (url) => {
      if (url.includes('/api/models/fetch')) {
        return jsonRes({
          provider: 'deepseek', count: 3, filtered: true,
          models: [
            { id: 'deepseek-v4-flash', name: 'V4 Flash', known: true, enabled: true },
            { id: 'deepseek-v4', name: 'V4', known: true, enabled: false },
            { id: 'deepseek-r1', name: 'R1', known: true, enabled: false },
          ],
        });
      }
      return defaultPlan(url);
    };
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });

    expect(result.current.ticked.deepseek).toEqual(['deepseek-v4-flash']);
  });

  it('calls the vendor again only on the first open, then reuses the cached list', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });
    const first = calls.filter(c => c.url.includes('/api/models/fetch')).length;

    act(() => { result.current.close(); });          // close the panel
    await act(async () => { await result.current.fetchModels('deepseek'); });   // reopen

    expect(result.current.panel).toBe('deepseek');
    expect(calls.filter(c => c.url.includes('/api/models/fetch')).length)
      .toBe(first);
  });

  it('toggles the panel shut when the same vendor is fetched twice', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });
    expect(result.current.panel).toBe('deepseek');
    await act(async () => { await result.current.fetchModels('deepseek'); });
    expect(result.current.panel).toBeNull();
  });

  it('saves only the ticked subset to /api/models/selection', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });
    act(() => { result.current.toggle('deepseek', 'deepseek-v4'); });   // untick one

    await act(async () => { await result.current.save('deepseek'); });

    const put = selCalls()[0];
    expect(put?.init?.method).toBe('PUT');
    expect(JSON.parse(String(put.init.body)))
      .toEqual({ provider: 'deepseek', models: ['deepseek-v4-flash', 'deepseek-r1'] });
    expect(result.current.panel).toBeNull();
  });

  it('select-all / select-none drive the whole list', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });

    act(() => { result.current.selectAll('deepseek', false); });
    expect(result.current.ticked.deepseek).toEqual([]);

    act(() => { result.current.selectAll('deepseek', true); });
    expect(result.current.ticked.deepseek).toHaveLength(3);
  });

  it('refuses an empty selection client-side instead of re-enabling the catalogue', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });
    act(() => { result.current.selectAll('deepseek', false); });

    await act(async () => { await result.current.save('deepseek'); });

    // An empty filter would be stored as "no restriction" — the exact opposite
    // of the intent — so the request must never be sent.
    expect(selCalls()).toHaveLength(0);
    expect(result.current.error.deepseek).toBe('models.selectAtLeastOne');
    expect(result.current.panel).toBe('deepseek');
  });

  it('surfaces the backend message when the vendor rejects the key', async () => {
    plan = (url) => {
      if (url.includes('/api/models/fetch')) return jsonRes({ error: 'HTTP 401 unauthorized' }, 502);
      return defaultPlan(url);
    };
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });

    expect(result.current.error.deepseek).toBe('HTTP 401 unauthorized');
    expect(result.current.panel).toBeNull();
  });

  it('reports an empty catalogue as an error rather than an empty checklist', async () => {
    plan = (url) => {
      if (url.includes('/api/models/fetch')) return jsonRes({ provider: 'deepseek', count: 0, models: [] });
      return defaultPlan(url);
    };
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });

    expect(result.current.error.deepseek).toBe('models.fetchEmpty');
    expect(result.current.panel).toBeNull();
  });

  it('propagates a save failure without closing the panel', async () => {
    plan = (url, init) => {
      if (url.includes('/api/models/selection')) return jsonRes({ error: 'disk full' }, 500);
      return defaultPlan(url, init);
    };
    const { result } = renderHook(() => useModelFetch(BASE));
    await act(async () => { await result.current.fetchModels('deepseek'); });
    await act(async () => { await result.current.save('deepseek'); });

    expect(result.current.error.deepseek).toBe('disk full');
    expect(result.current.panel).toBe('deepseek');
    expect(result.current.saving).toBe(false);
  });

  it('drops the stale cached list after a save and reloads meta', async () => {
    const { result } = renderHook(() => useModelFetch(BASE));
    await waitFor(() => expect(result.current.meta.deepseek).toBeTruthy());
    await act(async () => { await result.current.fetchModels('deepseek'); });
    await act(async () => { await result.current.save('deepseek'); });

    // Its `enabled` flags predate the save, so it must not be reused.
    expect(result.current.lists.deepseek).toBeUndefined();
    const metaCalls = calls.filter(c => c.url.includes('/api/providers')).length;
    expect(metaCalls).toBeGreaterThan(1);
  });
});
