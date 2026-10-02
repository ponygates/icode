import { describe, it, expect, vi } from 'vitest';
import { apiAppendMessage, apiUpdateMessage, apiForkMessages, apiClearSession, sliceThrough } from '../sessionMessages';

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

describe('sliceThrough (fork branch point)', () => {
  const msgs = [
    { id: 'm1' },
    { id: 'm2' },
    { id: 'm3' },
  ];

  it('keeps the whole list when no branch point is given', () => {
    expect(sliceThrough(msgs)).toEqual(msgs);
  });

  it('keeps everything up to and including the branch point', () => {
    expect(sliceThrough(msgs, 'm2')).toEqual([{ id: 'm1' }, { id: 'm2' }]);
  });

  it('branching at the first message keeps exactly that message', () => {
    expect(sliceThrough(msgs, 'm1')).toEqual([{ id: 'm1' }]);
  });

  it('returns null for a message that is no longer in the list', () => {
    expect(sliceThrough(msgs, 'gone')).toBeNull();
    expect(sliceThrough([], 'm1')).toBeNull();
  });

  it('the fork payload of a branch point is the prefix, not the whole session', async () => {
    const { fn, calls } = mock();
    const prefix = sliceThrough(
      [
        { id: 'm1', role: 'user', content: 'one' },
        { id: 'm2', role: 'assistant', content: 'two' },
        { id: 'm3', role: 'user', content: 'three' },
      ],
      'm2',
    );
    await apiForkMessages('http://x', 'fork1', prefix!, fn);
    expect(calls().map((c) => JSON.parse(String(c.init.body)).content)).toEqual(['one', 'two']);
  });
});