// Backend discovery for the desktop shell.
//
// The desktop build is served either from the bundled webview (origin is
// `file://`/null) or from the Go HTTP server itself. In the first case the
// base URL is unknown at boot, so we probe same-origin first and then a short
// list of ports the CLI/dev server is known to bind.
//
// Extracted from ChatPage's send path so the "which URL do I talk to" rule is
// testable without a live server.

/** Ports the backend may listen on when it isn't on the current origin. */
export const CANDIDATE_PORTS = [57356, 57357, 8080, 3000, 9090] as const;

export interface DiscoveryResult {
  /** Base URL to use, or null when nothing answered. */
  url: string | null;
  /** Where the hit came from — useful for logging/diagnostics. */
  source: 'explicit' | 'origin' | 'port' | 'none';
}

/**
 * discoverBackend resolves the backend base URL.
 *
 * - An explicitly configured URL wins and costs no requests.
 * - `origin` is only probed when it is a real http(s) origin; a webview loaded
 *   from disk reports `file://` or the string "null", which no fetch can reach.
 * - Ports are probed in order; the first healthy one wins.
 *
 * A failing probe is always swallowed: at boot there is nothing useful to show
 * the user, and a rejected fetch would abort the whole send.
 */
export async function discoverBackend(opts: {
  explicit?: string;
  origin?: string;
  fetchImpl?: typeof fetch;
} = {}): Promise<DiscoveryResult> {
  const { explicit, origin } = opts;
  const doFetch = opts.fetchImpl ?? fetch;

  if (explicit) return { url: explicit, source: 'explicit' };

  const healthy = async (base: string): Promise<boolean> => {
    try {
      const res = await doFetch(`${base}/api/health`);
      return res.ok;
    } catch {
      return false;
    }
  };

  if (origin && origin !== 'null' && !origin.startsWith('file://')) {
    if (await healthy(origin)) return { url: origin, source: 'origin' };
  }

  for (const port of CANDIDATE_PORTS) {
    const base = `http://127.0.0.1:${port}`;
    if (await healthy(base)) return { url: base, source: 'port' };
  }

  return { url: null, source: 'none' };
}
