import { describe, it, expect } from 'vitest';
import { applyStreamEvent, initialStreamState, type StreamState } from '../streamReducer';

const reduce = (state: StreamState, ev: Parameters<typeof applyStreamEvent>[1]) =>
  applyStreamEvent(state, ev).state;

describe('applyStreamEvent — text accumulation', () => {
  it('appends plain text deltas and asks for a coalesced flush', () => {
    const r = applyStreamEvent(initialStreamState(), { type: 'text', content: 'Hello ' });
    expect(r.state.accumulated).toBe('Hello ');
    expect(r.flush).toBe(true);
  });

  it('folds buffered thinking into a labelled block when the first text arrives', () => {
    let s = reduce(initialStreamState(), { type: 'thinking', content: 'pondering' });
    s = reduce(s, { type: 'text', content: 'Answer' });
    expect(s.accumulated).toBe('\n[Thinking]\npondering\nAnswer');
    expect(s.thinkingBuf).toBe('');
  });

  it('caps the thinking buffer so a runaway stream cannot grow unbounded', () => {
    const big = 'x'.repeat(4000);
    const s = reduce(initialStreamState(), { type: 'thinking', content: big });
    expect(s.thinkingBuf.length).toBe(3000);
  });
});

describe('applyStreamEvent — tool wrapper de-duplication', () => {
  it('streams live tool output then keeps only the live copy, dropping the summary wrapper', () => {
    let s = reduce(initialStreamState(), { type: 'tool_use', name: 'bash', argSummary: ' command=ls' });
    s = reduce(s, { type: 'tool_progress', content: 'file1\nfile2\n' });
    expect(s.toolHadProgress).toBe(true);
    // engine later emits the short "[Tool: bash]\n<summary>" wrapper
    s = reduce(s, { type: 'text', content: '[Tool: bash]\nfile1 file2' });
    // wrapper body must NOT be appended again (dedupe)
    expect(s.accumulated).toBe('\n[Tool: bash] command=ls\nfile1\nfile2\n');
  });

  it('keeps the summary wrapper body when there was no live progress', () => {
    let s = reduce(initialStreamState(), { type: 'tool_use', name: 'read', argSummary: ' path=a.ts' });
    s = reduce(s, { type: 'text', content: '[Tool: read]\n42 lines' });
    expect(s.accumulated).toContain('42 lines');
    expect(s.accumulated).toBe('\n[Tool: read] path=a.ts\n42 lines');
  });
});

describe('applyStreamEvent — settle transitions', () => {
  it('done folds trailing thinking, marks settled and emits a settle effect', () => {
    let s = reduce(initialStreamState(), { type: 'thinking', content: 'tail' });
    const r = applyStreamEvent(s, { type: 'done' });
    expect(r.state.settled).toBe(true);
    expect(r.effects).toEqual([{ type: 'settle', content: '\n[Thinking]\ntail\n' }]);
  });

  it('error appends a failure marker and settles', () => {
    const r = applyStreamEvent({ ...initialStreamState(), accumulated: 'partial' }, { type: 'error', content: 'boom' });
    expect(r.state.settled).toBe(true);
    expect(r.state.accumulated).toBe('partial\n❌ boom');
    expect(r.effects[0].type).toBe('error-settle');
  });

  it('ignores any event that arrives after the turn settled', () => {
    const settled: StreamState = { ...initialStreamState(), settled: true, accumulated: 'final' };
    const r = applyStreamEvent(settled, { type: 'text', content: 'late' });
    expect(r.state).toBe(settled);
    expect(r.flush).toBe(false);
  });

  it('routes system/permission/plan events to effects without touching text', () => {
    const base = initialStreamState();
    expect(applyStreamEvent(base, { type: 'system', content: 'notice' }).effects)
      .toEqual([{ type: 'system-message', content: 'notice' }]);
    expect(applyStreamEvent(base, { type: 'permission' }).effects)
      .toEqual([{ type: 'permission' }]);
    expect(applyStreamEvent(base, { type: 'plan_proposal' }).effects)
      .toEqual([{ type: 'plan-proposal' }]);
  });
});
