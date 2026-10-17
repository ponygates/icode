// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { discoverBackend, CANDIDATE_PORTS } from '../backendDiscovery';

/** Builds a fetch mock that answers /api/health 200 only for `okBases`. */
function healthMock(okBases: string[]) {
  return vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    const base = url.replace('/api/health', '');
    return Promise.resolve(new Response('', { status: okBases.includes(base) ? 200 : 503 }));
  });
}

describe('discoverBackend', () => {
  it('returns the configured URL without probing', async () => {
    const f = healthMock([]);
    const res = await discoverBackend({ explicit: 'http://cfg:1234', fetchImpl: f as unknown as typeof fetch });

    expect(res).toEqual({ url: 'http://cfg:1234', source: 'explicit' });
    expect(f).not.toHaveBeenCalled();
  });

  it('prefers the same origin when it is healthy', async () => {
    const f = healthMock(['http://ui:8080']);
    const res = await discoverBackend({ origin: 'http://ui:8080', fetchImpl: f as unknown as typeof fetch });

    expect(res).toEqual({ url: 'http://ui:8080', source: 'origin' });
    expect(f).toHaveBeenCalledTimes(1);
  });

  it('falls back to the port list in order and stops at the first healthy one', async () => {
    const f = healthMock(['http://127.0.0.1:57357']);
    const res = await discoverBackend({ origin: 'http://ui:8080', fetchImpl: f as unknown as typeof fetch });

    expect(res).toEqual({ url: 'http://127.0.0.1:57357', source: 'port' });
    // origin + first failing port + the hit: stops before the remaining ports.
    expect(f).toHaveBeenCalledTimes(3);
    expect(String(f.mock.calls[2][0])).toBe('http://127.0.0.1:57357/api/health');
  });

  it('skips a file:// origin (webview loaded from disk cannot be fetched)', async () => {
    const f = healthMock(['http://127.0.0.1:8080']);
    await discoverBackend({ origin: 'file:///C:/app/index.html', fetchImpl: f as unknown as typeof fetch });

    const urls = f.mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.startsWith('file://'))).toBe(false);
    expect(urls.length).toBe(CANDIDATE_PORTS.indexOf(8080) + 1);
  });

  it('returns null when nothing answers, without throwing', async () => {
    const f = vi.fn(() => Promise.reject(new Error('ECONNREFUSED')));
    const res = await discoverBackend({ origin: 'http://ui:8080', fetchImpl: f as unknown as typeof fetch });

    expect(res).toEqual({ url: null, source: 'none' });
  });
});
