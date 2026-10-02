// Bearer-token plumbing for the desktop web UI.
//
// Mutating endpoints (shell / config / permission / update) require the
// per-launch API token. The desktop shell and `icode server` hand it over
// via the `?token=` URL query; on boot we stash it in sessionStorage and
// scrub the address bar, then patch window.fetch to attach
// `Authorization: Bearer <token>` to every *same-origin* request. The token
// is never sent cross-origin, and never re-appears in the visible URL.

const TOKEN_KEY = 'icode_token'

// Returns the current token: sessionStorage first (survives F5), falling
// back to the value captured from the boot-time URL query (covers browsers
// where sessionStorage is unavailable, e.g. strict private mode).
function currentToken(): string {
  try {
    const stored = window.sessionStorage.getItem(TOKEN_KEY)
    if (stored) return stored
  } catch {
    // sessionStorage unavailable — fall through to closure value
  }
  return ''
}

export function initAuthFetch(): void {
  if (typeof window === 'undefined') return

  // 1. Capture the token from the URL query into sessionStorage.
  let bootToken = ''
  try {
    bootToken = new URLSearchParams(window.location.search).get('token') ?? ''
  } catch {
    bootToken = ''
  }
  if (bootToken) {
    try {
      window.sessionStorage.setItem(TOKEN_KEY, bootToken)
    } catch {
      // Keep the closure value below as the in-memory fallback.
    }
    // 2. Scrub the query from the address bar so the token never leaks
    //    through copy/paste or screen sharing. Keep the hash — the app
    //    uses HashRouter and would otherwise reset to the default route.
    try {
      window.history.replaceState(null, '', window.location.pathname + window.location.hash)
    } catch {
      // Non-fatal: WebView2 always supports replaceState.
    }
  }

  // 3. Patch fetch: same-origin requests get the Bearer header injected.
  const original = window.fetch.bind(window)
  const fallbackToken = bootToken
  window.fetch = ((input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    let url = ''
    if (typeof input === 'string') url = input
    else if (input instanceof URL) url = input.toString()
    else if (typeof Request !== 'undefined' && input instanceof Request) url = input.url

    const token = currentToken() || fallbackToken
    if (!token || !url) return original(input, init)

    // Same-origin only — never send the token to a third party.
    let sameOrigin = false
    try {
      sameOrigin = new URL(url, window.location.origin).origin === window.location.origin
    } catch {
      sameOrigin = false
    }
    if (!sameOrigin) return original(input, init)

    const headers = new Headers(init?.headers)
    if (!headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${token}`)
    }
    if (typeof Request !== 'undefined' && input instanceof Request) {
      // Request objects are immutable — rebuild with the merged headers.
      return original(new Request(input, { headers }), init)
    }
    return original(input, { ...init, headers })
  }) as typeof window.fetch
}
