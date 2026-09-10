/**
 * Model-list ordering.
 *
 * Kept as a pure function (rather than inline in the page) because the
 * behaviour is easy to get subtly wrong and tedious to verify by clicking:
 * `Array#sort` is stable in every engine we target, so equal keys keep their
 * catalogue order — which is what makes "newly discovered first" readable
 * instead of random-looking within each group.
 */

export type ModelSort = 'default' | 'context-desc' | 'context-asc' | 'new-first';

export const MODEL_SORTS: ModelSort[] = ['default', 'context-desc', 'context-asc', 'new-first'];

/** Minimal shape needed to order a model; keeps this usable for any DTO. */
export interface SortableModel {
  contextWindow?: number;
  maxOutputTokens?: number;
  custom?: boolean;
}

/**
 * Returns a new array; never mutates the input, since callers pass state
 * derived straight from the store.
 *
 * Models with no known context window always sort last, in both directions.
 * Treating "unknown" as 0 would float it to the top of an ascending list, and
 * a large sentinel would float it to the top of a descending one — either way
 * a wall of blanks, which is worse than useless. A missing value is not the
 * smallest value; it is simply absent.
 */
export function sortModels<T extends SortableModel>(list: readonly T[], sort: ModelSort): T[] {
  if (sort === 'default') return list as T[];
  const out = [...list];
  switch (sort) {
    case 'new-first':
      out.sort((a, b) => Number(!!b.custom) - Number(!!a.custom));
      break;
    case 'context-desc':
      out.sort(byContext(-1));
      break;
    case 'context-asc':
      out.sort(byContext(1));
      break;
  }
  return out;
}

function byContext<T extends SortableModel>(dir: 1 | -1) {
  return (a: T, b: T): number => {
    const av = known(a.contextWindow);
    const bv = known(b.contextWindow);
    if (av === null && bv === null) return 0;
    if (av === null) return 1;   // unknown last, whichever way we are sorting
    if (bv === null) return -1;
    return (av - bv) * dir;
  };
}

function known(v: number | undefined): number | null {
  return typeof v === 'number' && v > 0 ? v : null;
}
