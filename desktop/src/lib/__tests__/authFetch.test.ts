// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { initAuthFetch } from '../authFetch';

// Storage key is part of the (unexported) contract with the backend's URL
// handoff —hardcoded here on purpose so a rename cannot silently pass.
const TOKEN_KEY = 'icode_token';

// initAuthFetch wraps window.fetch in place; keep the pristine reference so
// each test starts from a controlled, inspectable mock.
const pristineFetch = window.fetch;

// The mock the pristine fetch is replaced with each test. Kept in a variable
// (not read off window.fetch) because initAuthFetch wraps it: after the patch,
// window.fetch is the wrapper, while THIS is what actually records calls.
// Built through a factory (so the Mock's type is inferred) that accepts
// fetch's (input, init) args — the wrapper forwards both.
const makeFetchMock = () =>
  vi.fn((..._args: unknown[]) => Promise.resolve(new Response('{}', { status: 200 })));
let fetchMock: ReturnType<typeof makeFetchMock>;

function setURL(url: string): void {
  window.history.pushState({}, '', url);
}

function lastCall(): { input: unknown; init: RequestInit | undefined } {
  const call = fetchMock.mock.calls[fetchMock.mock.calls.length - 1];
  return { input: call[0], init: call[1] as RequestInit | undefined };
}

function authHeader(init: RequestInit | undefined): string | null {
  if (!init || !init.headers) { return null; }
  return new Headers(init.headers).get('Authorization');
}

beforeEach(() => {
  window.sessionStorage.clear();
  setURL('/');
  fetchMock = makeFetchMock();
  window.fetch = fetchMock as unknown as typeof fetch;
});

afterEach(() => {
  window.fetch = pristineFetch;
});

describe('initAuthFetch', () => {
  it('captures ?token= into sessionStorage and scrubs the query (keeping the hash)', () => {
    setURL('/?token=abc123#/chat');
    const replaceSpy = vi.spyOn(window.history, 'replaceState');
    initAuthFetch();

    expect(window.sessionStorage.getItem(TOKEN_KEY)).toBe('abc123');
    // The token must not linger in the copyable address bar; HashRouter's
    // route must survive the scrub.
    expect(replaceSpy).toHaveBeenCalledWith(null, '', '/#/chat');
    expect(window.location.search).toBe('');
  });

  it('leaves the URL and storage untouched when no ?token= is present', () => {
    setURL('/#/settings');
    const replaceSpy = vi.spyOn(window.history, 'replaceState');
    initAuthFetch();

    expect(replaceSpy).not.toHaveBeenCalled();
    expect(window.sessionStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  it('injects Authorization: Bearer on same-origin requests', async () => {
    setURL('/?token=abc123');
    initAuthFetch();

    await window.fetch('/api/config', { method: 'PUT' });
    expect(authHeader(lastCall().init)).toBe('Bearer abc123');
  });

  it('reads a persisted token from sessionStorage when the URL has no query (F5 reload)', async () => {
    // Boot URL was scrubbed on first launch; sessionStorage carries the token
    // across reloads within the same window session.
    window.sessionStorage.setItem(TOKEN_KEY, 'persisted-token');
    setURL('/');
    initAuthFetch();

    await window.fetch('/api/shell', { method: 'POST', body: '{}' });
    expect(authHeader(lastCall().init)).toBe('Bearer persisted-token');
  });

  it('never injects the token into cross-origin requests', async () => {
    setURL('/?token=secret123');
    initAuthFetch();

    await window.fetch('https://evil.example/api/config', { method: 'PUT' });
    const { init } = lastCall();
    // Cross-origin branch passes init through untouched —no headers object
    // is created, let alone an Authorization one.
    expect(authHeader(init)).toBeNull();
    expect(init && (init as Record<string, unknown>).headers).toBeUndefined();
  });

  it('does not overwrite an existing Authorization header', async () => {
    setURL('/?token=abc123');
    initAuthFetch();

    await window.fetch('/api/config', {
      method: 'PUT',
      headers: { Authorization: 'Bearer custom' },
    });
    expect(authHeader(lastCall().init)).toBe('Bearer custom');
  });

  it('sends no Authorization when no token is available at all', async () => {
    setURL('/');
    initAuthFetch();

    await window.fetch('/api/config', { method: 'PUT' });
    expect(authHeader(lastCall().init)).toBeNull();
  });

  it('injects into Request objects targeting the same origin', async () => {
    if (typeof Request === 'undefined') {
      // Very old jsdom builds —skip rather than fake the platform API.
      return;
    }
    setURL('/?token=req-token');
    initAuthFetch();

    const req = new Request(`${window.location.origin}/api/config`, { method: 'PUT' });
    await window.fetch(req);
    const { input } = lastCall();
    expect(input).toBeInstanceOf(Request);
    expect((input as Request).headers.get('Authorization')).toBe('Bearer req-token');
  });
});
