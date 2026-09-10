import { describe, it, expect } from 'vitest';
import { sortModels, type SortableModel } from '../modelSort';

interface M extends SortableModel { id: string; name?: string }

const m = (id: string, contextWindow?: number, custom?: boolean): M =>
  ({ id, contextWindow, custom });

const ids = (list: M[]) => list.map(x => x.id);

describe('sortModels', () => {
  const catalogue: M[] = [
    m('mid', 128000),
    m('small', 32000),
    m('big', 1000000),
    m('unknown'),          // no context window reported
  ];

  it('"default" preserves catalogue order and does not copy', () => {
    const out = sortModels(catalogue, 'default');
    expect(ids(out)).toEqual(['mid', 'small', 'big', 'unknown']);
    expect(out).toBe(catalogue);   // no needless copy on the hot path
  });

  it('"context-desc" orders largest first with unknowns last', () => {
    expect(ids(sortModels(catalogue, 'context-desc')))
      .toEqual(['big', 'mid', 'small', 'unknown']);
  });

  it('"context-asc" orders smallest first with unknowns last', () => {
    // Unknown must not float to the top here — 0-filling would have done that.
    expect(ids(sortModels(catalogue, 'context-asc')))
      .toEqual(['small', 'mid', 'big', 'unknown']);
  });

  it('never mutates the input array', () => {
    const input = [...catalogue];
    sortModels(input, 'context-desc');
    expect(ids(input)).toEqual(['mid', 'small', 'big', 'unknown']);
  });

  it('"new-first" groups models absent from the built-in catalogue', () => {
    const list: M[] = [
      m('catalogue-a', 128000),
      m('vendor-new', 64000, true),
      m('catalogue-b', 8000),
      m('hand-added', 200000, true),
    ];
    const out = sortModels(list, 'new-first');
    expect(out.slice(0, 2).map(x => !!x.custom)).toEqual([true, true]);
    expect(out.slice(2).map(x => !!x.custom)).toEqual([false, false]);
  });

  it('is stable: equal keys keep their catalogue order', () => {
    // Grouped-by-provider rendering relies on this — an unstable sort would
    // reshuffle the list every render for no visible reason.
    const list: M[] = [
      m('a', 128000), m('b', 128000), m('c', 128000),
      m('d', 128000), m('e', 128000),
    ];
    expect(ids(sortModels(list, 'context-desc'))).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(ids(sortModels(list, 'new-first'))).toEqual(['a', 'b', 'c', 'd', 'e']);
  });

  it('treats a zero or negative window as unknown rather than smallest', () => {
    const list: M[] = [m('zero', 0), m('neg', -1), m('real', 1000)];
    expect(ids(sortModels(list, 'context-asc'))).toEqual(['real', 'zero', 'neg']);
    expect(ids(sortModels(list, 'context-desc'))).toEqual(['real', 'zero', 'neg']);
  });

  it('handles an empty list and an all-unknown list', () => {
    expect(sortModels([], 'context-desc')).toEqual([]);
    const allUnknown = [m('x'), m('y')];
    expect(ids(sortModels(allUnknown, 'context-asc'))).toEqual(['x', 'y']);
    expect(ids(sortModels(allUnknown, 'context-desc'))).toEqual(['x', 'y']);
  });
});
