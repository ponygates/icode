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
/** Set >0 to make every mocked request take a real tick, so concurrency and
 *  overlap are observable rather than instantaneous. */
let latencyMs = 0;
let inFlight = 0;
let peakInFlight = 0;

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
  latencyMs = 0;
  inFlight = 0;
  peakInFlight = 0;
  globalThis.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    inFlight++;
    peakInFlight = Math.max(peakInFlight, inFlight);
    try {
      if (latencyMs > 0) await new Promise(r => setTimeout(r, latencyMs));
      return plan(url, init);
    } finally {
      inFlight--;
    }
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
    expect(JSON.parse(String(put?.init?.body)))
      .toEqual({
        provider: 'deepseek',
        models: ['deepseek-v4-flash', 'deepseek-r1'],
        // The vendor-reported window rides along so an auto-registered model
        // keeps the real figure instead of a zero.
        meta: {
          'deepseek-v4-flash': { context_window: 128000, max_output_tokens: 0 },
          'deepseek-r1': { context_window: 64000, max_output_tokens: 0 },
        },
      });
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

  // ── "获取全部厂商" sweep ──────────────────────────────────────────

  describe('fetchAll', () => {
    const providerNames = ['pa', 'pb', 'pc', 'pd'];

    /** pa/pd succeed, pb answers 501 (no live discovery), pc rejects the key. */
    function sweepPlan(url: string, init?: RequestInit): Response {
      if (url.includes('/api/providers')) {
        return jsonRes(providerNames.map(n => ({ name: n, models: 1, enabled: 1, filtered: false })));
      }
      if (url.includes('/api/models/fetch')) {
        const p = new URL(url).searchParams.get('provider');
        if (p === 'pb') return jsonRes({ error: 'pb 暂不支持实时获取模型' }, 501);
        if (p === 'pc') return jsonRes({ error: '401 invalid api key' }, 502);
        return jsonRes({
          provider: p, count: 1, filtered: false,
          models: [{ id: `${p}-1`, name: `${p} 1`, known: true, enabled: true }],
        });
      }
      return defaultPlan(url, init);
    }

    it('separates unsupported vendors from genuinely failed ones', async () => {
      plan = sweepPlan;
      const { result } = renderHook(() => useModelFetch(BASE));
      await waitFor(() => expect(Object.keys(result.current.meta)).toHaveLength(4));

      let summary: unknown = null;
      await act(async () => {
        summary = await result.current.fetchAll(providerNames);
      });

      // "pb has no live endpoint" and "pc rejected your key" demand different
      // reactions, so lumping them into one error count would mislead.
      expect(summary).toEqual({ total: 4, ok: 2, unsupported: 1, failed: 1 });
      expect(result.current.allSummary).toEqual(summary);
      expect(result.current.allProgress).toBeNull();
      expect(result.current.lists.pa).toHaveLength(1);
      expect(result.current.lists.pd).toHaveLength(1);
      expect(result.current.lists.pb).toBeUndefined();
      expect(result.current.error.pc).toBe('401 invalid api key');
    });

    it('never runs more than `concurrency` requests at once', async () => {
      plan = sweepPlan;
      latencyMs = 5;
      const { result } = renderHook(() => useModelFetch(BASE));
      await waitFor(() => expect(Object.keys(result.current.meta)).toHaveLength(4));

      peakInFlight = 0;   // ignore the mount-time meta load
      await act(async () => {
        await result.current.fetchAll([...providerNames, 'pe', 'pf', 'pg', 'ph']);
      });

      // Unbounded fan-out would risk rate limits; serialising the whole sweep
      // would take minutes on a slow vendor. 3 is the agreed compromise.
      expect(peakInFlight).toBe(3);
    });

    it('ignores the session cache — "get the latest" is the whole point', async () => {
      const { result } = renderHook(() => useModelFetch(BASE));
      await act(async () => { await result.current.fetchModels('deepseek'); });
      const before = calls.filter(c => c.url.includes('/api/models/fetch')).length;

      await act(async () => { await result.current.fetchAll(['deepseek']); });

      expect(calls.filter(c => c.url.includes('/api/models/fetch')).length)
        .toBe(before + 1);
    });

    it('refuses to start a second sweep over the same vendors', async () => {
      plan = sweepPlan;
      latencyMs = 5;
      const { result } = renderHook(() => useModelFetch(BASE));
      await waitFor(() => expect(Object.keys(result.current.meta)).toHaveLength(4));

      let first: Promise<unknown> | undefined;
      let second: unknown = 'unset';
      await act(async () => {
        first = result.current.fetchAll(providerNames);
        // Fired in the same tick, before React commits `allProgress` — the
        // synchronous ref guard is what has to catch this, not the state.
        second = await result.current.fetchAll(providerNames);
      });
      await act(async () => { await first; });

      expect(second).toBeNull();
      expect(calls.filter(c => c.url.includes('/api/models/fetch')).length)
        .toBe(providerNames.length);   // exactly one sweep reached the vendor
    });

    // The mid-flight progress *rendering* is asserted against the real
    // component in ModelFetchAllBar.test.tsx; React's act() only commits at
    // flush points, so sampling state mid-sweep here would test batching
    // rather than behaviour. What matters at this layer is that the sweep
    // leaves nothing behind.
    it('leaves no progress behind once the sweep finishes', async () => {
      plan = sweepPlan;
      latencyMs = 10;
      const { result } = renderHook(() => useModelFetch(BASE));

      await act(async () => { await result.current.fetchAll(providerNames); });

      expect(result.current.allProgress).toBeNull();
      expect(result.current.allSummary)
        .toEqual({ total: 4, ok: 2, unsupported: 1, failed: 1 });
    });

    it('clears a finished summary on request', async () => {
      plan = sweepPlan;
      const { result } = renderHook(() => useModelFetch(BASE));
      await act(async () => { await result.current.fetchAll(['pa']); });
      expect(result.current.allSummary).not.toBeNull();

      act(() => { result.current.clearAllSummary(); });
      expect(result.current.allSummary).toBeNull();
    });

    it('does nothing when handed an empty vendor list', async () => {
      const { result } = renderHook(() => useModelFetch(BASE));
      let summary: unknown = 'unset';
      await act(async () => { summary = await result.current.fetchAll([]); });
      expect(summary).toBeNull();
      expect(result.current.allSummary).toBeNull();
    });
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
