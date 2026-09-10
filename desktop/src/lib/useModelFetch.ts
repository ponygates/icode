import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * Live per-vendor model discovery + selection.
 *
 * The built-in catalogue shipped in the binary is only a build-time snapshot:
 * a vendor adds and retires models constantly, and which of them a given key
 * may call depends on the account's plan and region. So the vendor's own
 * `/models` endpoint is the only authority. This hook backs the "获取模型"
 * action: pull the live list for one vendor, tick the subset to keep, persist.
 *
 * The selection is stored per vendor on the backend and is deliberately
 * backward compatible — an unset filter restricts nothing, so users who never
 * touch this keep seeing the full catalogue.
 */

export interface FetchedModel {
  id: string;
  name: string;
  known: boolean;
  enabled: boolean;
  /**
   * In the built-in catalogue but absent from the vendor's live /models
   * response. Still callable, so the panel lists it and lets the user tick it
   * rather than dead-ending in the manual-add dialog.
   */
  builtin_only?: boolean;
  context_window?: number;
  max_output_tokens?: number;
}

/** Per-vendor summary from /api/providers: how many models survive the filter. */
export interface VendorMeta {
  enabled: number;
  models: number;
  filtered: boolean;
}

/** Why a vendor's live catalogue could not be pulled. */
export type PullKind = 'ok' | 'unsupported' | 'failed';

export interface PullResult {
  provider: string;
  kind: PullKind;
  /** Vendor-supplied message; empty on success. */
  message?: string;
}

/** Outcome of a "fetch every vendor" sweep. */
export interface FetchAllSummary {
  total: number;
  ok: number;
  /** Vendors that do not implement live discovery (backend returns 501). */
  unsupported: number;
  failed: number;
}

export interface UseModelFetch {
  /** Provider currently mid-request, or null. */
  fetching: string | null;
  /** Provider whose checklist is open, or null. */
  panel: string | null;
  /** Live-fetched list per provider (session cache, invalidated on save). */
  lists: Record<string, FetchedModel[]>;
  /** Ticked model ids per provider. */
  ticked: Record<string, string[]>;
  /** Last error per provider. */
  error: Record<string, string>;
  saving: boolean;
  /** enabled/models/filtered per provider, for the "已启用 N/M" badge. */
  meta: Record<string, VendorMeta>;
  loadMeta: () => Promise<void>;
  fetchModels: (provider: string, onOpen?: () => void) => Promise<void>;
  /**
   * Pull every vendor's live catalogue, at most `concurrency` in flight.
   *
   * Serialising the whole sweep would take minutes on a slow vendor, while
   * unbounded fan-out risks rate limits (and OpenRouter alone returns
   * megabytes). Three in flight is the compromise. Progress is reported via
   * `allProgress`; cached lists are refreshed rather than reused.
   */
  fetchAll: (providers: string[], concurrency?: number) => Promise<FetchAllSummary | null>;
  /** Providers done so far in the current sweep. */
  allProgress: { done: number; total: number } | null;
  /** Result of the last completed sweep, until dismissed. */
  allSummary: FetchAllSummary | null;
  clearAllSummary: () => void;
  toggle: (provider: string, id: string) => void;
  /**
   * Tick or untick a vendor's models. `subset` narrows the operation to what
   * the caller is currently showing — the panel passes its search results —
   * and omitting it acts on the whole list.
   */
  selectAll: (provider: string, on: boolean, subset?: string[]) => void;
  save: (provider: string, afterSave?: () => Promise<void> | void) => Promise<void>;
  close: () => void;
}

export function useModelFetch(backendUrl: string | null | undefined): UseModelFetch {
  const { t } = useTranslation();
  // The store types backendUrl as string|null (null until the backend handshake
  // completes); normalise once so every guard is a simple falsy check.
  const base = backendUrl || '';

  const [fetching, setFetching] = useState<string | null>(null);
  const [panel, setPanel]       = useState<string | null>(null);
  const [lists, setLists]       = useState<Record<string, FetchedModel[]>>({});
  const [ticked, setTicked]     = useState<Record<string, string[]>>({});
  const [error, setError]       = useState<Record<string, string>>({});
  const [saving, setSaving]     = useState(false);
  const [meta, setMeta]         = useState<Record<string, VendorMeta>>({});
  // Non-null while a sweep is running; also carries its progress.
  const [allProgress, setAllProgress] = useState<{ done: number; total: number } | null>(null);
  const [allSummary, setAllSummary]   = useState<FetchAllSummary | null>(null);
  const sweepRef = useRef(false);   // synchronous re-entrancy guard

  const setErr = useCallback((provider: string, msg: string) => {
    setError(prev => ({ ...prev, [provider]: msg }));
  }, []);

  // /api/models already omits filtered-out models, so a card cannot tell
  // "this vendor has 3 models" from "you kept 3 of 23" on its own.
  // /api/providers carries both numbers; fetched once, refreshed after a save.
  const loadMeta = useCallback(async () => {
    if (!base) return;
    try {
      const res = await fetch(`${base}/api/providers`, { cache: 'no-cache' });
      if (!res.ok) return;
      const list = await res.json();
      if (!Array.isArray(list)) return;
      const out: Record<string, VendorMeta> = {};
      for (const p of list) {
        out[p.name] = { enabled: p.enabled ?? 0, models: p.models ?? 0, filtered: !!p.filtered };
      }
      setMeta(out);
    } catch {
      /* non-fatal — the badge simply stays hidden */
    }
  }, [base]);

  useEffect(() => { loadMeta(); }, [loadMeta]);

  /**
   * Pull one vendor's live catalogue and stash it (plus its pre-ticked set).
   * Pure data work — no panel/progress side effects, so the single-vendor and
   * fetch-all paths can share it without duplicating the parsing rules.
   *
   * `force` bypasses the session cache; a sweep refreshes every vendor because
   * "get me the latest" is the whole point of pressing it.
   */
  const pull = useCallback(async (provider: string, force = false): Promise<PullResult> => {
    if (!base) return { provider, kind: 'failed', message: 'no backend' };
    if (!force && lists[provider]) return { provider, kind: 'ok' };
    try {
      const res = await fetch(
        `${base}/api/models/fetch?provider=${encodeURIComponent(provider)}`,
        { cache: 'no-cache' },
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        const msg = (data && data.error) || `HTTP ${res.status}`;
        setErr(provider, msg);
        // 501 is the backend saying "this vendor has no live discovery", which
        // is a different story from "your key was rejected" — the sweep counts
        // them separately so the summary is not misleading.
        return { provider, kind: res.status === 501 ? 'unsupported' : 'failed', message: msg };
      }
      const list: FetchedModel[] = Array.isArray(data?.models) ? data.models : [];
      if (list.length === 0) {
        setErr(provider, t('models.fetchEmpty'));
        return { provider, kind: 'failed', message: t('models.fetchEmpty') };
      }
      setLists(prev => ({ ...prev, [provider]: list }));
      // Pre-tick whatever is currently in effect. Before the first save the
      // filter is unset, which restricts nothing, so everything comes back
      // enabled — meaning a user who only wants to drop a couple of models
      // starts from "select all" rather than from nothing.
      setTicked(prev => ({
        ...prev,
        [provider]: list.filter(m => m.enabled).map(m => m.id),
      }));
      setErr(provider, '');
      return { provider, kind: 'ok' };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setErr(provider, msg);
      return { provider, kind: 'failed', message: msg };
    }
  }, [base, lists, t]);

  const fetchModels = useCallback(async (provider: string, onOpen?: () => void) => {
    onOpen?.();                  // the caller usually expands the vendor card
    if (panel === provider) { setPanel(null); return; }   // clicking again closes
    if (lists[provider]) {       // already pulled this session — just reveal
      setErr(provider, '');
      setPanel(provider);
      return;
    }
    if (!base || fetching) return;
    setFetching(provider);
    try {
      const r = await pull(provider);
      if (r.kind === 'ok') setPanel(provider);
    } finally {
      setFetching(null);
    }
  }, [base, fetching, lists, panel, pull]);

  /**
   * Sweep every vendor, at most `concurrency` in flight.
   *
   * A shared cursor (`queue.shift()`) is safe here: JS is single-threaded and
   * there is no await between reading and removing the next item, so two
   * workers can never claim the same vendor.
   */
  const fetchAll = useCallback(async (
    providers: string[],
    concurrency = 3,
  ): Promise<FetchAllSummary | null> => {
    // The state value below drives the button's disabled look, but state
    // updates are not visible until the next render — two clicks in one tick
    // would both slip past it and start two sweeps over the same vendors.
    // The ref flips synchronously, so it is the real guard.
    if (!base || sweepRef.current) return null;
    const queue = [...providers];
    if (queue.length === 0) return null;

    sweepRef.current = true;
    setAllSummary(null);
    setAllProgress({ done: 0, total: queue.length });
    const tally: FetchAllSummary = {
      total: queue.length, ok: 0, unsupported: 0, failed: 0,
    };

    const worker = async () => {
      for (;;) {
        const provider = queue.shift();
        if (!provider) return;
        const r = await pull(provider, true);   // force: "latest" is the point
        if (r.kind === 'ok') tally.ok++;
        else if (r.kind === 'unsupported') tally.unsupported++;
        else tally.failed++;
        setAllProgress(prev => (prev ? { done: prev.done + 1, total: prev.total } : prev));
      }
    };

    try {
      await Promise.all(
        Array.from({ length: Math.min(concurrency, queue.length) }, () => worker()),
      );
    } finally {
      sweepRef.current = false;
      setAllProgress(null);
      setAllSummary({ ...tally });
    }
    return { ...tally };
  }, [base, pull]);

  const clearAllSummary = useCallback(() => setAllSummary(null), []);

  const toggle = useCallback((provider: string, id: string) => {
    setTicked(prev => {
      const cur = prev[provider] || [];
      return { ...prev, [provider]: cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id] };
    });
  }, []);

  /**
   * Tick or untick models, optionally scoped to `subset`.
   *
   * Two deliberate choices, both about not silently losing a selection:
   *
   *   - ticking keeps whatever is already ticked (union) instead of resetting
   *     to the given ids. The panel passes its *filtered* rows, so a reset
   *     would quietly untick every model the search is hiding — and saving
   *     then writes that shrunken set to the vendor filter, vanishing those
   *     models from the model list.
   *   - ticks that no longer exist in the list are dropped, so a refresh that
   *     retires a model cannot leave a dangling id in the selection.
   */
  const selectAll = useCallback((provider: string, on: boolean, subset?: string[]) => {
    const list = lists[provider] || [];
    const ids = subset ?? list.map(m => m.id);
    const live = new Set(list.map(m => m.id));
    setTicked(prev => {
      const cur = prev[provider] || [];
      if (!on) {
        const drop = new Set(ids);
        return { ...prev, [provider]: cur.filter(x => !drop.has(x)) };
      }
      return { ...prev, [provider]: Array.from(new Set([...cur.filter(x => live.has(x)), ...ids])) };
    });
  }, [lists]);

  const save = useCallback(async (provider: string, afterSave?: () => Promise<void> | void) => {
    const ids = ticked[provider] || [];
    if (!base || saving) return;
    // The backend rejects an empty selection: stored as a filter, "nothing
    // ticked" would mean "no restriction" and re-enable the whole catalogue —
    // the exact opposite of the intent. Disabling a vendor wholesale is what
    // the provider-level switch is for.
    if (ids.length === 0) {
      setErr(provider, t('models.selectAtLeastOne'));
      return;
    }
    setSaving(true);
    setErr(provider, '');
    // Pass the vendor-reported figures along with the ticked ids. A model the
    // vendor shipped after this build gets auto-registered on save; without
    // its context window it would be stored with a zero and fall back to a
    // guess, even though we had the real number from the fetch. Only the
    // ticked models are described — the rest are being excluded anyway.
    const tickedSet = new Set(ids);
    const meta: Record<string, { context_window: number; max_output_tokens: number }> = {};
    for (const m of lists[provider] || []) {
      if (!tickedSet.has(m.id)) continue;
      if (m.context_window || m.max_output_tokens) {
        meta[m.id] = {
          context_window: m.context_window ?? 0,
          max_output_tokens: m.max_output_tokens ?? 0,
        };
      }
    }
    try {
      const res = await fetch(`${base}/api/models/selection`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, models: ids, meta }),
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setErr(provider, (data && data.error) || `HTTP ${res.status}`);
        return;
      }
      setPanel(null);
      // The cached list is stale now — its `enabled` flags predate this save.
      setLists(prev => {
        const next = { ...prev };
        delete next[provider];
        return next;
      });
      await afterSave?.();
      await loadMeta();
    } catch (e) {
      setErr(provider, e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [base, saving, ticked, lists, t, loadMeta, setErr]);

  const close = useCallback(() => setPanel(null), []);

  return {
    fetching, panel, lists, ticked, error, saving, meta,
    loadMeta, fetchModels, fetchAll, allProgress, allSummary, clearAllSummary,
    toggle, selectAll, save, close,
  };
}
