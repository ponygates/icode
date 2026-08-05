import { describe, it, expect, vi } from 'vitest';
import { apiAppendMessage, apiUpdateMessage, apiForkMessages, apiClearSession } from '../sessionMessages';

function mockFetch(): {
  fn: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
  calls: () => { url: string; init: RequestInit }[];
} {
  const fn = vi.fn().mockResolvedValue({ ok: true, status: 200 } as Response);
  const calls = () =>
    (fn.mock.calls as unknown[]).map((c) => {
      const [url, init] = c as [string, RequestInit];
      return { url, init };
    });
  return { fn, calls };
}
const mock = mockFetch;

describe('sessionMessages persistence', () => {
  it('apiAppendMessage POSTs to /messages with id/role/content', async () => {
    const { fn, calls } = mock();
    await apiAppendMessage('http://x', 's1', { id: 'a', role: 'system', content: 'hi' }, fn);

    const c = calls()[0];
    expect(c.url).toBe('http://x/api/sessions/s1/messages');
    expect(c.init.method).toBe('POST');
    expect(JSON.parse(String(c.init.body))).toEqual({ id: 'a', role: 'system', content: 'hi' });
  });

  it('apiUpdateMessage PUTs to /messages/{id}', async () => {
    const { fn, calls } = mock();
    await apiUpdateMessage('http://x', 's1', 'mid', 'assistant', 'output', fn);
    const c = calls()[0];
    expect(c.url).toBe('http://x/api/sessions/s1/messages/mid');
    expect(c.init.method).toBe('PUT');
    expect(JSON.parse(String(c.init.body))).toEqual({ role: 'assistant', content: 'output' });
  });

  it('apiClearSession POSTs to /clear', async () => {
    const { fn, calls } = mock();
    await apiClearSession('http://x', 's1', fn);
    const c = calls()[0];
    expect(c.url).toBe('http://x/api/sessions/s1/clear');
    expect(c.init.method).toBe('POST');
  });

  it('apiForkMessages posts one message per source message with a new id', async () => {
    const { fn, calls } = mock();
    const res = await apiForkMessages(
      'http://x', 'fork1',
      [
        { id: 'old1', role: 'user', content: 'one' },
        { id: 'old2', role: 'assistant', content: 'two' },
      ],
      fn,
    );
    expect(res).toHaveLength(2);
    expect(calls()).toHaveLength(2);
    const bodies = calls().map((c) => JSON.parse(String(c.init.body)));
    expect(bodies.map((b) => b.id)).not.toContain('old1');
    expect(bodies.map((b) => b.content)).toEqual(['one', 'two']);
    expect(bodies.every((b) => b.id.length > 0)).toBe(true);
  });
});