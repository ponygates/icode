import { useCallback, useEffect, useState } from 'react';
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
  context_window?: number;
  max_output_tokens?: number;
}

/** Per-vendor summary from /api/providers: how many models survive the filter. */
export interface VendorMeta {
  enabled: number;
  models: number;
  filtered: boolean;
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
  toggle: (provider: string, id: string) => void;
  selectAll: (provider: string, on: boolean) => void;
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

  const setErr = (provider: string, msg: string) =>
    setError(prev => ({ ...prev, [provider]: msg }));

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
    setErr(provider, '');
    try {
      const res = await fetch(
        `${base}/api/models/fetch?provider=${encodeURIComponent(provider)}`,
        { cache: 'no-cache' },
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setErr(provider, (data && data.error) || `HTTP ${res.status}`);
        return;
      }
      const list: FetchedModel[] = Array.isArray(data?.models) ? data.models : [];
      if (list.length === 0) {
        setErr(provider, t('models.fetchEmpty'));
        return;
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
      setPanel(provider);
    } catch (e) {
      setErr(provider, e instanceof Error ? e.message : String(e));
    } finally {
      setFetching(null);
    }
  }, [base, fetching, lists, panel, t]);

  const toggle = useCallback((provider: string, id: string) => {
    setTicked(prev => {
      const cur = prev[provider] || [];
      return { ...prev, [provider]: cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id] };
    });
  }, []);

  const selectAll = useCallback((provider: string, on: boolean) => {
    const list = lists[provider] || [];
    setTicked(prev => ({ ...prev, [provider]: on ? list.map(m => m.id) : [] }));
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
    try {
      const res = await fetch(`${base}/api/models/selection`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, models: ids }),
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
  }, [base, saving, ticked, t, loadMeta]);

  const close = useCallback(() => setPanel(null), []);

  return {
    fetching, panel, lists, ticked, error, saving, meta,
    loadMeta, fetchModels, toggle, selectAll, save, close,
  };
}
