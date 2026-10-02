import { describe, it, expect } from 'vitest';
import {
  popHead,
  dropSession,
  planApproval,
  withSessionAllow,
  type PermEntry,
} from '../approval';

const entry = (over: Partial<PermEntry> = {}): PermEntry => ({
  request_id: 'r1', tool: 'write_file', sid: 's1', ...over,
});

describe('permission queue transitions', () => {
  it('popHead answers the front request and keeps the rest queued', () => {
    const q = [entry(), entry({ request_id: 'r2', sid: 's2' })];
    const { head, rest } = popHead(q);
    expect(head?.request_id).toBe('r1');
    expect(rest.map((r) => r.request_id)).toEqual(['r2']);
  });

  it('popHead on an empty queue resolves nothing', () => {
    expect(popHead([])).toEqual({ head: null, rest: [] });
  });

  it('dropSession clears only that session’s pending requests', () => {
    const q = [entry({ request_id: 'a', sid: 's1' }), entry({ request_id: 'b', sid: 's2' }), entry({ request_id: 'c', sid: 's1' })];
    expect(dropSession(q, 's1').map((r) => r.request_id)).toEqual(['b']);
  });
});

describe('approval backend planning', () => {
  it('a plain allow/deny is a single respond step with the chosen decision', () => {
    expect(planApproval(entry(), 'allow')).toEqual([{ kind: 'respond', requestId: 'r1', decision: 'allow' }]);
    expect(planApproval(entry(), 'deny')).toEqual([{ kind: 'respond', requestId: 'r1', decision: 'deny' }]);
  });

  it('allow_all is one respond call that flips the session to auto', () => {
    expect(planApproval(entry(), 'allow_all')).toEqual([
      { kind: 'respond', requestId: 'r1', decision: 'allow_all' },
    ]);
  });

  it('"always for this session" remembers the tool BEFORE responding, so a fast follow-up is covered', () => {
    const steps = planApproval(entry({ sid: 's9', tool: 'bash' }), 'allow', { alwaysForSession: true });
    expect(steps).toEqual([
      { kind: 'allow-tool', sessionId: 's9', tool: 'bash' },
      { kind: 'respond', requestId: 'r1', decision: 'allow' },
    ]);
  });
});

describe('session-allow set', () => {
  it('adds a tool and returns a new set (referential change for memo selectors)', () => {
    const a = new Set<string>(['read_file']);
    const b = withSessionAllow(a, 'bash');
    expect(b.has('bash')).toBe(true);
    expect(b).not.toBe(a);
  });

  it('is idempotent — re-adding returns the same set', () => {
    const a = new Set<string>(['bash']);
    expect(withSessionAllow(a, 'bash')).toBe(a);
  });
});
